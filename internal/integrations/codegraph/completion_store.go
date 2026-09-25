package codegraph

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	agentcompletion "go.mewis.me/codemcp/internal/history/completion"
	statepkg "go.mewis.me/codemcp/internal/state"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

const (
	completionOutcomeVersion = 1
	completionOutcomeFile    = "codegraph-completions.json"
	completionOutcomeLimit   = agentcompletion.DefaultMaxRecords
)

type completionOutcomeDisk struct {
	Version  int                     `json:"version"`
	Outcomes []CompletionSyncOutcome `json:"outcomes"`
}

func loadCompletionOutcomes(local workspacestate.Store) ([]CompletionSyncOutcome, error) {
	path, err := local.StatePath(completionOutcomeFile)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []CompletionSyncOutcome{}, nil
	}
	if err != nil {
		return nil, err
	}
	var stored completionOutcomeDisk
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("decode CodeGraph completion outcomes: %w", err)
	}
	if stored.Version != completionOutcomeVersion {
		return nil, fmt.Errorf("unsupported CodeGraph completion outcome version: %d", stored.Version)
	}
	if len(stored.Outcomes) > completionOutcomeLimit {
		stored.Outcomes = append([]CompletionSyncOutcome(nil), stored.Outcomes[len(stored.Outcomes)-completionOutcomeLimit:]...)
	}
	return stored.Outcomes, nil
}

func saveCompletionOutcome(local workspacestate.Store, outcome CompletionSyncOutcome) error {
	outcomes, err := loadCompletionOutcomes(local)
	if err != nil {
		return err
	}
	replaced := false
	for index := range outcomes {
		if outcomes[index].CompletionID == outcome.CompletionID {
			outcomes[index] = outcome
			replaced = true
			break
		}
	}
	if !replaced {
		outcomes = append(outcomes, outcome)
	}
	if overflow := len(outcomes) - completionOutcomeLimit; overflow > 0 {
		outcomes = append([]CompletionSyncOutcome(nil), outcomes[overflow:]...)
	}
	path, err := local.StatePath(completionOutcomeFile)
	if err != nil {
		return err
	}
	return statepkg.WriteJSONAtomic(path, completionOutcomeDisk{Version: completionOutcomeVersion, Outcomes: outcomes}, 0600)
}
