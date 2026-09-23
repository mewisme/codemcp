package update

import (
	"runtime"
	"strings"
	"testing"
)

func TestAssetName(t *testing.T) {
	tests := []struct {
		version, goos, goarch, want string
	}{
		{"v1.2.3", "linux", "amd64", "codemcp_1.2.3_linux_amd64.tar.gz"},
		{"1.2.3", "darwin", "arm64", "codemcp_1.2.3_darwin_arm64.tar.gz"},
		{"v1.2.3", "windows", "amd64", "codemcp_1.2.3_windows_amd64.zip"},
	}
	for _, test := range tests {
		got, err := AssetName(test.version, test.goos, test.goarch)
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Fatalf("AssetName(%q, %q, %q) = %q, want %q", test.version, test.goos, test.goarch, got, test.want)
		}
	}
	if _, err := AssetName("v1.2.3", "plan9", "amd64"); err == nil {
		t.Fatal("unsupported OS was accepted")
	}
	if _, err := AssetName("v1.2.3", "linux", "386"); err == nil {
		t.Fatal("unsupported architecture was accepted")
	}
}

func TestCurrentAssetName(t *testing.T) {
	name, err := CurrentAssetName("v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(name, "_"+runtime.GOOS+"_"+runtime.GOARCH) {
		t.Fatalf("current asset name = %q", name)
	}
}
