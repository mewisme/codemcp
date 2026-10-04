package config

import (
	"strings"
	"testing"
)

func TestBrowserIntegrationDefaultsEnabledWithoutExecutableOverride(t *testing.T) {
	cfg := Default()
	if !cfg.Integrations.Browser.Enabled || cfg.Integrations.Browser.Path != "" || cfg.Integrations.Browser.Headless {
		t.Fatalf("browser defaults=%#v", cfg.Integrations.Browser)
	}
	for _, test := range []struct {
		key  string
		want string
	}{
		{"integrations.browser.enabled", "true"},
		{"integrations.browser.path", ""},
		{"integrations.browser.headless", "false"},
	} {
		value, err := RawValue(cfg, test.key)
		if err != nil || value != test.want {
			t.Fatalf("%s=%q err=%v want=%q", test.key, value, err, test.want)
		}
	}
}

func TestBrowserExecutableConfigAcceptsNativeAndWindowsAbsolutePathsOnly(t *testing.T) {
	for _, test := range []struct {
		name    string
		path    string
		wantErr string
	}{
		{name: "native", path: "/opt/google/chrome"},
		{name: "windows", path: `C:\Program Files\Google\Chrome\Application\chrome.exe`},
		{name: "relative", path: "chrome", wantErr: "must be an absolute"},
		{name: "whitespace", path: " /opt/google/chrome ", wantErr: "leading or trailing whitespace"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			cfg.HTTP.MCP.Auth.TokenHash = "test-token-hash"
			cfg.HTTP.Admin.Auth.TokenHash = "test-admin-token-hash"
			cfg.Integrations.Browser.Path = test.path
			err := Validate(cfg)
			if test.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Validate path %q error=%v want contains %q", test.path, err, test.wantErr)
			}
		})
	}
}
