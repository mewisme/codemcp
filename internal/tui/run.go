package tui

import (
	"context"
	"errors"
	"io"
	"os"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	"go.mewis.me/codemcp/internal/config"
)

func TerminalIO(in io.Reader, out io.Writer) bool {
	input, inputOK := in.(*os.File)
	output, outputOK := out.(*os.File)
	return inputOK && outputOK && term.IsTerminal(int(input.Fd())) && term.IsTerminal(int(output.Fd()))
}

func Run(ctx context.Context, route Route, in io.Reader, out io.Writer) error {
	if !TerminalIO(in, out) {
		return errors.New("cgm tui requires terminal stdin and stdout")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := tea.NewProgram(NewModelWithState(ctx, route, config.RootPath()), tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out)).Run()
	return err
}
