package cli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/logger"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

var (
	commandLoggers          sync.Map
	commandProgressSessions sync.Map
)

type commandProgress struct {
	cmd       *cobra.Command
	log       *logger.Logger
	component string
	name      string
	label     string
	done      string
	session   *presentation.ProgressSession
}

type traceProgressSpec struct {
	start string
	done  string
}

var traceProgress = map[string]traceProgressSpec{
	"config.persist":              {start: "Saving configuration", done: "Saved configuration"},
	"config.persist.rollback":     {start: "Rolling back configuration", done: "Rolled back configuration"},
	"config.runtime.reload":       {start: "Reloading running runtime", done: "Reloaded runtime configuration"},
	"install.source.validate":     {start: "Validating source binary", done: "Validated source binary"},
	"install.legacy.discover":     {start: "Checking legacy installations", done: "Checked legacy installations"},
	"install.stage":               {start: "Staging installation binary", done: "Staged installation binary"},
	"install.activate":            {start: "Activating installation", done: "Activated installation"},
	"install.rollback":            {start: "Rolling back installation", done: "Rolled back installation"},
	"install.legacy.backup":       {start: "Backing up legacy installation", done: "Backed up legacy installation"},
	"install.canonical.install":   {start: "Installing canonical command", done: "Installed canonical command"},
	"install.metadata.write":      {start: "Writing installation metadata", done: "Wrote installation metadata"},
	"install.versions.cleanup":    {start: "Cleaning old installed versions", done: "Cleaned old installed versions"},
	"tunnel.admin.verify":         {start: "Verifying tunnel admin access", done: "Verified tunnel admin access"},
	"tunnel.admin.read-probe":     {start: "Checking tunnel admin read access", done: "Verified tunnel admin read access"},
	"tunnel.admin.list":           {start: "Loading managed tunnels", done: "Loaded managed tunnels"},
	"tunnel.admin.get":            {start: "Fetching managed tunnel", done: "Fetched managed tunnel"},
	"tunnel.admin.create":         {start: "Creating managed tunnel", done: "Created managed tunnel"},
	"tunnel.admin.update":         {start: "Updating managed tunnel", done: "Updated managed tunnel"},
	"tunnel.admin.delete":         {start: "Deleting managed tunnel", done: "Deleted managed tunnel"},
	"tunnel.runtime-key.generate": {start: "Generating runtime API key", done: "Generated runtime API key"},
	"tunnel.metadata.fetch":       {start: "Fetching tunnel metadata", done: "Fetched tunnel metadata"},
	"tunnel.metadata.refresh":     {start: "Refreshing tunnel metadata", done: "Refreshed tunnel metadata"},
}

func addLoggingFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().Bool("verbose", false, "show additional runtime context")
	cmd.PersistentFlags().Bool("debug", false, "show full diagnostic logging")
	cmd.PersistentFlags().String("log-format", "text", "diagnostic log format: text or json; does not change command result format")
}

func validateLoggingFlags(cmd *cobra.Command, _ []string) error {
	_, err := commandLogFormat(cmd)
	return err
}

func commandLogger(cmd *cobra.Command) *logger.Logger {
	if cmd != nil {
		if value, ok := commandLoggers.Load(cmd); ok {
			return value.(*logger.Logger)
		}
	}
	verbose, debug := commandLogMode(cmd)
	format, _ := commandLogFormat(cmd)
	level := logger.Info
	if debug {
		level = logger.Debug
	}
	capabilities := commandTerminalCapabilities(cmd)
	created := logger.NewWithOptions(logger.Options{Level: level, Mode: logger.ModeFor(verbose, debug), Format: format, Writer: commandLogWriter(cmd), Terminal: &logger.TerminalOptions{Color: capabilities.Color, Unicode: capabilities.Unicode, RawUnicode: capabilities.RawUnicode, Animate: capabilities.Animation}})
	if cmd == nil {
		return created
	}
	value, loaded := commandLoggers.LoadOrStore(cmd, created)
	if loaded {
		created.Close()
		return value.(*logger.Logger)
	}
	return created
}

func closeCommandLogger(cmd *cobra.Command) {
	if cmd == nil {
		return
	}
	if value, ok := commandLoggers.LoadAndDelete(cmd); ok {
		value.(*logger.Logger).Close()
	}
}

func newCommandProgress(cmd *cobra.Command, component string) *commandProgress {
	return &commandProgress{cmd: cmd, log: commandLogger(cmd), component: component, session: commandProgressSession(cmd)}
}

func (p *commandProgress) Start(name, message, done string) {
	if p == nil {
		return
	}
	p.Complete()
	p.name, p.label, p.done = name, message, done
	p.session.Update(presentation.ProgressPhase{ID: name, Label: message, State: presentation.ProgressRunning})
	p.log.Verbose(p.component, name, message)
}

func (p *commandProgress) Complete() {
	if p == nil || p.name == "" {
		return
	}
	p.session.Success(p.name, p.label, p.done)
	if format, _ := commandLogFormat(p.cmd); format == logger.FormatJSON {
		p.log.Ready(p.component, p.name+".completed", p.done)
	}
	p.name, p.label, p.done = "", "", ""
}

func (p *commandProgress) Stop() {
	if p == nil {
		return
	}
	p.session.Suspend()
	p.name, p.label, p.done = "", "", ""
}

func commandProgressSession(cmd *cobra.Command) *presentation.ProgressSession {
	if cmd == nil {
		return presentation.NewProgressSession(io.Discard, presentation.ModeJSON, presentation.Capabilities{})
	}
	if value, ok := commandProgressSessions.Load(cmd); ok {
		session := value.(*presentation.ProgressSession)
		if !session.Closed() {
			return session
		}
		commandProgressSessions.Delete(cmd)
	}
	mode := presentation.ModePlain
	if commandMachineOutput(cmd) {
		mode = presentation.ModeJSON
	} else if commandResultModeFor(cmd) == resultModeHuman {
		mode = presentation.ModeHuman
	}
	capabilities := commandTerminalCapabilities(cmd)
	verbose, debug := commandLogMode(cmd)
	format, _ := commandLogFormat(cmd)
	if format == logger.FormatJSON {
		mode = presentation.ModeJSON
	}
	if verbose || debug || format != logger.FormatText {
		capabilities.Animation = false
		capabilities.CursorControl = false
	}
	created := presentation.NewProgressSession(commandResultWriter(cmd), mode, capabilities)
	created.SetTitle(commandPresentationTitle(cmd))
	value, loaded := commandProgressSessions.LoadOrStore(cmd, created)
	if loaded {
		created.Close()
		return value.(*presentation.ProgressSession)
	}
	return created
}

func closeCommandProgress(cmd *cobra.Command, cause error) {
	session := takeCommandProgress(cmd)
	if session == nil {
		return
	}
	if cause != nil {
		failure := classifyCommandFailure(cmd, cause)
		if failure.Title == "" {
			failure.Title = "Command failed"
		}
		activeFailed := session.FailActive(failure.Title)
		session.Append(func(presenter *presentation.Presenter) {
			if !activeFailed {
				presenter.StateSection(presentation.StatusError, failure.Title)
			}
			if failure.Summary != "" && !strings.EqualFold(failure.Summary, failure.Title) {
				presenter.Fields(presentation.Field{Label: "reason", Value: failure.Summary})
			}
			for _, action := range failure.Actions {
				presenter.Subsection(action.Title)
				presenter.NestedFields(presentation.Field{Label: "command", Value: action.Command})
			}
		})
		session.CloseWith("Failed")
		return
	}
	session.Close()
}

func takeCommandProgress(cmd *cobra.Command) *presentation.ProgressSession {
	if cmd == nil {
		return nil
	}
	if value, ok := commandProgressSessions.LoadAndDelete(cmd); ok {
		return value.(*presentation.ProgressSession)
	}
	return nil
}

type commandDiagnosticWriter struct {
	cmd *cobra.Command
	out io.Writer
}

func (writer commandDiagnosticWriter) Write(data []byte) (int, error) {
	if writer.cmd != nil {
		if value, ok := commandProgressSessions.Load(writer.cmd); ok {
			session := value.(*presentation.ProgressSession)
			session.EnsureBegun()
			session.Suspend()
		}
	}
	return writer.out.Write(data)
}

func commandLogMode(cmd *cobra.Command) (bool, bool) {
	flags := cmd.Root().PersistentFlags()
	verbose, _ := flags.GetBool("verbose")
	debug, _ := flags.GetBool("debug")
	return verbose, debug
}

func commandLogFormat(cmd *cobra.Command) (logger.Format, error) {
	value, _ := cmd.Root().PersistentFlags().GetString("log-format")
	return logger.ParseFormat(value)
}

func commandLogWriter(cmd *cobra.Command) io.Writer {
	if cmd == nil {
		return io.Discard
	}
	if commandMachineOutput(cmd) {
		return commandDiagnosticWriter{cmd: cmd, out: cmd.ErrOrStderr()}
	}
	return commandDiagnosticWriter{cmd: cmd, out: cmd.OutOrStdout()}
}

func logCommandStart(cmd *cobra.Command, args []string) {
	log := commandLogger(cmd)
	log.Verbose("CLI", "cli.command.starting", "Executing command",
		logger.WithVerbose("command", cmd.CommandPath()),
		logger.WithVerbose("cwd", currentWorkingDirectory()),
		logger.WithVerbose("pid", os.Getpid()),
	)
	log.Diagnostic(logger.Info, "CLI", "cli.command.context", "Command context",
		logger.WithDebug("pid", os.Getpid()),
		logger.WithDebug("cwd", currentWorkingDirectory()),
		logger.WithDebug("config", config.RootPath()),
		logger.WithDebug("arg_count", len(args)),
		logger.WithDebug("changed_flags", commandChangedFlags(cmd)),
	)
}

func logCommandCompleted(cmd *cobra.Command, started time.Time) {
	commandLogger(cmd).Verbose("CLI", "cli.command.completed", "Command completed",
		logger.WithVerbose("command", cmd.CommandPath()),
		logger.WithVerbose("duration_ms", time.Since(started).Milliseconds()),
	)
}

func logCommandFailure(cmd *cobra.Command, err error, started time.Time) {
	if cmd == nil {
		return
	}
	log := commandLogger(cmd)
	fields := []logger.Field{
		logger.WithVerbose("command", cmd.CommandPath()),
		logger.WithVerbose("duration_ms", time.Since(started).Milliseconds()),
		logger.WithDebug("pid", os.Getpid()),
		logger.WithDebug("cwd", currentWorkingDirectory()),
		logger.WithDebug("config", config.RootPath()),
		logger.WithDebug("changed_flags", commandChangedFlags(cmd)),
		logger.WithDebug("error_type", fmt.Sprintf("%T", err)),
		logger.WithDebug("error_chain", commandErrorChain(err)),
	}
	format, _ := commandLogFormat(cmd)
	verbose, debug := commandLogMode(cmd)
	if commandMachineOutput(cmd) || format == logger.FormatJSON {
		log.Failure("CLI", "cli.command.failed", "Command failed", err, fields...)
		return
	}
	if debug {
		log.Diagnostic(logger.Error, "CLI", "cli.command.failed", "Command failure diagnostics", fields...)
		return
	}
	if verbose {
		log.Verbose("CLI", "cli.command.failed", "Failure context", fields...)
	}
}

func commandChangedFlags(cmd *cobra.Command) []string {
	if cmd == nil {
		return nil
	}
	seen := map[string]bool{}
	values := []string{}
	visit := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		values = append(values, "--"+name)
	}
	cmd.Flags().Visit(func(flag *pflag.Flag) { visit(flag.Name) })
	cmd.InheritedFlags().Visit(func(flag *pflag.Flag) { visit(flag.Name) })
	cmd.Root().PersistentFlags().Visit(func(flag *pflag.Flag) { visit(flag.Name) })
	sort.Strings(values)
	return values
}

func commandErrorChain(err error) []string {
	values := []string{}
	var walk func(error, int)
	walk = func(current error, depth int) {
		if current == nil || depth >= 32 {
			return
		}
		values = append(values, fmt.Sprintf("%T: %v", current, current))
		switch typed := current.(type) {
		case interface{ Unwrap() []error }:
			for _, nested := range typed.Unwrap() {
				walk(nested, depth+1)
			}
		case interface{ Unwrap() error }:
			walk(typed.Unwrap(), depth+1)
		}
	}
	walk(err, 0)
	return values
}

func currentWorkingDirectory() string {
	value, err := os.Getwd()
	if err != nil {
		return "<unavailable>"
	}
	return value
}

func logCommandStep(cmd *cobra.Command, component, name, message string, fields ...logger.Field) {
	commandLogger(cmd).Verbose(component, name, message, fields...)
}

func logCommandDebug(cmd *cobra.Command, component, name, message string, fields ...logger.Field) {
	commandLogger(cmd).Diagnostic(logger.Info, component, name, message, fields...)
}

func commandTraceObserver(cmd *cobra.Command) tracepkg.Observer {
	if cmd == nil {
		return nil
	}
	return func(event tracepkg.Event) {
		fields := make([]logger.Field, 0, len(event.Fields)+1)
		fields = append(fields, logger.WithDebug("trace_phase", event.Phase))
		for _, field := range event.Fields {
			fields = append(fields, logger.WithVerbose(field.Key, field.Value))
		}
		if spec, ok := commandTraceProgressSpec(event); ok {
			log := commandLogger(cmd)
			session := commandProgressSession(cmd)
			phase := tracePresentationPhase(event, spec)
			if !session.Update(phase) {
				return
			}
			if commandMachineOutput(cmd) {
				log.Verbose(event.Component, event.Name, event.Message, fields...)
				return
			}
			format, _ := commandLogFormat(cmd)
			if format == logger.FormatJSON {
				renderTraceProgressDiagnostic(log, event, spec, fields)
				return
			}
			renderTraceProgressTextDiagnostic(log, event, spec, fields)
			return
		}
		if commandMachineOutput(cmd) {
			commandLogger(cmd).Verbose(event.Component, event.Name, event.Message, fields...)
			return
		}
		commandLogger(cmd).Verbose(event.Component, event.Name, event.Message, fields...)
	}
}

func renderTraceProgressTextDiagnostic(log *logger.Logger, event tracepkg.Event, spec traceProgressSpec, fields []logger.Field) {
	if log == nil {
		return
	}
	message := "Progress metadata"
	if event.Phase == tracepkg.PhaseStart {
		message = spec.start
	}
	log.Verbose(event.Component, event.Name, message, fields...)
}

func tracePresentationPhase(event tracepkg.Event, spec traceProgressSpec) presentation.ProgressPhase {
	phase := presentation.ProgressPhase{ID: traceProgressID(event), Label: spec.start, Message: event.Message}
	switch event.Phase {
	case tracepkg.PhaseStart:
		phase.State = presentation.ProgressRunning
		phase.Message = spec.start
	case tracepkg.PhaseEnd:
		phase.State = presentation.ProgressSuccess
		phase.Message = spec.done
		if strings.Contains(strings.ToLower(event.Message), "skipped") {
			phase.State = presentation.ProgressSkipped
			phase.Message = event.Message
		}
	case tracepkg.PhaseError:
		phase.State = presentation.ProgressFailed
	}
	return phase
}

func traceProgressID(event tracepkg.Event) string {
	name := strings.TrimSpace(event.Name)
	switch event.Phase {
	case tracepkg.PhaseStart:
		return strings.TrimSuffix(name, ".started")
	case tracepkg.PhaseEnd:
		return strings.TrimSuffix(name, ".completed")
	case tracepkg.PhaseError:
		return strings.TrimSuffix(name, ".failed")
	default:
		return name
	}
}

func renderTraceProgressDiagnostic(log *logger.Logger, event tracepkg.Event, spec traceProgressSpec, fields []logger.Field) {
	if log == nil {
		return
	}
	switch event.Phase {
	case tracepkg.PhaseStart:
		log.Verbose(event.Component, event.Name, spec.start, fields...)
	case tracepkg.PhaseEnd:
		message := spec.done
		if strings.Contains(strings.ToLower(event.Message), "skipped") {
			message = event.Message
		}
		log.Ready(event.Component, event.Name, message, fields...)
	case tracepkg.PhaseError:
		log.Verbose(event.Component, event.Name, event.Message, fields...)
	}
}

func commandTraceProgressSpec(event tracepkg.Event) (traceProgressSpec, bool) {
	name := strings.TrimSpace(event.Name)
	switch event.Phase {
	case tracepkg.PhaseStart:
		name = strings.TrimSuffix(name, ".started")
	case tracepkg.PhaseEnd:
		name = strings.TrimSuffix(name, ".completed")
	case tracepkg.PhaseError:
		name = strings.TrimSuffix(name, ".failed")
	default:
		return traceProgressSpec{}, false
	}
	spec, ok := traceProgress[name]
	return spec, ok
}
