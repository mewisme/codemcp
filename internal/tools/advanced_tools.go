package tools

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"go.mewis.me/codemcp/internal/integrations/caveman"
	"go.mewis.me/codemcp/internal/integrations/fanout"
	"go.mewis.me/codemcp/internal/integrations/ponytail"
	"go.mewis.me/codemcp/internal/jsruntime"
	"go.mewis.me/codemcp/internal/workspace"
)

type NodeResetResult struct {
	Reset bool `json:"reset"`
}

func RegisterAdvancedTools(registry *Registry, workspaces *workspace.Manager) {
	nodeManager := jsruntime.NewManager()
	register := func(name, title, description, input, output string, risk Risk, handler Handler) {
		registry.MustRegister(name, Schema{
			Name: name, Title: title, Description: description,
			InputSchema: json.RawMessage(input), OutputSchema: json.RawMessage(output), Annotations: ToolAnnotations(risk), Capability: toolCapability(CapabilityDomainRuntime),
		}, handler)
	}

	register("node_repl", "Node REPL", "Stateful JavaScript session per workspace. globalThis persists across calls. Filesystem access is constrained by the Node permission model to the registered workspace.", `{"type":"object","properties":{"workspace_id":{"type":"string"},"action":{"type":"string","enum":["eval","reset","status"],"default":"eval"},"code":{"type":"string"},"timeout_ms":{"type":"integer","minimum":100,"maximum":60000,"default":30000}},"required":["workspace_id"],"additionalProperties":false}`, `{"type":"object","additionalProperties":true}`, RiskCommand, func(ctx context.Context, args map[string]any) (Result, error) {
		item, err := workspaceFromArgs(workspaces, args)
		if err != nil {
			return Result{}, err
		}
		action, err := optionalEnum(args, "action", "eval", "eval", "reset", "status")
		if err != nil {
			return Result{}, err
		}
		switch action {
		case "reset":
			if err := nodeManager.Reset(item.ID); err != nil {
				return Result{}, err
			}
			return JSONResult(NodeResetResult{Reset: true}), nil
		case "status":
			value, err := nodeManager.Status(ctx, item.ID, item.Path)
			if err != nil {
				return Result{}, err
			}
			return JSONResult(value), nil
		default:
			code, err := optionalString(args, "code")
			if err != nil {
				return Result{}, err
			}
			if code == "" {
				return Result{}, errors.New("code is required for node_repl eval")
			}
			timeoutMS, err := optionalInt(args, "timeout_ms", 30000, 100, 60000)
			if err != nil {
				return Result{}, err
			}
			callCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS+2000)*time.Millisecond)
			defer cancel()
			value, err := nodeManager.Eval(callCtx, item.ID, item.Path, code, time.Duration(timeoutMS)*time.Millisecond)
			if err != nil {
				return Result{}, err
			}
			return JSONResult(value), nil
		}
	})

}

func ponytailToolEntries(workspaces *workspace.Manager, ponytailManager *ponytail.Manager) map[string]Entry {
	return map[string]Entry{
		"ponytail_turn": integrationEntry("ponytail_turn", "Ponytail Turn Controller", "Ponytail integration controller. Call before each user-facing coding response; configured active/mode values seed each workspace state. Pass the exact current user prompt.", `{"type":"object","properties":{"workspace_id":{"type":"string"},"prompt":{"type":"string"},"action":{"type":"string","enum":["turn","refresh","status"],"default":"turn"}},"required":["workspace_id","prompt"],"additionalProperties":false}`, `{"type":"object","properties":{"available":{"type":"boolean"},"mode":{"type":"string","enum":["off","lite","full","ultra","review"]},"active":{"type":"boolean"},"active_instructions":{"type":"string"},"refresh_hint":{"type":"string"}},"required":["available","mode","active"],"additionalProperties":false}`, RiskRead, func(_ context.Context, args map[string]any) (Result, error) {
			item, err := workspaceFromArgs(workspaces, args)
			if err != nil {
				return Result{}, err
			}
			prompt, err := requiredString(args, "prompt")
			if err != nil {
				return Result{}, err
			}
			action, err := optionalEnum(args, "action", "turn", "turn", "refresh", "status")
			if err != nil {
				return Result{}, err
			}
			value, err := ponytailManager.Turn(item.ID, prompt, action)
			if err != nil {
				return Result{}, err
			}
			return JSONResult(value), nil
		}),
	}
}

func cavemanToolEntries(workspaces *workspace.Manager, cavemanManager *caveman.Manager) map[string]Entry {
	return map[string]Entry{
		"caveman_turn": integrationEntry("caveman_turn", "Caveman Turn Controller", "Caveman integration controller. Call before each user-facing response; configured active/mode values seed each workspace state. Pass the exact current user prompt.", `{"type":"object","properties":{"workspace_id":{"type":"string"},"prompt":{"type":"string"},"action":{"type":"string","enum":["turn","refresh","status"],"default":"turn"}},"required":["workspace_id","prompt"],"additionalProperties":false}`, `{"type":"object","properties":{"available":{"type":"boolean"},"mode":{"type":"string","enum":["off","lite","full","ultra","wenyan-lite","wenyan-full","wenyan-ultra"]},"active":{"type":"boolean"},"active_instructions":{"type":"string"},"refresh_hint":{"type":"string"}},"required":["available","mode","active"],"additionalProperties":false}`, RiskRead, func(_ context.Context, args map[string]any) (Result, error) {
			item, err := workspaceFromArgs(workspaces, args)
			if err != nil {
				return Result{}, err
			}
			prompt, err := requiredString(args, "prompt")
			if err != nil {
				return Result{}, err
			}
			action, err := optionalEnum(args, "action", "turn", "turn", "refresh", "status")
			if err != nil {
				return Result{}, err
			}
			value, err := cavemanManager.Turn(item.ID, prompt, action)
			if err != nil {
				return Result{}, err
			}
			return JSONResult(value), nil
		}),
	}
}

func fanoutToolEntries(runtime *Runtime) map[string]Entry {
	return map[string]Entry{
		"fanout_turn": integrationEntry("fanout_turn", "Fanout Turn Controller", "Fanout integration controller. Consult for substantial work that may benefit from managed-agent delegation. Strategy is advisory and never overrides workspace, security, plan, lifecycle, readiness, or capacity authority. Pass the exact current user prompt.", `{"type":"object","properties":{"workspace_id":{"type":"string"},"prompt":{"type":"string"},"action":{"type":"string","enum":["turn","refresh","status"],"default":"turn"}},"required":["workspace_id","prompt"],"additionalProperties":false}`, `{"type":"object","properties":{"available":{"type":"boolean"},"mode":{"type":"string","enum":["off","auto","conservative","aggressive"]},"active":{"type":"boolean"},"active_instructions":{"type":"string"},"refresh_hint":{"type":"string"}},"required":["available","mode","active"],"additionalProperties":false}`, RiskRead, func(ctx context.Context, args map[string]any) (Result, error) {
			if runtime == nil || runtime.Workspaces == nil || runtime.fanoutManager == nil {
				return Result{}, errors.New("fanout integration is unavailable")
			}
			for key := range args {
				switch key {
				case "workspace_id", "prompt", "action":
				default:
					return Result{}, errors.New("unsupported fanout controller argument")
				}
			}
			item, err := workspaceFromArgs(runtime.Workspaces, args)
			if err != nil {
				return Result{}, err
			}
			prompt, err := requiredString(args, "prompt")
			if err != nil {
				return Result{}, err
			}
			action, err := optionalEnum(args, "action", "turn", "turn", "refresh", "status")
			if err != nil {
				return Result{}, err
			}
			sessionID := MCPSessionID(ctx)
			controllerID := RuntimeStateKey(ctx)
			if controllerID == "" {
				return Result{}, errors.New("fanout controller requires trusted controller identity")
			}
			if runtime.Agents != nil {
				if binding, claimed := runtime.Agents.SessionBinding(sessionID); claimed && binding.Active {
					return JSONResult(fanout.Result{Available: true, Mode: fanout.Off, Active: false}), nil
				}
			}
			value, err := runtime.fanoutManager.Turn(controllerID, item.ID, prompt, action)
			if err != nil {
				return Result{}, err
			}
			return JSONResult(value), nil
		}),
	}
}

func integrationEntry(name, title, description, input, output string, risk Risk, handler Handler) Entry {
	return Entry{Schema: Schema{Name: name, Title: title, Description: description, InputSchema: json.RawMessage(input), OutputSchema: json.RawMessage(output), Annotations: ToolAnnotations(risk), Capability: toolCapability(CapabilityDomainIntegrations)}, Handler: handler}
}
