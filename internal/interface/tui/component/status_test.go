package component

import (
	"strings"
	"testing"
)

func TestProgressTransitions(t *testing.T) {
	progress := NewProgress("Loading")
	if progress.State() != OperationRunning || !strings.Contains(progress.View(), "Loading") {
		t.Fatalf("progress=%#v view=%q", progress, progress.View())
	}
	progress.Complete("Done")
	if progress.State() != OperationSuccess || !strings.Contains(progress.View(), "Done") {
		t.Fatalf("complete=%q", progress.View())
	}
	progress.Fail("Failed")
	if progress.State() != OperationFailed || !strings.Contains(progress.View(), "Failed") {
		t.Fatalf("failed=%q", progress.View())
	}
}

func TestStateView(t *testing.T) {
	if value := StateView(PageError, "Unable", "details"); !strings.Contains(value, "Unable") || !strings.Contains(value, "details") {
		t.Fatalf("state view=%q", value)
	}
}
