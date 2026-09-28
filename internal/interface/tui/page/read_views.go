package page

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/interface/tui/component"
	"go.mewis.me/codemcp/internal/workspace"
)

const integrationOperationTimeout = 2 * time.Minute

type IntegrationCommand string

const (
	IntegrationRTKEnable              IntegrationCommand = "integration.rtk.enable"
	IntegrationRTKDisable             IntegrationCommand = "integration.rtk.disable"
	IntegrationRTKInstall             IntegrationCommand = "integration.rtk.install"
	IntegrationRTKInstallGlobal       IntegrationCommand = "integration.rtk.install.global"
	IntegrationCodeGraphInstall       IntegrationCommand = "integration.codegraph.install"
	IntegrationCodeGraphInstallGlobal IntegrationCommand = "integration.codegraph.install.global"
	IntegrationCFProbe                IntegrationCommand = "integration.cf.probe"
	IntegrationCFInstall              IntegrationCommand = "integration.cf.install"
	IntegrationCFUpdate               IntegrationCommand = "integration.cf.update"
	IntegrationCFRemove               IntegrationCommand = "integration.cf.remove"
	IntegrationCodeGraphInit          IntegrationCommand = "integration.codegraph.workspace.init"
	IntegrationCodeGraphSync          IntegrationCommand = "integration.codegraph.workspace.sync"
	IntegrationTypeSafeEnable         IntegrationCommand = "integration.typesafe.enable"
	IntegrationTypeSafeDisable        IntegrationCommand = "integration.typesafe.disable"
)

type IntegrationCommandMsg struct {
	Command     IntegrationCommand
	WorkspaceID string
}

type readViewLoadMsg struct {
	value any
	err   error
}

type integrationOperationMsg struct {
	id        uint64
	operation capability.ID
	value     any
	err       error
}

type ReadViewPage struct {
	ctx     context.Context
	title   string
	load    func(context.Context) (any, error)
	value   any
	loaded  bool
	loading bool
	err     error
	notice  string

	integrationRun  func(context.Context, capability.ID, string) (any, error)
	operationID     uint64
	operationCancel context.CancelFunc
	progress        *component.Progress
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
	cfService := application.NewCFTunnelService()
	typeSafeService := application.NewTypeSafeService()
	resourceID = strings.ToLower(strings.TrimSpace(resourceID))
	action = strings.ToLower(strings.TrimSpace(action))
	workspaceID = strings.TrimSpace(workspaceID)
	page := newReadViewPage(ctx, "Integrations", func(ctx context.Context) (any, error) {
		if resourceID == "rtk" && action == "probe" {
			return rtkService.Probe(ctx)
		}
		if resourceID == "codegraph" && action == "probe" {
			return codeGraphService.Probe(ctx)
		}
		if resourceID == "cf" && action == "probe" {
			return cfService.Probe(ctx)
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
		cfStatus, err := cfService.Status(ctx)
		if err != nil {
			return nil, err
		}
		typeSafeStatus, err := typeSafeService.Status(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"rtk": rtkStatus, "codegraph": codeGraphStatus, "cf": cfStatus, "typesafe": typeSafeStatus}, nil
	})
	page.integrationRun = func(ctx context.Context, operation capability.ID, workspaceID string) (any, error) {
		switch operation {
		case capability.IntegrationRTKEnable:
			return rtkService.Enable(ctx)
		case capability.IntegrationRTKDisable:
			return rtkService.Disable(ctx)
		case capability.IntegrationRTKInstall:
			return rtkService.Install(ctx)
		case capability.IntegrationRTKInstallGlobal:
			return rtkService.ResolveGlobal(ctx)
		case capability.IntegrationCodeGraphInstall:
			return codeGraphService.Install(ctx)
		case capability.IntegrationCodeGraphInstallGlobal:
			return codeGraphService.ResolveGlobal(ctx)
		case capability.IntegrationCFProbe:
			return cfService.Probe(ctx)
		case capability.IntegrationCFInstall:
			return cfService.Install(ctx)
		case capability.IntegrationCFUpdate:
			return cfService.Update(ctx)
		case capability.IntegrationCFRemove:
			return cfService.Remove(ctx)
		case capability.IntegrationCodeGraphWorkspaceInit:
			return codeGraphService.InitWorkspace(ctx, application.CodeGraphWorkspaceInput{WorkspaceID: strings.TrimSpace(workspaceID)})
		case capability.IntegrationCodeGraphWorkspaceSync:
			return codeGraphService.SyncWorkspace(ctx, application.CodeGraphWorkspaceInput{WorkspaceID: strings.TrimSpace(workspaceID)})
		case capability.IntegrationTypeSafeEnable:
			return typeSafeService.Enable(ctx)
		case capability.IntegrationTypeSafeDisable:
			return typeSafeService.Disable(ctx)
		default:
			return nil, fmt.Errorf("unsupported integration operation: %s", operation)
		}
	}
	return page, nil
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

func (page *ReadViewPage) Close() {
	if page == nil || page.operationCancel == nil {
		return
	}
	page.operationCancel()
	page.operationCancel = nil
}

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
	case IntegrationCommandMsg:
		operation, err := integrationCommandOperation(msg.Command)
		if err != nil {
			page.err = err
			return page, nil
		}
		return page, page.startIntegrationOperation(operation, msg.WorkspaceID)
	case integrationOperationMsg:
		if msg.id != page.operationID {
			return page, nil
		}
		if page.operationCancel != nil {
			page.operationCancel()
			page.operationCancel = nil
		}
		page.loading, page.progress = false, nil
		if msg.err != nil {
			page.err = msg.err
			return page, nil
		}
		page.loaded, page.err, page.value = true, nil, msg.value
		page.notice = integrationOperationNotice(msg.operation)
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
	if page.loading && page.progress != nil {
		return component.BottomHelp(component.StateView(component.PageLoading, page.progress.View(), ""), help, width, height)
	}
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
	body := component.PageTitleNotice(page.title, page.notice, width) + "\n" + component.RenderCodeBlock(string(data), "json", width)
	return component.BottomHelp(body, help, width, height)
}

func (page *ReadViewPage) startIntegrationOperation(operation capability.ID, workspaceID string) tea.Cmd {
	if page == nil || page.integrationRun == nil {
		return func() tea.Msg { return integrationOperationMsg{err: fmt.Errorf("integration mutation is unavailable")} }
	}
	if page.operationCancel != nil {
		page.operationCancel()
	}
	ctx, cancel := context.WithTimeout(page.ctx, integrationOperationTimeout)
	page.operationCancel = cancel
	page.operationID++
	id := page.operationID
	page.loading, page.err, page.notice = true, nil, ""
	progress := component.NewProgress(integrationOperationTitle(operation))
	page.progress = &progress
	run := page.integrationRun
	return func() tea.Msg {
		value, err := run(ctx, operation, workspaceID)
		return integrationOperationMsg{id: id, operation: operation, value: value, err: err}
	}
}

func integrationCommandOperation(command IntegrationCommand) (capability.ID, error) {
	operation := capability.ID(command)
	switch operation {
	case capability.IntegrationRTKEnable,
		capability.IntegrationRTKDisable,
		capability.IntegrationRTKInstall,
		capability.IntegrationRTKInstallGlobal,
		capability.IntegrationCodeGraphInstall,
		capability.IntegrationCodeGraphInstallGlobal,
		capability.IntegrationCFProbe,
		capability.IntegrationCFInstall,
		capability.IntegrationCFUpdate,
		capability.IntegrationCFRemove,
		capability.IntegrationCodeGraphWorkspaceInit,
		capability.IntegrationCodeGraphWorkspaceSync,
		capability.IntegrationTypeSafeEnable,
		capability.IntegrationTypeSafeDisable:
		if _, ok := capability.Lookup(operation); !ok {
			return "", fmt.Errorf("unknown integration operation: %s", operation)
		}
		return operation, nil
	default:
		return "", fmt.Errorf("unsupported integration command: %s", command)
	}
}

func integrationOperationTitle(operation capability.ID) string {
	switch operation {
	case capability.IntegrationRTKEnable:
		return "Enabling RTK"
	case capability.IntegrationRTKDisable:
		return "Disabling RTK"
	case capability.IntegrationRTKInstall:
		return "Installing RTK"
	case capability.IntegrationRTKInstallGlobal:
		return "Checking global RTK"
	case capability.IntegrationCodeGraphInstall:
		return "Installing CodeGraph"
	case capability.IntegrationCodeGraphInstallGlobal:
		return "Checking global CodeGraph"
	case capability.IntegrationCFProbe:
		return "Probing Cloudflare Quick Tunnel"
	case capability.IntegrationCFInstall:
		return "Installing managed cf-tunnel"
	case capability.IntegrationCFUpdate:
		return "Updating managed cf-tunnel"
	case capability.IntegrationCFRemove:
		return "Removing managed cf-tunnel"
	case capability.IntegrationCodeGraphWorkspaceInit:
		return "Initializing CodeGraph workspace"
	case capability.IntegrationCodeGraphWorkspaceSync:
		return "Syncing CodeGraph workspace"
	case capability.IntegrationTypeSafeEnable:
		return "Enabling TypeSafe"
	case capability.IntegrationTypeSafeDisable:
		return "Disabling TypeSafe"
	default:
		return "Updating integration"
	}
}

func integrationOperationNotice(operation capability.ID) string {
	switch operation {
	case capability.IntegrationRTKEnable:
		return "RTK enabled"
	case capability.IntegrationRTKDisable:
		return "RTK disabled"
	case capability.IntegrationRTKInstall:
		return "RTK installed"
	case capability.IntegrationRTKInstallGlobal:
		return "Global RTK checked"
	case capability.IntegrationCodeGraphInstall:
		return "CodeGraph installed"
	case capability.IntegrationCodeGraphInstallGlobal:
		return "Global CodeGraph checked"
	case capability.IntegrationCFProbe:
		return "cf-tunnel probe completed"
	case capability.IntegrationCFInstall:
		return "Managed cf-tunnel installed"
	case capability.IntegrationCFUpdate:
		return "Managed cf-tunnel updated"
	case capability.IntegrationCFRemove:
		return "Managed cf-tunnel removed"
	case capability.IntegrationCodeGraphWorkspaceInit:
		return "CodeGraph workspace initialized"
	case capability.IntegrationCodeGraphWorkspaceSync:
		return "CodeGraph workspace synced"
	case capability.IntegrationTypeSafeEnable:
		return "TypeSafe enabled"
	case capability.IntegrationTypeSafeDisable:
		return "TypeSafe disabled"
	default:
		return "Integration updated"
	}
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
