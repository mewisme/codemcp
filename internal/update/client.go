package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

const maxReleaseResponseSize = 1 << 20

type Client struct {
	HTTPClient *http.Client
	BaseURL    string
	Owner      string
	Repo       string
	UserAgent  string
}

type githubRelease struct {
	TagName string `json:"tag_name"`
	Draft   bool   `json:"draft"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func (c Client) Latest(ctx context.Context) (Release, error) {
	return c.getRelease(ctx, "releases/latest")
}

func (c Client) Version(ctx context.Context, version string) (Release, error) {
	version, err := NormalizeVersion(version)
	if err != nil {
		return Release{}, err
	}
	release, err := c.getRelease(ctx, "releases/tags/"+url.PathEscape(version))
	if err != nil {
		return Release{}, err
	}
	if release.Version != version {
		return Release{}, fmt.Errorf("release tag mismatch: got %s, want %s", release.Version, version)
	}
	return release, nil
}

func (c Client) getRelease(ctx context.Context, endpoint string) (result Release, resultErr error) {
	owner := strings.TrimSpace(c.Owner)
	if owner == "" {
		owner = DefaultOwner
	}
	repo := strings.TrimSpace(c.Repo)
	if repo == "" {
		repo = DefaultRepo
	}
	baseURL := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	releaseURL := fmt.Sprintf("%s/repos/%s/%s/%s", baseURL, owner, repo, endpoint)
	span := tracepkg.Start(ctx, "UPDATE", "update.release.resolve", "Resolving release", tracepkg.String("owner", owner), tracepkg.String("repo", repo), tracepkg.URL("url", releaseURL))
	defer func() {
		if resultErr != nil {
			span.FailMessage("Release resolution failed", resultErr)
			return
		}
		span.EndMessage("Release resolved", tracepkg.String("version", result.Version), tracepkg.String("archive", result.ArchiveName), tracepkg.URL("archive_url", result.ArchiveURL), tracepkg.URL("checksum_url", result.ChecksumURL), tracepkg.URL("signature_url", result.SignatureURL))
	}()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseURL, nil)
	if err != nil {
		return Release{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	userAgent := strings.TrimSpace(c.UserAgent)
	if userAgent == "" {
		userAgent = DefaultRepo + "/update-check"
	}
	request.Header.Set("User-Agent", userAgent)
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	response, err := tracepkg.DoHTTP(client, request)
	if err != nil {
		return Release{}, fmt.Errorf("check latest release: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		message := strings.TrimSpace(string(body))
		if message != "" {
			return Release{}, fmt.Errorf("check latest release: GitHub returned %s: %s", response.Status, message)
		}
		return Release{}, fmt.Errorf("check latest release: GitHub returned %s", response.Status)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxReleaseResponseSize))
	var payload githubRelease
	if err := decoder.Decode(&payload); err != nil {
		return Release{}, fmt.Errorf("decode latest release: %w", err)
	}
	if payload.Draft {
		return Release{}, errors.New("latest GitHub release is unexpectedly marked as draft")
	}
	version, err := NormalizeVersion(payload.TagName)
	if err != nil {
		return Release{}, fmt.Errorf("latest release tag: %w", err)
	}
	archiveName, err := CurrentAssetName(version)
	if err != nil {
		return Release{}, err
	}
	release := Release{Version: version, ArchiveName: archiveName, ChecksumName: ChecksumName, SignatureName: ChecksumSignatureName}
	for _, asset := range payload.Assets {
		switch asset.Name {
		case archiveName:
			release.ArchiveURL = strings.TrimSpace(asset.BrowserDownloadURL)
		case release.ChecksumName:
			release.ChecksumURL = strings.TrimSpace(asset.BrowserDownloadURL)
		case release.SignatureName:
			release.SignatureURL = strings.TrimSpace(asset.BrowserDownloadURL)
		}
	}
	if release.ArchiveURL == "" {
		return Release{}, fmt.Errorf("latest release %s is missing asset %s", version, archiveName)
	}
	if release.ChecksumURL == "" {
		return Release{}, fmt.Errorf("latest release %s is missing asset %s", version, release.ChecksumName)
	}
	if release.SignatureURL == "" {
		return Release{}, fmt.Errorf("latest release %s is missing asset %s", version, release.SignatureName)
	}
	result = release
	return result, nil
}
