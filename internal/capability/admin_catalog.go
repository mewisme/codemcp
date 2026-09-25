package capability

var adminBindings = map[ID][]AdminBinding{
	HealthRead:                       {{Method: "GET", Path: "/api/health"}},
	NetworkInterfacesList:            {{Method: "GET", Path: "/api/network/interfaces"}},
	ConfigSnapshotRead:               {{Method: "GET", Path: "/api/config"}},
	ConfigPatch:                      {{Method: "PUT", Path: "/api/config"}},
	InstructionSettingsRead:          {{Method: "GET", Path: "/api/instructions/global"}},
	InstructionSettingsWrite:         {{Method: "PUT", Path: "/api/instructions/global"}},
	WorkspaceList:                    {{Method: "GET", Path: "/api/workspaces"}},
	WorkspaceRegister:                {{Method: "POST", Path: "/api/workspaces"}},
	WorkspaceShow:                    {{Method: "GET", Path: "/api/workspaces/{workspace_id}"}},
	WorkspaceUnregister:              {{Method: "DELETE", Path: "/api/workspaces/{workspace_id}"}},
	WorkspacePurge:                   {{Method: "POST", Path: "/api/workspaces/{workspace_id}/purge"}},
	ProjectContextRead:               {{Method: "GET", Path: "/api/workspaces/{workspace_id}/context"}},
	WorkspaceRelocate:                {{Method: "POST", Path: "/api/workspaces/{workspace_id}/relocate"}},
	WorkspaceContainerMembershipList: {{Method: "GET", Path: "/api/workspaces/{workspace_id}/containers"}, {Method: "GET", Path: "/api/workspace-containers/{container_id}/workspaces"}},
	WorkspaceContainerAdd:            {{Method: "POST", Path: "/api/workspaces/{workspace_id}/containers"}, {Method: "POST", Path: "/api/workspace-containers/{container_id}/workspaces"}},
	WorkspaceContainerRemove:         {{Method: "DELETE", Path: "/api/workspaces/{workspace_id}/containers"}, {Method: "DELETE", Path: "/api/workspace-containers/{container_id}/workspaces"}},
	ExecutionList:                    {{Method: "GET", Path: "/api/workspaces/{workspace_id}/executions"}},
	ExecutionFeed:                    {{Method: "GET", Path: "/api/workspaces/{workspace_id}/executions/stream"}},
	ExecutionView:                    {{Method: "GET", Path: "/api/workspaces/{workspace_id}/executions/{execution_id}"}},
	ExecutionStream:                  {{Method: "GET", Path: "/api/workspaces/{workspace_id}/executions/{execution_id}/stream"}},
	ProcessList:                      {{Method: "GET", Path: "/api/workspaces/{workspace_id}/processes"}},
	ProcessView:                      {{Method: "GET", Path: "/api/workspaces/{workspace_id}/processes/{process_id}"}},
	ProcessClear:                     {{Method: "DELETE", Path: "/api/workspaces/{workspace_id}/processes/{process_id}"}},
	WorkspaceContainerList:           {{Method: "GET", Path: "/api/workspace-containers"}},
	WorkspaceContainerCreate:         {{Method: "POST", Path: "/api/workspace-containers"}},
	WorkspaceContainerShow:           {{Method: "GET", Path: "/api/workspace-containers/{container_id}"}},
	WorkspaceContainerRename:         {{Method: "PATCH", Path: "/api/workspace-containers/{container_id}"}},
	WorkspaceContainerDelete:         {{Method: "DELETE", Path: "/api/workspace-containers/{container_id}"}},
	ToolInventoryRead:                {{Method: "GET", Path: "/api/tools"}},
	RequestList:                      {{Method: "GET", Path: "/api/requests"}},
	RequestStream:                    {{Method: "GET", Path: "/api/requests/stream"}},
	RequestView:                      {{Method: "GET", Path: "/api/requests/{request_id}"}},
	RequestApprove:                   {{Method: "POST", Path: "/api/requests/{request_id}/approve"}},
	RequestDeny:                      {{Method: "POST", Path: "/api/requests/{request_id}/deny"}},
	RequestGrantList:                 {{Method: "GET", Path: "/api/requests/grants"}},
	RequestGrantRevoke:               {{Method: "POST", Path: "/api/requests/grants/{request_id}/revoke"}},
	NotificationStatus:               {{Method: "GET", Path: "/api/notifications"}},
	UpstreamServerList:               {{Method: "GET", Path: "/api/upstream"}},
	UpstreamServerAdd:                {{Method: "POST", Path: "/api/upstream"}},
	UpstreamServerShow:               {{Method: "GET", Path: "/api/upstream/{server_id}"}},
	UpstreamServerConfigure:          {{Method: "PUT", Path: "/api/upstream/{server_id}"}},
	UpstreamServerRemove:             {{Method: "DELETE", Path: "/api/upstream/{server_id}"}},
	UpstreamServerStatus:             {{Method: "GET", Path: "/api/upstream/{server_id}/status"}},
	UpstreamServerTools:              {{Method: "GET", Path: "/api/upstream/{server_id}/tools"}},
	UpstreamAuthStatus:               {{Method: "GET", Path: "/api/upstream/{server_id}/auth/status"}},
	UpstreamAuthLogin:                {{Method: "POST", Path: "/api/upstream/{server_id}/auth/login"}},
	UpstreamAuthLogout:               {{Method: "DELETE", Path: "/api/upstream/{server_id}/auth/logout"}},
	TunnelConfigRead:                 {{Method: "GET", Path: "/api/tunnel/config"}},
	TunnelStatus:                     {{Method: "GET", Path: "/api/tunnel"}},
	TunnelEnable:                     {{Method: "POST", Path: "/api/tunnel"}},
	TunnelDisable:                    {{Method: "DELETE", Path: "/api/tunnel"}},
	TunnelConfigure:                  {{Method: "PUT", Path: "/api/tunnel"}},
	TunnelAdminKeyStatus:             {{Method: "GET", Path: "/api/tunnel/admin/key"}},
	TunnelAdminKeySet:                {{Method: "PUT", Path: "/api/tunnel/admin/key"}},
	TunnelAdminKeyVerify:             {{Method: "POST", Path: "/api/tunnel/admin/key"}},
	TunnelAdminKeyRemove:             {{Method: "DELETE", Path: "/api/tunnel/admin/key"}},
	TunnelList:                       {{Method: "GET", Path: "/api/tunnel/managed"}},
	TunnelCreate:                     {{Method: "POST", Path: "/api/tunnel/managed"}},
	TunnelUse:                        {{Method: "POST", Path: "/api/tunnel/managed/use"}},
	TunnelGet:                        {{Method: "GET", Path: "/api/tunnel/managed/{tunnel_id}"}},
	TunnelUpdate:                     {{Method: "PUT", Path: "/api/tunnel/managed/{tunnel_id}"}},
	TunnelDelete:                     {{Method: "DELETE", Path: "/api/tunnel/managed/{tunnel_id}"}},
	ActivityStream:                   {{Method: "GET", Path: "/api/activity/stream"}},
	ActivityView:                     {{Method: "GET", Path: "/api/activity/{call_id}"}},
	OAuthCallbackComplete:            {{Method: "GET", Path: "/oauth/callback/{server_id}"}},
}

var browserRequiredIDs = idSet(
	HealthRead, NetworkInterfacesList, ConfigSnapshotRead, ConfigPatch,
	InstructionSettingsRead, InstructionSettingsWrite,
	WorkspaceList, WorkspaceRegister, WorkspaceShow, WorkspaceUnregister, WorkspacePurge, ProjectContextRead,
	WorkspaceContainerList, WorkspaceContainerCreate, WorkspaceContainerShow, WorkspaceContainerRename, WorkspaceContainerDelete,
	WorkspaceContainerMembershipList, WorkspaceContainerAdd, WorkspaceContainerRemove,
	ToolInventoryRead,
	RequestList, RequestStream, RequestView, RequestApprove, RequestDeny,
	UpstreamServerList, UpstreamServerAdd, UpstreamServerShow, UpstreamServerConfigure, UpstreamServerRemove, UpstreamServerStatus, UpstreamServerTools,
	UpstreamAuthStatus, UpstreamAuthLogin, UpstreamAuthLogout,
	TunnelConfigRead, TunnelStatus, TunnelEnable, TunnelDisable, TunnelConfigure,
	TunnelAdminKeyStatus, TunnelAdminKeySet, TunnelAdminKeyVerify, TunnelAdminKeyRemove,
	TunnelList, TunnelCreate, TunnelUse, TunnelGet, TunnelUpdate, TunnelDelete,
	ExecutionList, ExecutionFeed, ExecutionView, ExecutionStream,
	ActivityStream, ActivityView,
)

func adminOnlySpecs() []Spec {
	requestStream := adminStreamSpec(RequestStream)
	requestStream.Audience = AudienceReviewer
	requestStream.Authorization = AuthorizationReviewer
	return []Spec{
		adminQuerySpec(HealthRead, false),
		adminQuerySpec(NetworkInterfacesList, false),
		adminQuerySpec(ConfigSnapshotRead, false),
		adminMutationSpec(ConfigPatch, RiskSensitive, false),
		adminQuerySpec(InstructionSettingsRead, false),
		adminMutationSpec(InstructionSettingsWrite, RiskState, false),
		adminQuerySpec(WorkspaceContainerMembershipList, false),
		adminQuerySpec(ProjectContextRead, false),
		adminQuerySpec(ToolInventoryRead, false),
		adminQuerySpec(ExecutionList, false),
		adminQuerySpec(ExecutionView, false),
		adminStreamSpec(ExecutionFeed),
		adminStreamSpec(ExecutionStream),
		adminQuerySpec(ProcessList, false),
		adminQuerySpec(ProcessView, false),
		adminDestructiveSpec(ProcessClear, false),
		requestStream,
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
