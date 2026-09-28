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
	maxBundleEntries   = 4096
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

type MaterializeResult struct {
	DestinationRoot string     `json:"destination_root"`
	BundleSHA256    string     `json:"bundle_sha256"`
	Inspection      Inspection `json:"inspection"`
}

func Inspect(filePath string) (Inspection, error) {
	_, inspection, err := readBundle(filePath)
	return inspection, err
}

func Materialize(filePath, destinationRoot string) (result MaterializeResult, retErr error) {
	value, inspection, err := readBundle(filePath)
	if err != nil {
		return MaterializeResult{}, err
	}
	destinationRoot = strings.TrimSpace(destinationRoot)
	if destinationRoot == "" {
		return MaterializeResult{}, errors.New("released config bundle materialization root is required")
	}
	absolute, err := filepath.Abs(destinationRoot)
	if err != nil {
		return MaterializeResult{}, fmt.Errorf("resolve released config bundle materialization root: %w", err)
	}
	absolute = filepath.Clean(absolute)
	if _, err := os.Lstat(absolute); err == nil {
		return MaterializeResult{}, errors.New("released config bundle materialization root already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return MaterializeResult{}, err
	}
	if err := os.MkdirAll(absolute, 0700); err != nil {
		return MaterializeResult{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(absolute)
		}
	}()

	for _, item := range value.Files {
		relative, ok := safeRelative(item.Path)
		if !ok {
			return MaterializeResult{}, fmt.Errorf("released config bundle contains unsafe path: %q", item.Path)
		}
		if relative == ".chatgpt-mcp-root" {
			if strings.TrimSpace(string(item.Data)) != "chatgpt-mcp" {
				return MaterializeResult{}, errors.New("released config bundle contains invalid root marker")
			}
			continue
		}
		destination := filepath.Join(absolute, filepath.FromSlash(relative))
		if !withinRoot(absolute, destination) {
			return MaterializeResult{}, fmt.Errorf("released config bundle path escapes materialization root: %s", relative)
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return MaterializeResult{}, err
		}
		mode := os.FileMode(item.Mode).Perm()
		if mode == 0 {
			mode = 0600
		}
		if err := os.WriteFile(destination, item.Data, mode); err != nil {
			return MaterializeResult{}, err
		}
	}
	if err := os.WriteFile(filepath.Join(absolute, ".chatgpt-mcp-root"), []byte("chatgpt-mcp\n"), 0600); err != nil {
		return MaterializeResult{}, err
	}
	serviceName := legacyService(absolute)
	secretRoot := filepath.Join(absolute, "state", "secrets")
	if len(value.Secrets) > 0 {
		if err := os.MkdirAll(secretRoot, 0700); err != nil {
			return MaterializeResult{}, err
		}
	}
	for account, secret := range value.Secrets {
		account = strings.TrimSpace(account)
		if account == "" {
			return MaterializeResult{}, errors.New("released config bundle contains empty secret account")
		}
		digest := sha256.Sum256([]byte(serviceName + "\x00" + account))
		path := filepath.Join(secretRoot, fmt.Sprintf("%x.secret", digest[:]))
		if err := os.WriteFile(path, []byte(secret), 0600); err != nil {
			return MaterializeResult{}, err
		}
	}
	sha, err := fileSHA256(filePath)
	if err != nil {
		return MaterializeResult{}, err
	}
	committed = true
	return MaterializeResult{DestinationRoot: absolute, BundleSHA256: sha, Inspection: inspection}, nil
}

func readBundle(filePath string) (bundle, Inspection, error) {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return bundle{}, Inspection{}, errors.New("released config bundle path is required")
	}
	absolute, err := filepath.Abs(filePath)
	if err != nil {
		return bundle{}, Inspection{}, fmt.Errorf("resolve released config bundle: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return bundle{}, Inspection{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return bundle{}, Inspection{}, errors.New("released config bundle must be a regular non-symlink file")
	}
	if info.Size() > maxBundleBytes {
		return bundle{}, Inspection{}, errors.New("released config bundle exceeds size limit")
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		return bundle{}, Inspection{}, err
	}
	value, err := decode(data)
	if err != nil {
		return bundle{}, Inspection{}, err
	}
	if value.Version != Version {
		return bundle{}, Inspection{}, fmt.Errorf("unsupported released config bundle version: %d", value.Version)
	}
	if strings.TrimSpace(value.Source.OS) == "" {
		return bundle{}, Inspection{}, errors.New("released config bundle source platform is missing")
	}
	if len(value.Files) > maxBundleEntries || len(value.Secrets) > maxBundleEntries {
		return bundle{}, Inspection{}, fmt.Errorf("released config bundle exceeds %d entries", maxBundleEntries)
	}
	paths := make([]string, 0, len(value.Files))
	seen := map[string]struct{}{}
	var total int64
	for _, item := range value.Files {
		clean, ok := safeRelative(item.Path)
		if !ok {
			return bundle{}, Inspection{}, fmt.Errorf("released config bundle contains unsafe path: %q", item.Path)
		}
		if len(item.Data) > maxBundleFileBytes {
			return bundle{}, Inspection{}, fmt.Errorf("released config bundle file exceeds size limit: %s", clean)
		}
		if _, exists := seen[clean]; exists {
			return bundle{}, Inspection{}, fmt.Errorf("released config bundle contains duplicate path: %s", clean)
		}
		seen[clean] = struct{}{}
		paths = append(paths, clean)
		total += int64(len(item.Data))
		if total > maxStateBytes {
			return bundle{}, Inspection{}, errors.New("released config bundle state exceeds size limit")
		}
	}
	sort.Strings(paths)
	inspection := Inspection{
		SourceRelease: SourceRelease,
		Version:       value.Version,
		CreatedAt:     value.CreatedAt.UTC(),
		SourceOS:      strings.TrimSpace(value.Source.OS),
		SourceArch:    strings.TrimSpace(value.Source.Arch),
		FileCount:     len(value.Files),
		SecretCount:   len(value.Secrets),
		FilesBytes:    total,
		Paths:         paths,
	}
	return value, inspection, nil
}

func legacyService(root string) string {
	digest := sha256.Sum256([]byte(filepath.Clean(root)))
	return "chatgpt-mcp/" + fmt.Sprintf("%x", digest[:8])
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(file, maxBundleBytes+1)); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func withinRoot(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
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
