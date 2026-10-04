package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeNativeSkillDescriptionUsesAgentSkillsCharacterLimit(t *testing.T) {
	ascii := strings.Repeat("a", MaxNativeSkillDescriptionChars)
	if got, err := NormalizeNativeSkillDescription(ascii); err != nil || got != ascii {
		t.Fatalf("max-length ASCII description: got=%q err=%v", got, err)
	}

	unicode := strings.Repeat("界", MaxNativeSkillDescriptionChars)
	if got, err := NormalizeNativeSkillDescription(unicode); err != nil || got != unicode {
		t.Fatalf("max-length Unicode description: runes=%d bytes=%d err=%v", len([]rune(unicode)), len([]byte(unicode)), err)
	}

	if _, err := NormalizeNativeSkillDescription(strings.Repeat("a", MaxNativeSkillDescriptionChars+1)); err == nil || !strings.Contains(err.Error(), "1024 characters") {
		t.Fatalf("over-limit description err=%v", err)
	}
}

func TestNativeSkillManifestCompatibilityRules(t *testing.T) {
	for _, name := range []string{"trailing-", "double--hyphen", "-leading", "Upper"} {
		if _, err := ValidateNativeSkillName(name); err == nil {
			t.Fatalf("invalid name %q was accepted", name)
		}
	}
	for _, name := range []string{"a", "skill-1", "frontend-design"} {
		if got, err := ValidateNativeSkillName(name); err != nil || got != name {
			t.Fatalf("valid name %q: got=%q err=%v", name, got, err)
		}
	}

	manifest, err := ParseManifest([]byte("---\nname: multiline\ndescription: >\n  First line\n  second line\ncompatibility: Works on supported agents\nallowed-tools: Bash Read\nmetadata:\n  vendor: example\nx-vendor: preserved\n---\n"))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Description != "First line second line\n" || strings.TrimSpace(manifest.Instructions) != "" {
		t.Fatalf("manifest=%#v", manifest)
	}
	if manifest.Frontmatter["x-vendor"] != "preserved" {
		t.Fatalf("vendor extension was not preserved: %#v", manifest.Frontmatter)
	}
}

func TestParseManifestAcceptsLoosePlainDescriptionWithColonSpace(t *testing.T) {
	description := "Create diagrams for everyday subjects with steps, parts, relationships, or states: a leave or travel plan, an application or approval process."
	manifest, err := ParseManifest([]byte("---\nname: archify\ndescription: " + description + "\nlicense: MIT\nmetadata:\n  version: \"3.0\"\n  author: tt-a1i\n---\n# Archify\n"))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Name != "archify" || manifest.Description != description {
		t.Fatalf("manifest=%#v", manifest)
	}
	metadata, ok := manifest.Frontmatter["metadata"].(map[string]any)
	if !ok || metadata["author"] != "tt-a1i" {
		t.Fatalf("metadata=%#v", manifest.Frontmatter["metadata"])
	}
}

func TestParseManifestLooseDescriptionFallbackDoesNotMaskOtherMalformedYAML(t *testing.T) {
	_, err := ParseManifest([]byte("---\nname: broken\ndescription: Contains a colon: but metadata is still malformed\nmetadata: [\n---\n"))
	if err == nil || !strings.Contains(err.Error(), "parse skill frontmatter") {
		t.Fatalf("malformed frontmatter err=%v", err)
	}
}

func TestValidateNativeSkillDirectoryRequiresNameMatch(t *testing.T) {
	root := filepath.Join(t.TempDir(), "directory-name")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("---\nname: other-name\ndescription: mismatch\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateNativeSkillDirectory(root); err == nil || !strings.Contains(err.Error(), "must match") {
		t.Fatalf("directory mismatch err=%v", err)
	}
}

func TestHashNativeSkillRootIsDeterministicAndContentSensitive(t *testing.T) {
	root := filepath.Join(t.TempDir(), "hash-skill")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("---\nname: hash-skill\ndescription: hash\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resource := filepath.Join(root, "resource.txt")
	if err := os.WriteFile(resource, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := HashNativeSkillRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := HashNativeSkillRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("hash is not deterministic: %q != %q", first, second)
	}
	if err := ValidateSkillContentHash(first); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resource, []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := HashNativeSkillRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Fatal("content change did not change skill hash")
	}
}

func TestValidateNativeSkillRootAllowsExternalSkillResourceSetsBeyondAuthoringQuota(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("---\nname: external-bundle\ndescription: External bundle\n---\nUse the bundled resources.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	references := filepath.Join(root, "references")
	if err := os.MkdirAll(references, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		name := filepath.Join(references, fmt.Sprintf("reference-%02d.md", i))
		if err := os.WriteFile(name, []byte(strings.Repeat("r", 22_000)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "large-resource.bin"), []byte(strings.Repeat("x", 300_000)), 0o644); err != nil {
		t.Fatal(err)
	}

	validated, err := ValidateNativeSkillRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(validated.Files) != 52 {
		t.Fatalf("validated files=%d want=52", len(validated.Files))
	}
}
