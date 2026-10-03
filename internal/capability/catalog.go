package capability

import "strings"

const RootPath = "<root>"

const (
	ServerForeground           ID = "server.foreground"
	InstallRun                 ID = "install.run"
	UpdateApply                ID = "update.apply"
	UpdateCheck                ID = "update.check"
	ConfigInit                 ID = "config.init"
	ConfigUninit               ID = "config.uninit"
	RuntimeUp                  ID = "runtime.up"
	RuntimeDown                ID = "runtime.down"
	RuntimeRestart             ID = "runtime.restart"
	LogsRead                   ID = "logs.read"
	LogsFollow                 ID = "logs.follow"
	LogsPath                   ID = "logs.path"
	LogsClear                  ID = "logs.clear"
	RequestList                ID = "request.list"
	RequestView                ID = "request.view"
	RequestApprove             ID = "request.approve"
	RequestDeny                ID = "request.deny"
	RequestExplain             ID = "request.explain"
	RequestExplanationView     ID = "request.explanation.view"
	RequestExplainStatus       ID = "request.explain.status"
	RequestGrantList           ID = "request.grant.list"
	RequestGrantRevoke         ID = "request.grant.revoke"
	CompletionCurrent          ID = "completion.current"
	CompletionDoctor           ID = "completion.doctor"
	CompletionList             ID = "completion.list"
	CompletionView             ID = "completion.view"
	ManagedAgentSpawn          ID = "managed-agent.spawn"
	ManagedAgentList           ID = "managed-agent.list"
	ManagedAgentGet            ID = "managed-agent.get"
	ManagedAgentWait           ID = "managed-agent.wait"
	ManagedAgentSend           ID = "managed-agent.send"
	ManagedAgentCancel         ID = "managed-agent.cancel"
	NotificationStatus         ID = "notification.status"
	ConfigPath                 ID = "config.path"
	ConfigExport               ID = "config.export"
	ConfigImport               ID = "config.import"
	ConfigGet                  ID = "config.get"
	ConfigList                 ID = "config.list"
	ConfigSet                  ID = "config.set"
	ConfigMigrate              ID = "config.migrate"
	ConfigMigrateSecrets       ID = "config.migrate.secrets"
	ConfigVerify               ID = "config.verify"
	PromptList                 ID = "prompt.list"
	PromptGet                  ID = "prompt.get"
	PromptCreate               ID = "prompt.create"
	PromptUpdate               ID = "prompt.update"
	PromptDelete               ID = "prompt.delete"
	AuthMCPRotate              ID = "auth.mcp.rotate"
	AuthMCPEnable              ID = "auth.mcp.enable"
	AuthMCPDisable             ID = "auth.mcp.disable"
	AuthAdminRotate            ID = "auth.admin.rotate"
	AuthAdminEnable            ID = "auth.admin.enable"
	AuthAdminDisable           ID = "auth.admin.disable"
	AuthStatus                 ID = "auth.status"
	WorkspaceContainerList     ID = "workspace.container.list"
	WorkspaceContainerCreate   ID = "workspace.container.create"
	WorkspaceContainerShow     ID = "workspace.container.show"
	WorkspaceContainerRename   ID = "workspace.container.rename"
	WorkspaceContainerDelete   ID = "workspace.container.delete"
	WorkspaceContainerAdd      ID = "workspace.container.add"
	WorkspaceContainerRemove   ID = "workspace.container.remove"
	WorkspaceAccessList        ID = "workspace.access.list"
	WorkspaceAccessAdd         ID = "workspace.access.add"
	WorkspaceAccessRemove      ID = "workspace.access.remove"
	WorkspaceRegister          ID = "workspace.register"
	WorkspaceList              ID = "workspace.list"
	WorkspaceShow              ID = "workspace.show"
	WorkspaceRelocate          ID = "workspace.relocate"
	WorkspaceUnregister        ID = "workspace.unregister"
	WorkspacePurge             ID = "workspace.purge"
	MCPStdio                   ID = "mcp.stdio"
	MCPHTTP                    ID = "mcp.http"
	UpstreamServerList         ID = "upstream.server.list"
	UpstreamServerAdd          ID = "upstream.server.add"
	UpstreamServerConfigure    ID = "upstream.server.configure"
	UpstreamServerShow         ID = "upstream.server.show"
	UpstreamServerRemove       ID = "upstream.server.remove"
	UpstreamServerEnable       ID = "upstream.server.enable"
	UpstreamServerDisable      ID = "upstream.server.disable"
	UpstreamServerStatus       ID = "upstream.server.status"
	UpstreamServerTools        ID = "upstream.server.tools"
	UpstreamAuthLogin          ID = "upstream.server.auth.login"
	UpstreamAuthStatus         ID = "upstream.server.auth.status"
	UpstreamAuthLogout         ID = "upstream.server.auth.logout"
	TunnelStatus               ID = "tunnel.status"
	TunnelSync                 ID = "tunnel.sync"
	TunnelConfigure            ID = "tunnel.configure"
	TunnelEnable               ID = "tunnel.enable"
	TunnelDisable              ID = "tunnel.disable"
	TunnelForeground           ID = "tunnel.foreground"
	TunnelAdminKeySet          ID = "tunnel.admin.key.set"
	TunnelAdminKeyStatus       ID = "tunnel.admin.key.status"
	TunnelAdminKeyVerify       ID = "tunnel.admin.key.verify"
	TunnelAdminKeyRemove       ID = "tunnel.admin.key.remove"
	TunnelList                 ID = "tunnel.list"
	TunnelGet                  ID = "tunnel.get"
	TunnelUse                  ID = "tunnel.use"
	TunnelCreate               ID = "tunnel.create"
	TunnelUpdate               ID = "tunnel.update"
	TunnelDelete               ID = "tunnel.delete"
	StatusOverview             ID = "status.overview"
	DoctorRead                 ID = "doctor.read"
	VersionAbout               ID = "version.about"
	TelemetryStatus            ID = "telemetry.status"
	TelemetryEnable            ID = "telemetry.enable"
	TelemetryDisable           ID = "telemetry.disable"
	TelemetryShow              ID = "telemetry.show"
	TelegramSetup              ID = "telegram.setup"
	LLMStatus                  ID = "llm.status"
	LLMProviderList            ID = "llm.provider.list"
	LLMProviderGet             ID = "llm.provider.get"
	LLMProviderAdd             ID = "llm.provider.add"
	LLMProviderConfigure       ID = "llm.provider.configure"
	LLMProviderRemove          ID = "llm.provider.remove"
	LLMProviderSelect          ID = "llm.provider.select"
	LLMProviderModels          ID = "llm.provider.models"
	LLMProviderProbe           ID = "llm.provider.probe"
	LLMProviderCredentialSet   ID = "llm.provider.credential.set"
	LLMProviderCredentialClear ID = "llm.provider.credential.clear"

	HealthRead                        ID = "health.read"
	NetworkInterfacesList             ID = "network.interfaces.list"
	ConfigSnapshotRead                ID = "config.snapshot.read"
	ConfigPatch                       ID = "config.patch"
	WorkspaceContainerMembershipList  ID = "workspace.container.membership.list"
	ProjectContextRead                ID = "project.context.read"
	ToolInventoryRead                 ID = "tools.inventory.read"
	ExecutionList                     ID = "execution.list"
	ExecutionView                     ID = "execution.view"
	ExecutionFeed                     ID = "execution.feed"
	ExecutionStream                   ID = "execution.stream"
	ProcessList                       ID = "process.list"
	ProcessView                       ID = "process.view"
	ProcessClear                      ID = "process.clear"
	RequestStream                     ID = "request.stream"
	CompletionFeed                    ID = "completion.feed"
	TunnelConfigRead                  ID = "tunnel.config.read"
	ActivityStream                    ID = "activity.stream"
	ActivityView                      ID = "activity.view"
	OAuthCallbackComplete             ID = "oauth.callback.complete"
	IntegrationRTKStatus              ID = "integration.rtk.status"
	IntegrationRTKEnable              ID = "integration.rtk.enable"
	IntegrationRTKDisable             ID = "integration.rtk.disable"
	IntegrationRTKProbe               ID = "integration.rtk.probe"
	IntegrationRTKInstall             ID = "integration.rtk.install"
	IntegrationRTKInstallGlobal       ID = "integration.rtk.install.global"
	IntegrationCodeGraphStatus        ID = "integration.codegraph.status"
	IntegrationCodeGraphProbe         ID = "integration.codegraph.probe"
	IntegrationCodeGraphInstall       ID = "integration.codegraph.install"
	IntegrationCodeGraphInstallGlobal ID = "integration.codegraph.install.global"
	IntegrationCFStatus               ID = "integration.cf.status"
	IntegrationCFProbe                ID = "integration.cf.probe"
	IntegrationCFInstall              ID = "integration.cf.install"
	IntegrationCFUpdate               ID = "integration.cf.update"
	IntegrationCFRemove               ID = "integration.cf.remove"
	IntegrationTypeSafeStatus         ID = "integration.typesafe.status"
	IntegrationTypeSafeDoctor         ID = "integration.typesafe.doctor"
	IntegrationTypeSafeEnable         ID = "integration.typesafe.enable"
	IntegrationTypeSafeDisable        ID = "integration.typesafe.disable"
	IntegrationTypeSafeProbe          ID = "integration.typesafe.probe"
	IntegrationBrowserStatus          ID = "integration.browser.status"
	IntegrationBrowserDoctor          ID = "integration.browser.doctor"
	IntegrationChatGPTWebStatus       ID = "integration.chatgpt-web.status"
	IntegrationChatGPTWebLogin        ID = "integration.chatgpt-web.login"
	IntegrationChatGPTWebLogout       ID = "integration.chatgpt-web.logout"
	IntegrationChatGPTWebDoctor       ID = "integration.chatgpt-web.doctor"
)

const (
	IntegrationCodeGraphWorkspaceStatus ID = "integration.codegraph.workspace.status"
	IntegrationCodeGraphWorkspaceInit   ID = "integration.codegraph.workspace.init"
	IntegrationCodeGraphWorkspaceSync   ID = "integration.codegraph.workspace.sync"
)

var specs = buildSpecs()

func buildSpecs() []Spec {
	values := []Spec{
		operatorRuntime(ServerForeground, "serve", true),
		operatorSensitive(InstallRun, "install", false),
		operatorSensitive(UpdateApply, "upgrade", true),
		operatorQueryOpenWorld(UpdateCheck, "upgrade check"),
		operatorMutation(ConfigInit, "init", RiskState, false),
		operatorDestructive(ConfigUninit, "uninit", false),
		operatorRuntime(RuntimeUp, "up", false),
		operatorRuntime(RuntimeDown, "down", false),
		operatorRuntime(RuntimeRestart, "restart", false),
		operatorQuery(LogsRead, "logs"),
		operatorStream(LogsFollow, "logs follow", false),
		operatorQuery(LogsPath, "logs path"),
		operatorDestructive(LogsClear, "logs clear", false),
		operatorQuery(RequestList, "request list"),
		operatorQuery(RequestView, "request view"),
		reviewerMutation(RequestApprove, "request approve"),
		reviewerMutation(RequestDeny, "request deny"),
		reviewerRuntime(RequestExplain, "request explain", true, "request explain retry"),
		reviewerQuery(RequestExplanationView, "request explain view", false),
		reviewerQuery(RequestExplainStatus, "request explain status", false),
		operatorQuery(RequestGrantList, "request grant list"),
		reviewerDestructive(RequestGrantRevoke, "request grant revoke"),
		operatorQuery(CompletionCurrent, "agent completion current"),
		operatorQuery(CompletionDoctor, "agent completion doctor"),
		operatorQuery(CompletionList, "agent completion list"),
		operatorQuery(CompletionView, "agent completion view"),
		operatorMutation(ManagedAgentSpawn, "agent spawn", RiskState, false),
		operatorQuery(ManagedAgentList, "agent list", "agent ls"),
		operatorQuery(ManagedAgentGet, "agent get"),
		operatorQuery(ManagedAgentWait, "agent wait"),
		operatorMutation(ManagedAgentSend, "agent send", RiskState, false),
		operatorMutation(ManagedAgentCancel, "agent cancel", RiskState, false),
		operatorQuery(DoctorRead, "doctor"),
		operatorQuery(ConfigPath, "config path"),
		operatorMutation(ConfigExport, "config export", RiskState, false),
		operatorSensitive(ConfigImport, "config import", false),
		operatorQuery(ConfigGet, "config get", "config why", "config diff", "telegram token status"),
		operatorQuery(ConfigList, "config list"),
		operatorMutation(ConfigSet, "config set", RiskState, false,
			"config unset", "config rotate", "config reveal",
			"http mcp enable", "http mcp disable", "http mcp port", "http exposure mode", "http exposure interface add", "http exposure interface remove",
			"http security insecure allow", "http security insecure deny", "http security loopback auth allow", "http security loopback auth require",
			"http admin enable", "http admin disable", "http admin port",
			"auth mcp legacy bearer enable", "auth mcp legacy bearer disable",
			"permissions allow dir add", "permissions allow dir remove",
			"shell path",
			"notification approval enable", "notification approval disable",
			"notification approval pending enable", "notification approval pending disable",
			"notification approval resolved enable", "notification approval resolved disable",
			"notification desktop enable", "notification desktop disable",
			"notification telegram enable", "notification telegram disable",
			"notification completion enable", "notification completion disable",
			"notification completion desktop enable", "notification completion desktop disable",
			"notification completion telegram enable", "notification completion telegram disable",
			"integration ponytail enable", "integration ponytail disable", "integration ponytail mode",
			"integration caveman enable", "integration caveman disable", "integration caveman mode",
			"integration rtk path",
			"integration codegraph enable", "integration codegraph disable", "integration codegraph path",
			"integration typesafe model", "integration typesafe timeout",
			"integration typesafe key set", "integration typesafe key remove",
			"telegram token set", "telegram token remove", "telegram logout",
			"tunnel admin organization set", "tunnel admin workspace set", "tunnel admin tenant set",
			"tunnel admin enable", "tunnel admin disable",
			"request explain mode",
		),
		operatorSensitive(ConfigMigrate, "config migrate", false),
		operatorSensitive(ConfigMigrateSecrets, "config migrate secrets", false),
		operatorQuery(ConfigVerify, "config verify", "config validate"),
		operatorQuery(PromptList, "prompt list"),
		operatorQuery(PromptGet, "prompt get"),
		operatorMutation(PromptCreate, "prompt create", RiskState, false),
		operatorMutation(PromptUpdate, "prompt update", RiskState, false),
		operatorDestructive(PromptDelete, "prompt delete", false),
		operatorSensitive(AuthMCPRotate, "auth mcp create", false),
		operatorSensitive(AuthMCPEnable, "auth mcp enable", false),
		operatorSensitive(AuthMCPDisable, "auth mcp disable", false),
		operatorSensitive(AuthAdminRotate, "auth admin create", false),
		operatorSensitive(AuthAdminEnable, "auth admin enable", false),
		operatorSensitive(AuthAdminDisable, "auth admin disable", false),
		operatorQuery(AuthStatus, "auth status"),
		operatorQuery(WorkspaceContainerList, "workspace container list"),
		operatorMutation(WorkspaceContainerCreate, "workspace container create", RiskState, false),
		operatorQuery(WorkspaceContainerShow, "workspace container show"),
		operatorMutation(WorkspaceContainerRename, "workspace container rename", RiskState, false),
		operatorDestructive(WorkspaceContainerDelete, "workspace container delete", false),
		operatorMutation(WorkspaceContainerAdd, "workspace container add", RiskState, false),
		operatorMutation(WorkspaceContainerRemove, "workspace container remove", RiskState, false),
		operatorQuery(WorkspaceAccessList, "workspace access list"),
		operatorSensitive(WorkspaceAccessAdd, "workspace access add", false),
		operatorSensitive(WorkspaceAccessRemove, "workspace access remove", false),
		operatorMutation(WorkspaceRegister, "workspace register", RiskState, false),
		operatorQuery(WorkspaceList, "workspace list"),
		operatorQuery(WorkspaceShow, "workspace show", "workspace doctor"),
		operatorMutation(WorkspaceRelocate, "workspace relocate", RiskState, false),
		operatorMutation(WorkspaceUnregister, "workspace unregister", RiskState, false),
		operatorDeleteRequired(WorkspacePurge, "workspace purge", false),
		operatorRuntime(MCPStdio, "mcp stdio", false),
		operatorRuntime(MCPHTTP, "mcp http", true),
		operatorQuery(UpstreamServerList, "upstream server list"),
		operatorMutation(UpstreamServerAdd, "upstream server add", RiskSensitive, true),
		operatorMutation(UpstreamServerConfigure, "upstream server configure", RiskSensitive, true),
		operatorQuery(UpstreamServerShow, "upstream server show"),
		operatorDestructive(UpstreamServerRemove, "upstream server remove", true),
		operatorMutation(UpstreamServerEnable, "upstream server enable", RiskState, true),
		operatorMutation(UpstreamServerDisable, "upstream server disable", RiskState, true),
		operatorQueryOpenWorld(UpstreamServerStatus, "upstream server status"),
		operatorQueryOpenWorld(UpstreamServerTools, "upstream server tools"),
		operatorSensitive(UpstreamAuthLogin, "upstream server auth login", true),
		operatorQuery(UpstreamAuthStatus, "upstream server auth status"),
		operatorSensitive(UpstreamAuthLogout, "upstream server auth logout", true),
		operatorQuery(TunnelStatus, "tunnel status"),
		operatorMutation(TunnelSync, "tunnel sync", RiskSensitive, true),
		operatorSensitive(TunnelConfigure, "tunnel configure", true, "tunnel key set", "tunnel key remove"),
		operatorRuntime(TunnelEnable, "tunnel enable", true),
		operatorRuntime(TunnelDisable, "tunnel disable", true),
		operatorRuntime(TunnelForeground, "tunnel run", true),
		operatorSensitive(TunnelAdminKeySet, "tunnel admin key set", true),
		operatorQuery(TunnelAdminKeyStatus, "tunnel admin key status"),
		operatorSensitive(TunnelAdminKeyVerify, "tunnel admin key verify", true, "tunnel admin verify"),
		operatorSensitive(TunnelAdminKeyRemove, "tunnel admin key remove", false),
		operatorQueryOpenWorld(TunnelList, "tunnel list"),
		operatorQueryOpenWorld(TunnelGet, "tunnel get"),
		operatorSensitive(TunnelUse, "tunnel use", true),
		operatorMutation(TunnelCreate, "tunnel create", RiskSensitive, true),
		operatorMutation(TunnelUpdate, "tunnel update", RiskSensitive, true),
		operatorDeleteRequired(TunnelDelete, "tunnel delete", true),
		operatorQuery(StatusOverview, "status"),
		operatorQuery(VersionAbout, "version"),
		operatorQuery(TelemetryStatus, "telemetry status"),
		operatorMutation(TelemetryEnable, "telemetry enable", RiskState, false),
		operatorMutation(TelemetryDisable, "telemetry disable", RiskState, false),
		operatorQuery(TelemetryShow, "telemetry show"),
		operatorSensitive(TelegramSetup, "telegram setup", true),
		operatorQuery(LLMStatus, "llm status"),
		operatorQuery(LLMProviderList, "llm provider list"),
		operatorQuery(LLMProviderGet, "llm provider show", "llm ollama status"),
		operatorMutation(LLMProviderAdd, "llm provider add", RiskState, false),
		operatorMutation(LLMProviderConfigure, "llm provider configure", RiskState, false, "llm ollama model", "llm ollama mode"),
		operatorDeleteRequired(LLMProviderRemove, "llm provider remove", false),
		operatorMutation(LLMProviderSelect, "llm use", RiskState, false, "llm ollama use"),
		operatorQueryOpenWorld(LLMProviderModels, "llm models", "llm ollama models"),
		operatorQueryOpenWorld(LLMProviderProbe, "llm probe"),
		operatorSensitive(LLMProviderCredentialSet, "llm provider key set", false, "llm ollama key set"),
		operatorSensitive(LLMProviderCredentialClear, "llm provider key clear", false, "llm ollama key clear"),
	}
	values = append(values, integrationSpecs()...)
	values = append(values, adminOnlySpecs()...)
	values = append(values, agentOnlySpecs()...)
	for index := range values {
		values[index].Admin = append([]AdminBinding(nil), adminBindings[values[index].ID]...)
		values[index].MCPTools = append(values[index].MCPTools, mcpToolBindings[values[index].ID]...)
		values[index].PlannedMCPTools = append(values[index].PlannedMCPTools, plannedMCPToolBindings[values[index].ID]...)
		values[index].Surfaces = surfaceContracts(values[index])
	}
	return values
}

func operatorQuery(id ID, path string, aliases ...string) Spec {
	return operatorSpec(id, KindQuery, RiskNone, ConfirmationPolicy{Mode: ConfirmationNone}, SemanticEffects{Key: string(id), ReadOnly: true, Idempotent: true}, path, aliases...)
}

func operatorQueryOpenWorld(id ID, path string, aliases ...string) Spec {
	spec := operatorQuery(id, path, aliases...)
	spec.Effects.OpenWorld = true
	return spec
}

func operatorStream(id ID, path string, openWorld bool, aliases ...string) Spec {
	return operatorSpec(id, KindStream, RiskNone, ConfirmationPolicy{Mode: ConfirmationNone}, SemanticEffects{Key: string(id), ReadOnly: true, Idempotent: true, OpenWorld: openWorld}, path, aliases...)
}

func operatorMutation(id ID, path string, risk MutationRisk, openWorld bool, aliases ...string) Spec {
	return operatorSpec(id, KindMutation, risk, ConfirmationPolicy{Mode: ConfirmationNone, ControlApproval: true}, SemanticEffects{Key: string(id), Destructive: risk == RiskDestructive, OpenWorld: openWorld}, path, aliases...)
}

func operatorSensitive(id ID, path string, openWorld bool, aliases ...string) Spec {
	return operatorMutation(id, path, RiskSensitive, openWorld, aliases...)
}

func operatorDestructive(id ID, path string, openWorld bool, aliases ...string) Spec {
	spec := operatorMutation(id, path, RiskDestructive, openWorld, aliases...)
	spec.Confirmation.Mode = ConfirmationRecommended
	return spec
}

func operatorDeleteRequired(id ID, path string, openWorld bool, aliases ...string) Spec {
	spec := operatorMutation(id, path, RiskDestructive, openWorld, aliases...)
	spec.Confirmation.Mode = ConfirmationRequired
	return spec
}

func operatorRuntime(id ID, path string, openWorld bool, aliases ...string) Spec {
	return operatorSpec(id, KindRuntime, RiskSensitive, ConfirmationPolicy{Mode: ConfirmationNone, ControlApproval: true}, SemanticEffects{Key: string(id), OpenWorld: openWorld}, path, aliases...)
}

func reviewerMutation(id ID, path string, aliases ...string) Spec {
	spec := operatorSpec(id, KindMutation, RiskSensitive, ConfirmationPolicy{Mode: ConfirmationReview}, SemanticEffects{Key: string(id)}, path, aliases...)
	spec.Audience = AudienceReviewer
	spec.Authorization = AuthorizationReviewer
	return spec
}

func reviewerQuery(id ID, path string, openWorld bool, aliases ...string) Spec {
	spec := operatorQuery(id, path, aliases...)
	spec.Audience = AudienceReviewer
	spec.Authorization = AuthorizationReviewer
	spec.Effects.OpenWorld = openWorld
	return spec
}

func reviewerRuntime(id ID, path string, openWorld bool, aliases ...string) Spec {
	spec := operatorSpec(id, KindRuntime, RiskSensitive, ConfirmationPolicy{Mode: ConfirmationNone}, SemanticEffects{Key: string(id), OpenWorld: openWorld}, path, aliases...)
	spec.Audience = AudienceReviewer
	spec.Authorization = AuthorizationReviewer
	return spec
}

func reviewerDestructive(id ID, path string, aliases ...string) Spec {
	spec := reviewerMutation(id, path, aliases...)
	spec.Risk = RiskDestructive
	spec.Effects.Destructive = true
	return spec
}

func operatorSpec(id ID, kind Kind, risk MutationRisk, confirmation ConfirmationPolicy, effects SemanticEffects, path string, aliases ...string) Spec {
	return Spec{
		ID: id, Kind: kind, Audience: AudienceOperator, Authorization: AuthorizationOperator,
		Risk: risk, Confirmation: confirmation, Effects: effects,
		CLI: CLIBinding{CanonicalPath: path, Aliases: append([]string(nil), aliases...)},
	}
}

func All() []Spec {
	out := make([]Spec, len(specs))
	for i, spec := range specs {
		out[i] = cloneSpec(spec)
	}
	return out
}

func Lookup(id ID) (Spec, bool) {
	for _, spec := range specs {
		if spec.ID == id {
			return cloneSpec(spec), true
		}
	}
	return Spec{}, false
}

func ForPath(path string) (ID, bool) {
	path = NormalizePath(path)
	for _, spec := range specs {
		for _, candidate := range spec.CLIPaths() {
			if NormalizePath(candidate) == path {
				return spec.ID, true
			}
		}
	}
	return "", false
}

func ForAdmin(method, path string) (ID, bool) {
	want := normalizeAdminBinding(AdminBinding{Method: method, Path: path})
	for _, spec := range specs {
		for _, binding := range spec.Admin {
			if normalizeAdminBinding(binding) == want {
				return spec.ID, true
			}
		}
	}
	return "", false
}

func ForAdminRequest(method, path string) (ID, bool) {
	want := normalizeAdminBinding(AdminBinding{Method: method, Path: path})
	for _, spec := range specs {
		for _, binding := range spec.Admin {
			candidate := normalizeAdminBinding(binding)
			if candidate.Method == want.Method && matchAdminPath(candidate.Path, want.Path) {
				return spec.ID, true
			}
		}
	}
	return "", false
}

func matchAdminPath(pattern, path string) bool {
	patternParts := strings.Split(strings.Trim(pattern, "/"), "/")
	pathParts := strings.Split(strings.Trim(path, "/"), "/")
	if len(patternParts) != len(pathParts) {
		return false
	}
	for index, part := range patternParts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			if strings.TrimSpace(pathParts[index]) == "" {
				return false
			}
			continue
		}
		if part != pathParts[index] {
			return false
		}
	}
	return true
}

func ForMCPTool(name string) (ID, bool) {
	name = strings.TrimSpace(name)
	for _, spec := range specs {
		for _, tool := range spec.MCPTools {
			if tool == name {
				return spec.ID, true
			}
		}
	}
	return "", false
}

func ForPlannedMCPTool(name string) (ID, bool) {
	name = strings.TrimSpace(name)
	for _, spec := range specs {
		for _, tool := range spec.PlannedMCPTools {
			if tool == name {
				return spec.ID, true
			}
		}
	}
	return "", false
}

func SearchText(ids []ID) string {
	parts := make([]string, 0, len(ids)*4)
	for _, id := range ids {
		if spec, ok := Lookup(id); ok {
			parts = append(parts, spec.CLI.CanonicalPath)
			parts = append(parts, spec.CLI.Aliases...)
			parts = append(parts, spec.MCPTools...)
			parts = append(parts, spec.PlannedMCPTools...)
		}
	}
	return strings.Join(parts, " ")
}

func NormalizePath(path string) string {
	if strings.TrimSpace(path) == RootPath {
		return RootPath
	}
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(path)))
	kept := fields[:0]
	for _, field := range fields {
		if strings.HasPrefix(field, "-") {
			continue
		}
		kept = append(kept, field)
	}
	return strings.Join(kept, " ")
}

func cloneSpec(spec Spec) Spec {
	spec.CLI.Aliases = append([]string(nil), spec.CLI.Aliases...)
	spec.Admin = append([]AdminBinding(nil), spec.Admin...)
	spec.MCPTools = append([]string(nil), spec.MCPTools...)
	spec.PlannedMCPTools = append([]string(nil), spec.PlannedMCPTools...)
	spec.Surfaces = append([]SurfaceContract(nil), spec.Surfaces...)
	return spec
}
