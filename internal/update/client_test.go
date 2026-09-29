package update

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientLatest(t *testing.T) {
	asset, err := CurrentAssetName("v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	var gotPath, gotAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"tag_name":"v1.2.3","draft":false,"assets":[{"name":%q,"browser_download_url":"https://example.test/archive"},{"name":"codemcp_checksums.txt","browser_download_url":"https://example.test/checksums"},{"name":"codemcp_checksums.txt.sigstore.json","browser_download_url":"https://example.test/signature"}]}`, asset)
	}))
	defer server.Close()

	release, err := (Client{BaseURL: server.URL, HTTPClient: server.Client(), UserAgent: "codemcp/test"}).Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/repos/mewisme/codemcp/releases/latest" {
		t.Fatalf("request path = %q", gotPath)
	}
	if gotAgent != "codemcp/test" {
		t.Fatalf("user agent = %q", gotAgent)
	}
	if release.Version != "v1.2.3" || release.ArchiveName != asset || release.ArchiveURL != "https://example.test/archive" || release.ChecksumURL != "https://example.test/checksums" || release.SignatureURL != "https://example.test/signature" {
		t.Fatalf("release = %+v", release)
	}
}

func TestClientLatestRequiresExpectedAssets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v1.2.3","assets":[]}`)
	}))
	defer server.Close()
	if _, err := (Client{BaseURL: server.URL, HTTPClient: server.Client()}).Latest(context.Background()); err == nil {
		t.Fatal("missing release assets were accepted")
	}
}

func TestClientLatestAllowsMissingOptionalSignatureAsset(t *testing.T) {
	asset, err := CurrentAssetName("v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"v1.2.3","assets":[{"name":%q,"browser_download_url":"https://example.test/archive"},{"name":"codemcp_checksums.txt","browser_download_url":"https://example.test/checksums"}]}`, asset)
	}))
	defer server.Close()
	release, err := (Client{BaseURL: server.URL, HTTPClient: server.Client()}).Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if release.SignatureName != ChecksumSignatureName || release.SignatureURL != "" {
		t.Fatalf("optional signature metadata = %#v", release)
	}
}

func TestClientLatestRejectsHTTPFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer server.Close()
	if _, err := (Client{BaseURL: server.URL, HTTPClient: server.Client()}).Latest(context.Background()); err == nil {
		t.Fatal("HTTP failure was accepted")
	}
}

func TestClientVersion(t *testing.T) {
	asset, err := CurrentAssetName("v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		fmt.Fprintf(w, `{"tag_name":"v1.2.3","assets":[{"name":%q,"browser_download_url":"https://example.test/archive"},{"name":"codemcp_checksums.txt","browser_download_url":"https://example.test/checksums"},{"name":"codemcp_checksums.txt.sigstore.json","browser_download_url":"https://example.test/signature"}]}`, asset)
	}))
	defer server.Close()
	release, err := (Client{BaseURL: server.URL, HTTPClient: server.Client()}).Version(context.Background(), "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/repos/mewisme/codemcp/releases/tags/v1.2.3" {
		t.Fatalf("request path = %q", gotPath)
	}
	if release.Version != "v1.2.3" {
		t.Fatalf("release version = %q", release.Version)
	}
}

func TestClientVersionRejectsTagMismatch(t *testing.T) {
	asset, err := CurrentAssetName("v1.2.4")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"v1.2.4","assets":[{"name":%q,"browser_download_url":"https://example.test/archive"},{"name":"codemcp_checksums.txt","browser_download_url":"https://example.test/checksums"},{"name":"codemcp_checksums.txt.sigstore.json","browser_download_url":"https://example.test/signature"}]}`, asset)
	}))
	defer server.Close()
	if _, err := (Client{BaseURL: server.URL, HTTPClient: server.Client()}).Version(context.Background(), "v1.2.3"); err == nil {
		t.Fatal("release tag mismatch was accepted")
	}
}
