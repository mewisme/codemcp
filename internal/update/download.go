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
	"runtime"
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
	Dir      string
	Binary   string
	Release  Release
	Warnings []string
}

type PackageArtifact struct {
	Dir      string
	Path     string
	Release  PackageRelease
	Warnings []string
}

func (a PackageArtifact) Cleanup() error {
	if strings.TrimSpace(a.Dir) == "" {
		return nil
	}
	return os.RemoveAll(a.Dir)
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
	if err := d.downloadFile(ctx, release.ArchiveURL, archivePath, maxArchiveSize); err != nil {
		return Artifact{}, fmt.Errorf("download release archive: %w", err)
	}
	if err := VerifyChecksumContext(ctx, archivePath, checksumPath, release.ArchiveName); err != nil {
		return Artifact{}, err
	}
	artifact.Warnings = d.verifyOptionalSignature(ctx, release, checksumPath, signaturePath)
	extractDir := filepath.Join(dir, "extract")
	binary, err := extractReleaseBinaryContext(ctx, archivePath, extractDir, release.ArchiveName, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return Artifact{}, err
	}
	artifact.Binary = binary
	ok = true
	result = artifact
	return result, nil
}

func (d Downloader) DownloadPackage(ctx context.Context, release PackageRelease) (result PackageArtifact, resultErr error) {
	if err := validatePackageReleaseDownload(release); err != nil {
		return PackageArtifact{}, err
	}
	dir, err := os.MkdirTemp(strings.TrimSpace(d.TempDir), "cm-package-update-")
	if err != nil {
		return PackageArtifact{}, err
	}
	artifact := PackageArtifact{Dir: dir, Release: release}
	ok := false
	defer func() {
		if !ok {
			_ = artifact.Cleanup()
		}
	}()
	packagePath := filepath.Join(dir, release.PackageName)
	checksumPath := filepath.Join(dir, release.ChecksumName)
	signaturePath := filepath.Join(dir, release.SignatureName)
	if err := d.downloadFile(ctx, release.ChecksumURL, checksumPath, maxChecksumSize); err != nil {
		return PackageArtifact{}, fmt.Errorf("download release checksums: %w", err)
	}
	if err := d.downloadFile(ctx, release.PackageURL, packagePath, maxArchiveSize); err != nil {
		return PackageArtifact{}, fmt.Errorf("download release package: %w", err)
	}
	if err := VerifyChecksumContext(ctx, packagePath, checksumPath, release.PackageName); err != nil {
		return PackageArtifact{}, err
	}
	trust := Release{
		Version: release.Version, ChecksumName: release.ChecksumName, ChecksumURL: release.ChecksumURL,
		SignatureName: release.SignatureName, SignatureURL: release.SignatureURL,
	}
	artifact.Warnings = d.verifyOptionalSignature(ctx, trust, checksumPath, signaturePath)
	artifact.Path = packagePath
	ok = true
	result = artifact
	return result, nil
}

func (d Downloader) verifyOptionalSignature(ctx context.Context, release Release, checksumPath, signaturePath string) []string {
	verifySpan := tracepkg.Start(ctx, "UPDATE", "update.signature.verify", "Verifying optional release signature", tracepkg.String("checksum", checksumPath), tracepkg.String("signature", signaturePath), tracepkg.String("version", release.Version))
	warn := func(reason error) []string {
		warning := signatureTrustWarning(reason)
		verifySpan.FailMessage("Optional release signature verification unavailable", errors.New(warning))
		return []string{warning}
	}
	if strings.TrimSpace(release.SignatureURL) == "" {
		return warn(errors.New("signature bundle is not published for this release"))
	}
	if err := d.downloadFile(ctx, release.SignatureURL, signaturePath, maxSignatureSize); err != nil {
		return warn(fmt.Errorf("download signature bundle: %w", err))
	}
	verifier := d.SignatureVerifier
	if verifier == nil {
		verifier = VerifyChecksumSignature
	}
	if err := verifier(ctx, checksumPath, signaturePath, release.Version); err != nil {
		return warn(fmt.Errorf("verify signature bundle: %w", err))
	}
	verifySpan.EndMessage("Optional release signature verified")
	return nil
}

func signatureTrustWarning(reason error) string {
	message := "signature verification unavailable"
	if reason != nil {
		if value := strings.Join(strings.Fields(reason.Error()), " "); value != "" {
			message = value
		}
	}
	const maxReasonBytes = 320
	if len(message) > maxReasonBytes {
		message = message[:maxReasonBytes] + "..."
	}
	return "Sigstore defense-in-depth unavailable: " + message + "; SHA-256 checksum verified"
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
	if _, err := NormalizeVersion(release.Version); err != nil {
		return fmt.Errorf("release version: %w", err)
	}
	if strings.TrimSpace(release.ArchiveName) == "" || filepath.Base(release.ArchiveName) != release.ArchiveName {
		return errors.New("release archive name must be a base filename")
	}
	expectedArchive, err := CurrentArchiveName()
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
	if strings.TrimSpace(release.ArchiveURL) == "" || strings.TrimSpace(release.ChecksumURL) == "" {
		return errors.New("release archive and checksum download URLs are required")
	}
	return nil
}

func validatePackageReleaseDownload(release PackageRelease) error {
	if _, err := NormalizeVersion(release.Version); err != nil {
		return fmt.Errorf("release version: %w", err)
	}
	if release.Kind != ArtifactDebian && release.Kind != ArtifactRPM {
		return fmt.Errorf("unsupported release package kind %q", release.Kind)
	}
	expected, err := ArtifactName(release.Kind, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	if release.PackageName != expected || filepath.Base(release.PackageName) != release.PackageName {
		return fmt.Errorf("release package name %q does not match expected asset %q", release.PackageName, expected)
	}
	if release.ChecksumName != ChecksumName {
		return fmt.Errorf("release checksum asset must be %s", ChecksumName)
	}
	if release.SignatureName != ChecksumSignatureName {
		return fmt.Errorf("release checksum signature asset must be %s", ChecksumSignatureName)
	}
	if strings.TrimSpace(release.PackageURL) == "" || strings.TrimSpace(release.ChecksumURL) == "" {
		return errors.New("release package and checksum download URLs are required")
	}
	return nil
}
