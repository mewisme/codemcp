package memory

import (
	"errors"
	"testing"
)

func TestMergeDocumentsDedupesAndRejectsSemanticConflicts(t *testing.T) {
	registered := Document{Entries: []Entry{
		{Scope: "project", Key: "alpha", Note: "one"},
		{Scope: "project", Key: "shared", Note: "same"},
	}}
	destination := Document{Entries: []Entry{
		{Scope: "PROJECT", Key: "SHARED", Note: "same"},
		{Scope: "project", Key: "beta", Note: "two"},
	}}
	merged, err := MergeDocuments(registered, destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Entries) != 3 {
		t.Fatalf("entries=%#v", merged.Entries)
	}
	_, err = MergeDocuments(
		Document{Entries: []Entry{{Scope: "project", Key: "same", Note: "left"}}},
		Document{Entries: []Entry{{Scope: "PROJECT", Key: "SAME", Note: "right"}}},
	)
	if !errors.Is(err, ErrMergeConflict) {
		t.Fatalf("conflict err=%v", err)
	}
}
