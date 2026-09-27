package mcpconfig

import (
	"fmt"
	"strings"

	"go.mewis.me/codemcp/internal/config"
	mcpconfigwire "go.mewis.me/codemcp/internal/mcpconfig/wire"
)

const (
	ListToolName = mcpconfigwire.ListToolName
	GetToolName  = mcpconfigwire.GetToolName
	SetToolName  = mcpconfigwire.SetToolName

	ReadEligibilityKey  = "permissions.mcp_config_read"
	WriteEligibilityKey = "permissions.mcp_config_write"

	DefaultListLimit = mcpconfigwire.DefaultListLimit
	MaxListLimit     = mcpconfigwire.MaxListLimit
	MaxCursorBytes   = mcpconfigwire.MaxCursorBytes
	MaxKeyBytes      = mcpconfigwire.MaxKeyBytes
	MaxChanges       = mcpconfigwire.MaxChanges
	MaxValueBytes    = mcpconfigwire.MaxValueBytes
)

var (
	ListInputSchema  = mcpconfigwire.ListInputSchema
	GetInputSchema   = mcpconfigwire.GetInputSchema
	SetInputSchema   = mcpconfigwire.SetInputSchema
	ListOutputSchema = mcpconfigwire.ListOutputSchema
	GetOutputSchema  = mcpconfigwire.GetOutputSchema
	SetOutputSchema  = mcpconfigwire.SetOutputSchema
)

type Access string

const (
	AccessRead  Access = "read"
	AccessWrite Access = "write"
)

func Eligible(cfg config.Config, access Access) bool {
	switch access {
	case AccessRead:
		return cfg.Permissions.MCPConfigRead
	case AccessWrite:
		return cfg.Permissions.MCPConfigWrite
	default:
		return false
	}
}

type Setting = mcpconfigwire.Setting

func ProjectSetting(spec config.FieldSpec, value string, configured *bool) (Setting, bool) {
	if !AgentReadable(spec) {
		return Setting{}, false
	}
	out := Setting{
		Key: spec.Key, Label: spec.Label, Section: string(spec.Section), Kind: string(spec.Kind),
		Options:  append([]string(nil), spec.Options...),
		Readable: true, Writable: AgentWritable(spec), Derived: spec.Derived, Secret: spec.Secret,
	}
	if spec.Secret {
		out.Configured = cloneBool(configured)
		return out, true
	}
	if len(value) > MaxValueBytes {
		return Setting{}, false
	}
	copied := value
	out.Value = &copied
	if configured != nil {
		out.Configured = cloneBool(configured)
	}
	return out, true
}

func AgentReadable(spec config.FieldSpec) bool {
	if spec.Key == "telemetry.enabled" || strings.HasPrefix(spec.Key, "telegram.") {
		return false
	}
	if spec.InternalOnly || spec.ValueRole == config.SettingValueInternal {
		return false
	}
	if unsafeValueBearingKey(spec.Key) {
		return false
	}
	if spec.Secret {
		return strings.TrimSpace(spec.ConfiguredStateKey) != ""
	}
	return spec.Readable
}

func AgentWritable(spec config.FieldSpec) bool {
	if spec.Key == "telemetry.enabled" || strings.HasPrefix(spec.Key, "telegram.") {
		return false
	}
	if !AgentReadable(spec) || spec.Secret || spec.Derived || spec.InternalOnly || !spec.Writable {
		return false
	}
	switch strings.TrimSpace(spec.Key) {
	case ReadEligibilityKey, WriteEligibilityKey:
		return false
	}
	return !unsafeValueBearingKey(spec.Key)
}

func SupportedSettings(access Access) []config.FieldSpec {
	all := config.Settings()
	result := make([]config.FieldSpec, 0, len(all))
	for _, spec := range all {
		switch access {
		case AccessRead:
			if AgentReadable(spec) {
				result = append(result, spec)
			}
		case AccessWrite:
			if AgentWritable(spec) {
				result = append(result, spec)
			}
		}
	}
	return result
}

func ResolveSetting(key string, access Access) (config.FieldSpec, bool) {
	key = strings.TrimSpace(key)
	if spec, ok := config.SettingByKey(key); ok {
		return supportedSpec(spec, key, access)
	}
	if match, ok := config.MatchSettingSelector(key); ok {
		return supportedSpec(match.Spec, key, access)
	}
	return config.FieldSpec{}, false
}

func supportedSpec(spec config.FieldSpec, concreteKey string, access Access) (config.FieldSpec, bool) {
	switch access {
	case AccessRead:
		if !AgentReadable(spec) {
			return config.FieldSpec{}, false
		}
	case AccessWrite:
		if !AgentWritable(spec) {
			return config.FieldSpec{}, false
		}
	default:
		return config.FieldSpec{}, false
	}
	spec.Key = concreteKey
	return spec, true
}

func ValidateWriteSpec(spec config.FieldSpec) error {
	if spec.Secret {
		return fmt.Errorf("setting %q is a managed secret and cannot be written by MCP agents", spec.Key)
	}
	if !AgentWritable(spec) {
		return fmt.Errorf("setting %q is not writable by MCP agents", spec.Key)
	}
	return nil
}

func unsafeValueBearingKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "tunnel.control_plane_base_url" {
		return true
	}
	if strings.HasPrefix(key, "upstream.servers[") {
		return strings.HasSuffix(key, "].url") ||
			strings.HasSuffix(key, "].command") ||
			strings.HasSuffix(key, "].args")
	}
	return false
}

type ListResult = mcpconfigwire.ListResult
type GetResult = mcpconfigwire.GetResult

type Change = mcpconfigwire.Change
type BatchSummary = mcpconfigwire.BatchSummary
type ErrorCode = mcpconfigwire.ErrorCode
type PublicError = mcpconfigwire.PublicError
type MutationState = mcpconfigwire.MutationState
type RuntimeSyncState = mcpconfigwire.RuntimeSyncState
type MutationOutcome = mcpconfigwire.MutationOutcome
type MutationResult = mcpconfigwire.MutationResult
type MutationError = mcpconfigwire.MutationError

const (
	ErrorAccessDenied         = mcpconfigwire.ErrorAccessDenied
	ErrorInvalidRequest       = mcpconfigwire.ErrorInvalidRequest
	ErrorUnsupportedSetting   = mcpconfigwire.ErrorUnsupportedSetting
	ErrorSecretWriteForbidden = mcpconfigwire.ErrorSecretWriteForbidden
	ErrorApprovalRequired     = mcpconfigwire.ErrorApprovalRequired
	ErrorApplyFailed          = mcpconfigwire.ErrorApplyFailed
	ErrorReconciliationNeeded = mcpconfigwire.ErrorReconciliationNeeded

	MutationUnchanged              = mcpconfigwire.MutationUnchanged
	MutationPersisted              = mcpconfigwire.MutationPersisted
	MutationRuntimeSynced          = mcpconfigwire.MutationRuntimeSynced
	MutationRolledBack             = mcpconfigwire.MutationRolledBack
	MutationReconciliationRequired = mcpconfigwire.MutationReconciliationRequired

	RuntimeSyncPersisted = mcpconfigwire.RuntimeSyncPersisted
	RuntimeSyncCurrent   = mcpconfigwire.RuntimeSyncCurrent
	RuntimeSyncPending   = mcpconfigwire.RuntimeSyncPending
)

func ValidateChanges(changes []Change) error {
	return mcpconfigwire.ValidateChanges(changes)
}

func SummarizeChanges(changes []Change) BatchSummary {
	return mcpconfigwire.SummarizeChanges(changes)
}

func SummarizeArguments(arguments map[string]any) BatchSummary {
	return mcpconfigwire.SummarizeArguments(arguments)
}

func SummarizeRawArguments(raw []byte) BatchSummary {
	return mcpconfigwire.SummarizeRawArguments(raw)
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
