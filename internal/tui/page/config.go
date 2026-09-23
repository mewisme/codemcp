package page

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/tui/component"
)

const configOperationTimeout = 60 * time.Second

type ConfigCommand string

const (
	ConfigRefresh        ConfigCommand = "config.refresh"
	ConfigEdit           ConfigCommand = "config.edit"
	ConfigVerify         ConfigCommand = "config.verify"
	ConfigMigrate        ConfigCommand = "config.migrate"
	ConfigMigrateSecrets ConfigCommand = "config.migrate.secrets"
	ConfigConvert        ConfigCommand = "config.convert"
	ConfigExport         ConfigCommand = "config.export"
	ConfigImport         ConfigCommand = "config.import"
)

type ConfigCommandMsg struct {
	Command    ConfigCommand
	ResourceID string
}

type configOverlay uint8

const (
	configOverlayNone configOverlay = iota
	configOverlayConfirm
	configOverlayOperation
)

type configLoadMsg struct {
	overview application.ConfigOverview
	err      error
}

type configOperationMsg struct {
	operationID uint64
	command     ConfigCommand
	mutation    application.ConfigMutationResult
	verify      config.VerifyResult
	format      configformat.Format
	converted   int
	files       int
	secrets     int
	migrated    int
	path        string
	err         error
}

type ConfigPage struct {
	ctx             context.Context
	resourceID      string
	section         string
	action          string
	overview        application.ConfigOverview
	browser         component.Browser
	detail          component.DetailPage
	loaded          bool
	loading         bool
	overlay         configOverlay
	editor          *component.Editor
	confirm         component.ConfirmButtons
	command         ConfigCommand
	targetKey       string
	fieldForm       *configFieldFormData
	convertForm     *configConvertFormData
	bundleForm      *configBundleFormData
	operationCancel context.CancelFunc
	operationID     uint64
	progress        *component.Progress
	notice          string
	err             error
	width           int
	height          int
	searching       bool
}

type configDomain struct {
	ID          string
	Title       string
	Description string
}

var configDomains = []configDomain{
	{ID: "runtime", Title: "Runtime & Network", Description: "MCP HTTP and admin server configuration"},
	{ID: "access", Title: "Access & Security", Description: "Authentication and filesystem access"},
	{ID: "shell", Title: "Shell & Execution", Description: "Approval, sandbox, environment, and network policy"},
	{ID: "features", Title: "Features", Description: "Ponytail and Caveman behavior"},
	{ID: "tunnel", Title: "Tunnel", Description: "OpenAI Secure MCP Tunnel configuration"},
	{ID: "storage", Title: "Storage & Maintenance", Description: "Storage, verification, import, export, and migration"},
}

func NewConfig(ctx context.Context) (*ConfigPage, error) {
	return NewConfigRoute(ctx, "")
}

func NewConfigRoute(ctx context.Context, resourceID string) (*ConfigPage, error) {
	return NewConfigRouteAction(ctx, resourceID, "", "")
}

func NewConfigRouteAction(ctx context.Context, resourceID, section, action string) (*ConfigPage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	page := &ConfigPage{ctx: ctx, resourceID: strings.TrimSpace(resourceID), section: strings.TrimSpace(section), action: strings.TrimSpace(action)}
	page.rebuildBrowser("")
	return page, nil
}

func (page *ConfigPage) Init() tea.Cmd {
	if page == nil {
		return nil
	}
	page.loading = true
	return page.loadCmd()
}

func (page *ConfigPage) OverlayActive() bool {
	return page != nil && page.overlay != configOverlayNone
}

func (page *ConfigPage) InputActive() bool {
	return page != nil && (page.editor != nil || page.isBrowserRoute() && page.browser.InputActive())
}

func (page *ConfigPage) Dirty() bool { return page != nil && page.editor != nil && page.editor.Dirty() }
func (page *ConfigPage) Submitting() bool {
	return page != nil && page.editor != nil && page.editor.Submitting()
}

func (page *ConfigPage) Notice() string {
	if page == nil {
		return ""
	}
	return page.notice
}

func (page *ConfigPage) SetNotice(value string) {
	if page != nil {
		page.notice = strings.TrimSpace(value)
	}
}

func (page *ConfigPage) Update(message tea.Msg) (Model, tea.Cmd) {
	if page == nil {
		return page, nil
	}
	switch msg := message.(type) {
	case configLoadMsg:
		page.loading = false
		if msg.err != nil {
			page.err = msg.err
			return page, nil
		}
		page.loaded, page.err = true, nil
		page.overview = msg.overview
		if page.action != "" && page.editor == nil {
			if err := page.initConfigEditor(); err != nil {
				page.err = err
				return page, nil
			}
			return page, page.editor.Init()
		}
		if page.isFieldRoute() {
			page.syncDetail()
		} else {
			page.rebuildBrowser(page.selectedKey())
		}
		return page, nil
	case configOperationMsg:
		return page, page.finishOperation(msg)
	case tea.WindowSizeMsg:
		page.width, page.height = msg.Width, msg.Height
		if page.editor != nil {
			page.resizeConfigEditor()
			return page, nil
		}
		var browserCmd tea.Cmd
		if page.isFieldRoute() {
			page.detail.Resize(msg.Width, msg.Height)
		} else {
			browserCmd = page.resizeBrowser()
		}
		return page, browserCmd
	case component.EditorSubmitMsg:
		return page, page.submitConfigEditor()
	case component.EditorCancelMsg:
		return page, page.configEditorParentNavigation()
	case component.ConfirmChoiceMsg:
		if page.overlay == configOverlayConfirm {
			page.confirm.Select(msg.Affirmative)
			return page, page.updateConfigImportConfirm(tea.KeyPressMsg{Code: tea.KeyEnter})
		}
		return page, nil
	case ConfigCommandMsg:
		cmd, err := page.openCommand(msg.Command, msg.ResourceID)
		if err != nil {
			page.err = err
		}
		return page, cmd
	case component.BrowserOpenMsg:
		if page.searching && msg.Row.ID != "" {
			page.searching = false
			return page, func() tea.Msg { return NavigateMsg{Path: []string{"config", msg.Row.ID}} }
		}
		if msg.Row.ID != "" && page.isStorageRoute() {
			command, ok := configMaintenanceCommand(msg.Row.ID)
			if !ok {
				return page, nil
			}
			cmd, err := page.openCommand(command, "")
			if err != nil {
				page.err = err
			}
			return page, cmd
		}
		if msg.Row.ID != "" && page.isBrowserRoute() {
			if page.resourceID == "" {
				return page, func() tea.Msg { return NavigateMsg{Path: []string{"config", msg.Row.ID}} }
			}
			return page, func() tea.Msg { return NavigateMsg{Path: []string{"config", msg.Row.ID}} }
		}
		return page, nil
	case tea.KeyPressMsg:
		if page.overlay == configOverlayOperation {
			if msg.String() == "esc" {
				page.cancelOperation()
				return page, nil
			}
			if page.progress != nil {
				updated, cmd := page.progress.Update(msg)
				page.progress = &updated
				return page, cmd
			}
			return page, nil
		}
		if page.overlay == configOverlayConfirm {
			return page, page.updateConfigImportConfirm(msg)
		}
		if page.editor != nil {
			updated, cmd := page.editor.Update(msg)
			page.editor = &updated
			return page, cmd
		}
		if page.isBrowserRoute() && page.browser.InputActive() {
			updated, cmd := page.browser.Update(msg)
			page.browser = updated.(component.Browser)
			return page, cmd
		}
		if page.isFieldRoute() {
			updated, cmd := page.detail.Update(msg)
			page.detail = updated
			return page, cmd
		} else if cmd, handled := page.handleKey(msg); handled {
			return page, cmd
		}
	}
	if page.editor != nil {
		updated, cmd := page.editor.Update(message)
		page.editor = &updated
		return page, cmd
	}
	if page.isFieldRoute() {
		updated, cmd := page.detail.Update(message)
		page.detail = updated
		return page, cmd
	}
	updated, cmd := page.browser.Update(message)
	page.browser = updated.(component.Browser)
	return page, cmd
}

func (page *ConfigPage) View(width, height int) string {
	if page == nil {
		return component.StateView(component.PageError, "Config page unavailable", "")
	}
	page.width, page.height = width, height
	if !page.loaded && page.loading {
		return component.StateView(component.PageLoading, "Loading configuration", "")
	}
	var content string
	if page.editor != nil {
		content = page.configEditorView(width, height)
	} else if page.isFieldRoute() {
		page.detail.SetFeedback(page.notice, page.err)
		page.detail.Resize(width, height)
		content = page.detail.View()
	} else {
		header := page.browserHeaderView(width)
		feedback := page.browserFeedback(width)
		headerHeight := lipgloss.Height(header)
		browserHeight := max(1, height-headerHeight-pageFeedbackHeight(feedback))
		updated, _ := page.browser.Update(tea.WindowSizeMsg{Width: width, Height: browserHeight})
		page.browser = updated.(component.Browser)
		content = header + "\n" + prependPageFeedback(feedback, page.browser.Content())
	}
	switch page.overlay {
	case configOverlayConfirm:
		modalWidth := overlayWidth(width, 76)
		content = component.CenterOverlay(content, component.Modal(page.configImportConfirmBody(modalWidth), modalWidth), width, height)
	case configOverlayOperation:
		body := ""
		if page.progress != nil {
			body = page.progress.View()
		}
		body += "\n\n" + component.Muted("Esc cancel")
		content = component.CenterOverlay(content, component.Modal(body, overlayWidth(width, 72)), width, height)
	}
	return content
}

func (page *ConfigPage) MouseTargets(originX, originY, z int) []component.MouseTarget {
	if page == nil {
		return nil
	}
	switch page.overlay {
	case configOverlayConfirm:
		modalWidth := overlayWidth(page.width, 76)
		body := page.configImportConfirmBody(modalWidth)
		foreground := component.Modal(body, modalWidth)
		targets, x, y := component.CenteredOverlayTargets(foreground, page.width, page.height, originX, originY, z+20, tea.KeyPressMsg{Code: tea.KeyEscape})
		if rect, ok := component.FindRenderedRect(foreground, page.confirm.View()); ok {
			targets = append(targets, page.confirm.MouseTargets(x+rect.X, y+rect.Y, z+22)...)
		}
		return targets
	case configOverlayOperation:
		return []component.MouseTarget{mouseBlocker(originX, originY, page.width, page.height, z+20)}
	default:
		if page.editor != nil {
			return page.configEditorMouseTargets(originX, originY, z)
		}
		if page.isFieldRoute() {
			return page.detail.MouseTargets(originX, originY, z)
		}
		feedback := page.browserFeedback(page.width)
		offsetY := lipgloss.Height(page.browserHeaderView(page.width)) + pageFeedbackHeight(feedback)
		return page.browser.MouseTargets(originX, originY+offsetY, z)
	}
}

func (page *ConfigPage) handleKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if page.resourceID == "" && msg.String() == "s" {
		page.searching = true
		page.browser = component.NewBrowser(page.ctx, "Search configuration", page.searchRows(), nil).WithTitleVisible(false).WithHelpBindings(component.Binding([]string{"esc"}, "esc", "cancel"))
		page.browser.StartFilter()
		if page.width > 0 && page.height > 0 {
			_ = page.resizeBrowser()
		}
		return nil, true
	}
	if page.searching && msg.String() == "esc" {
		page.searching = false
		page.rebuildBrowser("")
		return nil, true
	}
	if page.isStorageRoute() && msg.String() == "enter" {
		selected, ok := page.browser.Selected()
		if !ok {
			return nil, true
		}
		command, ok := configMaintenanceCommand(selected.ID)
		if !ok {
			return nil, true
		}
		cmd, err := page.openCommand(command, "")
		if err != nil {
			page.err = err
		}
		return cmd, true
	}
	if page.isDomainRoute() && msg.String() == "e" {
		cmd, err := page.openCommand(ConfigEdit, page.selectedKey())
		if err != nil {
			page.err = err
		}
		return cmd, true
	}
	commands := map[string]ConfigCommand{"r": ConfigRefresh}
	command, ok := commands[msg.String()]
	if !ok {
		return nil, false
	}
	cmd, err := page.openCommand(command, "")
	if err != nil {
		page.err = err
	}
	return cmd, true
}

func (page *ConfigPage) openCommand(command ConfigCommand, resourceID string) (tea.Cmd, error) {
	page.err, page.notice = nil, ""
	page.command, page.targetKey = command, strings.TrimSpace(resourceID)
	switch command {
	case ConfigRefresh:
		page.loading = true
		return page.loadCmd(), nil
	case ConfigEdit:
		if !page.loaded {
			return nil, fmt.Errorf("configuration is still loading")
		}
		if page.targetKey == "" {
			page.targetKey = page.selectedKey()
		}
		if page.targetKey == "" {
			return nil, fmt.Errorf("select a configuration field first")
		}
		spec, ok := config.FieldByKey(page.targetKey)
		if !ok {
			return nil, fmt.Errorf("unknown config field: %s", page.targetKey)
		}
		if !spec.Editable {
			page.notice = spec.Guidance
			if page.notice == "" {
				page.notice = spec.Key + " is read-only"
			}
			return nil, nil
		}
		return func() tea.Msg { return NavigateMsg{Path: []string{"config", page.targetKey, "edit"}} }, nil
	case ConfigVerify:
		return page.startOperation(command, "Verifying configuration", func(context.Context) configOperationMsg {
			result, err := application.VerifyConfig()
			return configOperationMsg{command: command, verify: result, err: err}
		}), nil
	case ConfigMigrate:
		return page.startOperation(command, "Migrating stored credentials", func(context.Context) configOperationMsg {
			err := application.MigrateLegacySecrets()
			return configOperationMsg{command: command, err: err}
		}), nil
	case ConfigMigrateSecrets:
		return page.startOperation(command, "Encrypting secret files", func(context.Context) configOperationMsg {
			migrated, err := application.MigrateSecretEncryption()
			return configOperationMsg{command: command, migrated: migrated, err: err}
		}), nil
	case ConfigConvert:
		return func() tea.Msg { return NavigateMsg{Path: []string{"config", "storage", "convert"}} }, nil
	case ConfigExport:
		return func() tea.Msg { return NavigateMsg{Path: []string{"config", "storage", "export"}} }, nil
	case ConfigImport:
		return func() tea.Msg { return NavigateMsg{Path: []string{"config", "storage", "import"}} }, nil
	default:
		return nil, fmt.Errorf("unsupported config action: %s", command)
	}
}

func (page *ConfigPage) startOperation(command ConfigCommand, title string, run func(context.Context) configOperationMsg) tea.Cmd {
	ctx, cancel := context.WithTimeout(page.ctx, configOperationTimeout)
	page.operationID++
	operationID := page.operationID
	page.operationCancel = cancel
	page.command = command
	progress := component.NewProgress(title)
	page.progress = &progress
	page.overlay = configOverlayOperation
	page.err = nil
	return func() tea.Msg {
		message := run(ctx)
		message.operationID = operationID
		return message
	}
}

func (page *ConfigPage) finishOperation(msg configOperationMsg) tea.Cmd {
	if msg.operationID != 0 && msg.operationID != page.operationID {
		return nil
	}
	if page.operationCancel != nil {
		page.operationCancel()
	}
	page.operationCancel = nil
	page.overlay, page.progress = configOverlayNone, nil
	if msg.err != nil {
		if page.editor != nil {
			page.editor.SetSubmitting(false)
			page.editor.SetFeedback("", msg.err)
			page.err = nil
		} else {
			page.err = msg.err
		}
		return nil
	}
	page.err = nil
	switch msg.command {
	case ConfigEdit:
		page.overview.Config = msg.mutation.Config
		if fingerprint, err := config.RuntimeFingerprint(msg.mutation.Config); err == nil {
			page.overview.RuntimeSync.PersistedFingerprint = fingerprint
			if msg.mutation.RuntimeReloaded {
				page.overview.RuntimeRunning = true
				page.overview.RuntimeSync.State = application.ConfigRuntimeCurrent
				page.overview.RuntimeSync.RuntimeFingerprint = fingerprint
			} else {
				page.overview.RuntimeSync.State = application.ConfigRuntimeStopped
			}
		}
		page.notice = application.ConfigOperationNotice(msg.mutation.RuntimeReloaded)
	case ConfigVerify:
		page.notice = fmt.Sprintf("Configuration verified · %s · %d structured files", msg.verify.Format, msg.verify.Files)
	case ConfigMigrate:
		page.notice = "Legacy credentials migrated to the secret store"
	case ConfigMigrateSecrets:
		page.notice = fmt.Sprintf("Secret files encrypted at rest · %d migrated", msg.migrated)
	case ConfigConvert:
		page.notice = fmt.Sprintf("Configuration converted to %s · %d files", msg.format, msg.converted)
	case ConfigExport:
		page.notice = fmt.Sprintf("Configuration exported · %d files · %d secrets · %s", msg.files, msg.secrets, msg.path)
	case ConfigImport:
		page.notice = fmt.Sprintf("Configuration imported · %d files · %d secrets", msg.files, msg.secrets)
	}
	if page.editor != nil {
		page.editor.SetSubmitting(false)
		page.editor.Accept()
		notice := page.notice
		return tea.Batch(page.configEditorParentNavigation(), func() tea.Msg { return ToastMsg{Title: "Configuration", Message: notice, Tone: component.ToneSuccess} })
	}
	return func() tea.Msg {
		overview, err := application.LoadConfigOverview(page.ctx)
		return configLoadMsg{overview: overview, err: err}
	}
}

func (page *ConfigPage) cancelOperation() {
	if page.operationCancel != nil {
		page.operationCancel()
	}
	page.operationCancel = nil
	page.operationID++
	page.overlay, page.progress = configOverlayNone, nil
	if page.editor != nil {
		page.editor.SetSubmitting(false)
	}
	page.notice = "Configuration operation cancellation requested"
}

func (page *ConfigPage) loadCmd() tea.Cmd {
	return func() tea.Msg {
		overview, err := application.LoadConfigOverview(page.ctx)
		return configLoadMsg{overview: overview, err: err}
	}
}

func (page *ConfigPage) rebuildBrowser(selected string) {
	if page.isFieldRoute() {
		page.syncDetail()
		return
	}
	helpExpanded := page.browser.HelpExpanded()
	rows := page.configRows()
	title := "Configuration domains"
	bindings := []key.Binding{component.Binding([]string{"s"}, "s", "search"), component.Binding([]string{"r"}, "r", "refresh")}
	if page.isDomainRoute() {
		title = page.domainTitle() + " fields"
		bindings = []key.Binding{component.Binding([]string{"e"}, "e", "edit"), component.Binding([]string{"/"}, "/", "filter"), component.Binding([]string{"r"}, "r", "refresh")}
	} else if page.isStorageRoute() {
		title = "Storage & Maintenance actions"
		bindings = []key.Binding{component.Binding([]string{"enter"}, "enter", "run"), component.Binding([]string{"r"}, "r", "refresh")}
	}
	page.browser = component.NewBrowser(page.ctx, title, rows, nil).WithTitleVisible(false).WithHelpBindings(bindings...)
	page.browser.SetHelpExpanded(helpExpanded)
	if page.width > 0 && page.height > 0 {
		_ = page.resizeBrowser()
	}
	if selected != "" {
		page.browser.SelectID(selected)
	}
}

func (page *ConfigPage) searchRows() []component.Row {
	rows := make([]component.Row, 0, len(config.Fields()))
	for _, spec := range config.Fields() {
		value := "loading"
		state := config.FieldStateDefault
		if page.loaded {
			if display, err := config.DisplayValue(page.overview.Config, spec); err == nil {
				value = display
			}
			if current, err := config.State(page.overview.Config, spec); err == nil {
				state = current
			}
		}
		rows = append(rows, component.Row{ID: spec.Key, Title: spec.Label, Description: page.sectionLabel(spec.Section) + " · " + spec.Description, Meta: value + " · " + string(state), Search: strings.Join(append([]string{spec.Label, spec.Key, spec.Description, value, string(state), string(spec.Section)}, spec.Options...), " ")})
	}
	return rows
}

func (page *ConfigPage) sectionLabel(section config.FieldSection) string {
	for _, domain := range configDomains {
		candidate, ok := configSectionForRoute(domain.ID)
		if ok && candidate == section {
			return domain.Title
		}
	}
	return string(section)
}

func (page *ConfigPage) resizeBrowser() tea.Cmd {
	headerHeight := lipgloss.Height(page.browserHeaderView(page.width))
	feedback := page.browserFeedback(page.width)
	height := max(1, page.height-headerHeight-pageFeedbackHeight(feedback))
	updated, cmd := page.browser.Update(tea.WindowSizeMsg{Width: page.width, Height: height})
	page.browser = updated.(component.Browser)
	return cmd
}

func (page *ConfigPage) browserHeaderView(width int) string {
	_, overview := page.browserHeader(width)
	if page.resourceID != "" {
		return overview
	}
	return component.PageTitleNotice("Configuration", page.notice, width) + "\n" + overview
}

func (page *ConfigPage) browserFeedback(width int) string {
	feedback := ""
	if page.err != nil {
		feedback = component.BannerWidth(page.err.Error(), component.ToneDanger, width)
	}
	if page.resourceID != "" && strings.TrimSpace(page.notice) != "" {
		feedback = prependPageFeedback(feedback, component.BannerWidth(page.notice, component.ToneSuccess, width))
	}
	if warning := page.securityWarningBanner(width); warning != "" {
		feedback = prependPageFeedback(feedback, warning)
	}
	return feedback
}

func (page *ConfigPage) browserHeader(width int) (string, string) {
	pageTitle := "Configuration"
	overview := page.overviewView(width)
	if page.isDomainRoute() {
		return page.domainTitle(), component.WrapKeyValue("", page.domainSummary(page.resourceID), width)
	}
	if page.isStorageRoute() {
		return "Storage & Maintenance", page.storageOverview(width)
	}
	return pageTitle, overview
}

func (page *ConfigPage) configRows() []component.Row {
	if page.isStorageRoute() {
		return page.storageRows()
	}
	if page.isDomainRoute() {
		return page.domainRows()
	}
	rows := make([]component.Row, 0, len(configDomains))
	for _, domain := range configDomains {
		summary := "loading"
		if page.loaded {
			summary = page.domainSummary(domain.ID)
		}
		rows = append(rows, component.Row{ID: domain.ID, Title: domain.Title, Description: domain.Description, Meta: summary, Search: domain.Title + " " + domain.Description + " " + summary})
	}
	return rows
}

func (page *ConfigPage) storageRows() []component.Row {
	return []component.Row{
		{ID: "verify", Title: "Verify configuration", Description: "Validate stored configuration and structured files", Meta: string(page.overview.Source.Format)},
		{ID: "migrate", Title: "Migrate legacy credentials", Description: "Move legacy credentials into secret store", Meta: "credential maintenance"},
		{ID: "migrate-secrets", Title: "Encrypt secret files", Description: "Encrypt plaintext secret-store files at rest", Meta: "at-rest encryption"},
		{ID: "convert", Title: "Convert storage format", Description: "Convert persisted configuration format", Meta: string(page.overview.Source.Format)},
		{ID: "export", Title: "Export configuration bundle", Description: "Export configuration and managed secrets", Meta: "bundle"},
		{ID: "import", Title: "Import configuration bundle", Description: "Import configuration and managed secrets", Meta: "bundle"},
	}
}

func configMaintenanceCommand(id string) (ConfigCommand, bool) {
	switch id {
	case "verify":
		return ConfigVerify, true
	case "migrate":
		return ConfigMigrate, true
	case "migrate-secrets":
		return ConfigMigrateSecrets, true
	case "convert":
		return ConfigConvert, true
	case "export":
		return ConfigExport, true
	case "import":
		return ConfigImport, true
	default:
		return "", false
	}
}

func (page *ConfigPage) domainRows() []component.Row {
	section, ok := configSectionForRoute(page.resourceID)
	if !ok {
		return nil
	}
	rows := make([]component.Row, 0)
	for _, spec := range config.Fields() {
		if spec.Section != section {
			continue
		}
		value := "loading"
		state := config.FieldStateDefault
		if page.loaded {
			if display, err := config.DisplayValue(page.overview.Config, spec); err == nil {
				value = display
			}
			if current, err := config.State(page.overview.Config, spec); err == nil {
				state = current
			}
		}
		rows = append(rows, component.Row{ID: spec.Key, Title: spec.Label, Description: spec.Description, Meta: value + " · " + string(state), Search: strings.Join(append([]string{spec.Label, spec.Key, spec.Description, value, string(state), string(spec.Section)}, spec.Options...), " ")})
	}
	return rows
}

func (page *ConfigPage) syncDetail() {
	key := strings.TrimSpace(page.resourceID)
	spec, ok := config.FieldByKey(key)
	if !ok {
		page.err = fmt.Errorf("unknown config field: %s", key)
		page.detail = component.NewDetailPage(key, "unavailable", component.Muted("Configuration field not found.")).WithTitleVisible(false)
		page.detail.SetBindings(component.DetailPageBinding{Key: "f", Desc: "refresh", Message: ConfigCommandMsg{Command: ConfigRefresh, ResourceID: key}})
		return
	}
	value := "loading"
	defaultValue := "loading"
	state := config.FieldStateDefault
	if page.loaded {
		if display, err := config.DisplayValue(page.overview.Config, spec); err == nil {
			value = display
		}
		if display, err := config.DisplayValue(config.Default(), spec); err == nil {
			defaultValue = display
		}
		if current, err := config.State(page.overview.Config, spec); err == nil {
			state = current
		}
	}
	detail := detailFields([2]string{"Key", spec.Key}, [2]string{"Value", value}, [2]string{"Default", defaultValue}, [2]string{"State", string(state)}, [2]string{"Type", string(spec.Kind)}, [2]string{"Description", spec.Description})
	if len(spec.Options) > 0 {
		detail += "\n" + detailFields([2]string{"Options", strings.Join(spec.Options, ", ")})
	}
	if spec.Guidance != "" {
		detail += "\n" + detailFields([2]string{"Guidance", spec.Guidance})
	}
	page.detail = component.NewDetailPage(spec.Label, value+" · "+string(state), detail).WithTitleVisible(false)
	bindings := []component.DetailPageBinding{{Key: "r", Desc: "refresh", Message: ConfigCommandMsg{Command: ConfigRefresh, ResourceID: spec.Key}}}
	if spec.Editable {
		bindings = append([]component.DetailPageBinding{{Key: "e", Desc: "edit", Message: ConfigCommandMsg{Command: ConfigEdit, ResourceID: spec.Key}}}, bindings...)
	}
	page.detail.SetBindings(bindings...)
	if page.width > 0 && page.height > 0 {
		page.detail.Resize(page.width, page.height)
	}
}

func (page *ConfigPage) selectedKey() string {
	selected, ok := page.browser.Selected()
	if !ok {
		return ""
	}
	return selected.ID
}

func (page *ConfigPage) isBrowserRoute() bool {
	return page.resourceID == "" || page.isDomainRoute() || page.isStorageRoute()
}

func (page *ConfigPage) isDomainRoute() bool {
	_, ok := configSectionForRoute(page.resourceID)
	return ok
}

func (page *ConfigPage) isFieldRoute() bool { return configFieldRouteCompat(page.resourceID) }

func (page *ConfigPage) isStorageRoute() bool { return page.resourceID == "storage" }

func configFieldRouteCompat(resourceID string) bool {
	if resourceID == "" {
		return false
	}
	if _, ok := configSectionForRoute(resourceID); ok || resourceID == "storage" {
		return false
	}
	_, ok := config.FieldByKey(resourceID)
	return ok
}

func configSectionForRoute(resourceID string) (config.FieldSection, bool) {
	switch strings.TrimSpace(resourceID) {
	case "runtime":
		return config.FieldSectionRuntime, true
	case "access":
		return config.FieldSectionAccess, true
	case "shell":
		return config.FieldSectionShell, true
	case "features":
		return config.FieldSectionFeatures, true
	case "tunnel":
		return config.FieldSectionTunnel, true
	default:
		return "", false
	}
}

func (page *ConfigPage) domainTitle() string {
	for _, domain := range configDomains {
		if domain.ID == page.resourceID {
			return domain.Title
		}
	}
	return "Configuration"
}

func (page *ConfigPage) overviewView(width int) string {
	if !page.loaded {
		if page.err != nil {
			return component.StateView(component.PageError, "Configuration unavailable", page.err.Error())
		}
		return component.StateView(component.PageEmpty, "Configuration not loaded", "")
	}
	status := "runtime " + string(page.overview.RuntimeSync.State)
	initialized := "no"
	if page.overview.Source.Exists {
		initialized = "yes"
	}
	return component.WrapKeyValue("", fmt.Sprintf("%s · initialized %s · %s", page.overview.Source.Format, initialized, status), width)
}

func (page *ConfigPage) storageOverview(width int) string {
	initialized := "no"
	if page.overview.Source.Exists {
		initialized = "yes"
	}
	runtime := string(page.overview.RuntimeSync.State)
	return strings.Join([]string{
		component.WrapKeyValue("Format", string(page.overview.Source.Format), width),
		component.WrapKeyValue("Config", page.overview.Source.Path, width),
		component.WrapKeyValue("Root", page.overview.Root, width),
		component.WrapKeyValue("Initialized", initialized, width),
		component.WrapKeyValue("Runtime sync", runtime, width),
		component.WrapKeyValue("Persisted", shortFingerprint(page.overview.RuntimeSync.PersistedFingerprint), width),
		component.WrapKeyValue("Runtime", shortFingerprint(page.overview.RuntimeSync.RuntimeFingerprint), width),
	}, "\n")
}

func shortFingerprint(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unavailable"
	}
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}

func (page *ConfigPage) securityWarningBanner(width int) string {
	if page == nil || !page.loaded {
		return ""
	}
	warnings := config.SecurityWarnings(page.overview.Config)
	if len(warnings) == 0 {
		return ""
	}
	banners := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		banners = append(banners, component.BannerWidth(warning, component.ToneWarning, width))
	}
	return strings.Join(banners, "\n")
}

func (page *ConfigPage) domainSummary(domain string) string {
	cfg := page.overview.Config
	switch domain {
	case "runtime":
		summary := fmt.Sprintf("MCP HTTP %s :%d · Admin %s :%d · exposure %s", configOnOff(cfg.Server.Enabled), cfg.Server.Port, configOnOff(cfg.Admin.Enabled), cfg.Admin.Port, config.NormalizeExposure(cfg.Server.Expose).Mode)
		if config.CleartextHTTPActive(cfg) {
			summary += " · CLEARTEXT HTTP"
		}
		return summary
	case "access":
		summary := fmt.Sprintf("MCP auth %s · Admin auth %s · %d extra filesystem roots", configOnOff(cfg.Auth.MCPEnabled), configOnOff(cfg.Auth.AdminEnabled), len(cfg.Permissions.AllowDirs))
		if config.UnauthenticatedLoopbackActive(cfg) {
			summary += " · UNAUTHENTICATED LOOPBACK"
		}
		return summary
	case "shell":
		if len(cfg.Shell.Path) == 0 {
			return "inherits runtime PATH · risk-based mutation approvals"
		}
		return fmt.Sprintf("%d extra PATH entries · risk-based mutation approvals", len(cfg.Shell.Path))
	case "features":
		return fmt.Sprintf("Ponytail %s · Caveman %s", configOnOff(cfg.Features.Ponytail.Active), configOnOff(cfg.Features.Caveman.Active))
	case "tunnel":
		return fmt.Sprintf("%s · runtime key %s · admin key %s", configOnOff(cfg.Tunnel.Enabled), configuredState(cfg.Tunnel.APIKey), configuredState(cfg.Tunnel.AdminKey))
	case "storage":
		return fmt.Sprintf("%s · verify / convert / import / export", page.overview.Source.Format)
	default:
		return ""
	}
}

func configuredState(value string) string {
	if strings.TrimSpace(value) == "" {
		return "not configured"
	}
	return "configured"
}

func configOnOff(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}
