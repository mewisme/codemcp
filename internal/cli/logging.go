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

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/logger"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

var commandLoggers sync.Map

type commandProgress struct {
	cmd       *cobra.Command
	log       *logger.Logger
	component string
	name      string
	done      string
}

type traceProgressSpec struct {
	start string
	done  string
}

var traceProgress = map[string]traceProgressSpec{
	"config.persist":              {start: "Saving configuration", done: "Configuration saved"},
	"config.persist.rollback":     {start: "Rolling back configuration", done: "Configuration rollback complete"},
	"config.runtime.reload":       {start: "Reloading running runtime", done: "Runtime configuration reloaded"},
	"install.source.validate":     {start: "Validating source binary", done: "Source binary validated"},
	"install.legacy.discover":     {start: "Checking legacy installations", done: "Legacy installations checked"},
	"install.stage":               {start: "Staging installation binary", done: "Installation binary staged"},
	"install.activate":            {start: "Activating installation", done: "Installation activated"},
	"install.rollback":            {start: "Rolling back installation", done: "Installation rollback complete"},
	"install.legacy.backup":       {start: "Backing up legacy installation", done: "Legacy installation backed up"},
	"install.canonical.install":   {start: "Installing canonical command", done: "Canonical command installed"},
	"install.metadata.write":      {start: "Writing installation metadata", done: "Installation metadata written"},
	"install.versions.cleanup":    {start: "Cleaning old installed versions", done: "Old installed versions cleaned"},
	"tunnel.admin.verify":         {start: "Verifying tunnel admin access", done: "Tunnel admin access verified"},
	"tunnel.admin.read-probe":     {start: "Checking tunnel admin read access", done: "Tunnel admin read access verified"},
	"tunnel.admin.list":           {start: "Loading managed tunnels", done: "Managed tunnels loaded"},
	"tunnel.admin.get":            {start: "Fetching managed tunnel", done: "Managed tunnel fetched"},
	"tunnel.admin.create":         {start: "Creating managed tunnel", done: "Managed tunnel created"},
	"tunnel.admin.update":         {start: "Updating managed tunnel", done: "Managed tunnel updated"},
	"tunnel.admin.delete":         {start: "Deleting managed tunnel", done: "Managed tunnel deleted"},
	"tunnel.runtime-key.generate": {start: "Generating runtime API key", done: "Runtime API key generated"},
	"tunnel.metadata.fetch":       {start: "Fetching tunnel metadata", done: "Tunnel metadata fetched"},
	"tunnel.metadata.refresh":     {start: "Refreshing tunnel metadata", done: "Tunnel metadata refreshed"},
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
	created := logger.NewWithOptions(logger.Options{Level: level, Mode: logger.ModeFor(verbose, debug), Format: format, Writer: commandLogWriter(cmd)})
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

func startCommandSpinner(cmd *cobra.Command, log *logger.Logger, component, name, message string) {
	format, err := commandLogFormat(cmd)
	verbose, debug := commandLogMode(cmd)
	if err == nil && format == logger.FormatText && !verbose && !debug && logger.CanAnimate(commandLogWriter(cmd)) {
		log.Action(component, name, message)
	}
}

func newCommandProgress(cmd *cobra.Command, component string) *commandProgress {
	return &commandProgress{cmd: cmd, log: commandLogger(cmd), component: component}
}

func (p *commandProgress) Start(name, message, done string) {
	if p == nil {
		return
	}
	p.Complete()
	p.name, p.done = name, done
	startCommandSpinner(p.cmd, p.log, p.component, name, message)
	p.log.Verbose(p.component, name, message)
}

func (p *commandProgress) Complete() {
	if p == nil || p.name == "" {
		return
	}
	p.log.StopAnimation()
	p.log.Ready(p.component, p.name+".completed", p.done)
	p.name, p.done = "", ""
}

func (p *commandProgress) Stop() {
	if p == nil {
		return
	}
	p.log.StopAnimation()
	p.name, p.done = "", ""
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
		return cmd.ErrOrStderr()
	}
	return cmd.OutOrStdout()
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
	commandLogger(cmd).Failure("CLI", "cli.command.failed", "Command failed", err,
		logger.WithVerbose("command", cmd.CommandPath()),
		logger.WithVerbose("duration_ms", time.Since(started).Milliseconds()),
		logger.WithDebug("pid", os.Getpid()),
		logger.WithDebug("cwd", currentWorkingDirectory()),
		logger.WithDebug("config", config.RootPath()),
		logger.WithDebug("changed_flags", commandChangedFlags(cmd)),
		logger.WithDebug("error_type", fmt.Sprintf("%T", err)),
		logger.WithDebug("error_chain", commandErrorChain(err)),
	)
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
		if commandMachineOutput(cmd) {
			commandLogger(cmd).Verbose(event.Component, event.Name, event.Message, fields...)
			return
		}
		if spec, ok := commandTraceProgressSpec(event); ok {
			log := commandLogger(cmd)
			switch event.Phase {
			case tracepkg.PhaseStart:
				startCommandSpinner(cmd, log, event.Component, event.Name, spec.start)
				log.Verbose(event.Component, event.Name, spec.start, fields...)
			case tracepkg.PhaseEnd:
				message := spec.done
				if strings.Contains(strings.ToLower(event.Message), "skipped") {
					message = event.Message
				}
				log.Ready(event.Component, event.Name, message, fields...)
			case tracepkg.PhaseError:
				log.StopAnimation()
				log.Verbose(event.Component, event.Name, event.Message, fields...)
			}
			return
		}
		commandLogger(cmd).Verbose(event.Component, event.Name, event.Message, fields...)
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
