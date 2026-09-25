package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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

func TestRepairJSONLTailDropsOnlyIncompleteRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, []byte("{\"sequence\":1}\n{\"sequence\":2}\n{\"sequence\":3"), 0600); err != nil {
		t.Fatal(err)
	}
	repaired, err := RepairJSONLTail(path)
	if err != nil || !repaired {
		t.Fatalf("repaired=%t err=%v", repaired, err)
	}
	if err := AppendJSONL(path, map[string]int{"sequence": 4}, 0600, 128); err != nil {
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
	if !slices.Equal(sequences, []int{1, 2, 4}) {
		t.Fatalf("sequences=%v", sequences)
	}
	repaired, err = RepairJSONLTail(path)
	if err != nil || repaired {
		t.Fatalf("second repair repaired=%t err=%v", repaired, err)
	}
}

func TestJSONLRecoverTailIgnoresMalformedFinalRecordAndRejectsMiddleCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, []byte("{\"sequence\":1}\n{broken}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	sequences := []int{}
	tailIssue, err := ReadJSONLRecoverTail(path, 128, func(line []byte) error {
		var value struct {
			Sequence int `json:"sequence"`
		}
		if err := json.Unmarshal(line, &value); err != nil {
			return err
		}
		sequences = append(sequences, value.Sequence)
		return nil
	})
	if err != nil || !tailIssue || !slices.Equal(sequences, []int{1}) {
		t.Fatalf("tailIssue=%t sequences=%v err=%v", tailIssue, sequences, err)
	}
	repaired, err := RepairJSONLTail(path)
	if err != nil || !repaired {
		t.Fatalf("repair malformed tail repaired=%t err=%v", repaired, err)
	}
	if err := AppendJSONL(path, map[string]int{"sequence": 2}, 0600, 128); err != nil {
		t.Fatal(err)
	}

	middle := filepath.Join(t.TempDir(), "middle.jsonl")
	if err := os.WriteFile(middle, []byte("{\"sequence\":1}\n{broken}\n{\"sequence\":2}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadJSONLRecoverTail(middle, 128, func(line []byte) error {
		var value map[string]any
		return json.Unmarshal(line, &value)
	}); err == nil {
		t.Fatal("middle JSONL corruption was treated as recoverable tail")
	}
}
