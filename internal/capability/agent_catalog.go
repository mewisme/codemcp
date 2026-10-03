package capability

const (
	AgentComplete                   ID = "agent.complete"
	AgentClaim                      ID = "agent.claim"
	AgentStatusRead                 ID = "agent.status.read"
	FileReadText                    ID = "file.read.text"
	FileReadBinary                  ID = "file.read.binary"
	FileReadBatch                   ID = "file.read.batch"
	FileWriteText                   ID = "file.write.text"
	FileWriteBinary                 ID = "file.write.binary"
	FileEdit                        ID = "file.edit"
	FileMultiEdit                   ID = "file.edit.batch"
	FileReplaceRegex                ID = "file.replace.regex"
	FileCopy                        ID = "file.copy"
	FileMove                        ID = "file.move"
	FileDelete                      ID = "file.delete"
	DirectoryList                   ID = "directory.list"
	DirectoryTree                   ID = "directory.tree"
	DirectoryCreate                 ID = "directory.create"
	DirectoryDelete                 ID = "directory.delete"
	SearchGlob                      ID = "search.glob"
	SearchGrep                      ID = "search.grep"
	WorkspaceAllowedDirectoriesList ID = "workspace.allowed-directories.list"
	WorkspaceStatusRead             ID = "workspace.status.read"
	WorkspaceContainerContext       ID = "workspace.container.context"
	GitStatusRead                   ID = "git.status.read"
	GitDiffRead                     ID = "git.diff.read"
	GitLogRead                      ID = "git.log.read"
	GitBranch                       ID = "git.branch"
	GitAdd                          ID = "git.add"
	GitCommit                       ID = "git.commit"
	GitCheckout                     ID = "git.checkout"
	GitReset                        ID = "git.reset"
	GitRestore                      ID = "git.restore"
	GitStash                        ID = "git.stash"
	GitPull                         ID = "git.pull"
	GitPush                         ID = "git.push"
	ShellStatusRead                 ID = "shell.status.read"
	ShellReset                      ID = "shell.reset"
	ShellRun                        ID = "shell.run"
	ProcessStart                    ID = "process.start"
	ProcessStatus                   ID = "process.status"
	ProcessOutput                   ID = "process.output"
	ProcessStop                     ID = "process.stop"
	ProcessClearFinished            ID = "process.clear.finished"
	NodeREPL                        ID = "node.repl"
	SkillList                       ID = "skill.list"
	SkillLoad                       ID = "skill.load"
	InstructionRuleCreate           ID = "instruction.rule.create"
	InstructionSkillCreate          ID = "instruction.skill.create"
	PlanCreate                      ID = "plan.create"
	AgentPromptList                 ID = "agent.prompt.list"
	AgentPromptGet                  ID = "agent.prompt.get"
	AgentPromptCreate               ID = "agent.prompt.create"
	AgentPromptUpdate               ID = "agent.prompt.update"
	AgentPromptDelete               ID = "agent.prompt.delete"
	RulesLoadPath                   ID = "rules.path.load"
	MemoryRemember                  ID = "memory.remember"
	MemoryGet                       ID = "memory.get"
	MemorySearch                    ID = "memory.search"
	MemoryForget                    ID = "memory.forget"
	MemoryOptimize                  ID = "memory.optimize"
	PatchApply                      ID = "patch.apply"
	HistoryRewind                   ID = "history.rewind"
	UpstreamCall                    ID = "upstream.call"
	RequestControlApproval          ID = "request.control-approval"
	IntegrationPonytailTurn         ID = "integration.ponytail.turn"
	IntegrationCavemanTurn          ID = "integration.caveman.turn"
	IntegrationCodeGraphExplore     ID = "integration.codegraph.explore"
	AgentConfigList                 ID = "agent.config.list"
	AgentConfigGet                  ID = "agent.config.get"
	AgentConfigSet                  ID = "agent.config.set"
)

var mcpToolBindings = map[ID][]string{
	AgentComplete:                   {"agent_complete"},
	AgentClaim:                      {"agent_claim"},
	VersionAbout:                    {"get_version"},
	WorkspaceRegister:               {"workspace_register"},
	WorkspaceList:                   {"workspace_list"},
	WorkspaceContainerList:          {"workspace_container_list"},
	WorkspaceContainerShow:          {"workspace_container_status"},
	ProjectContextRead:              {"project_context"},
	UpstreamServerList:              {"upstream_servers"},
	UpstreamServerTools:             {"upstream_tools"},
	AgentStatusRead:                 {"agent_status"},
	FileReadText:                    {"read_text_file"},
	FileReadBinary:                  {"read_file_base64"},
	FileReadBatch:                   {"read_files"},
	FileWriteText:                   {"write_file"},
	FileWriteBinary:                 {"write_file_base64"},
	FileEdit:                        {"edit_file"},
	FileMultiEdit:                   {"multi_edit"},
	FileReplaceRegex:                {"replace_regex"},
	FileCopy:                        {"copy_file"},
	FileMove:                        {"move_file"},
	FileDelete:                      {"delete_file"},
	DirectoryList:                   {"list_directory"},
	DirectoryTree:                   {"directory_tree"},
	DirectoryCreate:                 {"create_directory"},
	DirectoryDelete:                 {"delete_directory"},
	SearchGlob:                      {"glob"},
	SearchGrep:                      {"grep"},
	WorkspaceAllowedDirectoriesList: {"list_allowed_directories"},
	WorkspaceStatusRead:             {"workspace_status"},
	WorkspaceContainerContext:       {"workspace_container_context"},
	GitStatusRead:                   {"git_status"},
	GitDiffRead:                     {"git_diff"},
	GitLogRead:                      {"git_log"},
	GitBranch:                       {"git_branch"},
	GitAdd:                          {"git_add"},
	GitCommit:                       {"git_commit"},
	GitCheckout:                     {"git_checkout"},
	GitReset:                        {"git_reset"},
	GitRestore:                      {"git_restore"},
	GitStash:                        {"git_stash"},
	GitPull:                         {"git_pull"},
	GitPush:                         {"git_push"},
	ShellStatusRead:                 {"shell_status"},
	ShellReset:                      {"shell_reset"},
	ShellRun:                        {"run_command"},
	ProcessStart:                    {"start_process"},
	ProcessStatus:                   {"process_status"},
	ProcessOutput:                   {"process_output"},
	ProcessStop:                     {"stop_process"},
	ProcessClearFinished:            {"clear_processes"},
	NodeREPL:                        {"node_repl"},
	SkillList:                       {"list_skills"},
	SkillLoad:                       {"load_skill"},
	InstructionRuleCreate:           {"create_rule"},
	InstructionSkillCreate:          {"create_skill"},
	PlanCreate:                      {"create_plan"},
	AgentPromptList:                 {"list_prompts"},
	AgentPromptGet:                  {"get_prompt"},
	AgentPromptCreate:               {"create_prompt"},
	AgentPromptUpdate:               {"update_prompt"},
	AgentPromptDelete:               {"delete_prompt"},
	RulesLoadPath:                   {"load_path_rules"},
	MemoryRemember:                  {"remember"},
	MemoryGet:                       {"memory_get"},
	MemorySearch:                    {"memory_search"},
	MemoryForget:                    {"forget"},
	MemoryOptimize:                  {"optimize_memory"},
	PatchApply:                      {"apply_patch"},
	HistoryRewind:                   {"rewind"},
	UpstreamCall:                    {"upstream_call"},
	RequestControlApproval:          {"request_control_approval"},
	IntegrationPonytailTurn:         {"ponytail_turn"},
	IntegrationCavemanTurn:          {"caveman_turn"},
	IntegrationCodeGraphExplore:     {"codegraph_explore"},
	AgentConfigList:                 {"config_list"},
	AgentConfigGet:                  {"config_get"},
	AgentConfigSet:                  {"config_set"},
}

var plannedMCPToolBindings = map[ID][]string{}

func agentOnlySpecs() []Spec {
	return []Spec{
		agentMutationSpec(AgentComplete, RiskState, false),
		agentMutationSpec(AgentClaim, RiskSensitive, false),
		agentQuerySpec(AgentStatusRead, false),
		agentQuerySpec(FileReadText, false),
		agentQuerySpec(FileReadBinary, false),
		agentQuerySpec(FileReadBatch, false),
		agentMutationSpec(FileWriteText, RiskState, false),
		agentMutationSpec(FileWriteBinary, RiskState, false),
		agentMutationSpec(FileEdit, RiskState, false),
		agentMutationSpec(FileMultiEdit, RiskState, false),
		agentMutationSpec(FileReplaceRegex, RiskState, false),
		agentMutationSpec(FileCopy, RiskState, false),
		agentMutationSpec(FileMove, RiskState, false),
		agentMutationSpec(FileDelete, RiskDestructive, false),
		agentQuerySpec(DirectoryList, false),
		agentQuerySpec(DirectoryTree, false),
		agentMutationSpec(DirectoryCreate, RiskState, false),
		agentMutationSpec(DirectoryDelete, RiskDestructive, false),
		agentQuerySpec(SearchGlob, false),
		agentQuerySpec(SearchGrep, false),
		agentQuerySpec(WorkspaceAllowedDirectoriesList, false),
		agentQuerySpec(WorkspaceStatusRead, false),
		agentQuerySpec(WorkspaceContainerContext, false),
		agentQuerySpec(GitStatusRead, false),
		agentQuerySpec(GitDiffRead, false),
		agentQuerySpec(GitLogRead, false),
		agentQuerySpec(GitBranch, false),
		agentMutationSpec(GitAdd, RiskState, false),
		agentMutationSpec(GitCommit, RiskState, false),
		agentMutationSpec(GitCheckout, RiskDestructive, false),
		agentMutationSpec(GitReset, RiskDestructive, false),
		agentMutationSpec(GitRestore, RiskDestructive, false),
		agentMutationSpec(GitStash, RiskState, false),
		agentMutationSpec(GitPull, RiskSensitive, true),
		agentMutationSpec(GitPush, RiskDestructive, true),
		agentQuerySpec(ShellStatusRead, false),
		agentMutationSpec(ShellReset, RiskState, false),
		agentMutationSpec(ShellRun, RiskSensitive, true),
		agentMutationSpec(ProcessStart, RiskSensitive, true),
		agentQuerySpec(ProcessStatus, false),
		agentQuerySpec(ProcessOutput, false),
		agentMutationSpec(ProcessStop, RiskState, false),
		agentMutationSpec(ProcessClearFinished, RiskDestructive, false),
		agentMutationSpec(NodeREPL, RiskSensitive, true),
		agentQuerySpec(SkillList, false),
		agentQuerySpec(SkillLoad, false),
		agentMutationSpec(InstructionRuleCreate, RiskState, false),
		agentMutationSpec(InstructionSkillCreate, RiskState, false),
		agentMutationSpec(PlanCreate, RiskState, false),
		agentQuerySpec(AgentPromptList, false),
		agentQuerySpec(AgentPromptGet, false),
		agentMutationSpec(AgentPromptCreate, RiskState, false),
		agentMutationSpec(AgentPromptUpdate, RiskState, false),
		agentMutationSpec(AgentPromptDelete, RiskDestructive, false),
		agentQuerySpec(RulesLoadPath, false),
		agentMutationSpec(MemoryRemember, RiskState, false),
		agentQuerySpec(MemoryGet, false),
		agentQuerySpec(MemorySearch, false),
		agentMutationSpec(MemoryForget, RiskDestructive, false),
		agentMutationSpec(MemoryOptimize, RiskState, false),
		agentMutationSpec(PatchApply, RiskState, false),
		agentMutationSpec(HistoryRewind, RiskDestructive, false),
		agentMutationSpec(UpstreamCall, RiskSensitive, true),
		agentApprovalSpec(RequestControlApproval),
		agentMutationSpec(IntegrationPonytailTurn, RiskState, false),
		agentMutationSpec(IntegrationCavemanTurn, RiskState, false),
		agentQuerySpec(IntegrationCodeGraphExplore, false),
		agentQuerySpec(AgentConfigList, false),
		agentQuerySpec(AgentConfigGet, false),
		agentConfigMutationSpec(AgentConfigSet),
	}
}

func agentQuerySpec(id ID, openWorld bool) Spec {
	return Spec{
		ID: id, Kind: KindQuery, Audience: AudienceAgent, Authorization: AuthorizationAgent, Risk: RiskNone,
		Confirmation: ConfirmationPolicy{Mode: ConfirmationNone},
		Effects:      SemanticEffects{Key: string(id), ReadOnly: true, Idempotent: true, OpenWorld: openWorld},
	}
}

func agentMutationSpec(id ID, risk MutationRisk, openWorld bool) Spec {
	return Spec{
		ID: id, Kind: KindMutation, Audience: AudienceAgent, Authorization: AuthorizationAgent, Risk: risk,
		Confirmation: ConfirmationPolicy{Mode: ConfirmationNone},
		Effects:      SemanticEffects{Key: string(id), Destructive: risk == RiskDestructive, OpenWorld: openWorld},
	}
}

func agentApprovalSpec(id ID) Spec {
	spec := agentMutationSpec(id, RiskSensitive, false)
	spec.Confirmation.Mode = ConfirmationReview
	return spec
}

func agentConfigMutationSpec(id ID) Spec {
	spec := agentMutationSpec(id, RiskSensitive, false)
	spec.Confirmation = ConfirmationPolicy{Mode: ConfirmationRequired, ControlApproval: true}
	return spec
}
