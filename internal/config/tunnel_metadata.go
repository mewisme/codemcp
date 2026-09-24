package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/state"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	"go.mewis.me/codemcp/internal/tunnel"
)

const tunnelMetadataVersion = 1

type tunnelMetadataFile struct {
	Version int `json:"version"`
	tunnel.Metadata
}

func TunnelMetadataDir() string { return filepath.Join(RootPath(), "tunnels") }

func TunnelMetadataPath(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("tunnel id is required")
	}
	if id == "." || id == ".." || strings.ContainsAny(id, `/\\`) || filepath.Base(id) != id {
		return "", fmt.Errorf("invalid tunnel id %q", id)
	}
	return filepath.Join(TunnelMetadataDir(), id+".json"), nil
}

func LoadTunnelMetadata(id string) (tunnel.Metadata, error) {
	path, err := TunnelMetadataPath(id)
	if err != nil {
		return tunnel.Metadata{}, err
	}
	root, err := openTunnelMetadataRoot(false)
	if err != nil {
		return tunnel.Metadata{}, err
	}
	defer root.Close()
	relative, err := tunnelMetadataRelative(path)
	if err != nil {
		return tunnel.Metadata{}, err
	}
	data, err := readTunnelMetadataFile(root, relative)
	if err != nil {
		return tunnel.Metadata{}, err
	}
	metadata, err := decodeTunnelMetadata(data)
	if err != nil {
		return tunnel.Metadata{}, fmt.Errorf("decode tunnel metadata %s: %w", path, err)
	}
	if strings.TrimSpace(metadata.ID) == "" {
		metadata.ID = strings.TrimSpace(id)
	}
	if metadata.ID != strings.TrimSpace(id) {
		return tunnel.Metadata{}, fmt.Errorf("tunnel metadata id mismatch: file %s contains %s", id, metadata.ID)
	}
	return metadata, nil
}

func SaveTunnelMetadata(metadata tunnel.Metadata) (string, error) {
	return SaveTunnelMetadataContext(context.Background(), metadata)
}

func SaveTunnelMetadataContext(ctx context.Context, metadata tunnel.Metadata) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	metadata.ID = strings.TrimSpace(metadata.ID)
	path, err := TunnelMetadataPath(metadata.ID)
	if err != nil {
		return "", err
	}
	span := tracepkg.Start(ctx, "TUNNEL", "tunnel.metadata.persist", "Persisting tunnel metadata cache", tracepkg.String("tunnel_id", metadata.ID), tracepkg.String("path", path), tracepkg.Bool("atomic", true))
	if metadata.FetchedAt.IsZero() {
		metadata.FetchedAt = time.Now().UTC()
	}
	data, err := state.MarshalJSON(tunnelMetadataFile{Version: tunnelMetadataVersion, Metadata: metadata})
	if err != nil {
		span.FailMessage("Tunnel metadata cache encoding failed", err)
		return "", err
	}
	root, err := openTunnelMetadataRoot(true)
	if err != nil {
		span.FailMessage("Tunnel metadata cache directory creation failed", err)
		return "", err
	}
	defer root.Close()
	relative, err := tunnelMetadataRelative(path)
	if err != nil {
		span.FailMessage("Tunnel metadata cache path validation failed", err)
		return "", err
	}
	if err := state.WriteFileAtomicRoot(root, relative, data, 0600); err != nil {
		span.FailMessage("Tunnel metadata cache persistence failed", err, tracepkg.Int64("bytes", int64(len(data))))
		return "", err
	}
	span.EndMessage("Tunnel metadata cache persisted", tracepkg.String("format", "json"), tracepkg.Int64("bytes", int64(len(data))))
	return path, nil
}

func RemoveTunnelMetadata(id string) error {
	return RemoveTunnelMetadataContext(context.Background(), id)
}

func RemoveTunnelMetadataContext(ctx context.Context, id string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	path, err := TunnelMetadataPath(id)
	if err != nil {
		return err
	}
	span := tracepkg.Start(ctx, "TUNNEL", "tunnel.metadata.remove", "Removing tunnel metadata cache", tracepkg.String("tunnel_id", strings.TrimSpace(id)), tracepkg.String("path", path))
	root, err := openTunnelMetadataRoot(false)
	if errors.Is(err, os.ErrNotExist) {
		span.EndMessage("Tunnel metadata cache removed")
		return nil
	}
	if err != nil {
		span.FailMessage("Tunnel metadata cache removal failed", err)
		return err
	}
	defer root.Close()
	relative, err := tunnelMetadataRelative(path)
	if err != nil {
		span.FailMessage("Tunnel metadata cache path validation failed", err)
		return err
	}
	if err := root.Remove(relative); err != nil && !errors.Is(err, os.ErrNotExist) {
		span.FailMessage("Tunnel metadata cache removal failed", err)
		return err
	}
	span.EndMessage("Tunnel metadata cache removed")
	return nil
}

func ListTunnelMetadata() ([]tunnel.Metadata, error) {
	root, err := openTunnelMetadataRoot(false)
	if errors.Is(err, os.ErrNotExist) {
		return []tunnel.Metadata{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	directory, err := root.Open("tunnels")
	if errors.Is(err, os.ErrNotExist) {
		return []tunnel.Metadata{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	result := make([]tunnel.Metadata, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			continue
		}
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		path := filepath.Join(TunnelMetadataDir(), entry.Name())
		data, err := readTunnelMetadataFile(root, filepath.Join("tunnels", entry.Name()))
		if err != nil {
			return nil, err
		}
		metadata, err := decodeTunnelMetadata(data)
		if err != nil {
			return nil, fmt.Errorf("decode tunnel metadata %s: %w", path, err)
		}
		if strings.TrimSpace(metadata.ID) == "" {
			metadata.ID = strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		}
		if _, err := TunnelMetadataPath(metadata.ID); err != nil {
			return nil, err
		}
		result = append(result, metadata)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func decodeTunnelMetadata(data []byte) (tunnel.Metadata, error) {
	var stored tunnelMetadataFile
	if err := json.Unmarshal(data, &stored); err != nil {
		return tunnel.Metadata{}, err
	}
	if stored.Version != 0 && stored.Version != tunnelMetadataVersion {
		return tunnel.Metadata{}, fmt.Errorf("unsupported tunnel metadata version: %d", stored.Version)
	}
	return stored.Metadata, nil
}

func openTunnelMetadataRoot(create bool) (*os.Root, error) {
	rootPath := filepath.Clean(RootPath())
	if create {
		if err := os.MkdirAll(rootPath, 0700); err != nil {
			return nil, err
		}
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	if create {
		if err := root.MkdirAll("tunnels", 0700); err != nil {
			_ = root.Close()
			return nil, err
		}
	}
	return root, nil
}

func tunnelMetadataRelative(path string) (string, error) {
	rootPath := filepath.Clean(RootPath())
	relative, err := filepath.Rel(rootPath, filepath.Clean(path))
	if err != nil {
		return "", err
	}
	if relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("tunnel metadata path escapes config root: %s", path)
	}
	return relative, nil
}

func readTunnelMetadataFile(root *os.Root, relative string) ([]byte, error) {
	info, err := root.Lstat(relative)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("tunnel metadata path is not a regular file: %s", relative)
	}
	file, err := root.Open(relative)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return nil, fmt.Errorf("tunnel metadata path changed while opening: %s", relative)
	}
	return io.ReadAll(file)
}

func SyncTunnelMetadata(ctx context.Context, cfg tunnel.Config) (tunnel.Metadata, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	span := tracepkg.Start(ctx, "TUNNEL", "tunnel.metadata.sync", "Synchronizing configured tunnel metadata", tracepkg.String("tunnel_id", strings.TrimSpace(cfg.ID)), tracepkg.URL("control_plane_base_url", cfg.ControlPlaneBaseURL))
	if !tunnel.Configured(cfg) {
		err := errors.New("tunnel id and runtime API key are required to sync metadata")
		span.FailMessage("Tunnel metadata synchronization validation failed", err)
		return tunnel.Metadata{}, "", err
	}
	metadata, err := tunnel.FetchMetadata(ctx, cfg)
	if err != nil {
		span.FailMessage("Tunnel metadata fetch failed", err)
		return tunnel.Metadata{}, "", err
	}
	path, err := SaveTunnelMetadataContext(ctx, metadata)
	if err != nil {
		span.FailMessage("Tunnel metadata cache persistence failed", err)
		return tunnel.Metadata{}, "", err
	}
	span.EndMessage("Configured tunnel metadata synchronized", tracepkg.String("tunnel_id", metadata.ID), tracepkg.String("metadata_path", path))
	return metadata, path, nil
}
