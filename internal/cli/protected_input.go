package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"go.mewis.me/codemcp/internal/cli/presentation"
)

const maxProtectedInputBytes = 16 * 1024

var errProtectedInputCancelled = errors.New("protected input cancelled")

var (
	protectedInputIsTerminal = term.IsTerminal
	protectedInputMakeRaw    = term.MakeRaw
	protectedInputRestore    = term.Restore
)

type protectedInputOptions struct {
	Label       string
	Explicit    string
	ExplicitSet bool
	FromEnv     string
	FallbackEnv string
	MaxBytes    int
}

func readProtectedInput(cmd *cobra.Command, options protectedInputOptions) (string, error) {
	if cmd == nil {
		return "", errors.New("protected input command is unavailable")
	}
	limit := options.MaxBytes
	if limit <= 0 {
		limit = maxProtectedInputBytes
	}
	if options.ExplicitSet && strings.TrimSpace(options.FromEnv) != "" {
		return "", errors.New("provide the secret either as an argument or with --from-env, not both")
	}
	if options.ExplicitSet {
		return validateProtectedInput(options.Explicit, limit)
	}
	if name := strings.TrimSpace(options.FromEnv); name != "" {
		value, ok := os.LookupEnv(name)
		if !ok {
			return "", fmt.Errorf("environment variable %q is not set", name)
		}
		return validateProtectedInput(value, limit)
	}
	if name := strings.TrimSpace(options.FallbackEnv); name != "" {
		if value, ok := os.LookupEnv(name); ok {
			return validateProtectedInput(value, limit)
		}
	}

	input := cmd.InOrStdin()
	if file, ok := input.(*os.File); ok && protectedInputIsTerminal(int(file.Fd())) {
		var value string
		err := commandProgressSession(cmd).WithInput(nil, func() error {
			var readErr error
			value, readErr = readProtectedTerminal(file, commandPresenter(cmd), options.Label, limit)
			return readErr
		})
		return value, err
	}
	return readProtectedStream(input, limit)
}

func readProtectedTerminal(file *os.File, presenter *presentation.Presenter, label string, limit int) (value string, resultErr error) {
	if file == nil {
		return "", errors.New("protected terminal input is unavailable")
	}
	state, err := protectedInputMakeRaw(int(file.Fd()))
	if err != nil {
		return "", errors.New("prepare protected terminal input")
	}
	defer func() {
		if restoreErr := protectedInputRestore(int(file.Fd()), state); restoreErr != nil && resultErr == nil {
			value = ""
			resultErr = errors.New("restore terminal after protected input")
		}
	}()

	reader := bufio.NewReader(file)
	runes := make([]rune, 0, 64)
	defer func() {
		for index := range runes {
			runes[index] = 0
		}
	}()
	escapeState := 0
	if presenter != nil {
		presenter.ProtectedInput(label, 0, false)
		defer func() { presenter.ProtectedInput(label, len(runes), true) }()
	}
	for {
		r, _, readErr := reader.ReadRune()
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return "", errors.New("protected input ended before submission")
			}
			return "", errors.New("read protected terminal input")
		}
		if escapeState != 0 {
			switch escapeState {
			case 1:
				if r == '[' {
					escapeState = 2
				} else {
					escapeState = 0
				}
			case 2:
				if r == '3' {
					escapeState = 3
				} else {
					escapeState = 0
				}
			case 3:
				escapeState = 0
				if r == '~' && len(runes) > 0 {
					runes = runes[:len(runes)-1]
				}
			}
			if presenter != nil {
				presenter.ProtectedInput(label, len(runes), false)
			}
			continue
		}
		switch r {
		case 3: // Ctrl-C in raw mode.
			return "", errProtectedInputCancelled
		case 4: // Ctrl-D / EOF in raw mode.
			return "", errors.New("protected input ended before submission")
		case '\r', '\n':
			return validateProtectedInput(string(runes), limit)
		case '\b', 0x7f:
			if len(runes) > 0 {
				runes = runes[:len(runes)-1]
			}
		case 0x1b:
			escapeState = 1
		default:
			if unicode.IsControl(r) {
				continue
			}
			candidate := append(runes, r)
			if len([]byte(string(candidate))) > limit {
				return "", errors.New("protected input exceeds size limit")
			}
			runes = candidate
		}
		if presenter != nil {
			presenter.ProtectedInput(label, len(runes), false)
		}
	}
}

func readProtectedStream(input io.Reader, limit int) (string, error) {
	if input == nil {
		return "", errors.New("protected input is unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(input, int64(limit)+1))
	if err != nil {
		return "", errors.New("read protected input")
	}
	if len(data) > limit {
		for index := range data {
			data[index] = 0
		}
		return "", errors.New("protected input exceeds size limit")
	}
	value := strings.TrimSpace(string(data))
	for index := range data {
		data[index] = 0
	}
	return validateProtectedInput(value, limit)
}

func validateProtectedInput(raw string, limit int) (string, error) {
	if len(raw) > limit {
		return "", errors.New("protected input exceeds size limit")
	}
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("secret value is required")
	}
	return value, nil
}

func zeroProtectedString(value *string) {
	if value != nil {
		*value = ""
	}
}

const protectedArgumentWarning = "Passing a secret as a command argument is supported for compatibility, but shells and process listings may retain it. Omit the secret argument for protected interactive input, or use --from-env for automation."
