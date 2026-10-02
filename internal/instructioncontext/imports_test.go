package instructioncontext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectMemoryExpandsNestedImportsAndTracksMetadata(t *testing.T) {
	root := t.TempDir()
	writeInstructionFile(t, filepath.Join(root, "AGENTS.md"), "root\n@docs/one.md\nend")
	writeInstructionFile(t, filepath.Join(root, "docs", "one.md"), "one\n@two.md")
	writeInstructionFile(t, filepath.Join(root, "docs", "two.md"), "two")
	bundle, err := LoadProjectMemory(root, MemoryLoadOptions{DisableUser: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Sections) != 1 || len(bundle.Imports) != 2 {
		t.Fatalf("bundle = %#v", bundle)
	}
	content := bundle.Sections[0].Content
	for _, expected := range []string{"root", "one", "two", "@import"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("content missing %q: %s", expected, content)
		}
	}
	if bundle.Imports[0].Kind != SectionImport || bundle.Imports[1].Kind != SectionImport {
		t.Fatalf("imports = %#v", bundle.Imports)
	}
}

func TestProjectMemorySkipsCircularImport(t *testing.T) {
	root := t.TempDir()
	writeInstructionFile(t, filepath.Join(root, "AGENTS.md"), "root\n@a.md")
	writeInstructionFile(t, filepath.Join(root, "a.md"), "a\n@AGENTS.md")
	bundle, err := LoadProjectMemory(root, MemoryLoadOptions{DisableUser: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Sections) != 1 || !strings.Contains(bundle.Sections[0].Content, "skipped circular import") {
		t.Fatalf("sections = %#v", bundle.Sections)
	}
}

func TestProjectMemoryIgnoresImportsInsideCodeFenceAndStripsHTMLComments(t *testing.T) {
	root := t.TempDir()
	writeInstructionFile(t, filepath.Join(root, "inside.md"), "should not load")
	fence := strings.Repeat(string(rune(96)), 3)
	content := "visible\n<!-- secret comment -->\n" + fence + "\n@inside.md\n" + fence
	writeInstructionFile(t, filepath.Join(root, "AGENTS.md"), content)
	bundle, err := LoadProjectMemory(root, MemoryLoadOptions{DisableUser: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Imports) != 0 || strings.Contains(bundle.Sections[0].Content, "secret comment") || !strings.Contains(bundle.Sections[0].Content, "@inside.md") {
		t.Fatalf("bundle = %#v", bundle)
	}
}

func TestProjectMemoryDeniesImportOutsideEffectiveRoots(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.md")
	writeInstructionFile(t, outside, "outside")
	writeInstructionFile(t, filepath.Join(root, "AGENTS.md"), "@"+outside)
	bundle, err := LoadProjectMemory(root, MemoryLoadOptions{DisableUser: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Imports) != 0 || !strings.Contains(bundle.Sections[0].Content, "import denied") || strings.Contains(bundle.Sections[0].Content, "\noutside") {
		t.Fatalf("bundle = %#v", bundle)
	}
}

func TestProjectMemoryAllowsImportFromAdditionalWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	extra := t.TempDir()
	shared := filepath.Join(extra, "shared.md")
	writeInstructionFile(t, shared, "shared instructions")
	writeInstructionFile(t, filepath.Join(root, "AGENTS.md"), "@"+shared)
	bundle, err := LoadProjectMemory(root, MemoryLoadOptions{DisableUser: true, WorkspaceRoots: []string{root, extra}})
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Imports) != 1 || !strings.Contains(bundle.Sections[0].Content, "shared instructions") {
		t.Fatalf("bundle = %#v", bundle)
	}
}

func TestProjectMemoryDeniesHomeImportOutsideWorkspaceRoots(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	writeInstructionFile(t, filepath.Join(home, "secret.md"), "home secret")
	writeInstructionFile(t, filepath.Join(root, "AGENTS.md"), "@~/secret.md")
	bundle, err := LoadProjectMemory(root, MemoryLoadOptions{DisableUser: true, HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Imports) != 0 || !strings.Contains(bundle.Sections[0].Content, "import denied") {
		t.Fatalf("bundle = %#v", bundle)
	}
}

func TestProjectMemoryDeniesSymlinkImportEscapingWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.md")
	writeInstructionFile(t, outside, "outside")
	link := filepath.Join(root, "link.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	writeInstructionFile(t, filepath.Join(root, "AGENTS.md"), "@link.md")
	bundle, err := LoadProjectMemory(root, MemoryLoadOptions{DisableUser: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Imports) != 0 || !strings.Contains(bundle.Sections[0].Content, "import denied") {
		t.Fatalf("bundle = %#v", bundle)
	}
}

func TestImportReadRejectsSymlinkSwapOutsideWorkspace(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symlink creation may require Windows Developer Mode or elevation")
	}
	root := t.TempDir()
	outside := t.TempDir()
	safeDir := filepath.Join(root, "safe")
	if err := os.Mkdir(safeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(safeDir, "import.md")
	writeInstructionFile(t, inside, "inside")
	writeInstructionFile(t, filepath.Join(outside, "import.md"), "outside secret")

	expander := newImportExpander([]string{root}, "", DefaultImportMaxDepth, DefaultSectionMaxBytes, DefaultSectionMaxLines)
	defer expander.Close()
	resolved, err := expander.resolveImport("safe/import.md", root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(inside); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(safeDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, safeDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	data, err := expander.readResolvedImport(resolved)
	if err == nil || strings.Contains(string(data), "outside secret") {
		t.Fatalf("rooted import followed swapped symlink: data=%q err=%v", data, err)
	}
}

func TestProjectMemoryImportDepthLimit(t *testing.T) {
	root := t.TempDir()
	writeInstructionFile(t, filepath.Join(root, "AGENTS.md"), "@one.md")
	writeInstructionFile(t, filepath.Join(root, "one.md"), "one\n@two.md")
	writeInstructionFile(t, filepath.Join(root, "two.md"), "two\n@three.md")
	writeInstructionFile(t, filepath.Join(root, "three.md"), "three")
	bundle, err := LoadProjectMemory(root, MemoryLoadOptions{DisableUser: true, ImportMaxDepth: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Imports) != 2 || !strings.Contains(bundle.Sections[0].Content, "@three.md") {
		t.Fatalf("bundle = %#v", bundle)
	}
}

func TestProjectMemoryDedupeUsesExpandedImportContent(t *testing.T) {
	root := t.TempDir()
	writeInstructionFile(t, filepath.Join(root, "AGENTS.md"), "@rule.md")
	writeInstructionFile(t, filepath.Join(root, "rule.md"), "root rule")
	writeInstructionFile(t, filepath.Join(root, ".agents", "AGENTS.md"), "@rule.md")
	writeInstructionFile(t, filepath.Join(root, ".agents", "rule.md"), "agents rule")
	bundle, err := LoadProjectMemory(root, MemoryLoadOptions{DisableUser: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Sections) != 2 || !strings.Contains(bundle.Sections[0].Content, "root rule") || !strings.Contains(bundle.Sections[1].Content, "agents rule") {
		t.Fatalf("sections = %#v", bundle.Sections)
	}
}
