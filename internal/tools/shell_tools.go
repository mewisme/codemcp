package tools

import (
	"context"
	"encoding/json"

	"go.mewis.me/codemcp/internal/backgrounddelivery"

	"go.mewis.me/codemcp/internal/instructioncontext"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
	"go.mewis.me/codemcp/internal/workspace"
)

type ProcessStatusResult struct {
	Processes []shellruntime.ProcessInfo `json:"processes"`
}

type ClearProcessesResult struct {
	Cleared int `json:"cleared"`
}

func RegisterShellTools(registry *Registry, workspaces *workspace.Manager, shell *shellruntime.Manager, processes *shellruntime.ProcessManager, brokers ...*backgrounddelivery.Broker) {
	var broker *backgrounddelivery.Broker
	if len(brokers) > 0 {
		broker = brokers[0]
	}
	register := func(name, title, description, input, output string, risk Risk, handler Handler) {
		registry.MustRegister(name, Schema{
			Name: name, Title: title, Description: description,
			InputSchema: json.RawMessage(input), OutputSchema: json.RawMessage(output), Annotations: ToolAnnotations(risk), Capability: toolCapability(CapabilityDomainShell),
		}, handler)
	}

	register("run_command", "Run Command", "Run shell commands in the workspace persisted cwd. Cwd changes persist server-side and all commands remain workspace-contained. Use start_process for builds, tests, or other commands that may run long enough to exceed a synchronous tunnel response deadline.", `{"type":"object","properties":{"workspace_id":{"type":"string"},"command":{"type":"string"}},"required":["workspace_id","command"],"additionalProperties":false}`, `{"type":"object","properties":{"command":{"type":"string"},"cwd":{"type":"string"},"stdout":{"type":"string"},"stderr":{"type":"string"},"stdout_truncated":{"type":"boolean"},"stderr_truncated":{"type":"boolean"},"exit_code":{"type":"integer"},"timed_out":{"type":"boolean"}},"required":["command","cwd","stdout","stderr","stdout_truncated","stderr_truncated","exit_code","timed_out"],"additionalProperties":false}`, RiskCommand, func(ctx context.Context, args map[string]any) (Result, error) {
		workspaceID, err := requiredString(args, "workspace_id")
		if err != nil {
			return Result{}, err
		}
		command, err := requiredString(args, "command")
		if err != nil {
			return Result{}, err
		}
		ctx = shellruntime.WithExecutionSource(ctx, CallSource(ctx))
		value, err := shell.Exec(ctx, workspaceID, command)
		if err != nil {
			return Result{}, err
		}
		result := JSONResult(value)
		if value.ExitCode != 0 {
			result.IsError = true
		}
		return result, nil
	})

	register("shell_status", "Shell Status", "Show persistent shell cwd and recent commands for a registered workspace.", `{"type":"object","properties":{"workspace_id":{"type":"string"}},"required":["workspace_id"],"additionalProperties":false}`, `{"type":"object","properties":{"active":{"type":"boolean"},"cwd":{"type":"string"},"started_at":{"type":"string"},"recent_commands":{"type":"array","items":{"type":"string"}}},"required":["active","cwd","started_at","recent_commands"],"additionalProperties":false}`, RiskRead, func(_ context.Context, args map[string]any) (Result, error) {
		workspaceID, err := requiredString(args, "workspace_id")
		if err != nil {
			return Result{}, err
		}
		value, err := shell.Status(workspaceID)
		if err != nil {
			return Result{}, err
		}
		return JSONResult(value), nil
	})

	register("shell_reset", "Shell Reset", "Reset persistent shell cwd to a workspace directory. Defaults to the registered workspace root.", `{"type":"object","properties":{"workspace_id":{"type":"string"},"path":{"type":"string"}},"required":["workspace_id"],"additionalProperties":false}`, `{"type":"object","properties":{"active":{"type":"boolean"},"cwd":{"type":"string"},"started_at":{"type":"string"},"recent_commands":{"type":"array","items":{"type":"string"}}},"required":["active","cwd","started_at","recent_commands"],"additionalProperties":false}`, RiskEdit, func(_ context.Context, args map[string]any) (Result, error) {
		workspaceID, err := requiredString(args, "workspace_id")
		if err != nil {
			return Result{}, err
		}
		path, err := optionalString(args, "path")
		if err != nil {
			return Result{}, err
		}
		value, err := shell.Reset(workspaceID, path)
		if err != nil {
			return Result{}, err
		}
		return JSONResult(value), nil
	})

	register("start_process", "Start Background Process", "Start a long-running command in the workspace persisted cwd. Background commands cannot contain cwd-changing directives. Completion is lifecycle-driven; do not poll process_status/process_output while waiting.", `{"type":"object","properties":{"workspace_id":{"type":"string"},"command":{"type":"string"}},"required":["workspace_id","command"],"additionalProperties":false}`, `{"type":"object","properties":{"id":{"type":"string"},"execution_id":{"type":"string"},"pid":{"type":"integer"},"command":{"type":"string"},"cwd":{"type":"string"},"started_at":{"type":"string"}},"required":["id","pid","command","cwd","started_at"],"additionalProperties":false}`, RiskCommand, func(ctx context.Context, args map[string]any) (Result, error) {
		workspaceID, err := requiredString(args, "workspace_id")
		if err != nil {
			return Result{}, err
		}
		command, err := requiredString(args, "command")
		if err != nil {
			return Result{}, err
		}
		value, err := processes.Start(ctx, workspaceID, command)
		if err != nil {
			return Result{}, err
		}
		if broker != nil {
			metadata := shellruntime.ExecutionMetadataFromContext(ctx)
			broker.RegisterStart(backgrounddelivery.Registration{
				WorkspaceID: workspaceID,
				ProcessID:   value.ID,
				ExecutionID: value.ExecutionID,
				CallID:      metadata.CallID,
				Owner:       backgrounddelivery.OwnerFromContext(ctx),
			})
		}
		return startProcessResult(ctx, value), nil
	})

	register("process_status", "Process Status", "Inspect background process state for explicit diagnosis or recovery. Do not use this tool as a waiting loop.", `{"type":"object","properties":{"workspace_id":{"type":"string"},"id":{"type":"string"}},"required":["workspace_id"],"additionalProperties":false}`, `{"type":"object","properties":{"processes":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},"execution_id":{"type":"string"},"pid":{"type":"integer"},"command":{"type":"string"},"cwd":{"type":"string"},"started_at":{"type":"string"},"running":{"type":"boolean"},"exit_code":{"type":["integer","null"]},"signal":{"type":["string","null"]}},"required":["id","pid","command","cwd","started_at","running","exit_code","signal"],"additionalProperties":false}}},"required":["processes"],"additionalProperties":false}`, RiskRead, func(_ context.Context, args map[string]any) (Result, error) {
		workspaceID, err := requiredString(args, "workspace_id")
		if err != nil {
			return Result{}, err
		}
		id, err := optionalString(args, "id")
		if err != nil {
			return Result{}, err
		}
		values, err := processes.Status(workspaceID, id)
		if err != nil {
			return Result{}, err
		}
		return JSONResult(ProcessStatusResult{Processes: values}), nil
	})

	register("process_output", "Process Output", "Inspect stdout/stderr for explicit diagnosis or recovery. Do not use this tool as a waiting loop.", `{"type":"object","properties":{"workspace_id":{"type":"string"},"id":{"type":"string"},"tail_chars":{"type":"integer","minimum":1,"maximum":200000,"default":40000}},"required":["workspace_id","id"],"additionalProperties":false}`, `{"type":"object","properties":{"id":{"type":"string"},"running":{"type":"boolean"},"exit_code":{"type":["integer","null"]},"signal":{"type":["string","null"]},"stdout":{"type":"string"},"stderr":{"type":"string"}},"required":["id","running","exit_code","signal","stdout","stderr"],"additionalProperties":false}`, RiskRead, func(_ context.Context, args map[string]any) (Result, error) {
		workspaceID, err := requiredString(args, "workspace_id")
		if err != nil {
			return Result{}, err
		}
		id, err := requiredString(args, "id")
		if err != nil {
			return Result{}, err
		}
		tailChars, err := optionalInt(args, "tail_chars", 40000, 1, 200000)
		if err != nil {
			return Result{}, err
		}
		value, err := processes.Output(workspaceID, id, tailChars)
		if err != nil {
			return Result{}, err
		}
		return JSONResult(value), nil
	})

	register("stop_process", "Stop Process", "Explicitly cancel or intervene in a background process by id. Do not use cancellation as a completion-wait mechanism.", `{"type":"object","properties":{"workspace_id":{"type":"string"},"id":{"type":"string"},"force":{"type":"boolean","default":false}},"required":["workspace_id","id"],"additionalProperties":false}`, `{"type":"object","properties":{"id":{"type":"string"},"force":{"type":"boolean"},"already_exited":{"type":"boolean"}},"required":["id"],"additionalProperties":false}`, RiskEdit, func(_ context.Context, args map[string]any) (Result, error) {
		workspaceID, err := requiredString(args, "workspace_id")
		if err != nil {
			return Result{}, err
		}
		id, err := requiredString(args, "id")
		if err != nil {
			return Result{}, err
		}
		force, err := optionalBool(args, "force", false)
		if err != nil {
			return Result{}, err
		}
		value, err := processes.Stop(workspaceID, id, force)
		if err != nil {
			return Result{}, err
		}
		return JSONResult(value), nil
	})

	register("clear_processes", "Clear Finished Processes", "Remove finished background process records for a workspace.", `{"type":"object","properties":{"workspace_id":{"type":"string"}},"required":["workspace_id"],"additionalProperties":false}`, `{"type":"object","properties":{"cleared":{"type":"integer"}},"required":["cleared"],"additionalProperties":false}`, RiskEdit, func(_ context.Context, args map[string]any) (Result, error) {
		workspaceID, err := requiredString(args, "workspace_id")
		if err != nil {
			return Result{}, err
		}
		cleared, err := processes.Clear(workspaceID)
		if err != nil {
			return Result{}, err
		}
		return JSONResult(ClearProcessesResult{Cleared: cleared}), nil
	})
}

func startProcessResult(ctx context.Context, value shellruntime.StartResult) Result {
	result := JSONResult(value)
	result.Content = []Content{{
		Type: "text",
		Text: "Background process started. " + instructioncontext.BackgroundWorkGuidanceFor(projectBackgroundCapabilities(ctx)),
	}}
	return result
}
