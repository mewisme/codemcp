package tui

import (
	"context"
	"errors"
	"io"
	"os"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/config"
)

func TerminalIO(in io.Reader, out io.Writer) bool {
	input, inputOK := in.(*os.File)
	output, outputOK := out.(*os.File)
	return inputOK && outputOK && term.IsTerminal(int(input.Fd())) && term.IsTerminal(int(output.Fd()))
}

func Run(ctx context.Context, route Route, in io.Reader, out io.Writer) error {
	if !TerminalIO(in, out) {
		return errors.New("cm tui requires terminal stdin and stdout")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = application.WithOperationInterface(ctx, application.OperationInterfaceTUI)
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	_, err := tea.NewProgram(NewModelWithState(sessionCtx, route, config.RootPath()), tea.WithContext(sessionCtx), tea.WithInput(in), tea.WithOutput(out)).Run()
	return err
}
