package cli

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

var completionShells = []string{"bash", "zsh", "fish", "powershell"}

func completionCommand() *cobra.Command {
	var noDescriptions, goRun bool
	cmd := &cobra.Command{
		Use:               "completion <bash|zsh|fish|powershell>",
		Short:             "Generate shell completion for cm",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeStatic(completionShells...),
		RunE: func(cmd *cobra.Command, args []string) error {
			shell := strings.ToLower(strings.TrimSpace(args[0]))
			descriptions := !noDescriptions
			span := tracepkg.Start(cmd.Context(), "COMPLETION", "completion.generate", "Generating shell completion output", tracepkg.String("shell", shell), tracepkg.Bool("descriptions_enabled", descriptions), tracepkg.Bool("go_run_requested", goRun))
			if !hasString(completionShells, shell) {
				err := fmt.Errorf("unsupported shell %q", shell)
				span.FailMessage("Shell completion generation failed", err)
				return err
			}
			if goRun && shell != "bash" && shell != "zsh" {
				err := fmt.Errorf("--go-run is supported for bash and zsh")
				span.FailMessage("Shell completion generation failed", err, tracepkg.Bool("go_run_supported", false))
				return err
			}
			generationSpan := tracepkg.Start(cmd.Context(), "COMPLETION", "completion.script.generate", "Generating base shell completion script", tracepkg.String("shell", shell), tracepkg.Bool("descriptions_enabled", descriptions))
			script, err := generateCompletion(cmd.Root(), shell, descriptions)
			if err != nil {
				generationSpan.FailMessage("Base shell completion generation failed", err)
				span.FailMessage("Shell completion generation failed", err)
				return err
			}
			generationSpan.EndMessage("Base shell completion script generated", tracepkg.Int("output_bytes", len(script)))
			goRunBytes := 0
			if goRun {
				extension := goRunCompletion(shell, cmd.Root().Name())
				goRunBytes = len(extension)
				script += extension
			}
			tracepkg.Emit(cmd.Context(), "COMPLETION", "completion.go-run-extension", "Resolved go-run completion extension decision", tracepkg.String("shell", shell), tracepkg.Bool("requested", goRun), tracepkg.Bool("supported", shell == "bash" || shell == "zsh"), tracepkg.Bool("added", goRunBytes > 0), tracepkg.Int("extension_bytes", goRunBytes))
			written, err := io.WriteString(cmd.OutOrStdout(), script)
			if err != nil {
				span.FailMessage("Shell completion output write failed", err, tracepkg.Int("output_bytes", written))
				return err
			}
			span.EndMessage("Shell completion output generated", tracepkg.String("shell", shell), tracepkg.Bool("descriptions_enabled", descriptions), tracepkg.Bool("go_run_added", goRunBytes > 0), tracepkg.Int("output_bytes", written))
			return nil
		},
	}
	cmd.Flags().BoolVar(&noDescriptions, "no-descriptions", false, "disable completion descriptions")
	cmd.Flags().BoolVar(&goRun, "go-run", false, "also complete direct 'go run .' invocations (bash and zsh)")
	markMachineOutput(cmd)
	return cmd
}

func generateCompletion(root *cobra.Command, shell string, descriptions bool) (string, error) {
	var output bytes.Buffer
	var err error
	switch shell {
	case "bash":
		err = root.GenBashCompletionV2(&output, descriptions)
	case "zsh":
		if descriptions {
			err = root.GenZshCompletion(&output)
		} else {
			err = root.GenZshCompletionNoDesc(&output)
		}
	case "fish":
		err = root.GenFishCompletion(&output, descriptions)
	case "powershell":
		if descriptions {
			err = root.GenPowerShellCompletionWithDesc(&output)
		} else {
			err = root.GenPowerShellCompletion(&output)
		}
	default:
		return "", fmt.Errorf("unsupported shell %q", shell)
	}
	return output.String(), err
}

func goRunCompletion(shell, root string) string {
	switch shell {
	case "bash":
		return fmt.Sprintf(`
# Optional source-tree completion for direct "go run ." invocations.
__cm_previous_go_completion="$(complete -p go 2>/dev/null | sed -n 's/.* -F \([^ ]*\) .*/\1/p')"
__cm_go_run_completion() {
    if [[ "${COMP_WORDS[1]}" != "run" || "${COMP_WORDS[2]}" != "." ]]; then
        if [[ -n "${__cm_previous_go_completion}" ]] && declare -F "${__cm_previous_go_completion}" >/dev/null; then
            "${__cm_previous_go_completion}" "$@"
        fi
        return
    fi
    local -a saved_words=("${COMP_WORDS[@]}")
    local saved_cword=${COMP_CWORD}
    COMP_WORDS=("go run ." "${COMP_WORDS[@]:3}")
    COMP_CWORD=$((saved_cword - 2))
    __start_%s
    local status=$?
    COMP_WORDS=("${saved_words[@]}")
    COMP_CWORD=${saved_cword}
    return ${status}
}
complete -o default -o nospace -F __cm_go_run_completion go
`, root)
	case "zsh":
		return fmt.Sprintf(`
# Optional source-tree completion for direct "go run ." invocations.
typeset -g __cm_previous_go_completion="${_comps[go]}"
__cm_go_run_completion() {
    if [[ "${words[2]}" != "run" || "${words[3]}" != "." ]]; then
        if [[ -n "${__cm_previous_go_completion}" && "${__cm_previous_go_completion}" != "__cm_go_run_completion" ]]; then
            "${__cm_previous_go_completion}"
            return $?
        fi
        return 1
    fi
    local -a saved_words=("${words[@]}")
    local saved_current=${CURRENT}
    words=("go run ." "${words[4,-1]}")
    CURRENT=$((saved_current - 2))
    _%s
    local status=$?
    words=("${saved_words[@]}")
    CURRENT=${saved_current}
    return ${status}
}
compdef __cm_go_run_completion go
`, root)
	default:
		return ""
	}
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
