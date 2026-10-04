package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/instructioncontext"
)

const maxCLIInputPromptBytes = 512 << 10

func promptServiceForCommand(cmd *cobra.Command) *application.PromptService {
	return &application.PromptService{Workspaces: workspaceManagerForCommand(cmd), AllowGlobalMutation: func(context.Context) bool { return true }}
}

func promptCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "prompt", Short: "Manage global and workspace Prompts"}
	cmd.AddCommand(promptListCommand(), promptGetCommand(), promptWriteCommand("create"), promptWriteCommand("update"), promptDeleteCommand())
	return cmd
}

func promptScopeForWorkspace(workspaceID string) instructioncontext.PromptScope {
	if workspaceID != "" {
		return instructioncontext.PromptScopeWorkspace
	}
	return instructioncontext.PromptScopeGlobal
}

func promptListCommand() *cobra.Command {
	var workspaceID string
	var asJSON bool
	cmd := &cobra.Command{Use: "list", Short: "List resolved Prompts", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		values, err := promptServiceForCommand(cmd).List(workspaceID)
		if err != nil {
			return err
		}
		if asJSON {
			return writeResultJSON(cmd, values)
		}
		p := commandPresenter(cmd)
		p.Section("Prompts")
		for _, value := range values {
			p.Fields(presentation.Field{Label: value.Definition.Name, Value: string(value.Scope) + " · " + value.Definition.Description})
		}
		return nil
	}}
	cmd.Flags().StringVar(&workspaceID, "workspace", "", "show the authorized workspace view over global Prompts")
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func promptGetCommand() *cobra.Command {
	var workspaceID string
	var asJSON bool
	var rawArguments []string
	cmd := &cobra.Command{Use: "get <name>", Short: "Read a Prompt and optionally render arguments", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		service := promptServiceForCommand(cmd)
		var result any
		if len(rawArguments) == 0 {
			value, err := service.Definition(workspaceID, args[0])
			if err != nil {
				return err
			}
			result = value
		} else {
			arguments := map[string]string{}
			for _, raw := range rawArguments {
				name, value, ok := strings.Cut(raw, "=")
				if !ok || name == "" {
					return errors.New("--arg requires name=value")
				}
				arguments[name] = value
			}
			value, messages, err := service.Get(workspaceID, args[0], arguments)
			if err != nil {
				return err
			}
			result = map[string]any{"scope": value.Scope, "definition": value.Definition, "messages": messages}
		}
		if asJSON {
			return writeResultJSON(cmd, result)
		}
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		p := commandPresenter(cmd)
		p.Section("Prompt")
		p.Fields(presentation.Field{Label: "definition", Value: string(data)})
		return nil
	}}
	cmd.Flags().StringVar(&workspaceID, "workspace", "", "select workspace Prompts over global Prompts")
	cmd.Flags().StringArrayVar(&rawArguments, "arg", nil, "render a named Prompt argument (name=value)")
	addJSONResultFlag(cmd, &asJSON)
	return cmd
}

func readPromptDefinition(cmd *cobra.Command, path string) (instructioncontext.PromptDefinition, error) {
	var reader io.Reader
	if path == "-" {
		reader = cmd.InOrStdin()
	} else {
		file, err := os.Open(path)
		if err != nil {
			return instructioncontext.PromptDefinition{}, err
		}
		defer file.Close()
		reader = file
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxCLIInputPromptBytes+1))
	if err != nil {
		return instructioncontext.PromptDefinition{}, err
	}
	if len(data) > maxCLIInputPromptBytes {
		return instructioncontext.PromptDefinition{}, fmt.Errorf("prompt input exceeds %d bytes", maxCLIInputPromptBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value instructioncontext.PromptDefinition
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return value, errors.New("prompt input must contain one JSON definition")
	}
	return value, nil
}

func promptWriteCommand(mode string) *cobra.Command {
	var workspaceID string
	use := mode + " <file.json|->"
	if mode == "update" {
		use = mode + " <name> <file.json|->"
	}
	cmd := &cobra.Command{Use: use, Short: strings.ToUpper(mode[:1]) + mode[1:] + " a Prompt definition", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		file := args[0]
		if mode == "update" {
			file = args[1]
		}
		definition, err := readPromptDefinition(cmd, file)
		if err != nil {
			return err
		}
		if mode == "update" && args[0] != definition.Name {
			return errors.New("prompt name cannot change during update")
		}
		result, err := promptServiceForCommand(cmd).Write(cmd.Context(), application.PromptWriteRequest{Scope: promptScopeForWorkspace(workspaceID), WorkspaceID: workspaceID, Mode: mode, Definition: definition})
		if err != nil {
			return err
		}
		renderEntityMutationSuccess(cmd, "Prompt "+mode+"d", result.Definition.Name, presentation.Field{Label: "scope", Value: result.Scope})
		return nil
	}}
	if mode == "update" {
		cmd.Args = cobra.ExactArgs(2)
	}
	cmd.Flags().StringVar(&workspaceID, "workspace", "", "write to workspace .cm/prompts instead of global Prompts")
	return cmd
}

func promptDeleteCommand() *cobra.Command {
	var workspaceID string
	cmd := &cobra.Command{Use: "delete <name>", Short: "Delete one scoped Prompt", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		scope := promptScopeForWorkspace(workspaceID)
		if err := promptServiceForCommand(cmd).Delete(cmd.Context(), application.PromptDeleteRequest{Scope: scope, WorkspaceID: workspaceID, Name: args[0]}); err != nil {
			return err
		}
		renderEntityMutationSuccess(cmd, "Prompt deleted", args[0], presentation.Field{Label: "scope", Value: scope})
		return nil
	}}
	cmd.Flags().StringVar(&workspaceID, "workspace", "", "delete workspace definition instead of global definition")
	return cmd
}
