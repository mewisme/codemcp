package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateWorkspaceStateAcceptsAbsentEmptyAndValidPlans(t *testing.T) {
	root := filepath.Join(t.TempDir(), "plans")
	if err := ValidateWorkspaceState(root); err != nil {
		t.Fatalf("absent state error = %v", err)
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWorkspaceState(root); err != nil {
		t.Fatalf("empty state error = %v", err)
	}
	document := testWorkspacePlanDocument(t, false)
	if err := os.WriteFile(filepath.Join(root, "oauth-redesign.md"), document, 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWorkspaceState(root); err != nil {
		t.Fatalf("valid state error = %v", err)
	}
}

func TestValidateWorkspaceStateRejectsUnsafeOrInvalidEntries(t *testing.T) {
	document := testWorkspacePlanDocument(t, false)
	tests := []struct {
		name  string
		setup func(*testing.T, string)
		want  string
	}{
		{
			name: "directory",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(root, "nested"), 0700); err != nil {
					t.Fatal(err)
				}
			},
			want: "non-regular",
		},
		{
			name: "unsupported extension",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, "oauth.txt"), document, 0600); err != nil {
					t.Fatal(err)
				}
			},
			want: "unsupported plan path",
		},
		{
			name: "invalid name",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, "OAuth.md"), document, 0600); err != nil {
					t.Fatal(err)
				}
			},
			want: "invalid plan name",
		},
		{
			name: "corrupt document",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, "oauth.md"), []byte("# incomplete\n"), 0600); err != nil {
					t.Fatal(err)
				}
			},
			want: "is invalid",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "plans")
			if err := os.MkdirAll(root, 0700); err != nil {
				t.Fatal(err)
			}
			tt.setup(t, root)
			err := ValidateWorkspaceState(root)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestValidateWorkspaceStateRejectsSymlinkWithoutFollowingIt(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symlink creation may require Windows Developer Mode or elevation")
	}
	root := filepath.Join(t.TempDir(), "plans")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	outsideData := testWorkspacePlanDocument(t, false)
	if err := os.WriteFile(outside, outsideData, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "oauth.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	err := ValidateWorkspaceState(root)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink error = %v", err)
	}
	data, readErr := os.ReadFile(outside)
	if readErr != nil || string(data) != string(outsideData) {
		t.Fatalf("outside target changed: err=%v data=%q", readErr, data)
	}
}

func TestEquivalentDocumentsUsesCanonicalRenderedIdentity(t *testing.T) {
	canonical := testWorkspacePlanDocument(t, false)
	crlf := []byte(strings.ReplaceAll(string(canonical), "\n", "\r\n"))
	equal, err := EquivalentDocuments(canonical, crlf)
	if err != nil || !equal {
		t.Fatalf("equivalent documents = %t err=%v", equal, err)
	}

	different := testWorkspacePlanDocument(t, true)
	equal, err = EquivalentDocuments(canonical, different)
	if err != nil {
		t.Fatal(err)
	}
	if equal {
		t.Fatal("different plan documents were considered equivalent")
	}

	if _, err := EquivalentDocuments(canonical, []byte("# corrupt")); err == nil {
		t.Fatal("invalid comparison document was accepted")
	}
}

func TestValidateWorkspaceStateRejectsSymlinkedRoot(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symlink creation may require Windows Developer Mode or elevation")
	}
	outside := t.TempDir()
	root := filepath.Join(t.TempDir(), "plans")
	if err := os.Symlink(outside, root); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := ValidateWorkspaceState(root); err == nil {
		t.Fatal("symlinked plans root was accepted")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("outside root changed: entries=%#v err=%v", entries, err)
	}
}

func TestValidateWorkspaceStateRejectsOversizeDocumentBeforeParsing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "plans")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "oversize.md")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", MaxDocumentBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	err := ValidateWorkspaceState(root)
	if err == nil || !strings.Contains(err.Error(), "exceeds size limit") {
		t.Fatalf("oversize error = %v", err)
	}
}

func testWorkspacePlanDocument(t *testing.T, completed bool) []byte {
	t.Helper()
	mark := " "
	if completed {
		mark = "x"
	}
	document, err := ParseParts(
		"# OAuth redesign\n\n## Goal\nUpdate authentication.\n\n## Phase 1A - Implement auth\n\n- ["+mark+"] Implement the flow.\n\n## Acceptance\nAuthentication is verified.",
		"## Execution rules\nComplete the phase.\n\n## Why this order\nThe implementation is direct.\n\n## Ordered phases\n\n- ["+mark+"] Phase 1A - Implement auth\n\n## Terminal acceptance\n\n- ["+mark+"] Integration tests pass.",
	)
	if err != nil {
		t.Fatal(err)
	}
	return document.Render()
}
