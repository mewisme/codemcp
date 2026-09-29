package update

import (
	"fmt"
	"runtime"
	"strings"
)

const (
	DefaultOwner          = "mewisme"
	PackageName           = "codemcp"
	DefaultRepo           = PackageName
	HomebrewCask          = PackageName
	ScoopPackage          = "mew/" + PackageName
	ChecksumName          = PackageName + "_checksums.txt"
	ChecksumSignatureName = ChecksumName + ".sigstore.json"
)

type ReleasePlatform struct {
	OS               string `json:"os"`
	Arch             string `json:"arch"`
	ArchiveExtension string `json:"archive_extension"`
	BinaryName       string `json:"binary_name"`
}

type ReleaseLayout struct {
	PackageName   string            `json:"package_name"`
	ChecksumName  string            `json:"checksum_name"`
	SignatureName string            `json:"signature_name"`
	Platforms     []ReleasePlatform `json:"platforms"`
}

var primaryReleasePlatforms = []ReleasePlatform{
	{OS: "linux", Arch: "amd64", ArchiveExtension: ".tar.gz", BinaryName: "cm"},
	{OS: "linux", Arch: "arm64", ArchiveExtension: ".tar.gz", BinaryName: "cm"},
	{OS: "darwin", Arch: "amd64", ArchiveExtension: ".tar.gz", BinaryName: "cm"},
	{OS: "darwin", Arch: "arm64", ArchiveExtension: ".tar.gz", BinaryName: "cm"},
	{OS: "windows", Arch: "amd64", ArchiveExtension: ".zip", BinaryName: "cm.exe"},
	{OS: "windows", Arch: "arm64", ArchiveExtension: ".zip", BinaryName: "cm.exe"},
}

func PrimaryReleaseLayout() ReleaseLayout {
	return ReleaseLayout{
		PackageName: PackageName, ChecksumName: ChecksumName, SignatureName: ChecksumSignatureName,
		Platforms: append([]ReleasePlatform(nil), primaryReleasePlatforms...),
	}
}

func ReleasePlatformFor(goos, goarch string) (ReleasePlatform, bool) {
	goos, goarch = strings.TrimSpace(goos), strings.TrimSpace(goarch)
	for _, platform := range primaryReleasePlatforms {
		if platform.OS == goos && platform.Arch == goarch {
			return platform, true
		}
	}
	return ReleasePlatform{}, false
}

type Release struct {
	Version       string
	ArchiveName   string
	ArchiveURL    string
	ChecksumName  string
	ChecksumURL   string
	SignatureName string
	SignatureURL  string
}

func AssetName(version, goos, goarch string) (string, error) {
	version, err := NormalizeVersion(version)
	if err != nil {
		return "", err
	}
	platform, ok := ReleasePlatformFor(goos, goarch)
	if !ok {
		return "", fmt.Errorf("unsupported update platform %q", strings.TrimSpace(goos)+"/"+strings.TrimSpace(goarch))
	}
	return fmt.Sprintf("%s_%s_%s_%s%s", PackageName, strings.TrimPrefix(version, "v"), platform.OS, platform.Arch, platform.ArchiveExtension), nil
}

func CurrentAssetName(version string) (string, error) {
	return AssetName(version, runtime.GOOS, runtime.GOARCH)
}

func BinaryName(goos, goarch string) (string, error) {
	platform, ok := ReleasePlatformFor(goos, goarch)
	if !ok {
		return "", fmt.Errorf("unsupported update platform %q", strings.TrimSpace(goos)+"/"+strings.TrimSpace(goarch))
	}
	return platform.BinaryName, nil
}
