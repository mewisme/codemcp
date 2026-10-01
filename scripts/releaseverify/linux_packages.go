package releaseverify

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	updatepkg "go.mewis.me/codemcp/internal/update"
)

const (
	linuxPackageDescription = "Secure, workspace-bound bridge between AI agents and your machine."
	linuxPackageMaintainer  = "mewisme"
	linuxPackageVendor      = "mewisme"
)

type goreleaserMetadata struct {
	ProjectName string `json:"project_name"`
	Tag         string `json:"tag"`
	Version     string `json:"version"`
}

type linuxPackageInfo struct {
	Name        string
	Version     string
	Arch        string
	Maintainer  string
	Homepage    string
	Description string
	Vendor      string
	License     string
	Binary      []byte
}

func verifyLinuxPackages(ctx context.Context, root string) error {
	metaData, err := os.ReadFile(filepath.Join(root, "metadata.json"))
	if err != nil {
		return fmt.Errorf("read release metadata for linux packages: %w", err)
	}
	var meta goreleaserMetadata
	if err := json.Unmarshal(metaData, &meta); err != nil {
		return fmt.Errorf("decode release metadata for linux packages: %w", err)
	}
	if meta.ProjectName != updatepkg.PackageName || strings.TrimSpace(meta.Version) == "" || strings.TrimSpace(meta.Tag) == "" {
		return fmt.Errorf("release metadata is incomplete for linux package verification: %+v", meta)
	}
	type expectation struct {
		kind        updatepkg.ArtifactKind
		format      string
		goarch      string
		packageArch string
	}
	expectations := []expectation{
		{updatepkg.ArtifactDebian, "deb", "amd64", "amd64"},
		{updatepkg.ArtifactDebian, "deb", "arm64", "arm64"},
		{updatepkg.ArtifactRPM, "rpm", "amd64", "x86_64"},
		{updatepkg.ArtifactRPM, "rpm", "arm64", "aarch64"},
	}
	expectedNames := make(map[string]struct{}, len(expectations))
	for _, expected := range expectations {
		name, err := updatepkg.ArtifactName(expected.kind, "linux", expected.goarch)
		if err != nil {
			return err
		}
		expectedNames[name] = struct{}{}
		path := filepath.Join(root, name)
		var info linuxPackageInfo
		switch expected.format {
		case "deb":
			info, err = inspectDebPackage(path)
		case "rpm":
			info, err = inspectRPMPackage(path)
		default:
			err = fmt.Errorf("unsupported linux package format %q", expected.format)
		}
		if err != nil {
			return fmt.Errorf("verify %s: %w", name, err)
		}
		if err := validateLinuxPackageInfo(info, expected.packageArch, meta.Version); err != nil {
			return fmt.Errorf("verify %s metadata: %w", name, err)
		}
		if runtime.GOOS == "linux" && runtime.GOARCH == expected.goarch {
			if err := verifyPackagedBinaryVersion(ctx, info.Binary, meta.Tag, expected.format); err != nil {
				return fmt.Errorf("verify %s binary: %w", name, err)
			}
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, updatepkg.PackageName+"_") && (strings.HasSuffix(name, ".deb") || strings.HasSuffix(name, ".rpm")) {
			if _, ok := expectedNames[name]; !ok {
				return fmt.Errorf("unexpected published linux package %q", name)
			}
		}
	}
	return nil
}

func validateLinuxPackageInfo(info linuxPackageInfo, expectedArch, releaseVersion string) error {
	if info.Name != updatepkg.PackageName {
		return fmt.Errorf("package name = %q, want %q", info.Name, updatepkg.PackageName)
	}
	if canonicalPackageVersion(info.Version) != canonicalPackageVersion(releaseVersion) {
		return fmt.Errorf("package version = %q, want release version %q", info.Version, releaseVersion)
	}
	if info.Arch != expectedArch {
		return fmt.Errorf("package architecture = %q, want %q", info.Arch, expectedArch)
	}
	if info.Homepage != ExpectedGitRemote {
		return fmt.Errorf("package homepage = %q, want %q", info.Homepage, ExpectedGitRemote)
	}
	if info.Description != linuxPackageDescription {
		return fmt.Errorf("package description = %q, want %q", info.Description, linuxPackageDescription)
	}
	if !validLinuxPackageMaintainer(info.Maintainer) {
		return fmt.Errorf("package maintainer = %q, want GitHub noreply identity for %s", info.Maintainer, linuxPackageMaintainer)
	}
	if info.Vendor != "" && info.Vendor != linuxPackageVendor {
		return fmt.Errorf("package vendor = %q, want %q", info.Vendor, linuxPackageVendor)
	}
	if info.License != "" && info.License != "MIT" {
		return fmt.Errorf("package license = %q, want MIT", info.License)
	}
	if len(info.Binary) == 0 {
		return errors.New("package does not contain a non-empty /usr/bin/cm")
	}
	return nil
}

func validLinuxPackageMaintainer(value string) bool {
	value = strings.TrimSpace(value)
	prefix := linuxPackageMaintainer + " <"
	suffix := "+" + linuxPackageMaintainer + "@users.noreply.github.com>"
	if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, suffix) {
		return false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(value, prefix), suffix)
	if id == "" {
		return false
	}
	for _, char := range id {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func canonicalPackageVersion(value string) string {
	return strings.ReplaceAll(strings.TrimSpace(value), "~", "-")
}

func verifyPackagedBinaryVersion(ctx context.Context, binaryData []byte, releaseTag, format string) error {
	dir, err := os.MkdirTemp("", "cm-package-binary-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "cm")
	if err := os.WriteFile(path, binaryData, 0755); err != nil {
		return err
	}
	configRoot := filepath.Join(dir, "config")
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0755); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, path, "--version")
	command.Env = replaceEnv(os.Environ(), map[string]string{
		"CM_CONFIG_DIR": configRoot,
		"HOME":          home,
		"USERPROFILE":   home,
	})
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("execute packaged %s binary: %w: %s", format, err, strings.TrimSpace(string(output)))
	}
	want := "cm version " + strings.TrimPrefix(strings.TrimSpace(releaseTag), "v")
	if !strings.Contains(string(output), want) {
		return fmt.Errorf("version output = %q, want %q", strings.TrimSpace(string(output)), want)
	}
	if entries, readErr := os.ReadDir(configRoot); readErr == nil && len(entries) != 0 {
		return errors.New("version probe initialized CodeMCP config state")
	} else if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return fmt.Errorf("inspect isolated config root: %w", readErr)
	}
	return nil
}

func inspectDebPackage(path string) (linuxPackageInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return linuxPackageInfo{}, err
	}
	members, err := readArMembers(data)
	if err != nil {
		return linuxPackageInfo{}, err
	}
	controlData, ok := members["control.tar.gz"]
	if !ok {
		return linuxPackageInfo{}, errors.New("deb package is missing control.tar.gz")
	}
	dataArchive, ok := members["data.tar.gz"]
	if !ok {
		return linuxPackageInfo{}, errors.New("deb package is missing data.tar.gz")
	}
	control, err := readDebControl(controlData)
	if err != nil {
		return linuxPackageInfo{}, err
	}
	binaryData, err := readTarPackageBinary(dataArchive)
	if err != nil {
		return linuxPackageInfo{}, err
	}
	return linuxPackageInfo{
		Name:        control["Package"],
		Version:     control["Version"],
		Arch:        control["Architecture"],
		Maintainer:  control["Maintainer"],
		Homepage:    control["Homepage"],
		Description: control["Description"],
		Binary:      binaryData,
	}, nil
}

func readArMembers(data []byte) (map[string][]byte, error) {
	if len(data) < 8 || string(data[:8]) != "!<arch>\n" {
		return nil, errors.New("invalid ar package header")
	}
	result := map[string][]byte{}
	for offset := 8; offset < len(data); {
		if offset+60 > len(data) {
			return nil, errors.New("truncated ar member header")
		}
		header := data[offset : offset+60]
		if string(header[58:60]) != "`\n" {
			return nil, errors.New("invalid ar member trailer")
		}
		name := strings.TrimSuffix(strings.TrimSpace(string(header[:16])), "/")
		size, err := strconv.ParseInt(strings.TrimSpace(string(header[48:58])), 10, 64)
		if err != nil || size < 0 {
			return nil, errors.New("invalid ar member size")
		}
		start := offset + 60
		end := start + int(size)
		if end < start || end > len(data) {
			return nil, errors.New("truncated ar member data")
		}
		result[name] = data[start:end]
		offset = end
		if offset%2 != 0 {
			offset++
		}
	}
	return result, nil
}

func readDebControl(compressed []byte) (map[string]string, error) {
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("open deb control archive: %w", err)
	}
	defer reader.Close()
	tarReader := tar.NewReader(reader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		name, err := normalizedPackagePath(header.Name)
		if err != nil {
			return nil, err
		}
		if name != "control" {
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != 0 {
			return nil, errors.New("deb control entry is not regular")
		}
		content, err := io.ReadAll(io.LimitReader(tarReader, 1<<20))
		if err != nil {
			return nil, err
		}
		return parseDebControlFields(string(content)), nil
	}
	return nil, errors.New("deb control archive is missing control metadata")
}

func parseDebControlFields(content string) map[string]string {
	fields := map[string]string{}
	var current string
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, " ") && current != "" {
			fields[current] += "\n" + strings.TrimSpace(line)
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			current = ""
			continue
		}
		current = strings.TrimSpace(key)
		fields[current] = strings.TrimSpace(value)
	}
	return fields
}

func readTarPackageBinary(compressed []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("open deb data archive: %w", err)
	}
	defer reader.Close()
	tarReader := tar.NewReader(reader)
	var binaryData []byte
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		name, err := normalizedPackagePath(header.Name)
		if err != nil {
			return nil, err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if name != "." && name != "usr" && name != "usr/bin" {
				return nil, fmt.Errorf("deb package contains unexpected directory %q", header.Name)
			}
		case tar.TypeReg, 0:
			if name != "usr/bin/cm" {
				return nil, fmt.Errorf("deb package contains unexpected file %q", header.Name)
			}
			if binaryData != nil {
				return nil, errors.New("deb package contains duplicate /usr/bin/cm")
			}
			if header.Uid != 0 || header.Gid != 0 || header.Mode&0111 == 0 {
				return nil, errors.New("deb /usr/bin/cm ownership or mode is not package-safe")
			}
			binaryData, err = io.ReadAll(io.LimitReader(tarReader, 256<<20))
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("deb package contains unsupported entry type for %q", header.Name)
		}
	}
	if len(binaryData) == 0 {
		return nil, errors.New("deb package is missing non-empty /usr/bin/cm")
	}
	return binaryData, nil
}

type rpmHeader struct {
	values map[uint32]string
	end    int
}

func inspectRPMPackage(path string) (linuxPackageInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return linuxPackageInfo{}, err
	}
	if len(data) < 96 || !bytes.Equal(data[:4], []byte{0xed, 0xab, 0xee, 0xdb}) {
		return linuxPackageInfo{}, errors.New("invalid rpm lead")
	}
	signature, err := parseRPMHeader(data, 96)
	if err != nil {
		return linuxPackageInfo{}, fmt.Errorf("parse rpm signature header: %w", err)
	}
	mainOffset := (signature.end + 7) &^ 7
	main, err := parseRPMHeader(data, mainOffset)
	if err != nil {
		return linuxPackageInfo{}, fmt.Errorf("parse rpm main header: %w", err)
	}
	if main.values[1124] != "cpio" || main.values[1125] != "gzip" {
		return linuxPackageInfo{}, fmt.Errorf("rpm payload = %q/%q, want cpio/gzip", main.values[1124], main.values[1125])
	}
	if main.end > len(data) {
		return linuxPackageInfo{}, errors.New("rpm payload offset exceeds package size")
	}
	binaryData, err := readCPIOPackageBinary(data[main.end:])
	if err != nil {
		return linuxPackageInfo{}, err
	}
	return linuxPackageInfo{
		Name:        main.values[1000],
		Version:     main.values[1001],
		Arch:        main.values[1022],
		Maintainer:  main.values[1015],
		Homepage:    main.values[1020],
		Description: main.values[1005],
		Vendor:      main.values[1011],
		License:     main.values[1014],
		Binary:      binaryData,
	}, nil
}

func parseRPMHeader(data []byte, offset int) (rpmHeader, error) {
	if offset < 0 || offset+16 > len(data) {
		return rpmHeader{}, errors.New("truncated rpm header")
	}
	header := data[offset:]
	if !bytes.Equal(header[:4], []byte{0x8e, 0xad, 0xe8, 0x01}) {
		return rpmHeader{}, errors.New("invalid rpm header magic")
	}
	count := int(binary.BigEndian.Uint32(header[8:12]))
	storeSize := int(binary.BigEndian.Uint32(header[12:16]))
	if count < 0 || count > 1<<16 || storeSize < 0 || storeSize > 16<<20 {
		return rpmHeader{}, errors.New("rpm header bounds are invalid")
	}
	indexStart := offset + 16
	storeStart := indexStart + count*16
	end := storeStart + storeSize
	if storeStart < indexStart || end < storeStart || end > len(data) {
		return rpmHeader{}, errors.New("truncated rpm header store")
	}
	values := map[uint32]string{}
	for i := 0; i < count; i++ {
		entry := data[indexStart+i*16 : indexStart+(i+1)*16]
		tag := binary.BigEndian.Uint32(entry[:4])
		valueType := binary.BigEndian.Uint32(entry[4:8])
		valueOffset := int(binary.BigEndian.Uint32(entry[8:12]))
		if valueType != 6 || valueOffset < 0 || valueOffset >= storeSize {
			continue
		}
		start := storeStart + valueOffset
		relativeEnd := bytes.IndexByte(data[start:end], 0)
		if relativeEnd < 0 {
			return rpmHeader{}, fmt.Errorf("rpm string tag %d is unterminated", tag)
		}
		values[tag] = string(data[start : start+relativeEnd])
	}
	return rpmHeader{values: values, end: end}, nil
}

func readCPIOPackageBinary(payload []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("open rpm payload: %w", err)
	}
	defer reader.Close()
	var binaryData []byte
	for {
		header := make([]byte, 110)
		if _, err := io.ReadFull(reader, header); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("read rpm cpio header: %w", err)
		}
		magic := string(header[:6])
		if magic != "070701" && magic != "070702" {
			return nil, fmt.Errorf("unsupported rpm cpio magic %q", magic)
		}
		mode, err := parseCPIOHex(header[14:22])
		if err != nil {
			return nil, err
		}
		uid, err := parseCPIOHex(header[22:30])
		if err != nil {
			return nil, err
		}
		gid, err := parseCPIOHex(header[30:38])
		if err != nil {
			return nil, err
		}
		fileSize, err := parseCPIOHex(header[54:62])
		if err != nil {
			return nil, err
		}
		nameSize, err := parseCPIOHex(header[94:102])
		if err != nil || nameSize == 0 || nameSize > 1<<20 {
			return nil, errors.New("invalid rpm cpio name size")
		}
		nameBytes := make([]byte, nameSize)
		if _, err := io.ReadFull(reader, nameBytes); err != nil {
			return nil, err
		}
		name := strings.TrimSuffix(string(nameBytes), "\x00")
		if err := discardPadding(reader, int64(110+nameSize)); err != nil {
			return nil, err
		}
		if name == "TRAILER!!!" {
			break
		}
		normalized, err := normalizedPackagePath(name)
		if err != nil {
			return nil, err
		}
		fileType := mode & 0170000
		switch fileType {
		case 0040000:
			if normalized != "." && normalized != "usr" && normalized != "usr/bin" {
				return nil, fmt.Errorf("rpm package contains unexpected directory %q", name)
			}
			if _, err := io.CopyN(io.Discard, reader, int64(fileSize)); err != nil {
				return nil, err
			}
		case 0100000:
			if normalized != "usr/bin/cm" {
				return nil, fmt.Errorf("rpm package contains unexpected file %q", name)
			}
			if binaryData != nil {
				return nil, errors.New("rpm package contains duplicate /usr/bin/cm")
			}
			if uid != 0 || gid != 0 || mode&0111 == 0 {
				return nil, errors.New("rpm /usr/bin/cm ownership or mode is not package-safe")
			}
			if fileSize == 0 || fileSize > 256<<20 {
				return nil, errors.New("rpm /usr/bin/cm has invalid size")
			}
			binaryData = make([]byte, fileSize)
			if _, err := io.ReadFull(reader, binaryData); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("rpm package contains unsupported entry type for %q", name)
		}
		if err := discardPadding(reader, int64(fileSize)); err != nil {
			return nil, err
		}
	}
	if len(binaryData) == 0 {
		return nil, errors.New("rpm package is missing non-empty /usr/bin/cm")
	}
	return binaryData, nil
}

func parseCPIOHex(value []byte) (uint64, error) {
	parsed, err := strconv.ParseUint(string(value), 16, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid cpio numeric field %q", string(value))
	}
	return parsed, nil
}

func discardPadding(reader io.Reader, consumed int64) error {
	padding := (4 - consumed%4) % 4
	if padding == 0 {
		return nil
	}
	_, err := io.CopyN(io.Discard, reader, padding)
	return err
}

func normalizedPackagePath(name string) (string, error) {
	name = filepath.ToSlash(strings.TrimSpace(name))
	if strings.HasPrefix(name, "//") || strings.Contains(name, ":") {
		return "", fmt.Errorf("unsafe package path %q", name)
	}
	name = strings.TrimPrefix(name, "/")
	name = strings.TrimPrefix(name, "./")
	name = strings.TrimSuffix(name, "/")
	if name == "" {
		return ".", nil
	}
	clean := filepath.ToSlash(filepath.Clean(name))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("unsafe package path %q", name)
	}
	return clean, nil
}
