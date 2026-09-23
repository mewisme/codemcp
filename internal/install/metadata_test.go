package install

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "install.json")
	data := []byte(`{"schema":1,"method":"direct","version":"v1.2.3","install_dir":"/tmp/codemcp","bin_dir":"/tmp/bin"}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	metadata, err := ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Method != MethodDirect || metadata.Version != "v1.2.3" {
		t.Fatalf("metadata = %+v", metadata)
	}
}

func TestReadMetadataNotFound(t *testing.T) {
	_, err := ReadMetadata(filepath.Join(t.TempDir(), "missing.json"))
	if !errors.Is(err, ErrMetadataNotFound) {
		t.Fatalf("error = %v", err)
	}
}

func TestMetadataValidation(t *testing.T) {
	for _, metadata := range []Metadata{
		{Schema: 2, Method: MethodDirect, Version: "v1.0.0", InstallDir: "/tmp/install"},
		{Schema: 1, Method: "invalid", Version: "v1.0.0", InstallDir: "/tmp/install"},
		{Schema: 1, Method: MethodDirect, InstallDir: "/tmp/install"},
		{Schema: 1, Method: MethodDirect, Version: "v1.0.0"},
	} {
		if err := metadata.Validate(); err == nil {
			t.Fatalf("expected invalid metadata: %+v", metadata)
		}
	}
}

func TestWriteMetadataRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "install.json")
	want := Metadata{Schema: MetadataSchema, Method: MethodDirect, Version: "v1.2.3", InstallDir: "/tmp/install", BinDir: "/tmp/bin"}
	if err := WriteMetadata(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("metadata = %+v, want %+v", got, want)
	}
}
