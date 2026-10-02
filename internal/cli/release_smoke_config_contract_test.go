package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/config"
)

var (
	releaseSmokeConfigActionPattern  = regexp.MustCompile(`\["config",\s*"(set|get|unset)",\s*"([^"]+)"`)
	releaseSmokeStaticSetPattern     = regexp.MustCompile(`\["config",\s*"set",\s*"([^"]+)",\s*"([^"]*)"\]`)
	releaseSmokeConfigCommandPattern = regexp.MustCompile(`\["config",\s*"([^"]+)"`)
)

func TestReleaseSmokeConfigSurfaceMatchesCanonicalAuthority(t *testing.T) {
	source := releaseSmokeSource(t)
	seen := map[string]struct{}{}

	for _, match := range releaseSmokeConfigActionPattern.FindAllStringSubmatch(source, -1) {
		action, key := match[1], match[2]
		spec, ok := config.SettingByKey(key)
		if !ok {
			t.Errorf("release smoke %s uses unknown config key %q", action, key)
			continue
		}
		if spec.Key != key {
			t.Errorf("release smoke %s uses non-canonical config key %q; canonical key is %q", action, key, spec.Key)
		}
		switch action {
		case "set", "unset":
			if !spec.Writable {
				t.Errorf("release smoke %s targets non-writable config key %q", action, key)
			}
		case "get":
			if !spec.Readable {
				t.Errorf("release smoke get targets non-readable config key %q", key)
			}
		}
		seen[key] = struct{}{}
	}

	if len(seen) == 0 {
		t.Fatal("release smoke contains no canonical config key coverage")
	}

	for _, match := range releaseSmokeStaticSetPattern.FindAllStringSubmatch(source, -1) {
		key, value := match[1], match[2]
		cfg := config.Default()
		if err := config.SetValue(&cfg, key, value); err != nil {
			t.Errorf("release smoke literal config value drifted: %s=%q: %v", key, value, err)
		}
	}
}

func TestReleaseSmokeConfigCommandsExist(t *testing.T) {
	source := releaseSmokeSource(t)
	root := newRootCommand()
	seen := map[string]struct{}{}

	for _, match := range releaseSmokeConfigCommandPattern.FindAllStringSubmatch(source, -1) {
		subcommand := strings.TrimSpace(match[1])
		if subcommand == "" {
			continue
		}
		seen[subcommand] = struct{}{}
		cmd, args, err := root.Find([]string{"config", subcommand})
		if err != nil || cmd == nil || len(args) != 0 || cmd.CommandPath() != root.CommandPath()+" config "+subcommand {
			t.Errorf("release smoke uses unavailable config command %q: cmd=%v args=%v err=%v", subcommand, cmd, args, err)
		}
	}

	if len(seen) == 0 {
		t.Fatal("release smoke contains no config command coverage")
	}
	commands := make([]string, 0, len(seen))
	for command := range seen {
		commands = append(commands, command)
	}
	sort.Strings(commands)
	for _, retired := range []string{"convert", "transform", "validate", "reload"} {
		if _, ok := seen[retired]; ok {
			t.Errorf("release smoke still uses retired config command %q; commands=%v", retired, commands)
		}
	}
}

func releaseSmokeSource(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve release smoke contract test path")
	}
	path := filepath.Clean(filepath.Join(filepath.Dir(current), "..", "..", "scripts", "release", "smoke.mjs"))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read release smoke %s: %v", path, err)
	}
	return string(data)
}
