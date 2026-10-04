package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/logger"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
	runtimeevent "go.mewis.me/codemcp/internal/runtime/event"
)

var errLogsClearConfirmationRequired = errors.New("refusing to clear runtime logs without --force")

type runtimeUnavailableError struct {
	Operation string
	Cause     error
}

func (err *runtimeUnavailableError) Error() string {
	if err == nil || strings.TrimSpace(err.Operation) == "" {
		return "runtime is not running"
	}
	return "runtime is not running; cannot " + strings.TrimSpace(err.Operation)
}

func (err *runtimeUnavailableError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

type logsOptions struct {
	tail       int
	follow     bool
	all        bool
	showTime   bool
	noTime     bool
	since      string
	until      string
	session    string
	level      string
	components string
	workspace  string
	tool       string
	status     string
	source     string
	event      string
	grep       string
}

func logsCommand() *cobra.Command {
	options := &logsOptions{tail: 100, showTime: true}
	cmd := &cobra.Command{Use: "logs", Short: "Read and follow structured runtime logs", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		return runLogs(cmd, *options)
	}}
	addLogsFlags(cmd, options, true)
	addLogsCompletions(cmd)
	followOptions := &logsOptions{tail: 100, follow: true, showTime: true}
	follow := &cobra.Command{Use: "follow", Short: "Follow runtime logs", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		return runLogs(cmd, *followOptions)
	}}
	addLogsFlags(follow, followOptions, false)
	addLogsCompletions(follow)
	pathCmd := &cobra.Command{Use: "path", Short: "Show the runtime journal path", Args: cobra.NoArgs, Run: func(cmd *cobra.Command, args []string) {
		path := runtimeevent.Path(config.RootPath())
		if format, _ := commandLogFormat(cmd); format == logger.FormatJSON {
			commandLogger(cmd).Notice("LOGS", "logs.path", path)
			return
		}
		presenter := commandPresenter(cmd)
		presenter.Fields(presentation.Field{Label: "path", Value: path})
		presenter.Complete("Done")
	}}
	var forceClear bool
	clear := &cobra.Command{Use: "clear", Short: "Clear runtime logs", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if !forceClear {
			return errLogsClearConfirmationRequired
		}
		log := commandLogger(cmd)
		logCommandStep(cmd, "LOGS", "logs.runtime.contacting", "Contacting runtime log endpoint")
		progress := newCommandProgress(cmd, "LOGS")
		progress.Start("logs.clearing", "Clearing runtime logs", "Runtime logs cleared")
		if err := clearRuntimeLogs(cmd); err != nil {
			progress.Stop()
			return err
		}
		progress.Complete()
		log.Verbose("LOGS", "logs.cleared", "Runtime logs cleared")
		return nil
	}}
	clear.Flags().BoolVar(&forceClear, "force", false, "clear current and rotated runtime logs")
	cmd.AddCommand(follow, pathCmd, clear)
	return cmd
}

func addLogsCompletions(cmd *cobra.Command) {
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionID)
	_ = cmd.RegisterFlagCompletionFunc("workspace", func(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return workspaceCompletions(cmd, toComplete)
	})
	_ = cmd.RegisterFlagCompletionFunc("level", completeStatic("debug", "info", "warn", "error"))
	_ = cmd.RegisterFlagCompletionFunc("component", completeStatic("SERVER", "TUNNEL", "CONFIG", "MCP", "TOOL", "UPSTREAM", "SESSION", "SERVICE"))
}

func clearRuntimeLogs(cmd *cobra.Command) error { return application.ClearLogs(cmd.Context()) }

func addLogsFlags(cmd *cobra.Command, options *logsOptions, includeFollow bool) {
	cmd.Flags().IntVarP(&options.tail, "tail", "n", 100, "show the last N matching events; 0 shows all")
	if includeFollow {
		cmd.Flags().BoolVarP(&options.follow, "follow", "f", false, "follow new runtime events")
	}
	cmd.Flags().BoolVar(&options.noTime, "no-time", false, "hide timestamps from runtime event lines")
	cmd.Flags().StringVar(&options.since, "since", "", "show events since a duration such as 30m or an RFC3339 timestamp")
	cmd.Flags().StringVar(&options.until, "until", "", "show events through an RFC3339 timestamp")
	cmd.Flags().StringVar(&options.session, "session", "", "filter by session/run ID or displayed prefix")
	cmd.Flags().BoolVar(&options.all, "all", false, "show events from all sessions")
	cmd.MarkFlagsMutuallyExclusive("all", "session")
	cmd.Flags().StringVar(&options.level, "level", "", "minimum level: debug, info, warn, or error")
	cmd.Flags().StringVar(&options.components, "component", "", "comma-separated components such as SERVER,TUNNEL")
	cmd.Flags().StringVar(&options.workspace, "workspace", "", "workspace ID or registered workspace path")
	cmd.Flags().StringVar(&options.tool, "tool", "", "filter by tool name")
	cmd.Flags().StringVar(&options.status, "status", "", "filter by status")
	cmd.Flags().StringVar(&options.source, "source", "", "filter by source")
	cmd.Flags().StringVar(&options.event, "event", "", "filter by event-name glob")
	cmd.Flags().StringVar(&options.grep, "grep", "", "filter by case-insensitive text")
}

func runLogs(cmd *cobra.Command, options logsOptions) error {
	if options.noTime {
		options.showTime = false
	}
	queryOptions := logsQueryOptions(options)
	logCommandStep(cmd, "LOGS", "logs.query.validating", "Validating runtime log query")
	if _, err := application.BuildLogsQueryContext(cmd.Context(), queryOptions, time.Now()); err != nil {
		return err
	}
	visibility := logsVisibility(cmd)
	followCtx := cmd.Context()
	var interrupt *foregroundInterrupt
	var stream *runtimecontrol.EventStream
	var followErr error
	if options.follow {
		logCommandStep(cmd, "LOGS", "logs.stream.connecting", "Connecting to runtime event stream")
		interrupt = newForegroundInterrupt(cmd, true)
		defer interrupt.Close()
		followCtx = interrupt.Context
		stream, _, followErr = runtimecontrol.OpenEvents(followCtx)
		if stream != nil {
			defer stream.Close()
		}
	}
	logCommandStep(cmd, "LOGS", "logs.snapshot.loading", "Loading runtime log snapshot")
	snapshot, err := application.LoadLogsContext(cmd.Context(), queryOptions, visibility, 0, time.Now())
	if err != nil {
		return err
	}
	replay := newRuntimeReplay(cmd, options.showTime)
	lastByRun := map[string]uint64{}
	for runID, sequence := range snapshot.LatestSequence {
		lastByRun[runID] = sequence
	}
	for _, event := range snapshot.Events {
		replay.Render(event)
	}
	if !options.follow {
		return nil
	}
	if followErr != nil {
		return &runtimeUnavailableError{Operation: "follow logs", Cause: followErr}
	}
	logCommandStep(cmd, "LOGS", "logs.stream.following", "Following runtime event stream")
	return followRuntimeEventStream(followCtx, stream, snapshot.Query, visibility, lastByRun, replay)
}

func logsQueryOptions(options logsOptions) application.LogsQueryOptions {
	return application.LogsQueryOptions{Tail: options.tail, All: options.all, Since: options.since, Until: options.until, Session: options.session, Level: options.level, Components: options.components, Workspace: options.workspace, Tool: options.tool, Status: options.status, Source: options.source, Event: options.event, Grep: options.grep}
}

func logsVisibility(cmd *cobra.Command) logger.Visibility {
	verbose, debug := commandLogMode(cmd)
	if debug {
		return logger.VisibilityDebug
	}
	if verbose {
		return logger.VisibilityVerbose
	}
	return logger.VisibilityDefault
}

func logReplayLogger(cmd *cobra.Command, showTime bool) *logger.Logger {
	verbose, debug := commandLogMode(cmd)
	format, _ := commandLogFormat(cmd)
	timeMode := logger.TimeHide
	if showTime {
		timeMode = logger.TimeShow
	}
	capabilities := commandTerminalCapabilities(cmd)
	return logger.NewWithOptions(logger.Options{Level: logger.Debug, Mode: logger.ModeFor(verbose, debug), Format: format, TimeMode: timeMode, Writer: commandLogWriter(cmd), Terminal: &logger.TerminalOptions{Color: capabilities.Color, Unicode: capabilities.Unicode}})
}

func renderRuntimeEvent(log *logger.Logger, event runtimeevent.Event) {
	value := event.LoggerEvent()
	value.Time = event.Time.Local()
	log.Emit(value)
}

type runtimeReplay struct {
	log       *logger.Logger
	out       io.Writer
	format    logger.Format
	showTime  bool
	theme     presentation.Theme
	glyphs    presentation.GlyphSet
	lastRunID string
}

func newRuntimeReplay(cmd *cobra.Command, showTime bool) *runtimeReplay {
	format, _ := commandLogFormat(cmd)
	capabilities := commandTerminalCapabilities(cmd)
	return &runtimeReplay{
		log:      logReplayLogger(cmd, showTime),
		out:      commandLogWriter(cmd),
		format:   format,
		showTime: showTime,
		theme:    presentation.NewTheme(capabilities),
		glyphs:   presentation.Glyphs(capabilities),
	}
}

func (replay *runtimeReplay) Render(event runtimeevent.Event) {
	if replay == nil {
		return
	}
	if replay.format == logger.FormatText && event.RunID != "" && event.RunID != replay.lastRunID {
		replay.renderSessionHeader(event)
		replay.lastRunID = event.RunID
	}
	if replay.format == logger.FormatText && event.Name == "runtime.session.started" {
		return
	}
	renderRuntimeEvent(replay.log, event)
}

func (replay *runtimeReplay) renderSessionHeader(event runtimeevent.Event) {
	prefix := ""
	if replay.showTime && !event.Time.IsZero() {
		prefix = event.Time.Local().Format("2006-01-02 15:04:05") + " "
	}
	mode := "foreground"
	if event.Managed {
		mode = "managed"
		if event.ServiceScope != "" {
			mode += "/" + event.ServiceScope
		}
	}
	pid := ""
	if event.PID > 0 {
		pid = fmt.Sprintf(" %s pid %d", replay.glyphs.Separator, event.PID)
	}
	line := fmt.Sprintf("%s%s%s session %s%s %s %s %s%s", prefix, replay.glyphs.Horizontal, replay.glyphs.Horizontal, shortSessionID(event.RunID), pid, replay.glyphs.Separator, mode, replay.glyphs.Horizontal, replay.glyphs.Horizontal)
	fmt.Fprintln(replay.out, replay.theme.Render(presentation.RoleMuted, line))
}

func shortSessionID(value string) string {
	value = strings.TrimSpace(value)
	const max = 20
	if len(value) <= max {
		return value
	}
	return value[:max]
}

func followRuntimeEventStream(ctx context.Context, stream *runtimecontrol.EventStream, query runtimeevent.Query, visibility logger.Visibility, lastByRun map[string]uint64, replay *runtimeReplay) error {
	for {
		event, err := stream.Next()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, runtimecontrol.ErrEventStreamGap) {
				replay.log.Warning("LOGS", "logs.stream.gap", "Runtime log stream gap detected; resyncing journal", err)
				if err := replayRuntimeEventGap(query, visibility, lastByRun, replay); err != nil {
					return err
				}
				continue
			}
			return err
		}
		if event.RunID != "" && event.Sequence <= lastByRun[event.RunID] {
			continue
		}
		if !query.Match(event) || event.Visibility > visibility {
			continue
		}
		replay.Render(event)
		if event.RunID != "" && event.Sequence > lastByRun[event.RunID] {
			lastByRun[event.RunID] = event.Sequence
		}
	}
}

func replayRuntimeEventGap(query runtimeevent.Query, visibility logger.Visibility, lastByRun map[string]uint64, replay *runtimeReplay) error {
	events, err := runtimeevent.Read(config.RootPath(), runtimeevent.Query{})
	if err != nil {
		return fmt.Errorf("resync runtime log journal: %w", err)
	}
	for _, event := range events {
		if event.RunID == "" || event.Sequence <= lastByRun[event.RunID] {
			continue
		}
		if query.Match(event) && event.Visibility <= visibility {
			replay.Render(event)
		}
		lastByRun[event.RunID] = event.Sequence
	}
	return nil
}
