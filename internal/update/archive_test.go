package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractTarBinary(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "release.tar.gz")
	writeTarArchive(t, archive, []tarEntry{{name: "LICENSE", content: []byte("license")}, {name: "cm", content: []byte("binary")}})
	binary, err := ExtractBinary(archive, filepath.Join(t.TempDir(), "extract"), "codemcp_linux_amd64.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "binary" {
		t.Fatalf("binary = %q", content)
	}
}

func TestExtractZipBinary(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "release.zip")
	writeZipArchive(t, archive, []zipEntry{{name: "README.md", content: []byte("readme")}, {name: "cm.exe", content: []byte("binary")}})
	binary, err := ExtractBinary(archive, filepath.Join(t.TempDir(), "extract"), "codemcp_windows_amd64.zip")
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "binary" {
		t.Fatalf("binary = %q", content)
	}
}

func TestExtractRejectsUnsafeArchivePaths(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", `C:\\escape`} {
		t.Run(name, func(t *testing.T) {
			archive := filepath.Join(t.TempDir(), "release.tar.gz")
			writeTarArchive(t, archive, []tarEntry{{name: name, content: []byte("bad")}, {name: "cm", content: []byte("binary")}})
			if _, err := ExtractBinary(archive, filepath.Join(t.TempDir(), "extract"), "release.tar.gz"); err == nil {
				t.Fatal("unsafe archive path was accepted")
			}
		})
	}
}

func TestExtractRejectsArchiveSymlinks(t *testing.T) {
	tarArchive := filepath.Join(t.TempDir(), "release.tar.gz")
	file, err := os.Create(tarArchive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	writer := tar.NewWriter(gz)
	if err := writer.WriteHeader(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "cm", Mode: 0777}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractBinary(tarArchive, filepath.Join(t.TempDir(), "extract"), "release.tar.gz"); err == nil {
		t.Fatal("tar symlink was accepted")
	}

	zipArchive := filepath.Join(t.TempDir(), "release.zip")
	zipFile, err := os.Create(zipArchive)
	if err != nil {
		t.Fatal(err)
	}
	zipWriter := zip.NewWriter(zipFile)
	header := &zip.FileHeader{Name: "link"}
	header.SetMode(os.ModeSymlink | 0777)
	stream, err := zipWriter.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Write([]byte("cm.exe")); err != nil {
		t.Fatal(err)
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zipFile.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractBinary(zipArchive, filepath.Join(t.TempDir(), "extract"), "release.zip"); err == nil {
		t.Fatal("zip symlink was accepted")
	}
}

func TestExtractRequiresRootBinary(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "release.zip")
	writeZipArchive(t, archive, []zipEntry{{name: "nested/cm.exe", content: []byte("binary")}})
	if _, err := ExtractBinary(archive, filepath.Join(t.TempDir(), "extract"), "release.zip"); err == nil {
		t.Fatal("nested binary was accepted")
	}
}

func TestExtractRejectsDuplicateCanonicalBinary(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "release.zip")
	writeZipArchive(t, archive, []zipEntry{{name: "cm.exe", content: []byte("one")}, {name: "cm.exe", content: []byte("two")}})
	if _, err := ExtractBinary(archive, filepath.Join(t.TempDir(), "extract"), "release.zip"); err == nil {
		t.Fatal("duplicate canonical binary was accepted")
	}
}

func TestExtractRejectsEmptyCanonicalBinary(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "release.tar.gz")
	writeTarArchive(t, archive, []tarEntry{{name: "cm", content: nil}})
	if _, err := ExtractBinary(archive, filepath.Join(t.TempDir(), "extract"), "release.tar.gz"); err == nil {
		t.Fatal("empty canonical binary was accepted")
	}
}

func TestExtractRejectsAliasedCanonicalBinary(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "release.tar.gz")
	writeTarArchive(t, archive, []tarEntry{{name: "./cm", content: []byte("binary")}})
	if _, err := ExtractBinary(archive, filepath.Join(t.TempDir(), "extract"), "release.tar.gz"); err == nil {
		t.Fatal("aliased canonical binary was accepted")
	}
}

func TestExtractRejectsTarNonRegularCanonicalBinary(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "release.tar.gz")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	writer := tar.NewWriter(gz)
	if err := writer.WriteHeader(&tar.Header{Name: "cm", Mode: 0600, Typeflag: tar.TypeFifo}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractBinary(archive, filepath.Join(t.TempDir(), "extract"), "release.tar.gz"); err == nil {
		t.Fatal("non-regular canonical binary was accepted")
	}
}

func TestExtractRejectsNonCanonicalNonRegularEntry(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "release.tar.gz")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	writer := tar.NewWriter(gz)
	if err := writer.WriteHeader(&tar.Header{Name: "cm", Mode: 0755, Typeflag: tar.TypeReg, Size: int64(len("binary"))}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("binary")); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteHeader(&tar.Header{Name: "payload.fifo", Mode: 0600, Typeflag: tar.TypeFifo}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractBinary(archive, filepath.Join(t.TempDir(), "extract"), "release.tar.gz"); err == nil {
		t.Fatal("non-canonical non-regular archive entry was accepted")
	}
}

func TestExtractRejectsZipReparseCanonicalBinary(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "release.zip")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	header := &zip.FileHeader{Name: "cm.exe", Method: zip.Deflate}
	header.SetMode(0755)
	header.ExternalAttrs |= 0x400
	stream, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Write([]byte("binary")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractBinary(archive, filepath.Join(t.TempDir(), "extract"), "release.zip"); err == nil {
		t.Fatal("reparse canonical binary was accepted")
	}
}

type tarEntry struct {
	name    string
	content []byte
}

func writeTarArchive(t *testing.T, path string, entries []tarEntry) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	writer := tar.NewWriter(gz)
	for _, entry := range entries {
		if err := writer.WriteHeader(&tar.Header{Name: entry.name, Mode: 0755, Size: int64(len(entry.content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(entry.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

type zipEntry struct {
	name    string
	content []byte
}

func writeZipArchive(t *testing.T, path string, entries []zipEntry) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.SetMode(0755)
		stream, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Write(entry.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
