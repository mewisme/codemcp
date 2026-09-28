package cli

import (
	"context"
	"testing"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func TestAliasDispatchTraceUsesCanonicalOperationAndPath(t *testing.T) {
	root := newRootCommand()
	cmd, remaining, err := root.Find([]string{"cfg", "set"})
	if err != nil || len(remaining) != 0 {
		t.Fatalf("resolve alias: remaining=%v err=%v", remaining, err)
	}
	operation, ok := canonicalCommandOperation(cmd)
	if !ok {
		t.Fatal("config set command has no canonical operation")
	}

	var dispatch tracepkg.Event
	ctx := tracepkg.WithObserver(context.Background(), func(event tracepkg.Event) {
		if event.Name == "cli.command.dispatch" {
			dispatch = event
		}
	})
	cmd.SetContext(ctx)
	traceCommandDispatch(cmd)

	if dispatch.Name == "" {
		t.Fatal("canonical dispatch trace was not emitted")
	}
	fields := map[string]any{}
	for _, field := range dispatch.Fields {
		fields[field.Key] = field.Value
	}
	if got := fields["command_path"]; got != "config set" {
		t.Fatalf("command_path=%v want config set", got)
	}
	if got := fields["operation_id"]; got != string(operation) {
		t.Fatalf("operation_id=%v want=%s", got, operation)
	}
}
