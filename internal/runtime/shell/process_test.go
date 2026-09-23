package shell

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

func TestProcessIDUsesCompactHex(t *testing.T) {
	id, err := processID()
	if err != nil {
		t.Fatal(err)
	}
	value := strings.TrimPrefix(id, "proc_")
	if len(value) != 16 {
		t.Fatalf("process id=%q", id)
	}
	if _, err := hex.DecodeString(value); err != nil {
		t.Fatalf("process id is not hex: %q", id)
	}
}

func TestProcessManagerPrunesFinishedHistory(t *testing.T) {
	now := time.Now().UTC()
	code := 0
	manager := &ProcessManager{processes: map[string]*managedProcess{}, maxFinished: 2, retention: time.Hour}
	add := func(id string, finishedAt time.Time, running bool) {
		process := &managedProcess{id: id, finishedAt: finishedAt, stdout: &logBuffer{}, stderr: &logBuffer{}}
		if !running {
			value := code
			process.exitCode = &value
		}
		manager.processes[id] = process
		manager.order = append(manager.order, id)
	}
	add("expired", now.Add(-2*time.Hour), false)
	add("old", now.Add(-30*time.Minute), false)
	add("middle", now.Add(-20*time.Minute), false)
	add("recent", now.Add(-10*time.Minute), false)
	add("running", time.Time{}, true)

	manager.pruneLocked(now)
	for _, id := range []string{"expired", "old"} {
		if manager.processes[id] != nil {
			t.Fatalf("process %s was not pruned", id)
		}
	}
	for _, id := range []string{"middle", "recent", "running"} {
		if manager.processes[id] == nil {
			t.Fatalf("process %s was pruned unexpectedly", id)
		}
	}
	if len(manager.order) != 3 {
		t.Fatalf("order = %#v", manager.order)
	}
}
