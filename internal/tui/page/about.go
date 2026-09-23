package page

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/tui/component"
)

type aboutLoadMsg struct {
	info application.AboutInfo
	err  error
}

type AboutPage struct {
	ctx     context.Context
	info    application.AboutInfo
	loaded  bool
	loading bool
	err     error
}

func NewAbout(ctx context.Context) (*AboutPage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return &AboutPage{ctx: ctx}, nil
}

func (page *AboutPage) Init() tea.Cmd {
	if page == nil {
		return nil
	}
	page.loading = true
	return page.loadCmd()
}

func (page *AboutPage) OverlayActive() bool { return false }
func (page *AboutPage) InputActive() bool   { return false }

func (page *AboutPage) Update(message tea.Msg) (Model, tea.Cmd) {
	if page == nil {
		return page, nil
	}
	switch msg := message.(type) {
	case aboutLoadMsg:
		page.loading = false
		if msg.err != nil {
			page.err = msg.err
			return page, nil
		}
		page.loaded, page.err, page.info = true, nil, msg.info
		return page, nil
	case tea.KeyPressMsg:
		if msg.String() == "r" {
			page.loading = true
			return page, page.loadCmd()
		}
	}
	return page, nil
}

func (page *AboutPage) View(width, height int) string {
	if page == nil {
		return component.StateView(component.PageError, "About unavailable", "")
	}
	help := component.DefaultHelp(width, component.Binding([]string{"r"}, "r", "refresh"))
	if !page.loaded && page.loading {
		return component.BottomHelp(component.StateView(component.PageLoading, "Loading build and runtime information", ""), help, width, height)
	}
	if page.err != nil {
		return component.BottomHelp(component.PageTitle("About", width)+"\n"+component.BannerWidth(page.err.Error(), component.ToneDanger, width), help, width, height)
	}
	serverUptime := "stopped"
	if page.info.RuntimeRunning {
		serverUptime = page.info.ServerUptime.String()
	}
	machineUptime := "unavailable"
	if page.info.MachineUptimeOK {
		machineUptime = page.info.MachineUptime.String()
	}
	runtimeMode := "stopped"
	if page.info.RuntimeRunning {
		runtimeMode = "foreground"
		if page.info.Runtime.Managed {
			runtimeMode = "managed / " + page.info.Runtime.ServiceScope
		}
	}
	sections := []string{
		component.PageTitle("About", width),
		detailFields(
			[2]string{"Version", page.info.Version},
			[2]string{"Commit", page.info.Commit},
			[2]string{"Build time", page.info.BuildTime},
			[2]string{"Platform", runtime.GOOS + "/" + runtime.GOARCH},
		),
		component.Divider(width),
		detailFields(
			[2]string{"Runtime", runtimeMode},
			[2]string{"Server uptime", serverUptime},
			[2]string{"Machine uptime", machineUptime},
		),
		component.Divider(width),
		detailFields(
			[2]string{"Executable", page.info.Executable},
			[2]string{"Config", page.info.ConfigPath},
			[2]string{"Config root", page.info.ConfigRoot},
			[2]string{"Logs", page.info.LogsPath},
			[2]string{"Install method", string(page.info.InstallMethod)},
			[2]string{"Install root", page.info.InstallRoot},
		),
	}
	return component.BottomHelp(strings.Join(sections, "\n"), help, width, height)
}

func (page *AboutPage) loadCmd() tea.Cmd {
	ctx := page.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		info, err := application.LoadAbout(ctx)
		if err != nil {
			return aboutLoadMsg{err: fmt.Errorf("load about: %w", err)}
		}
		return aboutLoadMsg{info: info}
	}
}
