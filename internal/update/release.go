package update

import (
	"fmt"
	"net/url"
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

type ArtifactKind string

const (
	ArtifactArchive ArtifactKind = "archive"
	ArtifactDebian  ArtifactKind = "deb"
	ArtifactRPM     ArtifactKind = "rpm"
	ArtifactSetup   ArtifactKind = "setup"
)

type ReleaseArtifact struct {
	Kind           ArtifactKind `json:"kind"`
	OS             string       `json:"os"`
	Arch           string       `json:"arch"`
	FilenameSuffix string       `json:"filename_suffix"`
	BinaryName     string       `json:"binary_name"`
}

type ReleaseLayout struct {
	PackageName   string            `json:"package_name"`
	ChecksumName  string            `json:"checksum_name"`
	SignatureName string            `json:"signature_name"`
	Platforms     []ReleasePlatform `json:"platforms"`
	Artifacts     []ReleaseArtifact `json:"artifacts"`
}

var primaryReleaseArtifacts = []ReleaseArtifact{
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

func PrimaryReleaseLayout() ReleaseLayout {
	platforms := make([]ReleasePlatform, 0, 4)
	for _, artifact := range primaryReleaseArtifacts {
		if artifact.Kind != ArtifactArchive {
			continue
		}
		platforms = append(platforms, ReleasePlatform{
			OS: artifact.OS, Arch: artifact.Arch, ArchiveExtension: artifact.FilenameSuffix, BinaryName: artifact.BinaryName,
		})
	}
	return ReleaseLayout{
		PackageName: PackageName, ChecksumName: ChecksumName, SignatureName: ChecksumSignatureName,
		Platforms: platforms, Artifacts: append([]ReleaseArtifact(nil), primaryReleaseArtifacts...),
	}
}

func ReleasePlatformFor(goos, goarch string) (ReleasePlatform, bool) {
	artifact, ok := ArtifactFor(ArtifactArchive, goos, goarch)
	if !ok {
		return ReleasePlatform{}, false
	}
	return ReleasePlatform{
		OS: artifact.OS, Arch: artifact.Arch, ArchiveExtension: artifact.FilenameSuffix, BinaryName: artifact.BinaryName,
	}, true
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

type PackageRelease struct {
	Version       string
	Kind          ArtifactKind
	PackageName   string
	PackageURL    string
	ChecksumName  string
	ChecksumURL   string
	SignatureName string
	SignatureURL  string
}

func ArtifactFor(kind ArtifactKind, goos, goarch string) (ReleaseArtifact, bool) {
	kind, goos, goarch = ArtifactKind(strings.TrimSpace(string(kind))), strings.TrimSpace(goos), strings.TrimSpace(goarch)
	for _, artifact := range primaryReleaseArtifacts {
		if artifact.Kind == kind && artifact.OS == goos && artifact.Arch == goarch {
			return artifact, true
		}
	}
	return ReleaseArtifact{}, false
}

func ArtifactName(kind ArtifactKind, goos, goarch string) (string, error) {
	artifact, ok := ArtifactFor(kind, goos, goarch)
	if !ok {
		return "", fmt.Errorf("unsupported release artifact %q for %q", strings.TrimSpace(string(kind)), strings.TrimSpace(goos)+"/"+strings.TrimSpace(goarch))
	}
	return fmt.Sprintf("%s_%s_%s%s", PackageName, artifact.OS, artifact.Arch, artifact.FilenameSuffix), nil
}

func ArchiveName(goos, goarch string) (string, error) {
	return ArtifactName(ArtifactArchive, goos, goarch)
}

func CurrentArchiveName() (string, error) {
	return ArchiveName(runtime.GOOS, runtime.GOARCH)
}

func BinaryName(goos, goarch string) (string, error) {
	platform, ok := ReleasePlatformFor(goos, goarch)
	if !ok {
		return "", fmt.Errorf("unsupported update platform %q", strings.TrimSpace(goos)+"/"+strings.TrimSpace(goarch))
	}
	return platform.BinaryName, nil
}

func ExactReleaseAssetURL(owner, repo, version, assetName string) (string, error) {
	owner, repo = strings.TrimSpace(owner), strings.TrimSpace(repo)
	if !validReleasePathComponent(owner) || !validReleasePathComponent(repo) {
		return "", fmt.Errorf("invalid GitHub repository %q", owner+"/"+repo)
	}
	version, err := NormalizeVersion(version)
	if err != nil {
		return "", err
	}
	assetName, err = validateReleaseAssetName(assetName)
	if err != nil {
		return "", err
	}
	return "https://github.com/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) +
		"/releases/download/" + url.PathEscape(version) + "/" + url.PathEscape(assetName), nil
}

func LatestReleaseAssetURL(owner, repo, assetName string) (string, error) {
	owner, repo = strings.TrimSpace(owner), strings.TrimSpace(repo)
	if !validReleasePathComponent(owner) || !validReleasePathComponent(repo) {
		return "", fmt.Errorf("invalid GitHub repository %q", owner+"/"+repo)
	}
	assetName, err := validateReleaseAssetName(assetName)
	if err != nil {
		return "", err
	}
	return "https://github.com/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) +
		"/releases/latest/download/" + url.PathEscape(assetName), nil
}

func validReleasePathComponent(value string) bool {
	return value != "" && value != "." && value != ".." && !strings.ContainsAny(value, "/\\?#")
}

func validateReleaseAssetName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." || strings.ContainsAny(value, "/\\?#") {
		return "", fmt.Errorf("invalid release asset name %q", value)
	}
	return value, nil
}
