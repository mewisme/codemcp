package tools

import (
	"context"
	"runtime"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/backgrounddelivery"
)

func TestStartProcessRegistersOwnerScopedBackgroundDelivery(t *testing.T) {
	runtimeTools := NewRuntime()
	t.Cleanup(func() {
		if runtimeTools.BackgroundDeliveries != nil {
			runtimeTools.BackgroundDeliveries.Close()
		}
	})
	workspaceItem, err := runtimeTools.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	owner := backgrounddelivery.Owner{ID: "owner_test", Generation: "generation_test"}
	ctx := backgrounddelivery.WithOwner(context.Background(), owner)
	ctx = WithCallSource(ctx, "test")
	command := "printf broker-ok"
	if runtime.GOOS == "windows" {
		command = "Write-Output broker-ok"
	}
	result, err := runtimeTools.Call(ctx, "start_process", map[string]any{
		"workspace_id": workspaceItem.ID,
		"command":      command,
	})
	if err != nil || result.IsError {
		t.Fatalf("start_process result=%#v err=%v", result, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		deliveries, listErr := runtimeTools.BackgroundDeliveries.List(workspaceItem.ID, owner)
		if listErr != nil {
			t.Fatal(listErr)
		}
		if len(deliveries) == 1 {
			if deliveries[0].ProcessID == "" || deliveries[0].ExecutionID == "" || deliveries[0].CallID == "" {
				t.Fatalf("delivery correlation incomplete: %#v", deliveries[0])
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("background terminal delivery was not materialized")
}

func TestStartProcessWithoutLogicalOwnerDoesNotCreateDelivery(t *testing.T) {
	runtimeTools := NewRuntime()
	t.Cleanup(func() {
		if runtimeTools.BackgroundDeliveries != nil {
			runtimeTools.BackgroundDeliveries.Close()
		}
	})
	workspaceItem, err := runtimeTools.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	command := "printf no-owner"
	if runtime.GOOS == "windows" {
		command = "Write-Output no-owner"
	}
	result, err := runtimeTools.Call(WithCallSource(context.Background(), "test"), "start_process", map[string]any{
		"workspace_id": workspaceItem.ID,
		"command":      command,
	})
	if err != nil || result.IsError {
		t.Fatalf("start_process result=%#v err=%v", result, err)
	}
	if _, err := runtimeTools.BackgroundDeliveries.List(workspaceItem.ID, backgrounddelivery.Owner{}); err == nil {
		t.Fatal("ownerless lookup unexpectedly authorized")
	}
}
