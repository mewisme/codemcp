package component

import (
	"errors"
	"testing"
)

func TestCopyTextUsesClipboardAndRejectsEmpty(t *testing.T) {
	previous := clipboardWriteAll
	t.Cleanup(func() { clipboardWriteAll = previous })
	got := ""
	clipboardWriteAll = func(value string) error { got = value; return nil }
	if err := CopyText("token"); err != nil || got != "token" {
		t.Fatalf("copy err=%v value=%q", err, got)
	}
	if err := CopyText(" "); err == nil {
		t.Fatal("empty copy unexpectedly succeeded")
	}
	clipboardWriteAll = func(string) error { return errors.New("unavailable") }
	if err := CopyText("token"); err == nil {
		t.Fatal("clipboard failure was swallowed")
	}
}
