package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

const (
	maxArchiveSize   int64 = 256 << 20
	maxChecksumSize  int64 = 1 << 20
	maxSignatureSize int64 = 1 << 20
)

type SignatureVerifier func(ctx context.Context, checksumPath, signaturePath, version string) error

type Downloader struct {
	HTTPClient        *http.Client
	TempDir           string
	UserAgent         string
	SignatureVerifier SignatureVerifier
}

type Artifact struct {
	Dir     string
	Binary  string
	Release Release
}

func (a Artifact) Cleanup() error {
	if strings.TrimSpace(a.Dir) == "" {
		return nil
	}
	return os.RemoveAll(a.Dir)
}

func (d Downloader) Download(ctx context.Context, release Release) (result Artifact, resultErr error) {
	span := tracepkg.Start(ctx, "UPDATE", "update.artifact.prepare", "Preparing release artifact", tracepkg.String("version", release.Version), tracepkg.String("archive", release.ArchiveName))
	defer func() {
		if resultErr != nil {
			span.FailMessage("Release artifact preparation failed", resultErr)
			return
		}
		span.EndMessage("Release artifact prepared", tracepkg.String("directory", result.Dir), tracepkg.String("binary", result.Binary))
	}()
	if err := validateReleaseDownload(release); err != nil {
		return Artifact{}, err
	}
	tempSpan := tracepkg.Start(ctx, "UPDATE", "update.workspace.create", "Creating update workspace", tracepkg.String("parent", strings.TrimSpace(d.TempDir)))
	dir, err := os.MkdirTemp(strings.TrimSpace(d.TempDir), "cm-update-")
	if err != nil {
		tempSpan.FailMessage("Update workspace creation failed", err)
		return Artifact{}, err
	}
	tempSpan.EndMessage("Update workspace created", tracepkg.String("path", dir))
	artifact := Artifact{Dir: dir, Release: release}
	ok := false
	defer func() {
		if !ok {
			cleanupSpan := tracepkg.Start(ctx, "UPDATE", "update.workspace.cleanup", "Cleaning failed update workspace", tracepkg.String("path", artifact.Dir))
			cleanupErr := artifact.Cleanup()
			if cleanupErr != nil {
				cleanupSpan.FailMessage("Failed update workspace cleanup failed", cleanupErr)
			} else {
				cleanupSpan.EndMessage("Failed update workspace cleaned")
			}
		}
	}()
	archivePath := filepath.Join(dir, release.ArchiveName)
	checksumPath := filepath.Join(dir, release.ChecksumName)
	signaturePath := filepath.Join(dir, release.SignatureName)
	if err := d.downloadFile(ctx, release.ChecksumURL, checksumPath, maxChecksumSize); err != nil {
		return Artifact{}, fmt.Errorf("download release checksums: %w", err)
	}
	if err := d.downloadFile(ctx, release.SignatureURL, signaturePath, maxSignatureSize); err != nil {
		return Artifact{}, fmt.Errorf("download release checksum signature: %w", err)
	}
	verifier := d.SignatureVerifier
	if verifier == nil {
		verifier = VerifyChecksumSignature
	}
	verifySpan := tracepkg.Start(ctx, "UPDATE", "update.signature.verify", "Verifying release signature", tracepkg.String("checksum", checksumPath), tracepkg.String("signature", signaturePath), tracepkg.String("version", release.Version))
	if err := verifier(ctx, checksumPath, signaturePath, release.Version); err != nil {
		verifySpan.FailMessage("Release signature verification failed", err)
		return Artifact{}, fmt.Errorf("verify release checksum signature: %w", err)
	}
	verifySpan.EndMessage("Release signature verified")
	if err := d.downloadFile(ctx, release.ArchiveURL, archivePath, maxArchiveSize); err != nil {
		return Artifact{}, fmt.Errorf("download release archive: %w", err)
	}
	if err := VerifyChecksumContext(ctx, archivePath, checksumPath, release.ArchiveName); err != nil {
		return Artifact{}, err
	}
	extractDir := filepath.Join(dir, "extract")
	binary, err := ExtractBinaryContext(ctx, archivePath, extractDir, release.ArchiveName)
	if err != nil {
		return Artifact{}, err
	}
	artifact.Binary = binary
	ok = true
	result = artifact
	return result, nil
}

func (d Downloader) downloadFile(ctx context.Context, rawURL, destination string, limit int64) error {
	span := tracepkg.Start(ctx, "UPDATE", "update.file.download", "Downloading release file", tracepkg.URL("url", rawURL), tracepkg.String("destination", destination), tracepkg.Int64("limit_bytes", limit))
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		span.FailMessage("Release file download failed", err)
		return err
	}
	if parsed.Scheme != "https" || parsed.Host == "" {
		err := fmt.Errorf("release download URL must use HTTPS: %s", rawURL)
		span.FailMessage("Release file download failed", err)
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		span.FailMessage("Release file download failed", err)
		return err
	}
	userAgent := strings.TrimSpace(d.UserAgent)
	if userAgent == "" {
		userAgent = DefaultRepo + "/updater"
	}
	request.Header.Set("User-Agent", userAgent)
	client := secureHTTPClient(d.HTTPClient)
	response, err := tracepkg.DoHTTP(client, request)
	if err != nil {
		span.FailMessage("Release file download failed", err)
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		err := fmt.Errorf("server returned %s", response.Status)
		span.FailMessage("Release file download failed", err, tracepkg.Int("status", response.StatusCode))
		return err
	}
	if response.ContentLength > limit {
		err := fmt.Errorf("download exceeds %d byte limit", limit)
		span.FailMessage("Release file download failed", err, tracepkg.Int64("content_length", response.ContentLength))
		return err
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		span.FailMessage("Release file download failed", err)
		return err
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(destination)
		}
	}()
	written, err := io.Copy(file, io.LimitReader(response.Body, limit+1))
	if err != nil {
		span.FailMessage("Release file download failed", err, tracepkg.Int64("bytes", written))
		return err
	}
	if written > limit {
		err := fmt.Errorf("download exceeds %d byte limit", limit)
		span.FailMessage("Release file download failed", err, tracepkg.Int64("bytes", written))
		return err
	}
	if written == 0 {
		err := errors.New("download is empty")
		span.FailMessage("Release file download failed", err)
		return err
	}
	if err := file.Sync(); err != nil {
		span.FailMessage("Release file download failed", err, tracepkg.Int64("bytes", written))
		return err
	}
	if err := file.Close(); err != nil {
		span.FailMessage("Release file download failed", err, tracepkg.Int64("bytes", written))
		return err
	}
	keep = true
	span.EndMessage("Release file downloaded", tracepkg.Int("status", response.StatusCode), tracepkg.Int64("bytes", written), tracepkg.Int64("content_length", response.ContentLength))
	return nil
}

func secureHTTPClient(base *http.Client) *http.Client {
	client := &http.Client{Timeout: 2 * time.Minute}
	if base != nil {
		*client = *base
		if client.Timeout == 0 {
			client.Timeout = 2 * time.Minute
		}
	}
	previousRedirect := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != "https" {
			return errors.New("release redirect must use HTTPS")
		}
		if previousRedirect != nil {
			return previousRedirect(request, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return client
}

func validateReleaseDownload(release Release) error {
	version, err := NormalizeVersion(release.Version)
	if err != nil {
		return fmt.Errorf("release version: %w", err)
	}
	if strings.TrimSpace(release.ArchiveName) == "" || filepath.Base(release.ArchiveName) != release.ArchiveName {
		return errors.New("release archive name must be a base filename")
	}
	expectedArchive, err := CurrentAssetName(version)
	if err != nil {
		return err
	}
	if release.ArchiveName != expectedArchive {
		return fmt.Errorf("release archive name %q does not match expected asset %q", release.ArchiveName, expectedArchive)
	}
	if release.ChecksumName != ChecksumName {
		return fmt.Errorf("release checksum asset must be %s", ChecksumName)
	}
	if release.SignatureName != ChecksumSignatureName {
		return fmt.Errorf("release checksum signature asset must be %s", ChecksumSignatureName)
	}
	if strings.TrimSpace(release.ArchiveURL) == "" || strings.TrimSpace(release.ChecksumURL) == "" || strings.TrimSpace(release.SignatureURL) == "" {
		return errors.New("release download URLs are required")
	}
	return nil
}
