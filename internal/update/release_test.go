package update

import (
	"reflect"
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

func TestPrimaryReleaseLayoutContract(t *testing.T) {
	layout := PrimaryReleaseLayout()
	if layout.PackageName != PackageName || layout.ChecksumName != ChecksumName || layout.SignatureName != ChecksumSignatureName {
		t.Fatalf("layout identity = %#v", layout)
	}
	want := []ReleasePlatform{
		{OS: "linux", Arch: "amd64", ArchiveExtension: ".tar.gz", BinaryName: "cm"},
		{OS: "linux", Arch: "arm64", ArchiveExtension: ".tar.gz", BinaryName: "cm"},
		{OS: "darwin", Arch: "amd64", ArchiveExtension: ".tar.gz", BinaryName: "cm"},
		{OS: "darwin", Arch: "arm64", ArchiveExtension: ".tar.gz", BinaryName: "cm"},
		{OS: "windows", Arch: "amd64", ArchiveExtension: ".zip", BinaryName: "cm.exe"},
		{OS: "windows", Arch: "arm64", ArchiveExtension: ".zip", BinaryName: "cm.exe"},
	}
	if !reflect.DeepEqual(layout.Platforms, want) {
		t.Fatalf("platforms = %#v, want %#v", layout.Platforms, want)
	}
	layout.Platforms[0].BinaryName = "mutated"
	if current, _ := BinaryName("linux", "amd64"); current != "cm" {
		t.Fatalf("caller mutated canonical layout: %q", current)
	}
}

func TestReleasePlatformContractOwnsAssetAndBinaryNames(t *testing.T) {
	for _, platform := range PrimaryReleaseLayout().Platforms {
		asset, err := AssetName("v9.9.9", platform.OS, platform.Arch)
		if err != nil {
			t.Fatal(err)
		}
		wantAsset := "codemcp_9.9.9_" + platform.OS + "_" + platform.Arch + platform.ArchiveExtension
		if asset != wantAsset {
			t.Fatalf("%s/%s asset = %q, want %q", platform.OS, platform.Arch, asset, wantAsset)
		}
		binary, err := BinaryName(platform.OS, platform.Arch)
		if err != nil || binary != platform.BinaryName {
			t.Fatalf("%s/%s binary = %q, error = %v", platform.OS, platform.Arch, binary, err)
		}
	}
	if _, err := BinaryName("plan9", "amd64"); err == nil {
		t.Fatal("unsupported platform returned a binary name")
	}
}
