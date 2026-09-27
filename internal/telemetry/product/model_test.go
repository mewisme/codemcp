package product

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTypedEventAndBatchUseOnlyFrozenSchema(t *testing.T) {
	duration := int64(42)
	success := true
	event, err := NewEvent(EventOperationCompleted, ClientFields{
		AnonymousID: "123e4567-e89b-12d3-a456-426614174000",
		Version:     "0.3.0", OS: "linux", Arch: "amd64",
	}, EventFields{
		Interface: InterfaceCLI, Command: "workspace.list", Feature: "workspace.list",
		ErrorCode: ErrorUnavailable, DurationMS: &duration, Success: &success,
	})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := NewBatch([]Event{event})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	if len(root) != 2 || root["schema"] != float64(1) {
		t.Fatalf("batch schema=%s", data)
	}
	events := root["events"].([]any)
	fields := events[0].(map[string]any)
	allowed := map[string]bool{
		"name": true, "anonymous_id": true, "version": true, "os": true, "arch": true,
		"interface": true, "command": true, "feature": true, "error_code": true,
		"duration_ms": true, "success": true,
	}
	for key := range fields {
		if !allowed[key] {
			t.Fatalf("unexpected outbound field %q in %s", key, data)
		}
	}
	for _, forbidden := range []string{"args", "path", "workspace_id", "raw_error", "metadata"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("forbidden telemetry material %q in %s", forbidden, data)
		}
	}
}

func TestEventValidationRejectsUnboundedAndArbitraryValues(t *testing.T) {
	client := ClientFields{AnonymousID: "123e4567-e89b-12d3-a456-426614174000"}
	tooLong := strings.Repeat("x", MaxStringLength+1)
	badDuration := MaxDurationMS + 1
	tests := []EventFields{
		{Interface: Interface("browser")},
		{Command: "workspace list --raw"},
		{Feature: tooLong},
		{ErrorCode: ErrorCode("provider_private_detail")},
		{DurationMS: &badDuration},
	}
	for _, fields := range tests {
		if _, err := NewEvent(EventOperationCompleted, client, fields); err == nil {
			t.Fatalf("unsafe fields accepted: %#v", fields)
		}
	}
	if _, err := NewEvent(EventName("raw.custom"), client, EventFields{}); err == nil {
		t.Fatal("arbitrary event name accepted")
	}
	if _, err := NewBatch(nil); err == nil {
		t.Fatal("empty batch accepted")
	}
	events := make([]Event, MaxBatchEvents+1)
	if _, err := NewBatch(events); err == nil {
		t.Fatal("oversized event-count batch accepted")
	}

	large := make([]Event, MaxBatchEvents)
	client = ClientFields{
		AnonymousID: "123e4567-e89b-42d3-a456-426614174000",
		Version:     strings.Repeat("v", MaxStringLength),
		OS:          strings.Repeat("o", MaxStringLength),
		Arch:        strings.Repeat("a", MaxStringLength),
	}
	for i := range large {
		event, err := NewEvent(EventOperationCompleted, client, EventFields{
			Command: strings.Repeat("c", MaxStringLength),
			Feature: strings.Repeat("f", MaxStringLength),
		})
		if err != nil {
			t.Fatal(err)
		}
		large[i] = event
	}
	if _, err := NewBatch(large); err == nil {
		t.Fatal("oversized serialized batch accepted")
	}
}
