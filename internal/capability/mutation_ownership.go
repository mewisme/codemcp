package capability

import "sort"

type MutationOwner string

const (
	MutationOwnerApplicationLifecycle    MutationOwner = "application.lifecycle"
	MutationOwnerApplicationSettings     MutationOwner = "application.setting-service"
	MutationOwnerApplicationAuth         MutationOwner = "application.auth"
	MutationOwnerApplicationPrompts      MutationOwner = "application.prompts"
	MutationOwnerApplicationWorkspace    MutationOwner = "application.workspace-service"
	MutationOwnerApplicationUpstream     MutationOwner = "application.upstream-service"
	MutationOwnerApplicationTunnel       MutationOwner = "application.tunnel"
	MutationOwnerApplicationTelemetry    MutationOwner = "application.telemetry"
	MutationOwnerApplicationTelegram     MutationOwner = "application.telegram"
	MutationOwnerApplicationIntegrations MutationOwner = "application.integrations"
	MutationOwnerApplicationInstructions MutationOwner = "application.instructions"
	MutationOwnerApplicationProcesses    MutationOwner = "application.process-service"
	MutationOwnerApplicationLogs         MutationOwner = "application.logs"
	MutationOwnerApplicationLLM          MutationOwner = "application.llm-service"
	MutationOwnerApproval                MutationOwner = "approval"
	MutationOwnerAgentCompletion         MutationOwner = "agent-completion"
	MutationOwnerManagedAgent            MutationOwner = "managed-agent"
	MutationOwnerFilesystem              MutationOwner = "tools.filesystem"
	MutationOwnerGit                     MutationOwner = "tools.git"
	MutationOwnerShell                   MutationOwner = "runtime.shell"
	MutationOwnerProcess                 MutationOwner = "runtime.process"
	MutationOwnerJSRuntime               MutationOwner = "jsruntime"
	MutationOwnerInstructionAuthoring    MutationOwner = "instruction-authoring"
	MutationOwnerPlanAuthoring           MutationOwner = "application.plan-authoring"
	MutationOwnerMemory                  MutationOwner = "memory"
	MutationOwnerPatch                   MutationOwner = "patch"
	MutationOwnerHistory                 MutationOwner = "history"
	MutationOwnerSemanticIntegration     MutationOwner = "integrations.semantic"
	MutationOwnerAgentConfig             MutationOwner = "application.mcp-config"
)

type MutationOwnership struct {
	Operation       ID            `json:"operation"`
	ValidationOwner MutationOwner `json:"validation_owner"`
	SideEffectOwner MutationOwner `json:"side_effect_owner"`
}

var mutationOwners = buildMutationOwners()

func MutationOwnershipFor(id ID) (MutationOwnership, bool) {
	owner, ok := mutationOwners[id]
	if !ok {
		return MutationOwnership{}, false
	}
	return MutationOwnership{Operation: id, ValidationOwner: owner, SideEffectOwner: owner}, true
}

func AllMutationOwnership() []MutationOwnership {
	result := make([]MutationOwnership, 0, len(mutationOwners))
	for id, owner := range mutationOwners {
		result = append(result, MutationOwnership{Operation: id, ValidationOwner: owner, SideEffectOwner: owner})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Operation < result[j].Operation })
	return result
}

func buildMutationOwners() map[ID]MutationOwner {
	owners := map[ID]MutationOwner{}
	add := func(owner MutationOwner, ids ...ID) {
		for _, id := range ids {
			if _, exists := owners[id]; exists {
				panic("duplicate mutation ownership: " + string(id))
			}
			owners[id] = owner
		}
	}

	add(MutationOwnerApplicationLifecycle,
		InstallRun, UpdateApply,
		ConfigInit, ConfigUninit, ConfigExport, ConfigImport, ConfigMigrate, ConfigMigrateSecrets,
	)
	add(MutationOwnerApplicationSettings, ConfigSet, ConfigPatch)
	add(MutationOwnerApplicationAuth,
		AuthMCPRotate, AuthMCPEnable, AuthMCPDisable,
		AuthAdminRotate, AuthAdminEnable, AuthAdminDisable,
	)
	add(MutationOwnerApplicationPrompts, PromptCreate, PromptUpdate, PromptDelete, AgentPromptCreate, AgentPromptUpdate, AgentPromptDelete)
	add(MutationOwnerApplicationWorkspace,
		WorkspaceContainerCreate, WorkspaceContainerRename, WorkspaceContainerDelete, WorkspaceContainerAdd, WorkspaceContainerRemove,
		WorkspaceAccessAdd, WorkspaceAccessRemove, WorkspaceRegister, WorkspaceRelocate, WorkspaceUnregister, WorkspacePurge,
	)
	add(MutationOwnerApplicationUpstream,
		UpstreamServerAdd, UpstreamServerConfigure, UpstreamServerRemove, UpstreamServerEnable, UpstreamServerDisable,
		UpstreamAuthLogin, UpstreamAuthLogout, UpstreamCall,
	)
	add(MutationOwnerApplicationTunnel,
		TunnelSync, TunnelConfigure, TunnelAdminKeySet, TunnelAdminKeyVerify, TunnelAdminKeyRemove,
		TunnelUse, TunnelCreate, TunnelUpdate, TunnelDelete,
	)
	add(MutationOwnerApplicationTelemetry, TelemetryEnable, TelemetryDisable)
	add(MutationOwnerApplicationTelegram, TelegramSetup)
	add(MutationOwnerApplicationIntegrations,
		IntegrationRTKEnable, IntegrationRTKDisable, IntegrationRTKInstall,
		IntegrationCodeGraphInstall, IntegrationCodeGraphWorkspaceInit, IntegrationCodeGraphWorkspaceSync,
		IntegrationCFInstall, IntegrationCFUpdate, IntegrationCFRemove,
		IntegrationTypeSafeEnable, IntegrationTypeSafeDisable,
		IntegrationChatGPTWebLogin, IntegrationChatGPTWebLogout,
	)
	add(MutationOwnerApplicationProcesses, ProcessClear)
	add(MutationOwnerApplicationLogs, LogsClear)
	add(MutationOwnerApplicationLLM,
		LLMProviderAdd, LLMProviderConfigure, LLMProviderRemove, LLMProviderSelect,
		LLMProviderCredentialSet, LLMProviderCredentialClear,
	)
	add(MutationOwnerApproval, RequestApprove, RequestDeny, RequestGrantRevoke, RequestControlApproval)
	add(MutationOwnerAgentCompletion, AgentComplete)
	add(MutationOwnerManagedAgent,
		AgentClaim, AgentSpawn, AgentSend, AgentCancel,
		ManagedAgentSpawn, ManagedAgentSend, ManagedAgentCancel,
	)
	add(MutationOwnerFilesystem,
		FileWriteText, FileWriteBinary, FileEdit, FileMultiEdit, FileReplaceRegex, FileCopy, FileMove, FileDelete,
		DirectoryCreate, DirectoryDelete,
	)
	add(MutationOwnerGit, GitAdd, GitCommit, GitCheckout, GitReset, GitRestore, GitStash, GitPull, GitPush)
	add(MutationOwnerShell, ShellReset, ShellRun)
	add(MutationOwnerProcess, ProcessStart, ProcessStop, ProcessClearFinished)
	add(MutationOwnerJSRuntime, NodeREPL)
	add(MutationOwnerInstructionAuthoring, InstructionRuleCreate, InstructionSkillCreate)
	add(MutationOwnerPlanAuthoring, PlanCreate)
	add(MutationOwnerMemory, MemoryRemember, MemoryForget, MemoryOptimize)
	add(MutationOwnerPatch, PatchApply)
	add(MutationOwnerHistory, HistoryRewind)
	add(MutationOwnerSemanticIntegration, IntegrationPonytailTurn, IntegrationCavemanTurn, IntegrationFanoutTurn)
	add(MutationOwnerAgentConfig, AgentConfigSet)
	return owners
}
