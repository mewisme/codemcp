package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

const (
	maxExtractedBinarySize int64 = 256 << 20
	maxArchiveEntries            = 4096
)

func ExtractBinary(archivePath, destinationDir, archiveName string) (string, error) {
	return ExtractBinaryContext(context.Background(), archivePath, destinationDir, archiveName)
}

func ExtractBinaryContext(ctx context.Context, archivePath, destinationDir, archiveName string) (string, error) {
	return extractBinaryForPlatformContext(ctx, archivePath, destinationDir, archiveName, runtime.GOOS, runtime.GOARCH, false)
}

func extractReleaseBinaryContext(ctx context.Context, archivePath, destinationDir, archiveName, goos, goarch string) (string, error) {
	return extractBinaryForPlatformContext(ctx, archivePath, destinationDir, archiveName, goos, goarch, true)
}

func extractBinaryForPlatformContext(ctx context.Context, archivePath, destinationDir, archiveName, goos, goarch string, requirePlatformContract bool) (string, error) {
	span := tracepkg.Start(ctx, "UPDATE", "update.archive.extract", "Extracting release archive", tracepkg.String("archive", archivePath), tracepkg.String("destination", destinationDir), tracepkg.String("asset", archiveName))
	binaryName := "cm"
	if requirePlatformContract {
		platform, ok := ReleasePlatformFor(goos, goarch)
		if !ok {
			err := fmt.Errorf("unsupported update platform %q", strings.TrimSpace(goos)+"/"+strings.TrimSpace(goarch))
			span.FailMessage("Release archive extraction failed", err)
			return "", err
		}
		binaryName = platform.BinaryName
		if !strings.HasSuffix(archiveName, platform.ArchiveExtension) {
			err := fmt.Errorf("release archive %q does not match platform archive type %s", archiveName, platform.ArchiveExtension)
			span.FailMessage("Release archive extraction failed", err)
			return "", err
		}
	} else if strings.HasSuffix(archiveName, ".zip") {
		binaryName = "cm.exe"
	}
	var path string
	var err error
	switch {
	case strings.HasSuffix(archiveName, ".zip"):
		path, err = extractZipBinary(archivePath, destinationDir, binaryName)
	case strings.HasSuffix(archiveName, ".tar.gz"):
		path, err = extractTarBinary(archivePath, destinationDir, binaryName)
	default:
		err = fmt.Errorf("unsupported release archive %q", archiveName)
	}
	if err != nil {
		span.FailMessage("Release archive extraction failed", err)
		return "", err
	}
	fields := []tracepkg.Field{tracepkg.String("binary", path)}
	if info, statErr := os.Stat(path); statErr == nil {
		fields = append(fields, tracepkg.Int64("bytes", info.Size()))
	}
	span.EndMessage("Release archive extracted", fields...)
	return path, nil
}

func extractTarBinary(archivePath, destinationDir, binaryName string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("open release tar.gz: %w", err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	var binary []byte
	entries := 0
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read release tar.gz: %w", err)
		}
		entries++
		if entries > maxArchiveEntries {
			return "", fmt.Errorf("release archive exceeds %d entry limit", maxArchiveEntries)
		}
		name, err := safeArchivePath(header.Name)
		if err != nil {
			return "", err
		}
		switch header.Typeflag {
		case tar.TypeReg, 0:
		case tar.TypeDir:
			continue
		default:
			return "", fmt.Errorf("release archive contains unsupported entry type: %s", name)
		}
		if name != binaryName {
			continue
		}
		if canonicalArchivePath(header.Name) != binaryName {
			return "", fmt.Errorf("release archive entrypoint must be exactly %s", binaryName)
		}
		if binary != nil {
			return "", fmt.Errorf("release archive contains duplicate %s", binaryName)
		}
		if header.Size <= 0 || header.Size > maxExtractedBinarySize {
			return "", fmt.Errorf("release binary has invalid size %d", header.Size)
		}
		binary, err = io.ReadAll(io.LimitReader(reader, maxExtractedBinarySize+1))
		if err != nil {
			return "", err
		}
		if int64(len(binary)) != header.Size || int64(len(binary)) > maxExtractedBinarySize {
			return "", errors.New("release binary size mismatch")
		}
	}
	return writeExtractedBinary(destinationDir, binaryName, binary)
}

func extractZipBinary(archivePath, destinationDir, binaryName string) (string, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", fmt.Errorf("open release zip: %w", err)
	}
	defer reader.Close()
	var binary []byte
	for index, entry := range reader.File {
		if index >= maxArchiveEntries {
			return "", fmt.Errorf("release archive exceeds %d entry limit", maxArchiveEntries)
		}
		name, err := safeArchivePath(entry.Name)
		if err != nil {
			return "", err
		}
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 || entry.ExternalAttrs&0x400 != 0 || !mode.IsRegular() && !mode.IsDir() {
			return "", fmt.Errorf("release archive contains unsupported entry type: %s", name)
		}
		if mode.IsDir() || name != binaryName {
			continue
		}
		if canonicalArchivePath(entry.Name) != binaryName {
			return "", fmt.Errorf("release archive entrypoint must be exactly %s", binaryName)
		}
		if binary != nil {
			return "", fmt.Errorf("release archive contains duplicate %s", binaryName)
		}
		if entry.UncompressedSize64 == 0 || entry.UncompressedSize64 > uint64(maxExtractedBinarySize) {
			return "", fmt.Errorf("release binary has invalid size %d", entry.UncompressedSize64)
		}
		stream, err := entry.Open()
		if err != nil {
			return "", err
		}
		binary, err = io.ReadAll(io.LimitReader(stream, maxExtractedBinarySize+1))
		closeErr := stream.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
		if uint64(len(binary)) != entry.UncompressedSize64 || int64(len(binary)) > maxExtractedBinarySize {
			return "", errors.New("release binary size mismatch")
		}
	}
	return writeExtractedBinary(destinationDir, binaryName, binary)
}

func safeArchivePath(name string) (string, error) {
	name = canonicalArchivePath(name)
	clean := filepath.ToSlash(filepath.Clean(name))
	unsafeParent := false
	for _, segment := range strings.Split(name, "/") {
		if segment == ".." {
			unsafeParent = true
			break
		}
	}
	if name == "" || clean == "." || strings.HasPrefix(name, "/") || unsafeParent || strings.Contains(name, ":") {
		return "", fmt.Errorf("unsafe release archive path %q", name)
	}
	return clean, nil
}

func canonicalArchivePath(name string) string {
	return strings.ReplaceAll(strings.TrimSpace(name), "\\", "/")
}

func writeExtractedBinary(destinationDir, binaryName string, content []byte) (string, error) {
	if len(content) == 0 {
		return "", fmt.Errorf("release archive is missing %s", binaryName)
	}
	if err := os.MkdirAll(destinationDir, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(destinationDir, binaryName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(content); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0755); err != nil {
		return "", err
	}
	ok = true
	return path, nil
}
