package state

import (
	"os"
	"reflect"
	"testing"
)

func TestStateRoundTripAndRecentActions(t *testing.T) {
	root := t.TempDir()
	value := Default()
	RecordRecent(&value, "config.verify")
	RecordRecent(&value, "workspace.register")
	RecordRecent(&value, "config.verify")
	if want := []string{"config.verify", "workspace.register"}; !reflect.DeepEqual(value.RecentActions, want) {
		t.Fatalf("recent = %v want %v", value.RecentActions, want)
	}
	if err := Save(root, value); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, value) {
		t.Fatalf("loaded = %#v want %#v", loaded, value)
	}
}

func TestCorruptStateFallsBackWithoutMutation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(Path(root), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	value, err := Load(root)
	if err == nil || !reflect.DeepEqual(value, Default()) {
		t.Fatalf("value=%#v err=%v", value, err)
	}
}
