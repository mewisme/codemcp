package bundle024

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	SourceRelease      = "0.2.24"
	Version            = 1
	magic              = "CGMCFG\x00\x01"
	maxBundleBytes     = 256 << 20
	maxStateBytes      = 128 << 20
	maxBundleFileBytes = 64 << 20
	bundleKeyMaterial  = "chatgpt-mcp portable config bundle v1 / mewis.me"
)

type Platform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
	Home string `json:"home,omitempty"`
}

type file struct {
	Path string `json:"path"`
	Mode uint32 `json:"mode,omitempty"`
	Data []byte `json:"data"`
}

type bundle struct {
	Version   int               `json:"version"`
	CreatedAt time.Time         `json:"created_at"`
	Source    Platform          `json:"source"`
	Files     []file            `json:"files"`
	Secrets   map[string]string `json:"secrets,omitempty"`
}

type Inspection struct {
	SourceRelease string    `json:"source_release"`
	Version       int       `json:"version"`
	CreatedAt     time.Time `json:"created_at"`
	SourceOS      string    `json:"source_os"`
	SourceArch    string    `json:"source_arch"`
	FileCount     int       `json:"file_count"`
	SecretCount   int       `json:"secret_count"`
	FilesBytes    int64     `json:"files_bytes"`
	Paths         []string  `json:"paths"`
}

func Inspect(filePath string) (Inspection, error) {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return Inspection{}, errors.New("released config bundle path is required")
	}
	absolute, err := filepath.Abs(filePath)
	if err != nil {
		return Inspection{}, fmt.Errorf("resolve released config bundle: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return Inspection{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return Inspection{}, errors.New("released config bundle must be a regular non-symlink file")
	}
	if info.Size() > maxBundleBytes {
		return Inspection{}, errors.New("released config bundle exceeds size limit")
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		return Inspection{}, err
	}
	value, err := decode(data)
	if err != nil {
		return Inspection{}, err
	}
	if value.Version != Version {
		return Inspection{}, fmt.Errorf("unsupported released config bundle version: %d", value.Version)
	}
	if strings.TrimSpace(value.Source.OS) == "" {
		return Inspection{}, errors.New("released config bundle source platform is missing")
	}
	paths := make([]string, 0, len(value.Files))
	seen := map[string]struct{}{}
	var total int64
	for _, item := range value.Files {
		clean, ok := safeRelative(item.Path)
		if !ok {
			return Inspection{}, fmt.Errorf("released config bundle contains unsafe path: %q", item.Path)
		}
		if len(item.Data) > maxBundleFileBytes {
			return Inspection{}, fmt.Errorf("released config bundle file exceeds size limit: %s", clean)
		}
		if _, exists := seen[clean]; exists {
			return Inspection{}, fmt.Errorf("released config bundle contains duplicate path: %s", clean)
		}
		seen[clean] = struct{}{}
		paths = append(paths, clean)
		total += int64(len(item.Data))
		if total > maxStateBytes {
			return Inspection{}, errors.New("released config bundle state exceeds size limit")
		}
	}
	sort.Strings(paths)
	return Inspection{
		SourceRelease: SourceRelease,
		Version:       value.Version,
		CreatedAt:     value.CreatedAt.UTC(),
		SourceOS:      strings.TrimSpace(value.Source.OS),
		SourceArch:    strings.TrimSpace(value.Source.Arch),
		FileCount:     len(value.Files),
		SecretCount:   len(value.Secrets),
		FilesBytes:    total,
		Paths:         paths,
	}, nil
}

func decode(data []byte) (bundle, error) {
	if len(data) < len(magic) || string(data[:len(magic)]) != magic {
		return bundle{}, errors.New("invalid released config bundle header")
	}
	aead, err := bundleAEAD()
	if err != nil {
		return bundle{}, err
	}
	offset := len(magic)
	if len(data) < offset+aead.NonceSize()+aead.Overhead() {
		return bundle{}, errors.New("released config bundle is truncated")
	}
	nonce := data[offset : offset+aead.NonceSize()]
	ciphertext := data[offset+aead.NonceSize():]
	compressed, err := aead.Open(nil, nonce, ciphertext, []byte(magic))
	if err != nil {
		return bundle{}, errors.New("released config bundle authentication failed")
	}
	zipper, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return bundle{}, fmt.Errorf("open released config bundle payload: %w", err)
	}
	defer zipper.Close()
	plain, err := io.ReadAll(io.LimitReader(zipper, maxBundleBytes+1))
	if err != nil {
		return bundle{}, err
	}
	if len(plain) > maxBundleBytes {
		return bundle{}, errors.New("released config bundle payload exceeds size limit")
	}
	var value bundle
	if err := json.Unmarshal(plain, &value); err != nil {
		return bundle{}, fmt.Errorf("decode released config bundle payload: %w", err)
	}
	if value.Files == nil {
		value.Files = []file{}
	}
	if value.Secrets == nil {
		value.Secrets = map[string]string{}
	}
	return value, nil
}

func bundleAEAD() (cipher.AEAD, error) {
	key := sha256.Sum256([]byte(bundleKeyMaterial))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func safeRelative(value string) (string, bool) {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	if value == "" || strings.HasPrefix(value, "/") {
		return "", false
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || windowsDrivePath(clean) {
		return "", false
	}
	return clean, true
}

func windowsDrivePath(value string) bool {
	return len(value) >= 2 && value[1] == ':' && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z'))
}
