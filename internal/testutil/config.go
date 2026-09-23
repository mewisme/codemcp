package testutil

import (
	"os"
	"path/filepath"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/secretstore"
)

func IsolateConfigHome() (string, func(), error) {
	home, err := os.MkdirTemp("", "cm-test-home-")
	if err != nil {
		return "", nil, err
	}
	previous := map[string]*string{}
	for _, key := range []string{"HOME", "USERPROFILE", configformat.EnvConfigDir} {
		if value, ok := os.LookupEnv(key); ok {
			copy := value
			previous[key] = &copy
		} else {
			previous[key] = nil
		}
	}
	if err := os.Setenv("HOME", home); err != nil {
		_ = os.RemoveAll(home)
		return "", nil, err
	}
	if err := os.Setenv("USERPROFILE", home); err != nil {
		_ = os.RemoveAll(home)
		return "", nil, err
	}
	configRoot := filepath.Join(home, "config")
	if err := os.Setenv(configformat.EnvConfigDir, configRoot); err != nil {
		_ = os.RemoveAll(home)
		return "", nil, err
	}
	_ = configformat.SetRootPath("")
	secretStoreCleanup := secretstore.UseMemoryForTesting()
	cleanup := func() {
		secretStoreCleanup()
		_ = configformat.SetRootPath("")
		for key, value := range previous {
			if value == nil {
				_ = os.Unsetenv(key)
			} else {
				_ = os.Setenv(key, *value)
			}
		}
		_ = os.RemoveAll(home)
	}
	return configRoot, cleanup, nil
}
