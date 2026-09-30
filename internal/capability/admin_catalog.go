package capability

var adminBindings = map[ID][]AdminBinding{
	HealthRead:                          {{Method: "GET", Path: "/api/health"}},
	StatusOverview:                      {{Method: "GET", Path: "/api/status"}},
	DoctorRead:                          {{Method: "GET", Path: "/api/doctor"}},
	VersionAbout:                        {{Method: "GET", Path: "/api/about"}},
	RuntimeUp:                           {{Method: "POST", Path: "/api/runtime/up"}},
	RuntimeDown:                         {{Method: "POST", Path: "/api/runtime/down"}},
	RuntimeRestart:                      {{Method: "POST", Path: "/api/runtime/restart"}},
	LogsRead:                            {{Method: "GET", Path: "/api/logs"}},
	LogsFollow:                          {{Method: "GET", Path: "/api/logs/follow"}},
	LogsPath:                            {{Method: "GET", Path: "/api/logs/info"}},
	LogsClear:                           {{Method: "DELETE", Path: "/api/logs"}},
	InstallRun:                          {{Method: "POST", Path: "/api/install"}},
	UpdateCheck:                         {{Method: "GET", Path: "/api/update"}},
	UpdateApply:                         {{Method: "POST", Path: "/api/update"}},
	TelemetryStatus:                     {{Method: "GET", Path: "/api/telemetry"}},
	TelemetryShow:                       {{Method: "GET", Path: "/api/telemetry/show"}},
	TelemetryEnable:                     {{Method: "POST", Path: "/api/telemetry/enable"}},
	TelemetryDisable:                    {{Method: "POST", Path: "/api/telemetry/disable"}},
	NetworkInterfacesList:               {{Method: "GET", Path: "/api/network/interfaces"}},
	ConfigSnapshotRead:                  {{Method: "GET", Path: "/api/config"}},
	ConfigPatch:                         {{Method: "PUT", Path: "/api/config"}},
	ConfigPath:                          {{Method: "GET", Path: "/api/config/path"}},
	ConfigVerify:                        {{Method: "GET", Path: "/api/config/verify"}},
	ConfigExport:                        {{Method: "GET", Path: "/api/settings/export"}},
	ConfigList:                          {{Method: "GET", Path: "/api/settings"}},
	ConfigGet:                           {{Method: "GET", Path: "/api/settings/{setting_key}"}},
	ConfigSet:                           {{Method: "PUT", Path: "/api/settings/{setting_key}"}},
	AuthStatus:                          {{Method: "GET", Path: "/api/auth"}},
	AuthMCPRotate:                       {{Method: "POST", Path: "/api/auth/mcp/rotate"}},
	AuthMCPEnable:                       {{Method: "POST", Path: "/api/auth/mcp/enable"}},
	AuthMCPDisable:                      {{Method: "POST", Path: "/api/auth/mcp/disable"}},
	AuthAdminRotate:                     {{Method: "POST", Path: "/api/auth/admin/rotate"}},
	AuthAdminEnable:                     {{Method: "POST", Path: "/api/auth/admin/enable"}},
	AuthAdminDisable:                    {{Method: "POST", Path: "/api/auth/admin/disable"}},
	InstructionSettingsRead:             {{Method: "GET", Path: "/api/instructions/global"}},
	InstructionSettingsWrite:            {{Method: "PUT", Path: "/api/instructions/global"}},
	PromptList:                          {{Method: "GET", Path: "/api/prompts"}},
	PromptGet:                           {{Method: "GET", Path: "/api/prompts/{name}"}},
	PromptCreate:                        {{Method: "POST", Path: "/api/prompts"}},
	PromptUpdate:                        {{Method: "PUT", Path: "/api/prompts/{name}"}},
	PromptDelete:                        {{Method: "DELETE", Path: "/api/prompts/{name}"}},
	WorkspaceList:                       {{Method: "GET", Path: "/api/workspaces"}},
	WorkspaceRegister:                   {{Method: "POST", Path: "/api/workspaces"}},
	WorkspaceShow:                       {{Method: "GET", Path: "/api/workspaces/{workspace_id}"}},
	WorkspaceUnregister:                 {{Method: "DELETE", Path: "/api/workspaces/{workspace_id}"}},
	WorkspacePurge:                      {{Method: "POST", Path: "/api/workspaces/{workspace_id}/purge"}},
	ProjectContextRead:                  {{Method: "GET", Path: "/api/workspaces/{workspace_id}/context"}},
	WorkspaceRelocate:                   {{Method: "POST", Path: "/api/workspaces/{workspace_id}/relocate"}},
	WorkspaceAccessList:                 {{Method: "GET", Path: "/api/workspaces/{workspace_id}/access"}},
	WorkspaceAccessAdd:                  {{Method: "POST", Path: "/api/workspaces/{workspace_id}/access"}},
	WorkspaceAccessRemove:               {{Method: "DELETE", Path: "/api/workspaces/{workspace_id}/access"}},
	WorkspaceContainerMembershipList:    {{Method: "GET", Path: "/api/workspaces/{workspace_id}/containers"}, {Method: "GET", Path: "/api/workspace-containers/{container_id}/workspaces"}},
	WorkspaceContainerAdd:               {{Method: "POST", Path: "/api/workspaces/{workspace_id}/containers"}, {Method: "POST", Path: "/api/workspace-containers/{container_id}/workspaces"}},
	WorkspaceContainerRemove:            {{Method: "DELETE", Path: "/api/workspaces/{workspace_id}/containers"}, {Method: "DELETE", Path: "/api/workspace-containers/{container_id}/workspaces"}},
	ExecutionList:                       {{Method: "GET", Path: "/api/workspaces/{workspace_id}/executions"}},
	ExecutionFeed:                       {{Method: "GET", Path: "/api/workspaces/{workspace_id}/executions/stream"}},
	ExecutionView:                       {{Method: "GET", Path: "/api/workspaces/{workspace_id}/executions/{execution_id}"}},
	ExecutionStream:                     {{Method: "GET", Path: "/api/workspaces/{workspace_id}/executions/{execution_id}/stream"}},
	ProcessList:                         {{Method: "GET", Path: "/api/workspaces/{workspace_id}/processes"}},
	ProcessView:                         {{Method: "GET", Path: "/api/workspaces/{workspace_id}/processes/{process_id}"}},
	ProcessClear:                        {{Method: "DELETE", Path: "/api/workspaces/{workspace_id}/processes/{process_id}"}},
	WorkspaceContainerList:              {{Method: "GET", Path: "/api/workspace-containers"}},
	WorkspaceContainerCreate:            {{Method: "POST", Path: "/api/workspace-containers"}},
	WorkspaceContainerShow:              {{Method: "GET", Path: "/api/workspace-containers/{container_id}"}},
	WorkspaceContainerRename:            {{Method: "PATCH", Path: "/api/workspace-containers/{container_id}"}},
	WorkspaceContainerDelete:            {{Method: "DELETE", Path: "/api/workspace-containers/{container_id}"}},
	ToolInventoryRead:                   {{Method: "GET", Path: "/api/tools"}},
	RequestList:                         {{Method: "GET", Path: "/api/requests"}},
	RequestStream:                       {{Method: "GET", Path: "/api/requests/stream"}},
	RequestView:                         {{Method: "GET", Path: "/api/requests/{request_id}"}},
	RequestApprove:                      {{Method: "POST", Path: "/api/requests/{request_id}/approve"}},
	RequestDeny:                         {{Method: "POST", Path: "/api/requests/{request_id}/deny"}},
	RequestExplainStatus:                {{Method: "GET", Path: "/api/requests/explain/status"}},
	RequestExplanationView:              {{Method: "GET", Path: "/api/requests/{request_id}/explanation"}},
	RequestExplain:                      {{Method: "POST", Path: "/api/requests/{request_id}/explain"}},
	RequestGrantList:                    {{Method: "GET", Path: "/api/requests/grants"}},
	RequestGrantRevoke:                  {{Method: "POST", Path: "/api/requests/grants/{request_id}/revoke"}},
	CompletionList:                      {{Method: "GET", Path: "/api/completions"}},
	CompletionCurrent:                   {{Method: "GET", Path: "/api/completions/current"}},
	CompletionFeed:                      {{Method: "GET", Path: "/api/completions/stream"}},
	CompletionView:                      {{Method: "GET", Path: "/api/completions/view/{completion_id}"}},
	CompletionDoctor:                    {{Method: "GET", Path: "/api/completions/doctor"}},
	NotificationStatus:                  {{Method: "GET", Path: "/api/notifications"}},
	TelegramSetup:                       {{Method: "PUT", Path: "/api/telegram/setup"}},
	IntegrationTypeSafeStatus:           {{Method: "GET", Path: "/api/integrations/typesafe"}},
	IntegrationTypeSafeDoctor:           {{Method: "GET", Path: "/api/integrations/typesafe/doctor"}},
	IntegrationTypeSafeProbe:            {{Method: "POST", Path: "/api/integrations/typesafe/probe"}},
	IntegrationTypeSafeEnable:           {{Method: "POST", Path: "/api/integrations/typesafe/enable"}},
	IntegrationTypeSafeDisable:          {{Method: "POST", Path: "/api/integrations/typesafe/disable"}},
	IntegrationRTKStatus:                {{Method: "GET", Path: "/api/integrations/rtk"}},
	IntegrationRTKEnable:                {{Method: "POST", Path: "/api/integrations/rtk/enable"}},
	IntegrationRTKDisable:               {{Method: "POST", Path: "/api/integrations/rtk/disable"}},
	IntegrationRTKProbe:                 {{Method: "POST", Path: "/api/integrations/rtk/probe"}},
	IntegrationRTKInstall:               {{Method: "POST", Path: "/api/integrations/rtk/install"}},
	IntegrationRTKInstallGlobal:         {{Method: "GET", Path: "/api/integrations/rtk/global"}},
	IntegrationCodeGraphStatus:          {{Method: "GET", Path: "/api/integrations/codegraph"}},
	IntegrationCodeGraphProbe:           {{Method: "POST", Path: "/api/integrations/codegraph/probe"}},
	IntegrationCodeGraphInstall:         {{Method: "POST", Path: "/api/integrations/codegraph/install"}},
	IntegrationCodeGraphInstallGlobal:   {{Method: "GET", Path: "/api/integrations/codegraph/global"}},
	IntegrationCFStatus:                 {{Method: "GET", Path: "/api/integrations/cf"}},
	IntegrationCFProbe:                  {{Method: "POST", Path: "/api/integrations/cf/probe"}},
	IntegrationCFInstall:                {{Method: "POST", Path: "/api/integrations/cf/install"}},
	IntegrationCFUpdate:                 {{Method: "POST", Path: "/api/integrations/cf/update"}},
	IntegrationCFRemove:                 {{Method: "DELETE", Path: "/api/integrations/cf"}},
	IntegrationCodeGraphWorkspaceStatus: {{Method: "GET", Path: "/api/workspaces/{workspace_id}/integrations/codegraph"}},
	IntegrationCodeGraphWorkspaceInit:   {{Method: "POST", Path: "/api/workspaces/{workspace_id}/integrations/codegraph/init"}},
	IntegrationCodeGraphWorkspaceSync:   {{Method: "POST", Path: "/api/workspaces/{workspace_id}/integrations/codegraph/sync"}},
	UpstreamServerList:                  {{Method: "GET", Path: "/api/upstream"}},
	UpstreamServerAdd:                   {{Method: "POST", Path: "/api/upstream"}},
	UpstreamServerShow:                  {{Method: "GET", Path: "/api/upstream/{server_id}"}},
	UpstreamServerConfigure:             {{Method: "PUT", Path: "/api/upstream/{server_id}"}},
	UpstreamServerRemove:                {{Method: "DELETE", Path: "/api/upstream/{server_id}"}},
	UpstreamServerEnable:                {{Method: "POST", Path: "/api/upstream/{server_id}/enable"}},
	UpstreamServerDisable:               {{Method: "POST", Path: "/api/upstream/{server_id}/disable"}},
	UpstreamServerStatus:                {{Method: "GET", Path: "/api/upstream/{server_id}/status"}},
	UpstreamServerTools:                 {{Method: "GET", Path: "/api/upstream/{server_id}/tools"}},
	UpstreamAuthStatus:                  {{Method: "GET", Path: "/api/upstream/{server_id}/auth/status"}},
	UpstreamAuthLogin:                   {{Method: "POST", Path: "/api/upstream/{server_id}/auth/login"}},
	UpstreamAuthLogout:                  {{Method: "DELETE", Path: "/api/upstream/{server_id}/auth/logout"}},
	TunnelConfigRead:                    {{Method: "GET", Path: "/api/tunnel/config"}},
	TunnelStatus:                        {{Method: "GET", Path: "/api/tunnel"}},
	TunnelSync:                          {{Method: "POST", Path: "/api/tunnel/sync"}},
	TunnelEnable:                        {{Method: "POST", Path: "/api/tunnel"}},
	TunnelDisable:                       {{Method: "DELETE", Path: "/api/tunnel"}},
	TunnelConfigure:                     {{Method: "PUT", Path: "/api/tunnel"}, {Method: "DELETE", Path: "/api/tunnel/runtime/key"}},
	TunnelAdminKeyStatus:                {{Method: "GET", Path: "/api/tunnel/admin/key"}},
	TunnelAdminKeySet:                   {{Method: "PUT", Path: "/api/tunnel/admin/key"}},
	TunnelAdminKeyVerify:                {{Method: "POST", Path: "/api/tunnel/admin/key"}},
	TunnelAdminKeyRemove:                {{Method: "DELETE", Path: "/api/tunnel/admin/key"}},
	TunnelList:                          {{Method: "GET", Path: "/api/tunnel/managed"}},
	TunnelCreate:                        {{Method: "POST", Path: "/api/tunnel/managed"}},
	TunnelUse:                           {{Method: "POST", Path: "/api/tunnel/managed/use"}},
	TunnelGet:                           {{Method: "GET", Path: "/api/tunnel/managed/{tunnel_id}"}},
	TunnelUpdate:                        {{Method: "PUT", Path: "/api/tunnel/managed/{tunnel_id}"}},
	TunnelDelete:                        {{Method: "DELETE", Path: "/api/tunnel/managed/{tunnel_id}"}},
	ActivityStream:                      {{Method: "GET", Path: "/api/activity/stream"}},
	ActivityView:                        {{Method: "GET", Path: "/api/activity/{call_id}"}},
	LLMStatus:                           {{Method: "GET", Path: "/api/llm/status"}},
	LLMProviderList:                     {{Method: "GET", Path: "/api/llm/providers"}},
	LLMProviderAdd:                      {{Method: "POST", Path: "/api/llm/providers"}},
	LLMProviderGet:                      {{Method: "GET", Path: "/api/llm/providers/{provider_id}"}},
	LLMProviderConfigure:                {{Method: "PUT", Path: "/api/llm/providers/{provider_id}"}},
	LLMProviderRemove:                   {{Method: "DELETE", Path: "/api/llm/providers/{provider_id}"}},
	LLMProviderSelect:                   {{Method: "POST", Path: "/api/llm/providers/{provider_id}/select"}},
	LLMProviderModels:                   {{Method: "GET", Path: "/api/llm/providers/{provider_id}/models"}},
	LLMProviderProbe:                    {{Method: "POST", Path: "/api/llm/providers/{provider_id}/probe"}},
	LLMProviderCredentialSet:            {{Method: "PUT", Path: "/api/llm/providers/{provider_id}/credential"}},
	LLMProviderCredentialClear:          {{Method: "DELETE", Path: "/api/llm/providers/{provider_id}/credential"}},
	OAuthCallbackComplete:               {{Method: "GET", Path: "/oauth/callback/{server_id}"}},
}

var browserFrontendOperationIDs = idSet(
	HealthRead, StatusOverview, DoctorRead, VersionAbout,
	RuntimeUp, RuntimeDown, RuntimeRestart,
	LogsRead, LogsFollow, LogsPath, LogsClear,
	InstallRun, UpdateCheck, UpdateApply,
	TelemetryStatus, TelemetryShow, TelemetryEnable, TelemetryDisable,
	NetworkInterfacesList, ConfigSnapshotRead, ConfigPatch, ConfigPath, ConfigVerify,
	ConfigExport, ConfigList, ConfigGet, ConfigSet,
	AuthStatus, AuthMCPRotate, AuthMCPEnable, AuthMCPDisable, AuthAdminRotate, AuthAdminEnable, AuthAdminDisable,
	NotificationStatus, TelegramSetup,
	IntegrationRTKStatus, IntegrationRTKEnable, IntegrationRTKDisable, IntegrationRTKProbe, IntegrationRTKInstall, IntegrationRTKInstallGlobal,
	IntegrationCodeGraphStatus, IntegrationCodeGraphProbe, IntegrationCodeGraphInstall, IntegrationCodeGraphInstallGlobal,
	IntegrationCFStatus, IntegrationCFProbe, IntegrationCFInstall, IntegrationCFUpdate, IntegrationCFRemove,
	IntegrationTypeSafeStatus, IntegrationTypeSafeDoctor, IntegrationTypeSafeProbe, IntegrationTypeSafeEnable, IntegrationTypeSafeDisable,
	InstructionSettingsRead, InstructionSettingsWrite,
	PromptList, PromptGet, PromptCreate, PromptUpdate, PromptDelete,
	WorkspaceList, WorkspaceRegister, WorkspaceShow, WorkspaceRelocate, WorkspaceUnregister, WorkspacePurge, ProjectContextRead,
	WorkspaceAccessList, WorkspaceAccessAdd, WorkspaceAccessRemove,
	WorkspaceContainerList, WorkspaceContainerCreate, WorkspaceContainerShow, WorkspaceContainerRename, WorkspaceContainerDelete,
	WorkspaceContainerMembershipList, WorkspaceContainerAdd, WorkspaceContainerRemove,
	ToolInventoryRead,
	RequestList, RequestStream, RequestView, RequestApprove, RequestDeny, RequestGrantList, RequestGrantRevoke,
	CompletionCurrent, CompletionList, CompletionView, CompletionFeed, CompletionDoctor,
	UpstreamServerList, UpstreamServerAdd, UpstreamServerShow, UpstreamServerConfigure, UpstreamServerRemove, UpstreamServerEnable, UpstreamServerDisable, UpstreamServerStatus, UpstreamServerTools,
	UpstreamAuthStatus, UpstreamAuthLogin, UpstreamAuthLogout,
	TunnelConfigRead, TunnelStatus, TunnelSync, TunnelEnable, TunnelDisable, TunnelConfigure,
	TunnelAdminKeyStatus, TunnelAdminKeySet, TunnelAdminKeyVerify, TunnelAdminKeyRemove,
	TunnelList, TunnelCreate, TunnelUse, TunnelGet, TunnelUpdate, TunnelDelete,
	ExecutionList, ExecutionFeed, ExecutionView, ExecutionStream,
	ProcessList, ProcessView, ProcessClear,
	IntegrationCodeGraphWorkspaceStatus, IntegrationCodeGraphWorkspaceInit, IntegrationCodeGraphWorkspaceSync,
	ActivityStream, ActivityView,
	LLMStatus, LLMProviderList, LLMProviderGet, LLMProviderAdd, LLMProviderConfigure, LLMProviderRemove,
	LLMProviderSelect, LLMProviderModels, LLMProviderProbe, LLMProviderCredentialSet, LLMProviderCredentialClear,
	RequestExplain, RequestExplanationView, RequestExplainStatus,
)

func adminOnlySpecs() []Spec {
	requestStream := adminStreamSpec(RequestStream)
	requestStream.Audience = AudienceReviewer
	requestStream.Authorization = AuthorizationReviewer
	requestStream.CLI = CLIBinding{CanonicalPath: "request stream"}
	return []Spec{
		adminCLIQuerySpec(HealthRead, false, "health"),
		adminCLIQuerySpec(NetworkInterfacesList, false, "network interfaces"),
		adminCLIQuerySpec(ConfigSnapshotRead, false, "config snapshot"),
		adminCLIMutationSpec(ConfigPatch, RiskSensitive, false, "config patch"),
		adminCLIQuerySpec(InstructionSettingsRead, false, "instructions get"),
		adminCLIMutationSpec(InstructionSettingsWrite, RiskState, false, "instructions set"),
		adminCLIQuerySpec(WorkspaceContainerMembershipList, false, "workspace container membership list"),
		adminCLIQuerySpec(ProjectContextRead, false, "workspace context"),
		adminCLIQuerySpec(ToolInventoryRead, false, "tools list"),
		adminCLIQuerySpec(ExecutionList, false, "execution list"),
		adminCLIQuerySpec(ExecutionView, false, "execution view"),
		adminCLIStreamSpec(ExecutionFeed, "execution feed"),
		adminCLIStreamSpec(ExecutionStream, "execution stream"),
		adminCLIQuerySpec(ProcessList, false, "process list"),
		adminCLIQuerySpec(ProcessView, false, "process view"),
		adminCLIDestructiveSpec(ProcessClear, false, "process clear"),
		requestStream,
		adminCLIStreamSpec(CompletionFeed, "agent completion feed"),
		adminCLIQuerySpec(NotificationStatus, false, "notification status"),
		adminCLIQuerySpec(TunnelConfigRead, false, "tunnel config"),
		adminCLIStreamSpec(ActivityStream, "activity stream"),
		adminCLIQuerySpec(ActivityView, false, "activity view"),
		protocolMutationSpec(OAuthCallbackComplete, RiskSensitive, true),
	}
}

func adminQuerySpec(id ID, openWorld bool) Spec {
	return Spec{
		ID: id, Kind: KindQuery, Audience: AudienceOperator, Authorization: AuthorizationOperator, Risk: RiskNone,
		Confirmation: ConfirmationPolicy{Mode: ConfirmationNone},
		Effects:      SemanticEffects{Key: string(id), ReadOnly: true, Idempotent: true, OpenWorld: openWorld},
	}
}

func adminCLIQuerySpec(id ID, openWorld bool, path string) Spec {
	spec := adminQuerySpec(id, openWorld)
	spec.CLI = CLIBinding{CanonicalPath: path}
	return spec
}

func adminStreamSpec(id ID) Spec {
	spec := adminQuerySpec(id, false)
	spec.Kind = KindStream
	return spec
}

func adminCLIStreamSpec(id ID, path string) Spec {
	spec := adminStreamSpec(id)
	spec.CLI = CLIBinding{CanonicalPath: path}
	return spec
}

func adminMutationSpec(id ID, risk MutationRisk, openWorld bool) Spec {
	return Spec{
		ID: id, Kind: KindMutation, Audience: AudienceOperator, Authorization: AuthorizationOperator, Risk: risk,
		Confirmation: ConfirmationPolicy{Mode: ConfirmationNone},
		Effects:      SemanticEffects{Key: string(id), Destructive: risk == RiskDestructive, OpenWorld: openWorld},
	}
}

func adminCLIMutationSpec(id ID, risk MutationRisk, openWorld bool, path string) Spec {
	spec := adminMutationSpec(id, risk, openWorld)
	spec.CLI = CLIBinding{CanonicalPath: path}
	return spec
}

func adminDestructiveSpec(id ID, openWorld bool) Spec {
	spec := adminMutationSpec(id, RiskDestructive, openWorld)
	spec.Confirmation.Mode = ConfirmationRecommended
	return spec
}

func adminCLIDestructiveSpec(id ID, openWorld bool, path string) Spec {
	spec := adminDestructiveSpec(id, openWorld)
	spec.CLI = CLIBinding{CanonicalPath: path}
	return spec
}

func protocolMutationSpec(id ID, risk MutationRisk, openWorld bool) Spec {
	return Spec{
		ID: id, Kind: KindProtocol, Audience: AudienceProtocol, Authorization: AuthorizationProtocol, Risk: risk,
		Confirmation: ConfirmationPolicy{Mode: ConfirmationNone},
		Effects:      SemanticEffects{Key: string(id), OpenWorld: openWorld},
	}
}

func idSet(ids ...ID) map[ID]bool {
	out := make(map[ID]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}
