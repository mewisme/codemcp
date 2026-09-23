package page

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/tree"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/interface/tui/component"
	"go.mewis.me/codemcp/internal/projectcontext"
	"go.mewis.me/codemcp/internal/rules"
	"go.mewis.me/codemcp/internal/skills"
)

type workspaceContextPreviewTab int

const (
	workspaceContextPreviewRendered workspaceContextPreviewTab = iota
	workspaceContextPreviewSources
	workspaceContextPreviewJSON
)

var workspaceContextPreviewTabLabels = []string{"Rendered", "Sources", "JSON"}

const workspaceContextSourcePreviewLimit = 512 * 1024

type workspaceContextPreviewNodeKind uint8

const (
	workspaceContextPreviewRoot workspaceContextPreviewNodeKind = iota
	workspaceContextPreviewProvider
	workspaceContextPreviewResource
	workspaceContextPreviewPath
	workspaceContextPreviewContent
)

type workspaceContextPreviewNode struct {
	Kind     workspaceContextPreviewNodeKind
	Provider string
	Resource string
	Path     string
	Content  string
	Label    string
}

func (node workspaceContextPreviewNode) String() string { return node.Label }

type workspaceContextPreviewState struct {
	result       *projectcontext.Result
	tab          workspaceContextPreviewTab
	rendered     component.MarkdownViewer
	sources      tree.Model
	json         component.CodeViewer
	sourceViewer *component.MarkdownViewer
	sourcePath   string
	help         component.HelpFooter
	isDark       bool
}

type workspaceContextPreviewTabMsg struct{ Tab workspaceContextPreviewTab }
type workspaceContextPreviewWheelMsg int

func (page *WorkspacePage) syncWorkspaceContextPreview() {
	state := &workspaceContextPreviewState{isDark: true, tab: workspaceContextPreviewTabFromSession(page.contextSession)}
	state.help = component.NewHelpFooter(
		component.Binding([]string{"1"}, "1", "rendered"),
		component.Binding([]string{"2"}, "2", "sources"),
		component.Binding([]string{"3"}, "3", "json"),
		component.Binding([]string{"e"}, "e", "configure"),
		component.Binding([]string{"r"}, "r", "rebuild"),
	)
	if page.contextSession == nil || page.contextSession.Result == nil {
		page.contextPreview = state
		return
	}
	result := *page.contextSession.Result
	state.result = &result
	state.rendered = component.NewMarkdownViewer(result.InstructionContext.InstructionsText)
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		encoded = []byte(fmt.Sprintf(`{"error":%q}`, err.Error()))
	}
	state.json = component.NewCodeViewerLanguage(string(encoded), "json")
	state.sources = newWorkspaceContextSourceTree(result, state.isDark)
	page.contextPreview = state
	if page.width > 0 && page.height > 0 {
		page.resizeWorkspaceContextPreview(page.width, page.height)
	}
}

func workspaceContextPreviewTabFromSession(session *WorkspaceContextSession) workspaceContextPreviewTab {
	if session == nil {
		return workspaceContextPreviewRendered
	}
	switch strings.ToLower(strings.TrimSpace(session.PreviewTab)) {
	case "sources":
		return workspaceContextPreviewSources
	case "json":
		return workspaceContextPreviewJSON
	default:
		return workspaceContextPreviewRendered
	}
}

func (page *WorkspacePage) setWorkspaceContextPreviewTab(tab workspaceContextPreviewTab) {
	if page == nil || page.contextPreview == nil || int(tab) < 0 || int(tab) >= len(workspaceContextPreviewTabLabels) {
		return
	}
	page.contextPreview.tab = tab
	if page.contextSession != nil {
		page.contextSession.PreviewTab = strings.ToLower(workspaceContextPreviewTabLabels[tab])
	}
}

func newWorkspaceContextSourceTree(result projectcontext.Result, isDark bool) tree.Model {
	root := tree.Root(workspaceContextPreviewNode{Kind: workspaceContextPreviewRoot, Label: "Sources"}).Open()
	context := result.InstructionContext
	userSources := tree.Root(workspaceContextPreviewNode{Kind: workspaceContextPreviewResource, Resource: "user-sources", Label: "User-level Sources"}).Open()
	groups := map[string][]instructioncontext.SourceSnapshot{}
	for _, source := range context.Sources {
		provider := strings.TrimSpace(source.Provider)
		if provider == "" {
			provider = "unknown"
		}
		groups[provider] = append(groups[provider], source)
	}
	providers := make([]string, 0, len(groups))
	for provider := range groups {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	for _, provider := range providers {
		providerNode := tree.Root(workspaceContextPreviewNode{Kind: workspaceContextPreviewProvider, Provider: provider, Label: providerLabel(provider)}).Open()
		values := groups[provider]
		sort.SliceStable(values, func(i, j int) bool { return values[i].Kind < values[j].Kind })
		for _, source := range values {
			state := "disabled"
			if source.Enabled {
				state = "detected"
				if source.Loaded {
					state = "included"
				}
			}
			label := fmt.Sprintf("%s · %d · %s", sourceResourceLabelString(source.Kind), source.Count, state)
			resourceNode := tree.Root(workspaceContextPreviewNode{Kind: workspaceContextPreviewResource, Provider: provider, Resource: source.Kind, Label: label}).Open()
			for _, path := range source.Paths {
				resourceNode.Child(tree.Root(workspaceContextPreviewNode{Kind: workspaceContextPreviewPath, Provider: provider, Resource: source.Kind, Path: path, Label: path}))
			}
			providerNode.Child(resourceNode)
		}
		userSources.Child(providerNode)
	}
	root.Child(userSources)
	if strings.TrimSpace(context.GlobalContext) != "" {
		root.Child(tree.Root(workspaceContextPreviewNode{Kind: workspaceContextPreviewContent, Resource: "global-context", Content: context.GlobalContext, Label: fmt.Sprintf("Global Context · %s", formatInstructionBytes(len([]byte(context.GlobalContext))))}))
	}
	if context.AutoMemory.Loaded {
		label := fmt.Sprintf("Auto Memory · %d entries · %s", context.AutoMemory.Entries, formatInstructionBytes(context.AutoMemory.Bytes))
		if context.AutoMemory.Truncated {
			label += " · truncated"
		}
		root.Child(tree.Root(workspaceContextPreviewNode{Kind: workspaceContextPreviewContent, Resource: "auto-memory", Content: context.AutoMemory.Content, Label: label}))
	}
	root.Child(workspaceContextSectionTree("Project/User Instruction Files", append(append([]instructioncontext.Section(nil), context.ProjectMemory.Sections...), context.ProjectMemory.Imports...)))
	root.Child(workspaceContextRuleTree("Global Rules", context.GlobalRules))
	root.Child(workspaceContextRuleTree("Rules", context.Rules))
	root.Child(workspaceContextSkillTree(context.Skills))
	model := tree.New(root, 80, 20)
	model.SetShowHelp(true)
	model.SetAdditionalShortHelpKeys(func() []key.Binding {
		return []key.Binding{component.Binding([]string{"e"}, "e", "configure"), component.Binding([]string{"r"}, "r", "rebuild")}
	})
	applyWorkspaceContextTreeTheme(&model, isDark)
	return model
}

func workspaceContextSectionTree(label string, sections []instructioncontext.Section) *tree.Node {
	node := tree.Root(workspaceContextPreviewNode{Kind: workspaceContextPreviewResource, Resource: "instructions", Label: fmt.Sprintf("%s · %d", label, len(sections))}).Open()
	for _, section := range sections {
		itemLabel := section.Path
		if section.Truncated {
			itemLabel += " · truncated"
		}
		node.Child(tree.Root(workspaceContextPreviewNode{Kind: workspaceContextPreviewPath, Resource: string(section.Kind), Path: section.Path, Content: section.Content, Label: itemLabel}))
	}
	return node
}

func workspaceContextRuleTree(label string, values []rules.Rule) *tree.Node {
	node := tree.Root(workspaceContextPreviewNode{Kind: workspaceContextPreviewResource, Resource: strings.ToLower(strings.ReplaceAll(label, " ", "-")), Label: fmt.Sprintf("%s · %d", label, len(values))}).Open()
	for _, rule := range values {
		node.Child(tree.Root(workspaceContextPreviewNode{Kind: workspaceContextPreviewPath, Resource: "rule", Path: rule.Path, Content: rule.Content, Label: rule.Path}))
	}
	return node
}

func workspaceContextSkillTree(values []skills.Skill) *tree.Node {
	node := tree.Root(workspaceContextPreviewNode{Kind: workspaceContextPreviewResource, Resource: "skills", Label: fmt.Sprintf("Skills · %d", len(values))}).Open()
	for _, skill := range values {
		label := strings.TrimSpace(skill.Name)
		if label == "" {
			label = skill.Path
		}
		if skill.Path != "" && skill.Path != label {
			label += " · " + skill.Path
		}
		node.Child(tree.Root(workspaceContextPreviewNode{Kind: workspaceContextPreviewPath, Resource: "skill", Path: skill.Path, Label: label}))
	}
	return node
}

func sourceResourceLabelString(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "context":
		return "Context"
	case "rules":
		return "Rules"
	case "skills":
		return "Skills"
	case "global-context":
		return "Global Context"
	case "auto-memory":
		return "Auto Memory"
	default:
		return strings.TrimSpace(kind)
	}
}

func (page *WorkspacePage) handleWorkspaceContextPreviewKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	state := page.contextPreview
	if state == nil {
		return nil, false
	}
	if state.sourceViewer != nil {
		if msg.String() == "esc" {
			state.sourceViewer, state.sourcePath = nil, ""
			return nil, true
		}
		updated, cmd := state.sourceViewer.Update(msg)
		state.sourceViewer = &updated
		return cmd, true
	}
	switch msg.String() {
	case "1":
		page.setWorkspaceContextPreviewTab(workspaceContextPreviewRendered)
		return nil, true
	case "2":
		page.setWorkspaceContextPreviewTab(workspaceContextPreviewSources)
		return nil, true
	case "3":
		page.setWorkspaceContextPreviewTab(workspaceContextPreviewJSON)
		return nil, true
	case "e", "r":
		return func() tea.Msg { return NavigateMsg{Path: []string{"workspaces", page.resourceID, "context"}} }, true
	case "enter", "o":
		if state.tab == workspaceContextPreviewSources && state.result != nil {
			if page.openWorkspaceContextSelectedSource() {
				return nil, true
			}
		}
	}
	if state.result == nil {
		return nil, false
	}
	switch state.tab {
	case workspaceContextPreviewRendered:
		updated, cmd := state.rendered.Update(msg)
		state.rendered = updated
		return cmd, true
	case workspaceContextPreviewSources:
		updated, cmd := state.sources.Update(msg)
		state.sources = updated
		return cmd, true
	case workspaceContextPreviewJSON:
		updated, cmd := state.json.Update(msg)
		state.json = updated
		return cmd, true
	}
	return nil, false
}

func (page *WorkspacePage) openWorkspaceContextSelectedSource() bool {
	state := page.contextPreview
	if state == nil || state.result == nil {
		return false
	}
	selected := state.sources.NodeAtCurrentOffset()
	if selected == nil {
		return false
	}
	node, ok := selected.GivenValue().(workspaceContextPreviewNode)
	if !ok || node.Kind != workspaceContextPreviewPath && node.Kind != workspaceContextPreviewContent {
		return false
	}
	content, err := node.Content, error(nil)
	if strings.TrimSpace(content) == "" && strings.TrimSpace(node.Path) != "" {
		content, err = workspaceContextSourceContent(*state.result, node.Path)
	}
	state.sourcePath = strings.TrimSpace(node.Path)
	if state.sourcePath == "" {
		state.sourcePath = sourceResourceLabelString(node.Resource)
	}
	if err != nil {
		content = "# Source unavailable\n\n" + err.Error()
	}
	viewer := component.NewMarkdownViewer(content)
	state.sourceViewer = &viewer
	page.resizeWorkspaceContextPreview(page.width, page.height)
	return true
}

func (page *WorkspacePage) updateWorkspaceContextPreview(message tea.Msg) tea.Cmd {
	state := page.contextPreview
	if state == nil {
		return nil
	}
	if msg, ok := message.(workspaceContextPreviewTabMsg); ok {
		if int(msg.Tab) >= 0 && int(msg.Tab) < len(workspaceContextPreviewTabLabels) {
			page.setWorkspaceContextPreviewTab(msg.Tab)
			state.sourceViewer, state.sourcePath = nil, ""
		}
		return nil
	}
	if msg, ok := message.(workspaceContextPreviewWheelMsg); ok {
		if state.tab == workspaceContextPreviewSources && state.sourceViewer == nil {
			for range 3 {
				if msg < 0 {
					state.sources.Up()
				} else if msg > 0 {
					state.sources.Down()
				}
			}
		}
		return nil
	}
	if background, ok := message.(tea.BackgroundColorMsg); ok {
		state.isDark = background.IsDark()
		applyWorkspaceContextTreeTheme(&state.sources, state.isDark)
		if state.result != nil {
			updated, _ := state.rendered.Update(background)
			state.rendered = updated
		}
		if state.sourceViewer != nil {
			updated, _ := state.sourceViewer.Update(background)
			state.sourceViewer = &updated
		}
		state.help.Update(background)
		return nil
	}
	if state.sourceViewer != nil {
		updated, cmd := state.sourceViewer.Update(message)
		state.sourceViewer = &updated
		return cmd
	}
	if state.result == nil {
		return nil
	}
	switch state.tab {
	case workspaceContextPreviewRendered:
		updated, cmd := state.rendered.Update(message)
		state.rendered = updated
		return cmd
	case workspaceContextPreviewSources:
		updated, cmd := state.sources.Update(message)
		state.sources = updated
		return cmd
	case workspaceContextPreviewJSON:
		updated, cmd := state.json.Update(message)
		state.json = updated
		return cmd
	}
	return nil
}

func (page *WorkspacePage) resizeWorkspaceContextPreview(width, height int) tea.Cmd {
	state := page.contextPreview
	if state == nil {
		return nil
	}
	bodyWidth, bodyHeight := page.workspaceContextPreviewBodySize(width, height)
	if state.sourceViewer != nil {
		header := page.workspaceContextSourceViewerHeader(bodyWidth)
		state.sourceViewer.Resize(bodyWidth, max(1, bodyHeight-lipgloss.Height(header)))
		return nil
	}
	if state.result == nil {
		return nil
	}
	state.rendered.Resize(bodyWidth, bodyHeight)
	state.json.Resize(bodyWidth, bodyHeight)
	state.sources.SetSize(bodyWidth, bodyHeight)
	return nil
}

func (page *WorkspacePage) workspaceContextPreviewBodySize(width, height int) (int, int) {
	tabs := component.PageTabsNotice(workspaceContextPreviewTabLabels, int(page.contextPreview.tab), page.notice, width)
	feedback := page.listFeedback(width)
	bodyHeight := max(1, height-lipgloss.Height(tabs)-1)
	layout := page.workspaceContextPreviewSectionLayout(width, bodyHeight, feedback)
	return max(1, width), layout.BodyHeight
}

func (page *WorkspacePage) workspaceContextPreviewView(width, height int) string {
	state := page.contextPreview
	if state == nil {
		return component.StateView(component.PageError, "Project Context preview unavailable", "")
	}
	tabs := component.PageTabsNotice(workspaceContextPreviewTabLabels, int(state.tab), page.notice, width)
	feedback := page.listFeedback(width)
	bodyWidth, bodyHeight := page.workspaceContextPreviewBodySize(width, height)
	body := ""
	if state.result == nil {
		body = component.Muted("Project Context is not built. Press e to configure and build it.")
	} else if state.sourceViewer != nil {
		header := page.workspaceContextSourceViewerHeader(bodyWidth)
		state.sourceViewer.Resize(bodyWidth, max(1, bodyHeight-lipgloss.Height(header)))
		body = header + "\n" + state.sourceViewer.View()
	} else {
		switch state.tab {
		case workspaceContextPreviewRendered:
			state.rendered.Resize(bodyWidth, bodyHeight)
			body = state.rendered.View()
		case workspaceContextPreviewSources:
			state.sources.SetSize(bodyWidth, bodyHeight)
			body = state.sources.View()
		case workspaceContextPreviewJSON:
			state.json.Resize(bodyWidth, bodyHeight)
			body = state.json.View()
		}
	}
	if state.sourceViewer == nil {
		help := state.help.View(width)
		if help != "" && lipgloss.Height(body)+lipgloss.Height(help)+1 <= bodyHeight {
			body += "\n" + help
		}
	}
	layoutHeight := max(1, height-lipgloss.Height(tabs)-1)
	layout := page.workspaceContextPreviewSectionLayout(width, layoutHeight, feedback)
	return tabs + "\n" + layout.View(body)
}

func (page *WorkspacePage) workspaceContextPreviewSectionLayout(width, height int, feedback string) component.SectionLayout {
	state := page.contextPreview
	meta := ""
	if state != nil && state.result != nil {
		meta = workspaceContextPreviewSummary(*state.result, width)
	}
	return component.NewSectionLayout("", meta, feedback, width, height, 0)
}

func workspaceContextPreviewSummary(result projectcontext.Result, width int) string {
	parts := []string{
		formatInstructionBytes(result.Summary.InstructionBytes) + " instructions",
		formatInstructionBytes(result.Summary.MemoryBytes) + " memory",
		fmt.Sprintf("%d rules", result.Summary.Rules),
		fmt.Sprintf("%d skills", result.Summary.Skills),
	}
	if result.InstructionContext.InstructionTruncated {
		parts = append(parts, "truncated")
	}
	return component.WrapContent(strings.Join(parts, " · "), max(1, width))
}

func (page *WorkspacePage) workspaceContextSourceViewerHeader(width int) string {
	state := page.contextPreview
	if state == nil || state.sourceViewer == nil {
		return ""
	}
	return component.WrapKeyValue("Source", state.sourcePath, max(1, width)) + "\n" + component.Muted("Esc close")
}

func (page *WorkspacePage) workspaceContextPreviewMouseTargets(originX, originY, z int) []component.MouseTarget {
	state := page.contextPreview
	if state == nil {
		return nil
	}
	_, spans := component.PageTabsLayout(workspaceContextPreviewTabLabels, int(state.tab), page.notice, page.width)
	tabs := component.PageTabsNotice(workspaceContextPreviewTabLabels, int(state.tab), page.notice, page.width)
	tabsY := originY
	targets := make([]component.MouseTarget, 0, len(spans)+1)
	for _, span := range spans {
		tab := workspaceContextPreviewTab(span.Index)
		targets = append(targets, component.MouseTarget{
			ID: "workspace.context.tab", Rect: component.Rect{X: originX + span.X, Y: tabsY, Width: span.Width, Height: 1}, Z: z + 2,
			Handle: func(event component.MouseEvent) tea.Msg {
				if event.Button != tea.MouseLeft {
					return nil
				}
				return workspaceContextPreviewTabMsg{Tab: tab}
			},
		})
	}
	feedback := page.listFeedback(page.width)
	layoutHeight := max(1, page.height-lipgloss.Height(tabs)-1)
	layout := page.workspaceContextPreviewSectionLayout(page.width, layoutHeight, feedback)
	contentY := tabsY + lipgloss.Height(tabs) + 1 + layout.BodyY
	if state.sourceViewer != nil {
		header := page.workspaceContextSourceViewerHeader(max(1, page.width))
		return append(targets, state.sourceViewer.MouseTargets(originX, contentY+lipgloss.Height(header), z)...)
	}
	if state.result == nil {
		return targets
	}
	switch state.tab {
	case workspaceContextPreviewRendered:
		targets = append(targets, state.rendered.MouseTargets(originX, contentY, z)...)
	case workspaceContextPreviewSources:
		targets = append(targets, component.MouseTarget{
			ID: "workspace.context.sources.scroll", Rect: component.Rect{X: originX, Y: contentY, Width: max(1, page.width), Height: max(1, page.height-contentY+originY)}, Z: z,
			Handle: func(event component.MouseEvent) tea.Msg {
				switch event.Button {
				case tea.MouseWheelUp:
					return workspaceContextPreviewWheelMsg(-1)
				case tea.MouseWheelDown:
					return workspaceContextPreviewWheelMsg(1)
				default:
					return nil
				}
			},
		})
	case workspaceContextPreviewJSON:
		targets = append(targets, state.json.MouseTargets(originX, contentY, z)...)
	}
	return targets
}

func applyWorkspaceContextTreeTheme(model *tree.Model, isDark bool) {
	if model == nil {
		return
	}
	if isDark {
		model.SetStyles(tree.DefaultDarkStyles())
	} else {
		model.SetStyles(tree.DefaultLightStyles())
	}
}

func workspaceContextSourceContent(result projectcontext.Result, path string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "." || clean == "" {
		return "", fmt.Errorf("source path is empty")
	}
	for _, section := range append(append([]instructioncontext.Section(nil), result.InstructionContext.ProjectMemory.Sections...), result.InstructionContext.ProjectMemory.Imports...) {
		if filepath.Clean(section.Path) == clean {
			return section.Content, nil
		}
	}
	for _, rule := range result.InstructionContext.GlobalRules {
		if filepath.Clean(rule.Path) == clean {
			return rule.Content, nil
		}
	}
	for _, rule := range result.InstructionContext.Rules {
		if filepath.Clean(rule.Path) == clean {
			return rule.Content, nil
		}
	}
	info, err := os.Lstat(clean)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("source is not a regular file")
	}
	file, err := os.Open(clean)
	if err != nil {
		return "", err
	}
	defer file.Close()
	value, err := io.ReadAll(io.LimitReader(file, workspaceContextSourcePreviewLimit+1))
	if err != nil {
		return "", err
	}
	if len(value) > workspaceContextSourcePreviewLimit {
		value = value[:workspaceContextSourcePreviewLimit]
		return string(value) + "\n\n> Source preview truncated at 512 KiB.", nil
	}
	return string(value), nil
}
