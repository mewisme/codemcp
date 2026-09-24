package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteJSONAtomicReplacesCompleteDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := WriteJSONAtomic(path, map[string]any{"version": 1, "value": "first"}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSONAtomic(path, map[string]any{"version": 1, "value": "second"}, 0600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	if value["value"] != "second" {
		t.Fatalf("value = %#v", value)
	}
}

func TestJSONLAppendReadBoundsAndIgnoresIncompleteTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	for _, value := range []map[string]any{{"sequence": 1}, {"sequence": 2}} {
		if err := AppendJSONL(path, value, 0600, 128); err != nil {
			t.Fatal(err)
		}
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"sequence":3`); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	sequences := []int{}
	if err := ReadJSONL(path, 128, func(line []byte) error {
		var value struct {
			Sequence int `json:"sequence"`
		}
		if err := json.Unmarshal(line, &value); err != nil {
			return err
		}
		sequences = append(sequences, value.Sequence)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(sequences) != 2 || sequences[0] != 1 || sequences[1] != 2 {
		t.Fatalf("sequences = %v", sequences)
	}
	if err := AppendJSONL(filepath.Join(t.TempDir(), "too-large.jsonl"), map[string]string{"value": "0123456789"}, 0600, 8); err == nil {
		t.Fatal("oversized JSONL record was accepted")
	}
}
