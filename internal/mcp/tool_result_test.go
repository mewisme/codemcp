package mcp

import (
	"context"
	"testing"

	"go.mewis.me/codemcp/internal/tools"
)

func TestRuntimeToolCallReturnsMCPResult(t *testing.T) {
	runtime := NewRuntime()
	result, err := runtime.Handle(context.Background(), "tools/call", map[string]any{
		"name": "read_text_file",
		"arguments": map[string]any{
			"workspace_id": "ws_missing",
			"path":         "missing.txt",
		},
	})
	if err != nil {
		t.Fatalf("tool failure returned protocol error: %v", err)
	}
	toolResult, ok := result.(tools.Result)
	if !ok {
		t.Fatalf("result = %T, want tools.Result", result)
	}
	if !toolResult.IsError || len(toolResult.Content) == 0 {
		t.Fatalf("unexpected tool result: %#v", toolResult)
	}
}

func TestRuntimeUnknownToolIsInvalidParams(t *testing.T) {
	runtime := NewRuntime()
	_, err := runtime.Handle(context.Background(), "tools/call", map[string]any{
		"name":      "missing",
		"arguments": map[string]any{},
	})
	protocolErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("error = %T, want *mcp.Error", err)
	}
	if protocolErr.Code != ErrInvalidParams {
		t.Fatalf("code = %d, want %d", protocolErr.Code, ErrInvalidParams)
	}
}
