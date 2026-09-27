package capability

import (
	"sort"
	"strings"
)

type TelegramRolloutState string

const (
	TelegramRolloutLive    TelegramRolloutState = "live"
	TelegramRolloutPlanned TelegramRolloutState = "planned"
	TelegramRolloutExempt  TelegramRolloutState = "exempt"
)

type TelegramRolloutStage string

const (
	TelegramStageRuntimeAuthorization TelegramRolloutStage = "runtime-authorization"
	TelegramStageSetupPairing         TelegramRolloutStage = "setup-pairing"
	TelegramStageNavigationDispatch   TelegramRolloutStage = "navigation-dispatch"
	TelegramStageWorkspaceApproval    TelegramRolloutStage = "workspace-approval"
	TelegramStageNetworkUpstream      TelegramRolloutStage = "network-upstream"
	TelegramStageSettingsIntegration  TelegramRolloutStage = "settings-integration"
	TelegramStageSystemInstruction    TelegramRolloutStage = "system-instruction"
	TelegramStageLogsExecution        TelegramRolloutStage = "logs-execution"
	TelegramStageCompletion           TelegramRolloutStage = "completion"
	TelegramStageParityResilience     TelegramRolloutStage = "parity-resilience"
)

type TelegramOwner string

const (
	TelegramOwnerRuntime     TelegramOwner = "runtime"
	TelegramOwnerSetup       TelegramOwner = "setup"
	TelegramOwnerNavigation  TelegramOwner = "navigation"
	TelegramOwnerWorkspace   TelegramOwner = "workspace"
	TelegramOwnerApproval    TelegramOwner = "approval"
	TelegramOwnerNetwork     TelegramOwner = "network"
	TelegramOwnerSettings    TelegramOwner = "settings"
	TelegramOwnerIntegration TelegramOwner = "integration"
	TelegramOwnerSystem      TelegramOwner = "system"
	TelegramOwnerInstruction TelegramOwner = "instruction"
	TelegramOwnerLogs        TelegramOwner = "logs"
	TelegramOwnerExecution   TelegramOwner = "execution"
	TelegramOwnerCompletion  TelegramOwner = "completion"
	TelegramOwnerParity      TelegramOwner = "parity"
	TelegramOwnerLocal       TelegramOwner = "local"
)

type TelegramEntryPointKind string

const (
	TelegramEntryCLICommand  TelegramEntryPointKind = "cli-command"
	TelegramEntryCommand     TelegramEntryPointKind = "telegram-command"
	TelegramEntryRoute       TelegramEntryPointKind = "telegram-route"
	TelegramEntryCallback    TelegramEntryPointKind = "telegram-callback"
	TelegramEntryRuntime     TelegramEntryPointKind = "runtime"
	TelegramEntryApplication TelegramEntryPointKind = "application"
)

type TelegramEntryPoint struct {
	Kind  TelegramEntryPointKind `json:"kind"`
	Value string                 `json:"value"`
}

type TelegramRolloutItem struct {
	ID          string               `json:"id"`
	State       TelegramRolloutState `json:"state"`
	Stage       TelegramRolloutStage `json:"stage"`
	Owner       TelegramOwner        `json:"owner"`
	Operation   ID                   `json:"operation,omitempty"`
	EntryPoints []TelegramEntryPoint `json:"entry_points,omitempty"`
	Reason      string               `json:"reason,omitempty"`
}

const (
	telegramReasonLocalOnly    = "operation requires local process or filesystem ownership"
	telegramReasonSingleTunnel = "managed tunnel collections are outside Telegram single-tunnel administration"
)

var telegramCompletedRollout = []TelegramRolloutItem{
	{
		ID: "runtime.polling", State: TelegramRolloutLive, Stage: TelegramStageRuntimeAuthorization, Owner: TelegramOwnerRuntime,
		EntryPoints: []TelegramEntryPoint{{Kind: TelegramEntryRuntime, Value: "telegram.Runtime.Reconcile"}},
	},
	{
		ID: "runtime.authorization", State: TelegramRolloutLive, Stage: TelegramStageRuntimeAuthorization, Owner: TelegramOwnerRuntime,
		EntryPoints: []TelegramEntryPoint{{Kind: TelegramEntryRuntime, Value: "telegram.authorizedUpdate"}},
	},
	{
		ID: "setup.pairing", State: TelegramRolloutLive, Stage: TelegramStageSetupPairing, Owner: TelegramOwnerSetup, Operation: TelegramSetup,
		EntryPoints: []TelegramEntryPoint{
			{Kind: TelegramEntryCLICommand, Value: "telegram setup"},
			{Kind: TelegramEntryApplication, Value: "app.PrepareTelegramPairing"},
		},
	},
	{
		ID: "setup.token.read", State: TelegramRolloutLive, Stage: TelegramStageSetupPairing, Owner: TelegramOwnerSetup, Operation: ConfigGet,
		EntryPoints: []TelegramEntryPoint{{Kind: TelegramEntryCLICommand, Value: "telegram token status"}},
	},
	{
		ID: "setup.token.write", State: TelegramRolloutLive, Stage: TelegramStageSetupPairing, Owner: TelegramOwnerSetup, Operation: ConfigSet,
		EntryPoints: []TelegramEntryPoint{
			{Kind: TelegramEntryCLICommand, Value: "telegram token set"},
			{Kind: TelegramEntryCLICommand, Value: "telegram token remove"},
		},
	},
	{
		ID: "setup.logout", State: TelegramRolloutLive, Stage: TelegramStageSetupPairing, Owner: TelegramOwnerSetup,
		EntryPoints: []TelegramEntryPoint{
			{Kind: TelegramEntryCLICommand, Value: "telegram logout"},
			{Kind: TelegramEntryApplication, Value: "application.SetTelegramAuthorizedUser"},
		},
	},
	{
		ID: "navigation.home", State: TelegramRolloutLive, Stage: TelegramStageNavigationDispatch, Owner: TelegramOwnerNavigation,
		EntryPoints: []TelegramEntryPoint{
			{Kind: TelegramEntryCommand, Value: "start"},
			{Kind: TelegramEntryCommand, Value: "home"},
			{Kind: TelegramEntryRoute, Value: "home"},
		},
	},
	{
		ID: "navigation.status", State: TelegramRolloutLive, Stage: TelegramStageNavigationDispatch, Owner: TelegramOwnerNavigation, Operation: StatusOverview,
		EntryPoints: []TelegramEntryPoint{
			{Kind: TelegramEntryCommand, Value: "status"},
			{Kind: TelegramEntryRoute, Value: "status"},
		},
	},
	{
		ID: "navigation.commands", State: TelegramRolloutLive, Stage: TelegramStageNavigationDispatch, Owner: TelegramOwnerNavigation,
		EntryPoints: []TelegramEntryPoint{
			{Kind: TelegramEntryCommand, Value: "commands"},
			{Kind: TelegramEntryCommand, Value: "help"},
			{Kind: TelegramEntryRoute, Value: "commands"},
		},
	},
	{
		ID: "navigation.callback", State: TelegramRolloutLive, Stage: TelegramStageNavigationDispatch, Owner: TelegramOwnerNavigation,
		EntryPoints: []TelegramEntryPoint{{Kind: TelegramEntryCallback, Value: "*"}},
	},
	{
		ID: "navigation.dispatch", State: TelegramRolloutLive, Stage: TelegramStageNavigationDispatch, Owner: TelegramOwnerNavigation,
		EntryPoints: []TelegramEntryPoint{{Kind: TelegramEntryApplication, Value: "application.OperationDispatcher.Dispatch"}},
	},
}

var telegramLocalOnlyOperations = idSet(
	ServerForeground,
	ConfigInit,
	ConfigUninit,
	ConfigPath,
	ConfigExport,
	ConfigImport,
	ConfigMigrate,
	ConfigMigrateSecrets,
	MCPStdio,
	MCPHTTP,
	LogsPath,
	TunnelForeground,
)

var telegramManagedTunnelCollectionOperations = idSet(
	TunnelList,
	TunnelGet,
	TunnelUse,
	TunnelCreate,
	TunnelUpdate,
	TunnelDelete,
)

func TelegramRolloutInventory() []TelegramRolloutItem {
	items := cloneTelegramRolloutItems(telegramCompletedRollout)
	for _, spec := range All() {
		if spec.Audience != AudienceOperator && spec.Audience != AudienceReviewer {
			continue
		}
		if spec.ID == TelegramSetup || spec.ID == StatusOverview {
			continue
		}
		stage, owner, state, reason := telegramFutureContract(spec.ID)
		items = append(items, TelegramRolloutItem{
			ID: "operation." + string(spec.ID), State: state, Stage: stage, Owner: owner, Operation: spec.ID, Reason: reason,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

func telegramFutureContract(id ID) (TelegramRolloutStage, TelegramOwner, TelegramRolloutState, string) {
	if telegramLocalOnlyOperations[id] {
		return TelegramStageParityResilience, TelegramOwnerLocal, TelegramRolloutExempt, telegramReasonLocalOnly
	}
	if telegramManagedTunnelCollectionOperations[id] {
		return TelegramStageNetworkUpstream, TelegramOwnerNetwork, TelegramRolloutExempt, telegramReasonSingleTunnel
	}
	value := string(id)
	switch {
	case strings.HasPrefix(value, "workspace."):
		return TelegramStageWorkspaceApproval, TelegramOwnerWorkspace, TelegramRolloutPlanned, ""
	case strings.HasPrefix(value, "request."):
		return TelegramStageWorkspaceApproval, TelegramOwnerApproval, TelegramRolloutPlanned, ""
	case strings.HasPrefix(value, "upstream."), strings.HasPrefix(value, "tunnel."), strings.HasPrefix(value, "network."):
		return TelegramStageNetworkUpstream, TelegramOwnerNetwork, TelegramRolloutPlanned, ""
	case strings.HasPrefix(value, "integration."):
		return TelegramStageSettingsIntegration, TelegramOwnerIntegration, TelegramRolloutPlanned, ""
	case strings.HasPrefix(value, "config."), strings.HasPrefix(value, "telemetry."), strings.HasPrefix(value, "auth."), strings.HasPrefix(value, "notification."):
		return TelegramStageSettingsIntegration, TelegramOwnerSettings, TelegramRolloutPlanned, ""
	case strings.HasPrefix(value, "logs."):
		return TelegramStageLogsExecution, TelegramOwnerLogs, TelegramRolloutPlanned, ""
	case strings.HasPrefix(value, "execution."), strings.HasPrefix(value, "process."), strings.HasPrefix(value, "activity."):
		return TelegramStageLogsExecution, TelegramOwnerExecution, TelegramRolloutPlanned, ""
	case strings.HasPrefix(value, "completion."):
		return TelegramStageCompletion, TelegramOwnerCompletion, TelegramRolloutPlanned, ""
	case strings.HasPrefix(value, "instructions."), strings.HasPrefix(value, "project.context."), strings.HasPrefix(value, "prompt."):
		return TelegramStageSystemInstruction, TelegramOwnerInstruction, TelegramRolloutPlanned, ""
	case strings.HasPrefix(value, "runtime."), strings.HasPrefix(value, "update."), strings.HasPrefix(value, "install."),
		strings.HasPrefix(value, "doctor."), strings.HasPrefix(value, "version."), strings.HasPrefix(value, "health."), strings.HasPrefix(value, "tools."):
		return TelegramStageSystemInstruction, TelegramOwnerSystem, TelegramRolloutPlanned, ""
	default:
		return TelegramStageParityResilience, TelegramOwnerParity, TelegramRolloutPlanned, ""
	}
}

func cloneTelegramRolloutItems(values []TelegramRolloutItem) []TelegramRolloutItem {
	out := make([]TelegramRolloutItem, len(values))
	for index, value := range values {
		out[index] = value
		out[index].EntryPoints = append([]TelegramEntryPoint(nil), value.EntryPoints...)
	}
	return out
}

func validTelegramRolloutState(value TelegramRolloutState) bool {
	switch value {
	case TelegramRolloutLive, TelegramRolloutPlanned, TelegramRolloutExempt:
		return true
	default:
		return false
	}
}

func validTelegramRolloutStage(value TelegramRolloutStage) bool {
	switch value {
	case TelegramStageRuntimeAuthorization, TelegramStageSetupPairing, TelegramStageNavigationDispatch,
		TelegramStageWorkspaceApproval, TelegramStageNetworkUpstream, TelegramStageSettingsIntegration,
		TelegramStageSystemInstruction, TelegramStageLogsExecution, TelegramStageCompletion, TelegramStageParityResilience:
		return true
	default:
		return false
	}
}

func validTelegramOwner(value TelegramOwner) bool {
	switch value {
	case TelegramOwnerRuntime, TelegramOwnerSetup, TelegramOwnerNavigation, TelegramOwnerWorkspace,
		TelegramOwnerApproval, TelegramOwnerNetwork, TelegramOwnerSettings, TelegramOwnerIntegration,
		TelegramOwnerSystem, TelegramOwnerInstruction, TelegramOwnerLogs, TelegramOwnerExecution,
		TelegramOwnerCompletion, TelegramOwnerParity, TelegramOwnerLocal:
		return true
	default:
		return false
	}
}

func validTelegramEntryPointKind(value TelegramEntryPointKind) bool {
	switch value {
	case TelegramEntryCLICommand, TelegramEntryCommand, TelegramEntryRoute, TelegramEntryCallback, TelegramEntryRuntime, TelegramEntryApplication:
		return true
	default:
		return false
	}
}
