package notification

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

var ErrProviderUnavailable = errors.New("notification provider is unavailable")

type DesktopProvider struct {
	goos     string
	lookPath func(string) (string, error)
	run      func(context.Context, string, ...string) error
}

func NewDesktopProvider() *DesktopProvider {
	return &DesktopProvider{
		goos:     runtime.GOOS,
		lookPath: exec.LookPath,
		run: func(ctx context.Context, name string, args ...string) error {
			return exec.CommandContext(ctx, name, args...).Run()
		},
	}
}

func (p *DesktopProvider) Name() string { return ProviderDesktop }

func (p *DesktopProvider) Available() bool {
	if p == nil {
		return false
	}
	name, _, ok := desktopCommand(p.goos, Message{})
	if !ok {
		return false
	}
	_, err := p.lookPath(name)
	return err == nil
}

func (p *DesktopProvider) Notify(ctx context.Context, message Message) error {
	if p == nil {
		return ErrProviderUnavailable
	}
	name, args, ok := desktopCommand(p.goos, message)
	if !ok {
		return ErrProviderUnavailable
	}
	path, err := p.lookPath(name)
	if err != nil {
		return ErrProviderUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return p.run(ctx, path, args...)
}

func desktopCommand(goos string, message Message) (string, []string, bool) {
	title := strings.TrimSpace(message.Title)
	body := strings.TrimSpace(message.Body)
	switch goos {
	case "linux":
		return "notify-send", []string{"--app-name", "CodeMCP", title, body}, true
	case "darwin":
		script := "display notification " + strconv.Quote(body) + " with title " + strconv.Quote(title)
		return "osascript", []string{"-e", script}, true
	default:
		return "", nil, false
	}
}
