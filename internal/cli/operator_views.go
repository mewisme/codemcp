package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/projectcontext"
)

func toolsCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "tools", Short: "Inspect canonical MCP tools"}
	var asJSON bool
	list := &cobra.Command{Use: "list", Short: "List canonical MCP tools", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		result, err := application.NewToolInventoryService().List(cmd.Context())
		if err != nil {
			return err
		}
		if asJSON {
			return writeResultJSON(cmd, result.Value)
		}
		p := commandPresenter(cmd)
		p.Section(fmt.Sprintf("Tools · %d", len(result.Value)))
		for index, value := range result.Value {
			p.SubsectionItem(value.Name, index == len(result.Value)-1)
			p.NestedFields(presentation.Field{Label: "description", Value: value.Description})
		}
		return nil
	}}
	addJSONResultFlag(list, &asJSON)
	cmd.AddCommand(list)
	return cmd
}

func executionCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "execution", Short: "Inspect workspace execution history"}
	service := application.NewRuntimeInspectionService()
	var listJSON bool
	list := &cobra.Command{Use: "list <workspace_id>", Short: "List workspace executions", Args: cobra.ExactArgs(1), ValidArgsFunction: completeWorkspaceID, RunE: func(cmd *cobra.Command, args []string) error {
		result, err := service.ListExecutions(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		if listJSON {
			return writeResultJSON(cmd, result.Value)
		}
		p := commandPresenter(cmd)
		p.Section(fmt.Sprintf("Executions · %d", len(result.Value)))
		for index, value := range result.Value {
			p.SubsectionItem(value.ID, index == len(result.Value)-1)
			p.NestedFields(presentation.Field{Label: "status", Value: value.Status}, presentation.Field{Label: "tool", Value: value.Tool}, presentation.Field{Label: "command", Value: value.Command})
		}
		return nil
	}}
	addJSONResultFlag(list, &listJSON)
	var viewJSON bool
	view := &cobra.Command{Use: "view <workspace_id> <execution_id>", Short: "Show one workspace execution", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		result, err := service.ViewExecution(cmd.Context(), args[0], args[1])
		if err != nil {
			return err
		}
		if viewJSON {
			return writeResultJSON(cmd, result.Value)
		}
		v := result.Value
		p := commandPresenter(cmd)
		p.Fields(presentation.Field{Label: "id", Value: v.Execution.ID}, presentation.Field{Label: "status", Value: v.Execution.Status}, presentation.Field{Label: "tool", Value: v.Execution.Tool}, presentation.Field{Label: "workspace", Value: v.Execution.WorkspaceID}, presentation.Field{Label: "source", Value: v.Execution.Source})
		renderCLIObservabilitySection(p, "Request", map[string]any{"command": v.Execution.Command, "requested_command": v.Execution.RequestedCommand, "effective_command": v.Execution.EffectiveCommand, "cwd": v.Execution.CWD})
		renderCLIObservabilitySection(p, "Response", map[string]any{"stdout": v.Stdout, "stderr": v.Stderr, "exit_code": v.Execution.ExitCode, "timed_out": v.Execution.TimedOut, "finished_at": v.Execution.FinishedAt})
		return nil
	}}
	addJSONResultFlag(view, &viewJSON)
	cmd.AddCommand(list, view, executionFeedCommand(), executionStreamCommand())
	return cmd
}

func processCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "process", Short: "Inspect managed workspace processes"}
	service := application.NewRuntimeInspectionService()
	var listJSON bool
	list := &cobra.Command{Use: "list <workspace_id>", Short: "List workspace processes", Args: cobra.ExactArgs(1), ValidArgsFunction: completeWorkspaceID, RunE: func(cmd *cobra.Command, args []string) error {
		result, err := service.ListProcesses(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		if listJSON {
			return writeResultJSON(cmd, result.Value)
		}
		p := commandPresenter(cmd)
		p.Section(fmt.Sprintf("Processes · %d", len(result.Value)))
		for index, value := range result.Value {
			p.SubsectionItem(value.ID, index == len(result.Value)-1)
			p.NestedFields(presentation.Field{Label: "pid", Value: value.PID}, presentation.Field{Label: "running", Value: value.Running}, presentation.Field{Label: "command", Value: value.Command})
		}
		return nil
	}}
	addJSONResultFlag(list, &listJSON)
	var viewJSON bool
	view := &cobra.Command{Use: "view <workspace_id> <process_id>", Short: "Show one workspace process", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		result, err := service.ViewProcess(cmd.Context(), args[0], args[1])
		if err != nil {
			return err
		}
		if viewJSON {
			return writeResultJSON(cmd, result.Value)
		}
		v := result.Value
		p := commandPresenter(cmd)
		p.Fields(presentation.Field{Label: "id", Value: v.ID}, presentation.Field{Label: "pid", Value: v.PID}, presentation.Field{Label: "running", Value: v.Running}, presentation.Field{Label: "command", Value: v.Command}, presentation.Field{Label: "cwd", Value: v.CWD})
		return nil
	}}
	addJSONResultFlag(view, &viewJSON)
	cmd.AddCommand(list, view, processClearCommand())
	return cmd
}

func workspaceContextCommand() *cobra.Command {
	defaults := projectcontext.DefaultOptions()
	options := defaults
	var asJSON bool
	cmd := &cobra.Command{Use: "context <workspace_id>", Short: "Build canonical Project Context for a workspace", Args: cobra.ExactArgs(1), ValidArgsFunction: completeWorkspaceID, RunE: func(cmd *cobra.Command, args []string) error {
		service := application.NewApplicationProjectContextService(workspaceManagerForCommand(cmd))
		result, err := service.Read(cmd.Context(), application.ProjectContextInput{WorkspaceID: args[0], Options: options})
		if err != nil {
			return err
		}
		if asJSON {
			return writeResultJSON(cmd, result.Value)
		}
		if commandResultModeFor(cmd) == resultModeHuman {
			p := commandPresenter(cmd)
			p.Note("", result.Value.InstructionContext.InstructionsText)
			return nil
		}
		return writePlainResultLine(cmd, result.Value.InstructionContext.InstructionsText)
	}}
	cmd.Flags().StringVar(&options.Path, "path", "", "project-relative or absolute path inside the workspace")
	cmd.Flags().StringVar(&options.MemoryQuery, "memory-query", "", "memory query used for context retrieval")
	cmd.Flags().BoolVar(&options.IncludeGit, "git", defaults.IncludeGit, "include Git context")
	cmd.Flags().BoolVar(&options.IncludeMemory, "memory", defaults.IncludeMemory, "include workspace memory")
	cmd.Flags().BoolVar(&options.IncludeSkills, "skills", defaults.IncludeSkills, "include skill summaries")
	cmd.Flags().IntVar(&options.MaxMemoryEntries, "max-memory-entries", defaults.MaxMemoryEntries, "maximum memory entries")
	cmd.Flags().IntVar(&options.MaxMemoryBytes, "max-memory-bytes", defaults.MaxMemoryBytes, "maximum memory bytes")
	cmd.Flags().IntVar(&options.MaxInstructionBytes, "max-instruction-bytes", defaults.MaxInstructionBytes, "maximum instruction bytes")
	cmd.Flags().IntVar(&options.MaxSectionBytes, "max-section-bytes", defaults.MaxSectionBytes, "maximum bytes per instruction section")
	cmd.Flags().IntVar(&options.MaxLinesPerSection, "max-lines-per-section", defaults.MaxLinesPerSection, "maximum lines per instruction section")
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}
