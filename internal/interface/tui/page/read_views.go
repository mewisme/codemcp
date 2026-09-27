package page

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/interface/tui/component"
	"go.mewis.me/codemcp/internal/workspace"
)

type readViewLoadMsg struct {
	value any
	err   error
}

type ReadViewPage struct {
	ctx     context.Context
	title   string
	load    func(context.Context) (any, error)
	value   any
	loaded  bool
	loading bool
	err     error
}

func newReadViewPage(ctx context.Context, title string, load func(context.Context) (any, error)) *ReadViewPage {
	if ctx == nil {
		ctx = context.Background()
	}
	return &ReadViewPage{ctx: ctx, title: title, load: load}
}

func NewToolsReadView(ctx context.Context) (*ReadViewPage, error) {
	service := application.NewToolInventoryService()
	return newReadViewPage(ctx, "Tools", func(ctx context.Context) (any, error) {
		result, err := service.List(ctx)
		return result.Value, err
	}), nil
}

func NewDoctorReadView(ctx context.Context) (*ReadViewPage, error) {
	service, err := application.NewDefaultDoctorService()
	if err != nil {
		return nil, err
	}
	return newReadViewPage(ctx, "Doctor", func(ctx context.Context) (any, error) {
		result, err := service.Run(ctx)
		return result.Value, err
	}), nil
}

func NewIntegrationsReadView(ctx context.Context, resourceID, action, workspaceID string) (*ReadViewPage, error) {
	rtkService := application.NewRTKService()
	manager := workspace.NewManager(workspace.DefaultStorePath())
	codeGraphService := application.NewCodeGraphService(manager)
	typeSafeService := application.NewTypeSafeService()
	resourceID = strings.ToLower(strings.TrimSpace(resourceID))
	action = strings.ToLower(strings.TrimSpace(action))
	workspaceID = strings.TrimSpace(workspaceID)
	return newReadViewPage(ctx, "Integrations", func(ctx context.Context) (any, error) {
		if resourceID == "rtk" && action == "probe" {
			return rtkService.Probe(ctx)
		}
		if resourceID == "codegraph" && action == "probe" {
			return codeGraphService.Probe(ctx)
		}
		if resourceID == "codegraph" && workspaceID != "" {
			return codeGraphService.WorkspaceStatus(ctx, application.CodeGraphWorkspaceInput{WorkspaceID: workspaceID})
		}
		rtkStatus, err := rtkService.Status(ctx)
		if err != nil {
			return nil, err
		}
		codeGraphStatus, err := codeGraphService.Status(ctx)
		if err != nil {
			return nil, err
		}
		typeSafeStatus, err := typeSafeService.Status(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"rtk": rtkStatus, "codegraph": codeGraphStatus, "typesafe": typeSafeStatus}, nil
	}), nil
}

func NewExecutionsReadView(ctx context.Context, workspaceID, executionID string) (*ReadViewPage, error) {
	service := application.NewRuntimeInspectionService()
	workspaceID, executionID = strings.TrimSpace(workspaceID), strings.TrimSpace(executionID)
	return newReadViewPage(ctx, "Command Executions", func(ctx context.Context) (any, error) {
		if executionID != "" {
			result, err := service.ViewExecution(ctx, workspaceID, executionID)
			return result.Value, err
		}
		result, err := service.ListExecutions(ctx, workspaceID)
		return result.Value, err
	}), nil
}

func NewProcessesReadView(ctx context.Context, workspaceID, processID string) (*ReadViewPage, error) {
	service := application.NewRuntimeInspectionService()
	workspaceID, processID = strings.TrimSpace(workspaceID), strings.TrimSpace(processID)
	return newReadViewPage(ctx, "Background Processes", func(ctx context.Context) (any, error) {
		if processID != "" {
			result, err := service.ViewProcess(ctx, workspaceID, processID)
			return result.Value, err
		}
		result, err := service.ListProcesses(ctx, workspaceID)
		return result.Value, err
	}), nil
}

func (page *ReadViewPage) Init() tea.Cmd {
	if page == nil {
		return nil
	}
	page.loading = true
	return page.loadCmd()
}

func (page *ReadViewPage) OverlayActive() bool { return false }
func (page *ReadViewPage) InputActive() bool   { return false }

func (page *ReadViewPage) Update(message tea.Msg) (Model, tea.Cmd) {
	if page == nil {
		return page, nil
	}
	switch msg := message.(type) {
	case readViewLoadMsg:
		page.loading = false
		page.loaded = msg.err == nil
		page.value, page.err = msg.value, msg.err
		return page, nil
	case tea.KeyPressMsg:
		if msg.String() == "r" {
			page.loading = true
			return page, page.loadCmd()
		}
	}
	return page, nil
}

func (page *ReadViewPage) View(width, height int) string {
	if page == nil {
		return component.StateView(component.PageError, "Read view unavailable", "")
	}
	help := component.DefaultHelp(width, component.Binding([]string{"r"}, "r", "refresh"))
	if !page.loaded && page.loading {
		return component.BottomHelp(component.StateView(component.PageLoading, "Loading "+strings.ToLower(page.title), ""), help, width, height)
	}
	if page.err != nil {
		return component.BottomHelp(component.PageTitle(page.title, width)+"\n"+component.BannerWidth(page.err.Error(), component.ToneDanger, width), help, width, height)
	}
	data, err := json.MarshalIndent(page.value, "", "  ")
	if err != nil {
		return component.BottomHelp(component.PageTitle(page.title, width)+"\n"+component.BannerWidth(err.Error(), component.ToneDanger, width), help, width, height)
	}
	body := component.PageTitle(page.title, width) + "\n" + component.RenderCodeBlock(string(data), "json", width)
	return component.BottomHelp(body, help, width, height)
}

func (page *ReadViewPage) loadCmd() tea.Cmd {
	ctx, load := page.ctx, page.load
	return func() tea.Msg {
		if load == nil {
			return readViewLoadMsg{err: fmt.Errorf("%s reader is unavailable", page.title)}
		}
		loadCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		value, err := load(loadCtx)
		return readViewLoadMsg{value: value, err: err}
	}
}
