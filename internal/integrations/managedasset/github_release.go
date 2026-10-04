package managedasset

import (
	"bufio"
	"context"
	"encoding/hex"
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

const (
	maxGitHubReleaseBytes  = int64(1 << 20)
	maxGitHubChecksumBytes = int64(1 << 20)
)

type GitHubRelease struct {
	Version   string
	Assets    map[string]string
	Checksums map[string]string
}

type githubReleaseResponse struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func LatestGitHubRelease(ctx context.Context, client *http.Client, repository, checksumAsset string) (result GitHubRelease, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	span := tracepkg.Start(ctx, "MANAGED_ASSET", "managed_asset.release.resolve", "Resolving managed asset release",
		tracepkg.String("repository", strings.TrimSpace(repository)),
	)
	defer func() { span.Finish(err) }()
	repository = strings.TrimSpace(repository)
	checksumAsset = strings.TrimSpace(checksumAsset)
	if !safeGitHubRepository(repository) {
		return GitHubRelease{}, errors.New("GitHub release repository must be owner/name")
	}
	if checksumAsset == "" || strings.ContainsAny(checksumAsset, "/\\") {
		return GitHubRelease{}, errors.New("GitHub checksum asset name is invalid")
	}
	client = releaseHTTPClient(client)
	endpoint := "https://api.github.com/repos/" + repository + "/releases/latest"
	var release githubReleaseResponse
	if err := getJSON(ctx, client, endpoint, maxGitHubReleaseBytes, &release); err != nil {
		return GitHubRelease{}, fmt.Errorf("resolve latest GitHub release: %w", err)
	}
	version := strings.TrimSpace(release.TagName)
	if version == "" || !safeComponent(version) {
		return GitHubRelease{}, errors.New("latest GitHub release has an invalid tag")
	}
	assets := make(map[string]string, len(release.Assets))
	checksumURL := ""
	for _, asset := range release.Assets {
		name := strings.TrimSpace(asset.Name)
		rawURL := strings.TrimSpace(asset.BrowserDownloadURL)
		if name == "" || rawURL == "" {
			continue
		}
		if err := validateReleaseAssetURL(rawURL, repository, version); err != nil {
			return GitHubRelease{}, fmt.Errorf("release asset %q: %w", name, err)
		}
		assets[name] = rawURL
		if name == checksumAsset {
			checksumURL = rawURL
		}
	}
	if checksumURL == "" {
		return GitHubRelease{}, fmt.Errorf("latest GitHub release is missing checksum asset %q", checksumAsset)
	}
	checksums, err := fetchChecksums(ctx, client, checksumURL)
	if err != nil {
		return GitHubRelease{}, err
	}
	return GitHubRelease{Version: version, Assets: assets, Checksums: checksums}, nil
}

func releaseHTTPClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func getJSON(ctx context.Context, client *http.Client, endpoint string, limit int64, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "CodeMCP")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("GitHub returned HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, limit+1))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func fetchChecksums(ctx context.Context, client *http.Client, endpoint string) (map[string]string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "text/plain")
	request.Header.Set("User-Agent", "CodeMCP")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download release checksums: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("download release checksums: HTTP %d", response.StatusCode)
	}
	result := map[string]string{}
	scanner := bufio.NewScanner(io.LimitReader(response.Body, maxGitHubChecksumBytes+1))
	scanner.Buffer(make([]byte, 4096), int(maxGitHubChecksumBytes))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		digest := strings.ToLower(strings.TrimSpace(fields[0]))
		if decoded, err := hex.DecodeString(digest); err != nil || len(decoded) != 32 {
			continue
		}
		name := strings.TrimPrefix(strings.TrimSpace(fields[len(fields)-1]), "*")
		if name == "" || strings.ContainsAny(name, "/\\") {
			continue
		}
		result[name] = digest
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read release checksums: %w", err)
	}
	if len(result) == 0 {
		return nil, errors.New("release checksum file contains no valid SHA-256 entries")
	}
	return result, nil
}

func safeGitHubRepository(value string) bool {
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\?#") {
			return false
		}
	}
	return true
}

func validateReleaseAssetURL(rawURL, repository, version string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "github.com") || parsed.User != nil {
		return errors.New("download URL must be HTTPS on github.com")
	}
	wantPrefix := "/" + repository + "/releases/download/" + url.PathEscape(version) + "/"
	if !strings.HasPrefix(parsed.EscapedPath(), wantPrefix) {
		return errors.New("download URL does not belong to the expected release")
	}
	return nil
}
