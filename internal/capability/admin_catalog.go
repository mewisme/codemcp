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
	RequestGrantList:                    {{Method: "GET", Path: "/api/requests/grants"}},
	RequestGrantRevoke:                  {{Method: "POST", Path: "/api/requests/grants/{request_id}/revoke"}},
	CompletionList:                      {{Method: "GET", Path: "/api/completions"}},
	CompletionCurrent:                   {{Method: "GET", Path: "/api/completions/current"}},
	CompletionFeed:                      {{Method: "GET", Path: "/api/completions/stream"}},
	CompletionView:                      {{Method: "GET", Path: "/api/completions/view/{completion_id}"}},
	NotificationStatus:                  {{Method: "GET", Path: "/api/notifications"}},
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
	IntegrationCodeGraphStatus:          {{Method: "GET", Path: "/api/integrations/codegraph"}},
	IntegrationCodeGraphProbe:           {{Method: "POST", Path: "/api/integrations/codegraph/probe"}},
	IntegrationCodeGraphInstall:         {{Method: "POST", Path: "/api/integrations/codegraph/install"}},
	IntegrationCodeGraphWorkspaceStatus: {{Method: "GET", Path: "/api/workspaces/{workspace_id}/integrations/codegraph"}},
	IntegrationCodeGraphWorkspaceInit:   {{Method: "POST", Path: "/api/workspaces/{workspace_id}/integrations/codegraph/init"}},
	IntegrationCodeGraphWorkspaceSync:   {{Method: "POST", Path: "/api/workspaces/{workspace_id}/integrations/codegraph/sync"}},
	UpstreamServerList:                  {{Method: "GET", Path: "/api/upstream"}},
	UpstreamServerAdd:                   {{Method: "POST", Path: "/api/upstream"}},
	UpstreamServerShow:                  {{Method: "GET", Path: "/api/upstream/{server_id}"}},
	UpstreamServerConfigure:             {{Method: "PUT", Path: "/api/upstream/{server_id}"}},
	UpstreamServerRemove:                {{Method: "DELETE", Path: "/api/upstream/{server_id}"}},
	UpstreamServerStatus:                {{Method: "GET", Path: "/api/upstream/{server_id}/status"}},
	UpstreamServerTools:                 {{Method: "GET", Path: "/api/upstream/{server_id}/tools"}},
	UpstreamAuthStatus:                  {{Method: "GET", Path: "/api/upstream/{server_id}/auth/status"}},
	UpstreamAuthLogin:                   {{Method: "POST", Path: "/api/upstream/{server_id}/auth/login"}},
	UpstreamAuthLogout:                  {{Method: "DELETE", Path: "/api/upstream/{server_id}/auth/logout"}},
	TunnelConfigRead:                    {{Method: "GET", Path: "/api/tunnel/config"}},
	TunnelStatus:                        {{Method: "GET", Path: "/api/tunnel"}},
	TunnelEnable:                        {{Method: "POST", Path: "/api/tunnel"}},
	TunnelDisable:                       {{Method: "DELETE", Path: "/api/tunnel"}},
	TunnelConfigure:                     {{Method: "PUT", Path: "/api/tunnel"}},
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
	OAuthCallbackComplete:               {{Method: "GET", Path: "/oauth/callback/{server_id}"}},
}

var browserRequiredIDs = idSet(
	HealthRead, StatusOverview, DoctorRead, VersionAbout,
	RuntimeUp, RuntimeDown, RuntimeRestart,
	LogsRead, LogsPath, LogsClear,
	InstallRun, UpdateCheck, UpdateApply,
	TelemetryStatus, TelemetryShow, TelemetryEnable, TelemetryDisable,
	NetworkInterfacesList, ConfigSnapshotRead, ConfigPatch, ConfigPath, ConfigVerify,
	IntegrationRTKStatus, IntegrationRTKEnable, IntegrationRTKDisable, IntegrationRTKProbe, IntegrationRTKInstall,
	IntegrationCodeGraphStatus, IntegrationCodeGraphProbe, IntegrationCodeGraphInstall,
	IntegrationTypeSafeStatus, IntegrationTypeSafeDoctor, IntegrationTypeSafeProbe, IntegrationTypeSafeEnable, IntegrationTypeSafeDisable,
	InstructionSettingsRead, InstructionSettingsWrite,
	PromptList, PromptGet, PromptCreate, PromptUpdate, PromptDelete,
	WorkspaceList, WorkspaceRegister, WorkspaceShow, WorkspaceUnregister, WorkspacePurge, ProjectContextRead,
	WorkspaceContainerList, WorkspaceContainerCreate, WorkspaceContainerShow, WorkspaceContainerRename, WorkspaceContainerDelete,
	WorkspaceContainerMembershipList, WorkspaceContainerAdd, WorkspaceContainerRemove,
	ToolInventoryRead,
	RequestList, RequestStream, RequestView, RequestApprove, RequestDeny,
	CompletionCurrent, CompletionList, CompletionView, CompletionFeed,
	UpstreamServerList, UpstreamServerAdd, UpstreamServerShow, UpstreamServerConfigure, UpstreamServerRemove, UpstreamServerStatus, UpstreamServerTools,
	UpstreamAuthStatus, UpstreamAuthLogin, UpstreamAuthLogout,
	TunnelConfigRead, TunnelStatus, TunnelEnable, TunnelDisable, TunnelConfigure,
	TunnelAdminKeyStatus, TunnelAdminKeySet, TunnelAdminKeyVerify, TunnelAdminKeyRemove,
	TunnelList, TunnelCreate, TunnelUse, TunnelGet, TunnelUpdate, TunnelDelete,
	ExecutionList, ExecutionFeed, ExecutionView, ExecutionStream,
	ProcessList, ProcessView, ProcessClear,
	IntegrationCodeGraphWorkspaceStatus, IntegrationCodeGraphWorkspaceInit, IntegrationCodeGraphWorkspaceSync,
	ActivityStream, ActivityView,
)

var adminSupplementRequiredIDs = idSet(
	WorkspaceRelocate,
	RequestGrantList, RequestGrantRevoke,
	NotificationStatus,
	OAuthCallbackComplete,
)

func adminRequired(id ID) bool {
	return browserRequiredIDs[id] || adminSupplementRequiredIDs[id]
}

var adminExemptionReasons = map[ID]string{
	ServerForeground:     "foreground server ownership remains outside the managed Admin API lifecycle",
	ConfigInit:           "configuration initialization precedes Admin API availability",
	ConfigUninit:         "configuration removal cannot be owned by the running server that depends on that configuration",
	ConfigExport:         "portable bundle export targets an operator-selected filesystem path and remains an out-of-process workflow",
	ConfigImport:         "portable bundle import requires the runtime to be stopped and remains an out-of-process workflow",
	ConfigGet:            "Admin uses the canonical config snapshot and setting registry instead of the CLI single-key facade",
	ConfigList:           "Admin uses the canonical config snapshot and setting registry instead of the CLI listing facade",
	ConfigSet:            "Admin mutations use the canonical config patch and SettingService transaction",
	ConfigMigrate:        "format migration is offline maintenance rather than a live Admin API mutation",
	ConfigMigrateSecrets: "secret migration is offline maintenance rather than a live Admin API mutation",
	LogsFollow:           "Admin exposes bounded journal reads; terminal-style continuous follow remains a CLI stream",
	TelegramSetup:        "Telegram pairing is an interactive setup workflow without a non-interactive Admin application operation",
}

func adminExemptionReason(id ID) string {
	return adminExemptionReasons[id]
}

func adminOnlySpecs() []Spec {
	requestStream := adminStreamSpec(RequestStream)
	requestStream.Audience = AudienceReviewer
	requestStream.Authorization = AuthorizationReviewer
	return []Spec{
		adminQuerySpec(HealthRead, false),
		adminQuerySpec(NetworkInterfacesList, false),
		adminQuerySpec(ConfigSnapshotRead, false),
		adminMutationSpec(ConfigPatch, RiskSensitive, false),
		adminCLIQuerySpec(InstructionSettingsRead, false, "instructions get"),
		adminCLIMutationSpec(InstructionSettingsWrite, RiskState, false, "instructions set"),
		adminQuerySpec(WorkspaceContainerMembershipList, false),
		adminCLIQuerySpec(ProjectContextRead, false, "workspace context"),
		adminCLIQuerySpec(ToolInventoryRead, false, "tools list"),
		adminCLIQuerySpec(ExecutionList, false, "execution list"),
		adminCLIQuerySpec(ExecutionView, false, "execution view"),
		adminStreamSpec(ExecutionFeed),
		adminStreamSpec(ExecutionStream),
		adminCLIQuerySpec(ProcessList, false, "process list"),
		adminCLIQuerySpec(ProcessView, false, "process view"),
		adminDestructiveSpec(ProcessClear, false),
		requestStream,
		adminStreamSpec(CompletionFeed),
		adminQuerySpec(NotificationStatus, false),
		adminQuerySpec(TunnelConfigRead, false),
		adminStreamSpec(ActivityStream),
		adminQuerySpec(ActivityView, false),
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
