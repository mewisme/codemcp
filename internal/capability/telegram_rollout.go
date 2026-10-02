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
	telegramReasonNotExposed   = "operation has no Telegram administration workflow"
)

var telegramLiveAdapterOperations = idSet(
	InstallRun, UpdateApply, UpdateCheck,
	RuntimeUp, RuntimeDown, RuntimeRestart,
	LogsRead, LogsFollow, LogsClear, LogsPath,
	RequestList, RequestView, RequestApprove, RequestDeny, RequestGrantList, RequestGrantRevoke, RequestStream,
	RequestExplain, RequestExplanationView, RequestExplainStatus,
	LLMStatus, LLMProviderList, LLMProviderGet, LLMProviderAdd, LLMProviderConfigure, LLMProviderRemove,
	LLMProviderSelect, LLMProviderModels, LLMProviderProbe, LLMProviderCredentialSet, LLMProviderCredentialClear,
	CompletionList, CompletionView, CompletionCurrent, CompletionDoctor, CompletionFeed,
	ConfigExport, ConfigGet, ConfigList, ConfigSet, ConfigPatch, ConfigPath, ConfigSnapshotRead, ConfigVerify,
	AuthStatus, AuthMCPRotate, AuthMCPEnable, AuthMCPDisable, AuthAdminRotate, AuthAdminEnable, AuthAdminDisable,
	PromptList, PromptGet, PromptCreate, PromptUpdate, PromptDelete,
	WorkspaceContainerList, WorkspaceContainerCreate, WorkspaceContainerShow, WorkspaceContainerRename, WorkspaceContainerDelete,
	WorkspaceContainerAdd, WorkspaceContainerRemove, WorkspaceContainerMembershipList,
	WorkspaceAccessList, WorkspaceAccessAdd, WorkspaceAccessRemove,
	WorkspaceRegister, WorkspaceList, WorkspaceShow, WorkspaceRelocate, WorkspaceUnregister, WorkspacePurge,
	UpstreamServerList, UpstreamServerAdd, UpstreamServerConfigure, UpstreamServerShow, UpstreamServerRemove,
	UpstreamServerEnable, UpstreamServerDisable, UpstreamServerStatus, UpstreamServerTools,
	UpstreamAuthLogin, UpstreamAuthStatus, UpstreamAuthLogout,
	TunnelStatus, TunnelConfigRead, TunnelSync, TunnelConfigure, TunnelEnable, TunnelDisable,
	TunnelAdminKeyStatus, TunnelAdminKeySet, TunnelAdminKeyVerify, TunnelAdminKeyRemove,
	TunnelList, TunnelGet, TunnelUse, TunnelCreate, TunnelUpdate, TunnelDelete,
	StatusOverview, DoctorRead, VersionAbout, HealthRead,
	TelemetryStatus, TelemetryShow, TelemetryEnable, TelemetryDisable,
	NetworkInterfacesList, NotificationStatus,
	TelegramSetup,
	ProjectContextRead, ToolInventoryRead,
	ExecutionList, ExecutionView, ExecutionFeed, ExecutionStream, ProcessList, ProcessView, ProcessClear,
	ActivityStream, ActivityView,
	IntegrationRTKStatus, IntegrationRTKEnable, IntegrationRTKDisable, IntegrationRTKProbe, IntegrationRTKInstall, IntegrationRTKInstallGlobal,
	IntegrationCodeGraphStatus, IntegrationCodeGraphProbe, IntegrationCodeGraphInstall, IntegrationCodeGraphInstallGlobal,
	IntegrationCodeGraphWorkspaceStatus, IntegrationCodeGraphWorkspaceInit, IntegrationCodeGraphWorkspaceSync,
	IntegrationCFStatus, IntegrationCFProbe, IntegrationCFInstall, IntegrationCFUpdate, IntegrationCFRemove,
	IntegrationTypeSafeStatus, IntegrationTypeSafeEnable, IntegrationTypeSafeDisable, IntegrationTypeSafeProbe, IntegrationTypeSafeDoctor,
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
	ConfigImport,
	ConfigMigrate,
	ConfigMigrateSecrets,
	MCPStdio,
	MCPHTTP,
	TunnelForeground,
)

var telegramManagedTunnelCollectionOperations = idSet()

func TelegramAdapterOperationLive(id ID) bool {
	return telegramLiveAdapterOperations[id]
}

func TelegramRolloutInventory() []TelegramRolloutItem {
	items := cloneTelegramRolloutItems(telegramCompletedRollout)
	for _, spec := range All() {
		if spec.Audience != AudienceOperator && spec.Audience != AudienceReviewer {
			continue
		}
		if spec.ID == TelegramSetup || spec.ID == StatusOverview {
			continue
		}
		stage, owner, state, reason := telegramFinalContract(spec.ID)
		entryPoints := []TelegramEntryPoint(nil)
		if state == TelegramRolloutLive {
			entryPoints = telegramOperationEntryPoints(spec.ID, owner)
		}
		items = append(items, TelegramRolloutItem{
			ID: "operation." + string(spec.ID), State: state, Stage: stage, Owner: owner, Operation: spec.ID, EntryPoints: entryPoints, Reason: reason,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

func telegramFinalContract(id ID) (TelegramRolloutStage, TelegramOwner, TelegramRolloutState, string) {
	if telegramLocalOnlyOperations[id] {
		return TelegramStageParityResilience, TelegramOwnerLocal, TelegramRolloutExempt, telegramReasonLocalOnly
	}
	if telegramManagedTunnelCollectionOperations[id] {
		return TelegramStageNetworkUpstream, TelegramOwnerNetwork, TelegramRolloutExempt, telegramReasonSingleTunnel
	}
	value := string(id)
	stage, owner := TelegramStageParityResilience, TelegramOwnerParity
	switch {
	case strings.HasPrefix(value, "workspace."):
		stage, owner = TelegramStageWorkspaceApproval, TelegramOwnerWorkspace
	case strings.HasPrefix(value, "request."):
		stage, owner = TelegramStageWorkspaceApproval, TelegramOwnerApproval
	case strings.HasPrefix(value, "upstream."), strings.HasPrefix(value, "tunnel."), strings.HasPrefix(value, "network."):
		stage, owner = TelegramStageNetworkUpstream, TelegramOwnerNetwork
	case strings.HasPrefix(value, "integration."):
		stage, owner = TelegramStageSettingsIntegration, TelegramOwnerIntegration
	case strings.HasPrefix(value, "config."), strings.HasPrefix(value, "telemetry."), strings.HasPrefix(value, "auth."), strings.HasPrefix(value, "notification."), strings.HasPrefix(value, "llm."):
		stage, owner = TelegramStageSettingsIntegration, TelegramOwnerSettings
	case strings.HasPrefix(value, "logs."):
		stage, owner = TelegramStageLogsExecution, TelegramOwnerLogs
	case strings.HasPrefix(value, "execution."), strings.HasPrefix(value, "process."), strings.HasPrefix(value, "activity."):
		stage, owner = TelegramStageLogsExecution, TelegramOwnerExecution
	case strings.HasPrefix(value, "completion."):
		stage, owner = TelegramStageCompletion, TelegramOwnerCompletion
	case strings.HasPrefix(value, "instructions."), strings.HasPrefix(value, "project.context."), strings.HasPrefix(value, "prompt."):
		stage, owner = TelegramStageSystemInstruction, TelegramOwnerInstruction
	case strings.HasPrefix(value, "runtime."), strings.HasPrefix(value, "update."), strings.HasPrefix(value, "install."),
		strings.HasPrefix(value, "doctor."), strings.HasPrefix(value, "version."), strings.HasPrefix(value, "health."), strings.HasPrefix(value, "tools."):
		stage, owner = TelegramStageSystemInstruction, TelegramOwnerSystem
	}
	if telegramLiveAdapterOperations[id] {
		return stage, owner, TelegramRolloutLive, ""
	}
	return stage, owner, TelegramRolloutExempt, telegramReasonNotExposed
}

func telegramOperationEntryPoints(id ID, owner TelegramOwner) []TelegramEntryPoint {
	switch id {
	case CompletionList:
		return []TelegramEntryPoint{{Kind: TelegramEntryRoute, Value: "completions"}, {Kind: TelegramEntryApplication, Value: "application.ListCompletions"}}
	case CompletionView:
		return []TelegramEntryPoint{{Kind: TelegramEntryApplication, Value: "application.ViewCompletion"}}
	case LogsRead, LogsFollow, ExecutionList, ExecutionView, ExecutionFeed, ExecutionStream, ActivityStream, ActivityView:
		return []TelegramEntryPoint{{Kind: TelegramEntryRuntime, Value: "telegram.LogsMiniAppRuntime"}}
	default:
		return []TelegramEntryPoint{{Kind: TelegramEntryApplication, Value: "telegram.Interface.dispatch"}, {Kind: TelegramEntryRuntime, Value: string(owner)}}
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
