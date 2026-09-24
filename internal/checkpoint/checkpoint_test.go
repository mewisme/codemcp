package checkpoint

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckpointBeforeAndRestore(t *testing.T) {
	root := t.TempDir()
	store := NewStore(filepath.Join(t.TempDir(), "state"))
	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("before"), 0644); err != nil {
		t.Fatal(err)
	}
	id, err := store.Before("ws_test", root, "write_file", []string{file}, false)
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("checkpoint id is empty")
	}
	if err := os.WriteFile(file, []byte("after"), 0644); err != nil {
		t.Fatal(err)
	}
	result, err := store.Restore("ws_test", root, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Restored) != 1 || result.Restored[0] != file {
		t.Fatalf("unexpected restore result: %#v", result)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "before" {
		t.Fatalf("restored content = %q", data)
	}
}

func TestCheckpointNewFileRestoreDeletes(t *testing.T) {
	root := t.TempDir()
	store := NewStore(filepath.Join(t.TempDir(), "state"))
	file := filepath.Join(root, "new.txt")
	id, err := store.Before("ws_test", root, "write_file", []string{file}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("created"), 0644); err != nil {
		t.Fatal(err)
	}
	result, err := store.Restore("ws_test", root, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Deleted) != 1 || result.Deleted[0] != file {
		t.Fatalf("unexpected delete result: %#v", result)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("file still exists: %v", err)
	}
}

func TestCheckpointRestorePreservesFileAndDirectoryModes(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("Windows does not expose Unix permission bits consistently")
	}
	root := t.TempDir()
	store := NewStore(filepath.Join(t.TempDir(), "state"))
	dir := filepath.Join(root, "bin")
	if err := os.MkdirAll(dir, 0710); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "tool.sh")
	if err := os.WriteFile(file, []byte("before"), 0751); err != nil {
		t.Fatal(err)
	}
	id, err := store.Before("ws_test", root, "delete_directory", []string{dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Restore("ws_test", root, id); err != nil {
		t.Fatal(err)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	fileInfo, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0710 || fileInfo.Mode().Perm() != 0751 {
		t.Fatalf("restored modes dir=%#o file=%#o", dirInfo.Mode().Perm(), fileInfo.Mode().Perm())
	}
}

func TestCheckpointLargeFileUsesBlobAndRestoresLosslessly(t *testing.T) {
	root := t.TempDir()
	store := NewStore(filepath.Join(t.TempDir(), "state"))
	store.MaxFileBytes = 1024
	file := filepath.Join(root, "large.bin")
	content := bytes.Repeat([]byte{0x00, 0x7f, 0xff, 0x42}, 2048)
	if err := os.WriteFile(file, content, 0640); err != nil {
		t.Fatal(err)
	}
	id, err := store.Before("ws_test", root, "delete_file", []string{file}, false)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := store.readManifest("ws_test", id)
	if err != nil {
		t.Fatal(err)
	}
	if manifest == nil || len(manifest.Files) != 1 || manifest.Files[0].Blob == "" || manifest.Files[0].Skipped {
		t.Fatalf("large file snapshot = %#v", manifest)
	}
	if manifest.Files[0].Content != "" || manifest.Files[0].Size != int64(len(content)) {
		t.Fatalf("large file stored inline or wrong size: %#v", manifest.Files[0])
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	result, err := store.Restore("ws_test", root, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Restored) != 1 || len(result.Skipped) != 0 {
		t.Fatalf("restore result = %#v", result)
	}
	restored, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, content) {
		t.Fatal("large file content changed after rewind")
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if os.PathSeparator != '\\' && info.Mode().Perm() != 0640 {
		t.Fatalf("restored mode = %#o", info.Mode().Perm())
	}
}

func TestCheckpointCorruptBlobFailsBeforeRestoreMutation(t *testing.T) {
	root := t.TempDir()
	store := NewStore(filepath.Join(t.TempDir(), "state"))
	store.MaxFileBytes = 16
	file := filepath.Join(root, "large.bin")
	before := bytes.Repeat([]byte("before"), 64)
	if err := os.WriteFile(file, before, 0644); err != nil {
		t.Fatal(err)
	}
	id, err := store.Before("ws_test", root, "write_file", []string{file}, false)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := store.readManifest("ws_test", id)
	if err != nil || manifest == nil || len(manifest.Files) != 1 || manifest.Files[0].Blob == "" {
		t.Fatalf("manifest = %#v err=%v", manifest, err)
	}
	blob, err := store.blobPath("ws_test", id, manifest.Files[0].Blob)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blob, bytes.Repeat([]byte("x"), len(before)), 0600); err != nil {
		t.Fatal(err)
	}
	current := []byte("current content must survive failed rewind")
	if err := os.WriteFile(file, current, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Restore("ws_test", root, id); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("restore error = %v", err)
	}
	data, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(data, current) {
		t.Fatalf("failed restore mutated target: content=%q err=%v", data, err)
	}
}

func TestCheckpointDirectorySymlinkRestores(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symlink creation may require Windows Developer Mode or elevation")
	}
	root := t.TempDir()
	store := NewStore(filepath.Join(t.TempDir(), "state"))
	dir := filepath.Join(root, "tree")
	target := filepath.Join(dir, "target.txt")
	link := filepath.Join(dir, "link.txt")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("target"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.txt", link); err != nil {
		t.Fatal(err)
	}
	id, err := store.Before("ws_test", root, "delete_directory", []string{dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Restore("ws_test", root, id); err != nil {
		t.Fatal(err)
	}
	linkTarget, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if linkTarget != "target.txt" {
		t.Fatalf("restored symlink target = %q", linkTarget)
	}
}

func TestCheckpointRejectsIncompleteDirectorySnapshot(t *testing.T) {
	root := t.TempDir()
	store := NewStore(filepath.Join(t.TempDir(), "state"))
	store.MaxDirectoryDepth = 1
	dir := filepath.Join(root, "tree")
	deep := filepath.Join(dir, "one", "two")
	if err := os.MkdirAll(deep, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "file.txt"), []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Before("ws_test", root, "delete_directory", []string{dir}, false); err == nil || !strings.Contains(err.Error(), "checkpoint directory depth exceeds") {
		t.Fatalf("error = %v", err)
	}
	values, err := store.List("ws_test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 0 {
		t.Fatalf("incomplete checkpoint was indexed: %#v", values)
	}
}

func TestCheckpointDryRunDoesNothing(t *testing.T) {
	root := t.TempDir()
	store := NewStore(filepath.Join(t.TempDir(), "state"))
	id, err := store.Before("ws_test", root, "edit_file", []string{filepath.Join(root, "x.txt")}, true)
	if err != nil {
		t.Fatal(err)
	}
	if id != "" {
		t.Fatalf("dry-run checkpoint id = %q", id)
	}
	values, err := store.List("ws_test", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 0 {
		t.Fatalf("dry-run created checkpoints: %#v", values)
	}
}

func TestCheckpointRejectsPathEscape(t *testing.T) {
	root := t.TempDir()
	store := NewStore(filepath.Join(t.TempDir(), "state"))
	_, err := store.Before("ws_test", root, "write_file", []string{filepath.Join(t.TempDir(), "x.txt")}, false)
	if err == nil {
		t.Fatal("expected checkpoint path escape to fail")
	}
}

func TestCheckpointMetadataUsesCanonicalJSON(t *testing.T) {
	stateRoot := t.TempDir()
	workspaceRoot := t.TempDir()
	file := filepath.Join(workspaceRoot, "file.txt")
	if err := os.WriteFile(file, []byte("before"), 0644); err != nil {
		t.Fatal(err)
	}
	store := NewStore(stateRoot)
	id, err := store.Before("ws_test", workspaceRoot, "write_file", []string{file}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(store.Path("ws_test"), "index.json")); err != nil {
		t.Fatalf("checkpoint index is not JSON: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.Path("ws_test"), "data", id, "manifest.json")); err != nil {
		t.Fatalf("checkpoint manifest is not JSON: %v", err)
	}
}

func TestCheckpointJSONManifestPreservesColonContent(t *testing.T) {
	stateRoot := t.TempDir()
	workspaceRoot := t.TempDir()
	file := filepath.Join(workspaceRoot, "file.txt")
	content := "name: value\nurl: https://example.com\nheader: x:y\nplain"
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	store := NewStore(stateRoot)
	id, err := store.Before("ws_test", workspaceRoot, "edit_file", []string{file}, false)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(store.Path("ws_test"), "data", id, "manifest.json")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), `"content": "name: value\nurl: https://example.com\nheader: x:y\nplain"`) {
		t.Fatalf("manifest content was not JSON encoded intact:\n%s", manifest)
	}
	if _, err := store.PreviewRestore("ws_test", workspaceRoot, id); err != nil {
		t.Fatalf("preview failed to decode JSON checkpoint: %v", err)
	}
	if err := os.WriteFile(file, []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Restore("ws_test", workspaceRoot, id); err != nil {
		t.Fatalf("restore failed to decode JSON checkpoint: %v", err)
	}
	restored, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != content {
		t.Fatalf("restored content = %q, want %q", restored, content)
	}
}
