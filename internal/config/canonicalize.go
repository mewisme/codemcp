package config

import (
	"errors"
	"fmt"
	"os"

	"go.mewis.me/codemcp/internal/configformat"
)

var startupDeprecatedConfigPaths = [][]string{
	{"interactive"},
	{"server", "host"},
	{"shell", "approval_policy"},
	{"shell", "mutation_approval"},
	{"shell", "approval_timeout"},
	{"tunnel", "command"},
	{"tunnel", "args"},
	{"tunnel", "origin"},
	{"tunnel", "public_url"},
	{"tunnel", "api_key"},
	{"tunnel", "admin_key"},
	{"tunnel", "admin_organization_id"},
	{"tunnel", "admin_workspace_id"},
	{"tunnel", "admin_tenant_id"},
}

var startupDeprecatedTunnelPaths = [][]string{
	{"api_key"},
	{"admin_key"},
}

// CanonicalizeStartup prunes only explicitly retired configuration keys after
// startup loading/migration has completed. Unknown keys are intentionally kept.
func CanonicalizeStartup() (int, error) {
	source, err := Source()
	if err != nil {
		return 0, err
	}
	if !source.Exists {
		return 0, nil
	}
	removed, err := pruneDeprecatedConfigKeys(source.Path, startupDeprecatedConfigPaths)
	if err != nil {
		return 0, err
	}
	tunnelPath := configformat.StructuredPathFrom(source.Path, "tunnel")
	tunnelRemoved, err := pruneDeprecatedConfigKeys(tunnelPath, startupDeprecatedTunnelPaths)
	if err != nil {
		return removed, err
	}
	return removed + tunnelRemoved, nil
}

func pruneDeprecatedConfigKeys(path string, paths [][]string) (int, error) {
	data, mode, err := readConfigFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	format, err := configformat.Detect(path)
	if err != nil {
		return 0, err
	}
	decoded, err := configformat.DecodeGeneric(format, data)
	if err != nil {
		return 0, fmt.Errorf("decode %s for startup canonicalization: %w", path, err)
	}
	root, ok := decoded.(map[string]any)
	if !ok {
		return 0, fmt.Errorf("configuration must encode as an object: %s", path)
	}
	removed := 0
	for _, keyPath := range paths {
		if deleteGenericPath(root, keyPath) {
			removed++
		}
	}
	if removed == 0 {
		return 0, nil
	}
	encoded, err := configformat.EncodeGeneric(format, root)
	if err != nil {
		return 0, fmt.Errorf("encode %s after startup canonicalization: %w", path, err)
	}
	if err := writeConfigFile(path, encoded, mode); err != nil {
		return 0, err
	}
	return removed, nil
}

func deleteGenericPath(root map[string]any, path []string) bool {
	if len(path) == 0 {
		return false
	}
	current := root
	for _, key := range path[:len(path)-1] {
		next, ok := current[key].(map[string]any)
		if !ok {
			return false
		}
		current = next
	}
	key := path[len(path)-1]
	if _, exists := current[key]; !exists {
		return false
	}
	delete(current, key)
	return true
}
