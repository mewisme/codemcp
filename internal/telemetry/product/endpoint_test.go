package product

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const injectedEndpointFixture = "https://telemetry.example/v1/products/codemcp/events"

func TestBuildEndpointMetadataSourceBuildIsUnavailable(t *testing.T) {
	t.Setenv("TELEMETRY_ENDPOINT", injectedEndpointFixture)
	if Endpoint != "" {
		t.Fatalf("source build endpoint = %q, want empty", Endpoint)
	}
	metadata, err := BuildEndpointMetadata()
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Available || metadata.Host != "" || metadata.Product != "" {
		t.Fatalf("source build metadata = %#v, want unavailable", metadata)
	}
}

func TestBuildEndpointMetadataInjectedBuildIsAvailable(t *testing.T) {
	if os.Getenv("CM_TEST_INJECTED_TELEMETRY_ENDPOINT") == "1" {
		metadata, err := BuildEndpointMetadata()
		if err != nil {
			t.Fatal(err)
		}
		if !metadata.Available || metadata.Host != "telemetry.example" || metadata.Product != "codemcp" {
			t.Fatalf("injected build metadata = %#v", metadata)
		}
		return
	}

	cmd := exec.Command(
		"go", "test", ".",
		"-run", "^TestBuildEndpointMetadataInjectedBuildIsAvailable$",
		"-count=1",
		"-ldflags=-X go.mewis.me/codemcp/internal/telemetry/product.Endpoint="+injectedEndpointFixture,
	)
	cmd.Env = append(os.Environ(), "CM_TEST_INJECTED_TELEMETRY_ENDPOINT=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("injected build test failed: %v\n%s", err, output)
	}
}

func TestParseEndpointRequiresExactReleaseRoute(t *testing.T) {
	tests := []string{
		"",
		injectedEndpointFixture,
	}
	for _, raw := range tests {
		metadata, err := ParseEndpoint(raw)
		if err != nil {
			t.Fatalf("ParseEndpoint(%q): %v", raw, err)
		}
		if raw == "" && metadata.Available {
			t.Fatalf("empty endpoint metadata = %#v", metadata)
		}
		if raw != "" && (!metadata.Available || metadata.Product != "codemcp") {
			t.Fatalf("endpoint metadata = %#v", metadata)
		}
	}

	for _, raw := range []string{
		"http://telemetry.example/v1/products/codemcp/events",
		"https://telemetry.example/v1/products/other/events",
		"https://telemetry.example/extra/v1/products/codemcp/events",
		"https://user@telemetry.example/v1/products/codemcp/events",
		"https://telemetry.example/v1/products/codemcp/events?debug=1",
		"https://telemetry.example/v1/products/codemcp/events#fragment",
		"telemetry.example/v1/products/codemcp/events",
	} {
		if metadata, err := ParseEndpoint(raw); err == nil || metadata.Available {
			t.Fatalf("ParseEndpoint(%q) = %#v, %v; want invalid", raw, metadata, err)
		}
	}
}

func TestGoTestsDoNotReferenceProductionTelemetryHost(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	productionHost := "telemetry." + "mewis.me"
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), productionHost) {
			t.Fatalf("test source %s references the production telemetry host", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
