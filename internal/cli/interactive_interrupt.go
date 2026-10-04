package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type foregroundInterrupt struct {
	Context     context.Context
	cancel      context.CancelFunc
	signalCh    chan os.Signal
	restore     func()
	reasonMu    sync.RWMutex
	reason      string
	cleanupOnce sync.Once
}

func newForegroundInterrupt(cmd *cobra.Command, enableKeys bool) *foregroundInterrupt {
	parent := context.Background()
	if cmd != nil && cmd.Context() != nil {
		parent = cmd.Context()
	}
	ctx, cancel := context.WithCancel(parent)
	value := &foregroundInterrupt{Context: ctx, cancel: cancel, signalCh: make(chan os.Signal, 1), restore: func() {}}
	signal.Notify(value.signalCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case sig := <-value.signalCh:
			value.stop(sig.String())
		case <-ctx.Done():
		}
	}()
	if enableKeys {
		value.enableTerminalKeys(cmd)
	}
	return value
}

func (value *foregroundInterrupt) enableTerminalKeys(cmd *cobra.Command) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return
	}
	state, err := term.MakeRaw(fd)
	if err != nil {
		return
	}
	value.restore = func() { _ = term.Restore(fd, state) }
	if cmd != nil {
		out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
		cmd.SetOut(newTerminalRawWriter(out))
		cmd.SetErr(newTerminalRawWriter(errOut))
		previousRestore := value.restore
		value.restore = func() {
			previousRestore()
			cmd.SetOut(out)
			cmd.SetErr(errOut)
		}
		format, _ := commandLogFormat(cmd)
		if format != "json" {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Press q or Ctrl+C to stop")
		}
	}
	go func() {
		buffer := []byte{0}
		for {
			if _, err := os.Stdin.Read(buffer); err != nil {
				return
			}
			if reason, ok := interactiveInterruptKey(buffer[0]); ok {
				value.stop(reason)
				return
			}
		}
	}()
}

type terminalRawWriter struct {
	mu     sync.Mutex
	out    io.Writer
	lastCR bool
}

func newTerminalRawWriter(out io.Writer) io.Writer {
	if out == nil {
		return io.Discard
	}
	return &terminalRawWriter{out: out}
}

func (writer *terminalRawWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	converted := make([]byte, 0, len(data)+8)
	for _, value := range data {
		if value == '\n' && !writer.lastCR {
			converted = append(converted, '\r')
		}
		converted = append(converted, value)
		writer.lastCR = value == '\r'
		if value != '\r' && value != '\n' {
			writer.lastCR = false
		}
	}
	_, err := writer.out.Write(converted)
	if err != nil {
		return 0, err
	}
	return len(data), nil
}

func interactiveInterruptKey(value byte) (string, bool) {
	switch value {
	case 'q', 'Q':
		return "q", true
	case 3:
		return "Ctrl+C", true
	default:
		return "", false
	}
}

func (value *foregroundInterrupt) stop(reason string) {
	if value == nil {
		return
	}
	value.reasonMu.Lock()
	if value.reason == "" {
		value.reason = strings.TrimSpace(reason)
	}
	value.reasonMu.Unlock()
	value.cancel()
}

func (value *foregroundInterrupt) Reason() string {
	if value == nil {
		return ""
	}
	value.reasonMu.RLock()
	defer value.reasonMu.RUnlock()
	return value.reason
}

func (value *foregroundInterrupt) Close() {
	if value == nil {
		return
	}
	value.cleanupOnce.Do(func() {
		signal.Stop(value.signalCh)
		value.cancel()
		value.restore()
	})
}
