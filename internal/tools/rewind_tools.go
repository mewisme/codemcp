package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"go.mewis.me/codemcp/internal/checkpoint"
	"go.mewis.me/codemcp/internal/controlguard"
	"go.mewis.me/codemcp/internal/workspace"
)

type RewindStatusResult struct {
	Action string         `json:"action"`
	Config map[string]any `json:"config"`
}

type RewindListResult struct {
	Action      string               `json:"action"`
	Count       int                  `json:"count"`
	Checkpoints []checkpoint.Summary `json:"checkpoints"`
	Hint        string               `json:"hint"`
}

type RewindClearResult struct {
	Action   string `json:"action"`
	Cleared  int    `json:"cleared"`
	Archived int    `json:"archived"`
}

type RewindPurgeResult struct {
	Action         string `json:"action"`
	Purged         int    `json:"purged"`
	ActivePurged   int    `json:"active_purged"`
	ArchivedPurged int    `json:"archived_purged"`
}

type RewindPreviewResult struct {
	Action           string                     `json:"action"`
	Checkpoint       checkpoint.Summary         `json:"checkpoint"`
	Changes          []checkpoint.RestoreChange `json:"changes"`
	SkippedSnapshots []checkpoint.RestoreChange `json:"skipped_snapshots"`
}

type RewindRestoreResult struct {
	Action        string                     `json:"action"`
	Checkpoint    checkpoint.Summary         `json:"checkpoint"`
	Restored      []string                   `json:"restored"`
	RestoredCount int                        `json:"restored_count"`
	Deleted       []string                   `json:"deleted"`
	Skipped       []checkpoint.RestoreChange `json:"skipped"`
	Archived      int                        `json:"archived"`
	Note          string                     `json:"note"`
}

func RegisterRewindTools(registry *Registry, workspaces *workspace.Manager, checkpoints *checkpoint.Store) {
	registry.MustRegister("rewind", Schema{
		Name:         "rewind",
		Title:        "Rewind",
		Description:  "List automatic file checkpoints, preview or restore changes, inspect config, archive active checkpoints with clear, or permanently delete checkpoint history with purge. Shell command file changes are not tracked.",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"workspace_id":{"type":"string"},"action":{"type":"string","enum":["list","preview","restore","status","clear","purge"],"default":"list"},"checkpoint_id":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":200,"default":30}},"required":["workspace_id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","additionalProperties":true}`),
		Annotations:  ToolAnnotations(RiskDestructive),
	}, func(ctx context.Context, args map[string]any) (Result, error) {
		item, err := workspaceFromArgs(workspaces, args)
		if err != nil {
			return Result{}, err
		}
		action, err := optionalEnum(args, "action", "list", "list", "preview", "restore", "status", "clear", "purge")
		if err != nil {
			return Result{}, err
		}
		limit, err := optionalInt(args, "limit", 30, 1, 200)
		if err != nil {
			return Result{}, err
		}

		switch action {
		case "status":
			return JSONResult(RewindStatusResult{Action: action, Config: checkpoints.Config(item.ID)}), nil
		case "list":
			values, err := checkpoints.List(item.ID, limit)
			if err != nil {
				return Result{}, err
			}
			return JSONResult(RewindListResult{
				Action: action, Count: len(values), Checkpoints: values,
				Hint: "Call rewind with action=preview or action=restore and checkpoint_id to revert file changes.",
			}), nil
		case "clear":
			value, err := checkpoints.Clear(item.ID)
			if err != nil {
				return Result{}, err
			}
			return JSONResult(RewindClearResult{Action: action, Cleared: value.Cleared, Archived: value.Archived}), nil
		case "purge":
			if grant, ok := controlguard.GrantFromContext(ctx); !ok || grant.Code != controlguard.CodeDestructiveMutation {
				return Result{}, controlguard.New(controlguard.CodeDestructiveMutation, "purging checkpoint history requires local approval", true, nil)
			}
			value, err := checkpoints.Purge(item.ID)
			if err != nil {
				return Result{}, err
			}
			return JSONResult(RewindPurgeResult{
				Action: action, Purged: value.Purged, ActivePurged: value.ActivePurged, ArchivedPurged: value.ArchivedPurged,
			}), nil
		}

		checkpointID, err := requiredString(args, "checkpoint_id")
		if err != nil {
			return Result{}, errors.New("checkpoint_id is required for preview and restore")
		}
		known, err := checkpoints.Get(item.ID, checkpointID)
		if err != nil {
			return Result{}, err
		}
		if known == nil {
			return Result{}, fmt.Errorf("unknown checkpoint_id: %s. Use action=list first", checkpointID)
		}

		roots, err := workspaces.EffectiveRoots(item.ID)
		if err != nil {
			return Result{}, err
		}
		preview, err := checkpoints.PreviewRestoreAllowed(item.ID, item.Path, roots, checkpointID)
		if err != nil {
			return Result{}, err
		}
		for _, change := range preview.Changes {
			if _, err := workspaces.ResolvePath(item.ID, item.Path, change.Path, false); err != nil {
				return Result{}, fmt.Errorf("rewind path validation failed: %w", err)
			}
		}
		if err := checkpoints.ValidateRestorePathsAllowed(item.ID, item.Path, roots, checkpointID); err != nil {
			return Result{}, err
		}

		if action == "preview" {
			return JSONResult(RewindPreviewResult{
				Action: action, Checkpoint: preview.Checkpoint, Changes: preview.Changes, SkippedSnapshots: preview.SkippedSnapshots,
			}), nil
		}

		value, err := checkpoints.RestoreAllowed(item.ID, item.Path, roots, checkpointID)
		if err != nil {
			return Result{}, err
		}
		return JSONResult(RewindRestoreResult{
			Action: action, Checkpoint: value.Checkpoint, Restored: value.Restored, RestoredCount: value.RestoredCount,
			Deleted: value.Deleted, Skipped: value.Skipped, Archived: value.Archived,
			Note: "Code restored. Conversation history is unchanged. Checkpoints at and after this point were archived.",
		}), nil
	})
}
