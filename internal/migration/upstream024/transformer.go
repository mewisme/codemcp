package upstream024

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"go.mewis.me/codemcp/internal/secretstore"
	"go.mewis.me/codemcp/internal/state"
	"go.mewis.me/codemcp/internal/upstream"
)

const (
	SourceRelease      = "0.2.24"
	maxStructuredBytes = 16 << 20
	storeVersion       = 1
)

type Input struct {
	SourcePath       string
	DestinationPath  string
	PreserveSource   bool
	StagedSecretRoot string
}

type Result struct {
	DestinationPath string `json:"destination_path"`
	Migrated        bool   `json:"migrated"`
	AlreadyApplied  bool   `json:"already_applied"`
	SourceRemoved   bool   `json:"source_removed"`
}

type Inspection struct {
	SourceRelease string `json:"source_release"`
	Version       int    `json:"version"`
	Servers       int    `json:"servers"`
}

type releasedStore struct {
	Version int               `json:"version"`
	Servers []upstream.Server `json:"servers"`
}

type currentStore struct {
	Version   int               `json:"version"`
	Upstreams []upstream.Server `json:"upstreams"`
}

func Transform(input Input) (Result, error) {
	input, err := normalizeInput(input)
	if err != nil {
		return Result{}, err
	}

	destination, destinationExists, err := readCurrentIfExists(input.DestinationPath)
	if err != nil {
		return Result{}, err
	}

	sourceData, sourceInfo, err := readRegular(input.SourcePath, "released upstream store")
	if errors.Is(err, os.ErrNotExist) {
		if destinationExists {
			return Result{DestinationPath: input.DestinationPath, AlreadyApplied: true}, nil
		}
		return Result{}, fmt.Errorf("released upstream store is missing: %s", input.SourcePath)
	}
	if err != nil {
		return Result{}, err
	}
	released, err := decodeReleased(sourceData)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(input.StagedSecretRoot) != "" {
		if err := protectStagedSensitiveValues(released.Servers, input.StagedSecretRoot); err != nil {
			return Result{}, err
		}
	}
	if err := validatePresentationSafe(released.Servers); err != nil {
		return Result{}, err
	}
	expected := currentStore{Version: storeVersion, Upstreams: cloneServers(released.Servers)}
	encoded, err := state.MarshalJSON(expected)
	if err != nil {
		return Result{}, fmt.Errorf("encode migrated upstream store: %w", err)
	}

	if destinationExists {
		if !reflect.DeepEqual(destination, expected) {
			return Result{}, fmt.Errorf("upstream migration destination conflict: %s already exists with different content", input.DestinationPath)
		}
		if !input.PreserveSource {
			if err := removeUnchangedSource(input.SourcePath, sourceInfo); err != nil {
				return Result{}, err
			}
		}
		return Result{DestinationPath: input.DestinationPath, Migrated: true, AlreadyApplied: true, SourceRemoved: !input.PreserveSource}, nil
	}

	if err := state.WriteFileAtomic(input.DestinationPath, encoded, 0600); err != nil {
		return Result{}, fmt.Errorf("write migrated upstream store: %w", err)
	}
	verified, exists, err := readCurrentIfExists(input.DestinationPath)
	if err != nil {
		return Result{}, fmt.Errorf("verify migrated upstream store: %w", err)
	}
	if !exists || !reflect.DeepEqual(verified, expected) {
		return Result{}, errors.New("verify migrated upstream store: destination content mismatch")
	}
	if !input.PreserveSource {
		if err := removeUnchangedSource(input.SourcePath, sourceInfo); err != nil {
			return Result{}, err
		}
	}
	return Result{DestinationPath: input.DestinationPath, Migrated: true, SourceRemoved: !input.PreserveSource}, nil
}

func protectStagedSensitiveValues(servers []upstream.Server, root string) error {
	store := secretstore.New(root)
	for index := range servers {
		server := &servers[index]
		for _, group := range []struct {
			kind   string
			values map[string]string
		}{
			{kind: "header", values: server.Headers},
			{kind: "env", values: server.Env},
		} {
			for key, value := range group.values {
				if !upstream.SensitiveConfigKey(key) || strings.TrimSpace(value) == "" {
					continue
				}
				account := secretstore.AccountName(secretstore.DomainUpstream, server.ID, group.kind, key)
				staged, err := store.Get(account)
				if err != nil {
					return fmt.Errorf("verify staged upstream %s credential for %q: %w", group.kind, server.ID, err)
				}
				if !secretstore.IsMarker(value) && staged != value {
					return fmt.Errorf("staged upstream %s credential for %q does not match released state", group.kind, server.ID)
				}
				group.values[key] = secretstore.Marker
			}
		}
	}
	return nil
}

func Inspect(sourcePath string) (Inspection, error) {
	path := filepath.Clean(strings.TrimSpace(sourcePath))
	if path == "." {
		return Inspection{}, errors.New("released upstream source path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Inspection{}, fmt.Errorf("resolve released upstream source: %w", err)
	}
	data, _, err := readRegular(absolute, "released upstream store")
	if err != nil {
		return Inspection{}, err
	}
	released, err := decodeReleased(data)
	if err != nil {
		return Inspection{}, err
	}
	return Inspection{SourceRelease: SourceRelease, Version: released.Version, Servers: len(released.Servers)}, nil
}

func normalizeInput(input Input) (Input, error) {
	input.SourcePath = filepath.Clean(strings.TrimSpace(input.SourcePath))
	input.DestinationPath = filepath.Clean(strings.TrimSpace(input.DestinationPath))
	if input.SourcePath == "." || input.DestinationPath == "." {
		return Input{}, errors.New("upstream migration source and destination paths are required")
	}
	source, err := filepath.Abs(input.SourcePath)
	if err != nil {
		return Input{}, fmt.Errorf("resolve upstream migration source: %w", err)
	}
	destination, err := filepath.Abs(input.DestinationPath)
	if err != nil {
		return Input{}, fmt.Errorf("resolve upstream migration destination: %w", err)
	}
	if filepath.Clean(source) == filepath.Clean(destination) {
		return Input{}, errors.New("upstream migration source and destination must be different files")
	}
	input.SourcePath = filepath.Clean(source)
	input.DestinationPath = filepath.Clean(destination)
	return input, nil
}

func decodeReleased(data []byte) (releasedStore, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return releasedStore{}, errors.New("released upstream store is empty")
	}
	if trimmed[0] == '[' {
		var servers []upstream.Server
		if err := json.Unmarshal(trimmed, &servers); err != nil {
			return releasedStore{}, fmt.Errorf("decode released %s upstream store: %w", SourceRelease, err)
		}
		return releasedStore{Version: storeVersion, Servers: nonNilServers(servers)}, nil
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &envelope); err != nil {
		return releasedStore{}, fmt.Errorf("decode released %s upstream store: %w", SourceRelease, err)
	}
	if _, current := envelope["upstreams"]; current {
		return releasedStore{}, errors.New("released upstream store unexpectedly uses current upstreams schema")
	}
	if _, ok := envelope["servers"]; !ok {
		return releasedStore{}, errors.New("released upstream store does not contain servers")
	}
	var released releasedStore
	if err := json.Unmarshal(trimmed, &released); err != nil {
		return releasedStore{}, fmt.Errorf("decode released %s upstream store: %w", SourceRelease, err)
	}
	if released.Version != 0 && released.Version != storeVersion {
		return releasedStore{}, fmt.Errorf("unsupported released upstream store version: %d", released.Version)
	}
	released.Version = storeVersion
	released.Servers = nonNilServers(released.Servers)
	return released, nil
}

func readCurrentIfExists(path string) (currentStore, bool, error) {
	data, _, err := readRegular(path, "upstream migration destination")
	if errors.Is(err, os.ErrNotExist) {
		return currentStore{}, false, nil
	}
	if err != nil {
		return currentStore{}, false, err
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return currentStore{}, false, fmt.Errorf("decode upstream migration destination: %w", err)
	}
	if _, legacy := envelope["servers"]; legacy {
		return currentStore{}, false, errors.New("upstream migration destination uses legacy servers schema")
	}
	if _, ok := envelope["upstreams"]; !ok {
		return currentStore{}, false, errors.New("upstream migration destination does not contain upstreams")
	}
	var current currentStore
	if err := json.Unmarshal(data, &current); err != nil {
		return currentStore{}, false, fmt.Errorf("decode upstream migration destination: %w", err)
	}
	if current.Version != storeVersion {
		return currentStore{}, false, fmt.Errorf("unsupported upstream migration destination version: %d", current.Version)
	}
	current.Upstreams = nonNilServers(current.Upstreams)
	return current, true, nil
}

func validatePresentationSafe(servers []upstream.Server) error {
	for _, server := range servers {
		for key, value := range server.Headers {
			if upstream.SensitiveConfigKey(key) && strings.TrimSpace(value) != "" && !secretstore.IsMarker(value) {
				return fmt.Errorf("released upstream %q contains plaintext sensitive header state; credential migration must run before upstream state migration", server.ID)
			}
		}
		for key, value := range server.Env {
			if upstream.SensitiveConfigKey(key) && strings.TrimSpace(value) != "" && !secretstore.IsMarker(value) {
				return fmt.Errorf("released upstream %q contains plaintext sensitive environment state; credential migration must run before upstream state migration", server.ID)
			}
		}
	}
	return nil
}

func readRegular(path, label string) ([]byte, os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, nil, fmt.Errorf("%s is not a regular non-symlink file: %s", label, path)
	}
	if info.Size() > maxStructuredBytes {
		return nil, nil, fmt.Errorf("%s exceeds %d bytes", label, maxStructuredBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, nil, fmt.Errorf("%s changed while opening: %s", label, path)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxStructuredBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if len(data) > maxStructuredBytes {
		return nil, nil, fmt.Errorf("%s exceeds %d bytes", label, maxStructuredBytes)
	}
	return data, opened, nil
}

func removeUnchangedSource(path string, expected os.FileInfo) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect released upstream store before removal: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !os.SameFile(expected, info) {
		return errors.New("released upstream store changed during migration; refusing removal")
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove released upstream store after verified migration: %w", err)
	}
	return nil
}

func nonNilServers(values []upstream.Server) []upstream.Server {
	if values == nil {
		return []upstream.Server{}
	}
	return cloneServers(values)
}

func cloneServers(values []upstream.Server) []upstream.Server {
	result := make([]upstream.Server, len(values))
	for i, server := range values {
		server.Args = append([]string(nil), server.Args...)
		server.Tools = append([]string(nil), server.Tools...)
		server.DisabledTools = append([]string(nil), server.DisabledTools...)
		server.Headers = cloneMap(server.Headers)
		server.Env = cloneMap(server.Env)
		result[i] = server
	}
	return result
}

func cloneMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
