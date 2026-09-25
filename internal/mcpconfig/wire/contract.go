package mcpconfigwire

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	ListToolName = "config_list"
	GetToolName  = "config_get"
	SetToolName  = "config_set"

	DefaultListLimit = 50
	MaxListLimit     = 100
	MaxCursorBytes   = 1024
	MaxKeyBytes      = 256
	MaxChanges       = 32
	MaxValueBytes    = 32 * 1024
)

var (
	ListInputSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"prefix":{"type":"string","maxLength":256},
			"limit":{"type":"integer","minimum":1,"maximum":100},
			"cursor":{"type":"string","maxLength":1024}
		},
		"additionalProperties":false
	}`)
	GetInputSchema = json.RawMessage(`{
		"type":"object",
		"properties":{"key":{"type":"string","minLength":1,"maxLength":256}},
		"required":["key"],
		"additionalProperties":false
	}`)
	SetInputSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"workspace_id":{"type":"string","maxLength":128},
			"changes":{
				"type":"array",
				"minItems":1,
				"maxItems":32,
				"items":{
					"type":"object",
					"properties":{
						"key":{"type":"string","minLength":1,"maxLength":256},
						"value":{"type":"string","maxLength":32768}
					},
					"required":["key","value"],
					"additionalProperties":false
				}
			}
		},
		"required":["changes"],
		"additionalProperties":false
	}`)
	ListOutputSchema = json.RawMessage(`{
		"type":"object",
		"$defs":{
			"setting":{
				"type":"object",
				"properties":{
					"key":{"type":"string"},
					"label":{"type":"string"},
					"section":{"type":"string"},
					"kind":{"type":"string"},
					"options":{"type":"array","items":{"type":"string"}},
					"readable":{"type":"boolean"},
					"writable":{"type":"boolean"},
					"derived":{"type":"boolean"},
					"secret":{"type":"boolean"},
					"configured":{"type":"boolean"},
					"value":{"type":"string"}
				},
				"required":["key","readable","writable"],
				"additionalProperties":false
			}
		},
		"properties":{
			"settings":{"type":"array","items":{"$ref":"#/$defs/setting"},"maxItems":100},
			"next_cursor":{"type":"string","maxLength":1024}
		},
		"required":["settings"],
		"additionalProperties":false
	}`)
	GetOutputSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"setting":{
				"type":"object",
				"properties":{
					"key":{"type":"string"},
					"label":{"type":"string"},
					"section":{"type":"string"},
					"kind":{"type":"string"},
					"options":{"type":"array","items":{"type":"string"}},
					"readable":{"type":"boolean"},
					"writable":{"type":"boolean"},
					"derived":{"type":"boolean"},
					"secret":{"type":"boolean"},
					"configured":{"type":"boolean"},
					"value":{"type":"string"}
				},
				"required":["key","readable","writable"],
				"additionalProperties":false
			}
		},
		"required":["setting"],
		"additionalProperties":false
	}`)
	SetOutputSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"state":{"type":"string","enum":["unchanged","persisted","runtime_synced","rolled_back","reconciliation_required"]},
			"keys":{"type":"array","items":{"type":"string"},"maxItems":32},
			"change_count":{"type":"integer","minimum":0,"maximum":32},
			"changed":{"type":"boolean"},
			"runtime_reloaded":{"type":"boolean"}
		},
		"required":["state","keys","change_count","changed","runtime_reloaded"],
		"additionalProperties":false
	}`)
)

type Change struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func ValidateChanges(changes []Change) error {
	if len(changes) == 0 {
		return errors.New("at least one config change is required")
	}
	if len(changes) > MaxChanges {
		return fmt.Errorf("config change batch exceeds %d items", MaxChanges)
	}
	seen := make(map[string]struct{}, len(changes))
	for index, change := range changes {
		key := strings.TrimSpace(change.Key)
		if key == "" {
			return fmt.Errorf("change %d key is required", index)
		}
		if len(key) > MaxKeyBytes {
			return fmt.Errorf("change %d key exceeds %d bytes", index, MaxKeyBytes)
		}
		if len(change.Value) > MaxValueBytes {
			return fmt.Errorf("change %d value exceeds %d bytes", index, MaxValueBytes)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate config setting key %q", key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

type BatchSummary struct {
	ChangeCount int      `json:"change_count"`
	Keys        []string `json:"keys"`
}

func SummarizeChanges(changes []Change) BatchSummary {
	keys := make([]string, 0, len(changes))
	for _, change := range changes {
		keys = append(keys, strings.TrimSpace(change.Key))
	}
	return BatchSummary{ChangeCount: len(changes), Keys: keys}
}

func SummarizeArguments(arguments map[string]any) BatchSummary {
	changes := make([]Change, 0)
	switch raw := arguments["changes"].(type) {
	case []any:
		for _, item := range raw {
			entry, _ := item.(map[string]any)
			key, _ := entry["key"].(string)
			changes = append(changes, Change{Key: key})
		}
	case []map[string]any:
		for _, entry := range raw {
			key, _ := entry["key"].(string)
			changes = append(changes, Change{Key: key})
		}
	case []Change:
		changes = append(changes, raw...)
	}
	return SummarizeChanges(changes)
}

func SummarizeRawArguments(raw json.RawMessage) BatchSummary {
	if len(raw) == 0 {
		return BatchSummary{Keys: []string{}}
	}
	var arguments map[string]any
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return BatchSummary{Keys: []string{}}
	}
	return SummarizeArguments(arguments)
}

type ErrorCode string

const (
	ErrorAccessDenied         ErrorCode = "config_access_denied"
	ErrorInvalidRequest       ErrorCode = "config_invalid_request"
	ErrorUnsupportedSetting   ErrorCode = "config_unsupported_setting"
	ErrorSecretWriteForbidden ErrorCode = "config_secret_write_forbidden"
	ErrorApprovalRequired     ErrorCode = "config_approval_required"
	ErrorApplyFailed          ErrorCode = "config_apply_failed"
	ErrorReconciliationNeeded ErrorCode = "config_reconciliation_required"
)

type PublicError struct {
	Code ErrorCode `json:"code"`
	Key  string    `json:"key,omitempty"`
}

type MutationState string

const (
	MutationUnchanged              MutationState = "unchanged"
	MutationPersisted              MutationState = "persisted"
	MutationRuntimeSynced          MutationState = "runtime_synced"
	MutationRolledBack             MutationState = "rolled_back"
	MutationReconciliationRequired MutationState = "reconciliation_required"
)

type MutationResult struct {
	State           MutationState `json:"state"`
	Keys            []string      `json:"keys"`
	ChangeCount     int           `json:"change_count"`
	Changed         bool          `json:"changed"`
	RuntimeReloaded bool          `json:"runtime_reloaded"`
}
