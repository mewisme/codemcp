package codegraph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	gitexec "go.mewis.me/codemcp/internal/git"
	statepkg "go.mewis.me/codemcp/internal/state"
	"go.mewis.me/codemcp/internal/workspace"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

const projectConfigFileName = "codegraph.json"

// ReconcileProjectConfig mirrors Git's effective ignored paths into CodeGraph's
// explicit exclude list while preserving user-owned configuration.
//
// Git remains the authority for ignore semantics: --exclude-standard resolves
// root/nested .gitignore files, repository-local info/exclude, the configured
// global excludes file, and negation rules before CodeMCP sees the result.
func ReconcileProjectConfig(ctx context.Context, store workspacestate.Store, workspaceID, projectRoot, relativePath string) (bool, error) {
	if err := workspace.EnsureGitInfoExcludePath(projectRoot, projectConfigFileName); err != nil {
		return false, errors.New("exclude CodeGraph project config from Git: " + err.Error())
	}
	ignored, gitRepo, err := effectiveGitIgnoredPaths(ctx, projectRoot)
	if err != nil {
		return false, err
	}
	if !gitRepo {
		return false, nil
	}
	ignored = appendUnique(ignored, projectConfigFileName, ".cm/", ".codegraph/")
	sort.Strings(ignored)

	configPath := filepath.Join(projectRoot, projectConfigFileName)
	config, currentData, perm, err := loadProjectConfig(configPath)
	if err != nil {
		return false, err
	}
	currentExclude, err := stringArrayField(config, "exclude")
	if err != nil {
		return false, err
	}
	include, err := stringArrayField(config, "include")
	if err != nil {
		return false, err
	}
	includeIgnored, err := stringArrayField(config, "includeIgnored")
	if err != nil {
		return false, err
	}

	metadata, metadataErr := loadWorkspaceMetadata(store, workspaceID)
	if metadataErr != nil && !errors.Is(metadataErr, os.ErrNotExist) {
		return false, metadataErr
	}
	if metadata.Projects == nil {
		metadata = workspaceMetadata{Version: workspaceMetadataVersion, WorkspaceID: strings.TrimSpace(workspaceID), Projects: map[string]projectMetadata{}}
	}
	relativePath = normalizeRelativeProjectPath(relativePath)
	record := metadata.Projects[relativePath]
	currentHash := projectConfigHash(currentData)

	baseExclude := append([]string(nil), currentExclude...)
	if record.ManagedConfigHash != "" && record.ManagedConfigHash == currentHash {
		baseExclude = removeStrings(baseExclude, record.ManagedExclude)
	}

	managed := make([]string, 0, len(ignored))
	baseSet := stringSet(baseExclude)
	for _, candidate := range ignored {
		if candidate == "" {
			continue
		}
		if candidate != projectConfigFileName && coveredByExplicitInclude(candidate, include, includeIgnored) {
			continue
		}
		if _, userOwned := baseSet[candidate]; userOwned {
			continue
		}
		managed = append(managed, candidate)
	}
	sort.Strings(managed)
	merged := appendUnique(baseExclude, managed...)

	changed := !equalStrings(currentExclude, merged)
	newData := currentData
	if changed {
		encoded, err := encodeProjectConfig(config, merged)
		if err != nil {
			return false, err
		}
		if err := statepkg.WriteFileAtomic(configPath, encoded, perm); err != nil {
			return false, err
		}
		newData = encoded
	}

	record.ManagedExclude = append([]string(nil), managed...)
	record.ManagedConfigHash = projectConfigHash(newData)
	metadata.Version = workspaceMetadataVersion
	metadata.WorkspaceID = strings.TrimSpace(workspaceID)
	metadata.Projects[relativePath] = record
	if err := saveWorkspaceMetadata(store, metadata); err != nil {
		return false, err
	}
	return changed, nil
}

func ProjectConfigRequiresConservativeSync(projectRoot string) bool {
	config, _, _, err := loadProjectConfig(filepath.Join(projectRoot, projectConfigFileName))
	if err != nil {
		return true
	}
	include, err := stringArrayField(config, "include")
	if err != nil {
		return true
	}
	includeIgnored, err := stringArrayField(config, "includeIgnored")
	if err != nil {
		return true
	}
	return len(include) > 0 || len(includeIgnored) > 0
}

func effectiveGitIgnoredPaths(ctx context.Context, projectRoot string) ([]string, bool, error) {
	probe, err := gitexec.Run(ctx, projectRoot, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return nil, false, err
	}
	if probe.ExitCode != 0 || strings.TrimSpace(probe.Stdout) != "true" {
		return nil, false, nil
	}
	result, err := gitexec.Run(ctx, projectRoot, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z")
	if err != nil {
		return nil, true, err
	}
	if result.ExitCode != 0 {
		return nil, true, errors.New("resolve effective Git ignored paths: " + strings.TrimSpace(result.Stderr))
	}
	if strings.HasPrefix(result.Stdout, "[output truncated") {
		return nil, true, errors.New("effective Git ignored path inventory exceeds CodeMCP output limit")
	}
	values := strings.Split(result.Stdout, "\x00")
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = normalizeConfigPath(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out, true, nil
}

func loadProjectConfig(configPath string) (map[string]json.RawMessage, []byte, os.FileMode, error) {
	info, err := os.Lstat(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]json.RawMessage{}, nil, 0644, nil
	}
	if err != nil {
		return nil, nil, 0, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, nil, 0, errors.New("CodeGraph project config must be a regular non-symlink file")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, nil, 0, err
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, nil, 0, errors.New("decode CodeGraph project config: " + err.Error())
	}
	if config == nil {
		config = map[string]json.RawMessage{}
	}
	return config, data, info.Mode().Perm(), nil
}

func encodeProjectConfig(config map[string]json.RawMessage, exclude []string) ([]byte, error) {
	encodedExclude, err := json.Marshal(exclude)
	if err != nil {
		return nil, err
	}
	config["exclude"] = encodedExclude
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func stringArrayField(config map[string]json.RawMessage, field string) ([]string, error) {
	raw, ok := config[field]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, errors.New("CodeGraph project config field " + field + " must be an array of strings")
	}
	return values, nil
}

func coveredByExplicitInclude(candidate string, groups ...[]string) bool {
	candidate = strings.TrimSuffix(normalizeConfigPath(candidate), "/")
	for _, group := range groups {
		for _, patternValue := range group {
			patternValue = normalizeConfigPath(patternValue)
			if patternValue == "" {
				continue
			}
			if wildcard := strings.IndexAny(patternValue, "*?["); wildcard >= 0 {
				prefix := strings.Trim(strings.TrimSuffix(patternValue[:wildcard], "/"), "/")
				if prefix == "" || candidate == prefix || strings.HasPrefix(candidate, prefix+"/") || strings.HasPrefix(prefix, candidate+"/") {
					return true
				}
				continue
			}
			included := strings.TrimSuffix(strings.TrimPrefix(patternValue, "/"), "/")
			if included == "" || candidate == included || strings.HasPrefix(candidate, included+"/") || strings.HasPrefix(included, candidate+"/") {
				return true
			}
		}
	}
	return false
}

func normalizeConfigPath(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = strings.TrimPrefix(value, "./")
	if value == "" || value == "." {
		return ""
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return ""
	}
	if strings.HasSuffix(value, "/") {
		clean += "/"
	}
	return clean
}

func projectConfigHash(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func removeStrings(values, remove []string) []string {
	removeSet := stringSet(remove)
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := removeSet[value]; !ok {
			out = append(out, value)
		}
	}
	return out
}

func appendUnique(values []string, additions ...string) []string {
	out := append([]string(nil), values...)
	seen := stringSet(out)
	for _, value := range additions {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
