package update

import (
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestArchiveNameDoesNotContainReleaseVersion(t *testing.T) {
	tests := []struct {
		goos, goarch, want string
	}{
		{"linux", "amd64", "codemcp_linux_amd64.tar.gz"},
		{"linux", "arm64", "codemcp_linux_arm64.tar.gz"},
		{"darwin", "amd64", "codemcp_darwin_amd64.tar.gz"},
		{"windows", "amd64", "codemcp_windows_amd64.zip"},
	}
	for _, test := range tests {
		got, err := ArchiveName(test.goos, test.goarch)
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Fatalf("ArchiveName(%q, %q) = %q, want %q", test.goos, test.goarch, got, test.want)
		}
		first := Release{Version: "v1.2.3", ArchiveName: got}
		second := Release{Version: "v9.8.7", ArchiveName: got}
		if first.ArchiveName != second.ArchiveName || first.Version == second.Version {
			t.Fatalf("release and artifact identity were not independent: first=%#v second=%#v", first, second)
		}
	}
	if comparison, err := CompareVersions("v1.2.3", "v9.8.7"); err != nil || comparison >= 0 {
		t.Fatalf("release version identity was not preserved: comparison=%d err=%v", comparison, err)
	}
	if _, err := ArchiveName("plan9", "amd64"); err == nil {
		t.Fatal("unsupported OS was accepted")
	}
	if _, err := ArchiveName("linux", "386"); err == nil {
		t.Fatal("unsupported architecture was accepted")
	}
}

func TestCurrentArchiveName(t *testing.T) {
	name, err := CurrentArchiveName()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(name, "_"+runtime.GOOS+"_"+runtime.GOARCH) {
		t.Fatalf("current archive name = %q", name)
	}
}

func TestPrimaryReleaseLayoutContract(t *testing.T) {
	layout := PrimaryReleaseLayout()
	if layout.PackageName != PackageName || layout.ChecksumName != ChecksumName || layout.SignatureName != ChecksumSignatureName {
		t.Fatalf("layout identity = %#v", layout)
	}
	wantPlatforms := []ReleasePlatform{
		{OS: "linux", Arch: "amd64", ArchiveExtension: ".tar.gz", BinaryName: "cm"},
		{OS: "linux", Arch: "arm64", ArchiveExtension: ".tar.gz", BinaryName: "cm"},
		{OS: "darwin", Arch: "amd64", ArchiveExtension: ".tar.gz", BinaryName: "cm"},
		{OS: "windows", Arch: "amd64", ArchiveExtension: ".zip", BinaryName: "cm.exe"},
	}
	if !reflect.DeepEqual(layout.Platforms, wantPlatforms) {
		t.Fatalf("platforms = %#v, want %#v", layout.Platforms, wantPlatforms)
	}
	wantArtifacts := []ReleaseArtifact{
		{Kind: ArtifactArchive, OS: "linux", Arch: "amd64", FilenameSuffix: ".tar.gz", BinaryName: "cm"},
		{Kind: ArtifactArchive, OS: "linux", Arch: "arm64", FilenameSuffix: ".tar.gz", BinaryName: "cm"},
		{Kind: ArtifactArchive, OS: "darwin", Arch: "amd64", FilenameSuffix: ".tar.gz", BinaryName: "cm"},
		{Kind: ArtifactArchive, OS: "windows", Arch: "amd64", FilenameSuffix: ".zip", BinaryName: "cm.exe"},
		{Kind: ArtifactDebian, OS: "linux", Arch: "amd64", FilenameSuffix: ".deb", BinaryName: "cm"},
		{Kind: ArtifactDebian, OS: "linux", Arch: "arm64", FilenameSuffix: ".deb", BinaryName: "cm"},
		{Kind: ArtifactRPM, OS: "linux", Arch: "amd64", FilenameSuffix: ".rpm", BinaryName: "cm"},
		{Kind: ArtifactRPM, OS: "linux", Arch: "arm64", FilenameSuffix: ".rpm", BinaryName: "cm"},
		{Kind: ArtifactSetup, OS: "windows", Arch: "amd64", FilenameSuffix: "_setup.exe", BinaryName: "cm.exe"},
	}
	if !reflect.DeepEqual(layout.Artifacts, wantArtifacts) {
		t.Fatalf("artifacts = %#v, want %#v", layout.Artifacts, wantArtifacts)
	}
	layout.Platforms[0].BinaryName = "mutated"
	layout.Artifacts[0].BinaryName = "mutated"
	if current, _ := BinaryName("linux", "amd64"); current != "cm" {
		t.Fatalf("caller mutated canonical layout binary: %q", current)
	}
	if artifact, ok := ArtifactFor(ArtifactArchive, "linux", "amd64"); !ok || artifact.BinaryName != "cm" {
		t.Fatalf("caller mutated canonical artifact layout: %#v ok=%v", artifact, ok)
	}
}

func TestArtifactNameSupportedTuples(t *testing.T) {
	want := map[[3]string]string{
		{string(ArtifactArchive), "linux", "amd64"}:   "codemcp_linux_amd64.tar.gz",
		{string(ArtifactArchive), "linux", "arm64"}:   "codemcp_linux_arm64.tar.gz",
		{string(ArtifactArchive), "darwin", "amd64"}:  "codemcp_darwin_amd64.tar.gz",
		{string(ArtifactArchive), "windows", "amd64"}: "codemcp_windows_amd64.zip",
		{string(ArtifactDebian), "linux", "amd64"}:    "codemcp_linux_amd64.deb",
		{string(ArtifactDebian), "linux", "arm64"}:    "codemcp_linux_arm64.deb",
		{string(ArtifactRPM), "linux", "amd64"}:       "codemcp_linux_amd64.rpm",
		{string(ArtifactRPM), "linux", "arm64"}:       "codemcp_linux_arm64.rpm",
		{string(ArtifactSetup), "windows", "amd64"}:   "codemcp_windows_amd64_setup.exe",
	}
	for tuple, expected := range want {
		got, err := ArtifactName(ArtifactKind(tuple[0]), tuple[1], tuple[2])
		if err != nil {
			t.Fatalf("%v: %v", tuple, err)
		}
		if got != expected {
			t.Fatalf("%v = %q, want %q", tuple, got, expected)
		}
	}
	if len(PrimaryReleaseLayout().Artifacts) != len(want) {
		t.Fatalf("release artifact tuple count = %d, want %d", len(PrimaryReleaseLayout().Artifacts), len(want))
	}
	for _, tuple := range [][3]string{
		{string(ArtifactDebian), "darwin", "amd64"},
		{string(ArtifactRPM), "windows", "amd64"},
		{string(ArtifactSetup), "linux", "amd64"},
		{string(ArtifactArchive), "darwin", "arm64"},
		{string(ArtifactArchive), "windows", "arm64"},
		{string(ArtifactSetup), "windows", "arm64"},
		{string(ArtifactArchive), "linux", "386"},
		{"unknown", "linux", "amd64"},
	} {
		if _, err := ArtifactName(ArtifactKind(tuple[0]), tuple[1], tuple[2]); err == nil {
			t.Fatalf("unsupported tuple was accepted: %v", tuple)
		}
	}
}

func TestReleasePlatformContractOwnsBinaryNames(t *testing.T) {
	for _, platform := range PrimaryReleaseLayout().Platforms {
		asset, err := ArchiveName(platform.OS, platform.Arch)
		if err != nil {
			t.Fatal(err)
		}
		wantAsset := "codemcp_" + platform.OS + "_" + platform.Arch + platform.ArchiveExtension
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

func TestReleaseAssetURLsSeparateExactTagFromLatest(t *testing.T) {
	asset := "codemcp_windows_amd64_setup.exe"
	exact, err := ExactReleaseAssetURL(DefaultOwner, DefaultRepo, "1.4.0", asset)
	if err != nil {
		t.Fatal(err)
	}
	if exact != "https://github.com/mewisme/codemcp/releases/download/v1.4.0/"+asset {
		t.Fatalf("exact URL = %q", exact)
	}
	latest, err := LatestReleaseAssetURL(DefaultOwner, DefaultRepo, asset)
	if err != nil {
		t.Fatal(err)
	}
	if latest != "https://github.com/mewisme/codemcp/releases/latest/download/"+asset {
		t.Fatalf("latest URL = %q", latest)
	}
	if strings.Contains(exact, "/releases/latest/") || strings.Contains(latest, "/releases/download/v1.4.0/") {
		t.Fatalf("exact/latest URL contracts overlapped: exact=%q latest=%q", exact, latest)
	}
	if _, err := ExactReleaseAssetURL(DefaultOwner, DefaultRepo, "invalid", asset); err == nil {
		t.Fatal("exact URL accepted invalid release identity")
	}
	for _, invalid := range []string{"", "../asset", "dir/asset"} {
		if _, err := LatestReleaseAssetURL(DefaultOwner, DefaultRepo, invalid); err == nil {
			t.Fatalf("latest URL accepted invalid asset %q", invalid)
		}
	}
}
