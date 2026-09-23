package update

import (
	"fmt"
	"runtime"
	"strings"
)

const (
	DefaultOwner          = "mewisme"
	DefaultRepo           = "codemcp"
	ChecksumName          = "checksums.txt"
	ChecksumSignatureName = "checksums.txt.sigstore.json"
)

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
	if goos != "linux" && goos != "darwin" && goos != "windows" {
		return "", fmt.Errorf("unsupported update OS %q", goos)
	}
	if goarch != "amd64" && goarch != "arm64" {
		return "", fmt.Errorf("unsupported update architecture %q", goarch)
	}
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("chatgpt-mcp_%s_%s_%s%s", strings.TrimPrefix(version, "v"), goos, goarch, ext), nil
}

func CurrentAssetName(version string) (string, error) {
	return AssetName(version, runtime.GOOS, runtime.GOARCH)
}
