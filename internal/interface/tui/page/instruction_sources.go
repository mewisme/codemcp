package page

import (
	"fmt"
	"sort"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/tree"
	tea "charm.land/bubbletea/v2"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/instructionpolicy"
	"go.mewis.me/codemcp/internal/interface/tui/component"
)

type instructionSourceNodeKind uint8

const (
	instructionSourceRoot instructionSourceNodeKind = iota
	instructionSourceProvider
	instructionSourceResource
	instructionSourcePath
)

type instructionSourceNode struct {
	NodeKind instructionSourceNodeKind
	Provider string
	Resource instructionpolicy.ResourceKind
	Path     string
	Label    string
}

func (node instructionSourceNode) String() string { return node.Label }

type instructionSourcesSavedMsg struct {
	settings application.InstructionSettings
	notice   string
	err      error
}

type instructionSourceWheelMsg int

func (page *InstructionPage) syncSourceTree() {
	if page == nil {
		return
	}
	providers := groupedInstructionSources(page.settings.DetectedSources)
	root := tree.Root(instructionSourceNode{NodeKind: instructionSourceRoot, Label: "Providers"}).Open()
	providerNames := make([]string, 0, len(providers))
	for provider := range providers {
		providerNames = append(providerNames, provider)
	}
	sort.Strings(providerNames)
	for _, provider := range providerNames {
		policy := page.settings.SourcePolicy[provider]
		master := sourceProviderEnabled(policy)
		providerLabel := providerLabel(provider) + " · " + sourceStateLabel(master)
		providerNode := tree.Root(instructionSourceNode{NodeKind: instructionSourceProvider, Provider: provider, Label: providerLabel}).Open()
		sources := providers[provider]
		sort.SliceStable(sources, func(i, j int) bool { return sources[i].Kind < sources[j].Kind })
		for _, source := range sources {
			resource := instructionpolicy.ResourceKind(source.Kind)
			enabled := sourceResourceEnabled(policy, resource)
			state := "disabled"
			if master && enabled {
				state = "detected"
				if source.Loaded {
					state = "included"
				}
			}
			label := fmt.Sprintf("%s · %d · %s", sourceResourceLabel(resource), source.Count, state)
			resourceNode := tree.Root(instructionSourceNode{NodeKind: instructionSourceResource, Provider: provider, Resource: resource, Label: label})
			for _, path := range source.Paths {
				resourceNode.Child(tree.Root(instructionSourceNode{NodeKind: instructionSourcePath, Provider: provider, Resource: resource, Path: path, Label: path}))
			}
			providerNode.Child(resourceNode)
		}
		root.Child(providerNode)
	}
	treeWidth, treeHeight := page.width, page.height
	if treeWidth <= 0 {
		treeWidth = 80
	}
	if treeHeight <= 0 {
		treeHeight = 20
	}
	model := tree.New(root, treeWidth, treeHeight)
	model.SetShowHelp(true)
	model.SetAdditionalShortHelpKeys(func() []key.Binding {
		return []key.Binding{
			component.Binding([]string{"space"}, "space", "toggle"),
			component.Binding([]string{"r"}, "r", "refresh"),
		}
	})
	model.SetAdditionalFullHelpKeys(func() []key.Binding {
		return []key.Binding{
			component.Binding([]string{"space"}, "space", "toggle policy"),
			component.Binding([]string{"r"}, "r", "refresh"),
		}
	})
	page.applySourceTreeTheme(&model)
	page.sources = model
	page.resizeContent()
}

func (page *InstructionPage) handleSourceKey(msg tea.KeyPressMsg) tea.Cmd {
	if page == nil || page.tab != instructionTabSources || page.saving {
		return nil
	}
	switch msg.String() {
	case "space":
		return page.toggleSelectedSourceCmd()
	case "r":
		page.err, page.notice = nil, ""
		return page.refreshCmd()
	default:
		updated, cmd := page.sources.Update(msg)
		page.sources = updated
		return cmd
	}
}

func (page *InstructionPage) toggleSelectedSourceCmd() tea.Cmd {
	selected := page.sources.NodeAtCurrentOffset()
	if selected == nil {
		return nil
	}
	value, ok := selected.GivenValue().(instructionSourceNode)
	if !ok {
		return nil
	}
	policy := page.settings.SourcePolicy[value.Provider]
	switch value.NodeKind {
	case instructionSourceProvider:
		next := !sourceProviderEnabled(policy)
		policy.Enabled = boolPointer(next)
		page.saving = true
		page.err, page.notice = nil, ""
		return page.saveSourcePolicyCmd(value.Provider, policy, fmt.Sprintf("%s source %s", providerLabel(value.Provider), sourceStateLabel(next)))
	case instructionSourceResource:
		if !sourceProviderEnabled(policy) {
			page.notice = "Enable the provider before changing resource policy"
			return nil
		}
		next := !sourceResourceEnabled(policy, value.Resource)
		switch value.Resource {
		case instructionpolicy.ResourceContext:
			policy.Context = boolPointer(next)
		case instructionpolicy.ResourceRules:
			policy.Rules = boolPointer(next)
		case instructionpolicy.ResourceSkills:
			policy.Skills = boolPointer(next)
		default:
			return nil
		}
		page.saving = true
		page.err, page.notice = nil, ""
		return page.saveSourcePolicyCmd(value.Provider, policy, fmt.Sprintf("%s %s %s", providerLabel(value.Provider), sourceResourceLabel(value.Resource), sourceStateLabel(next)))
	}
	return nil
}

func (page *InstructionPage) saveSourcePolicyCmd(provider string, policy instructionpolicy.SourcePolicy, notice string) tea.Cmd {
	service := page.service
	return func() tea.Msg {
		settings, err := service.Save(application.InstructionSettingsPatch{SourcePolicy: map[string]instructionpolicy.SourcePolicy{provider: policy}})
		return instructionSourcesSavedMsg{settings: settings, notice: notice, err: err}
	}
}

func (page *InstructionPage) sourcesView(tabs string, width, bodyHeight int) string {
	feedback := ""
	if page.err != nil {
		feedback = component.BannerWidth(page.err.Error(), component.ToneDanger, width)
	}
	layout := page.instructionSectionLayout(instructionTabSources, feedback, width, bodyHeight)
	page.sources.SetSize(width, layout.BodyHeight)
	return tabs + "\n" + layout.View(page.sources.View())
}

func (page *InstructionPage) sourceMouseTargets(originX, originY, z int) []component.MouseTarget {
	if page == nil {
		return nil
	}
	return []component.MouseTarget{{
		ID: "instruction.sources.scroll", Rect: component.Rect{X: originX, Y: originY, Width: max(1, page.width), Height: max(1, page.sources.Height())}, Z: z,
		Handle: func(event component.MouseEvent) tea.Msg {
			switch event.Button {
			case tea.MouseWheelUp:
				return instructionSourceWheelMsg(-1)
			case tea.MouseWheelDown:
				return instructionSourceWheelMsg(1)
			default:
				return nil
			}
		},
	}}
}

func (page *InstructionPage) updateSourceWheel(msg instructionSourceWheelMsg) {
	if page == nil {
		return
	}
	for range 3 {
		if msg < 0 {
			page.sources.Up()
		} else if msg > 0 {
			page.sources.Down()
		}
	}
}

func (page *InstructionPage) applySourceTreeTheme(model *tree.Model) {
	if model == nil {
		return
	}
	if page.sourceDark {
		model.SetStyles(tree.DefaultDarkStyles())
	} else {
		model.SetStyles(tree.DefaultLightStyles())
	}
}

func groupedInstructionSources(values []instructioncontext.SourceSnapshot) map[string][]instructioncontext.SourceSnapshot {
	result := map[string][]instructioncontext.SourceSnapshot{}
	for _, source := range values {
		provider := instructionpolicy.ProviderID(source.Provider)
		if provider == "" {
			continue
		}
		source.Provider = provider
		result[provider] = append(result[provider], source)
	}
	return result
}

func sourceProviderEnabled(policy instructionpolicy.SourcePolicy) bool {
	return policy.Enabled == nil || *policy.Enabled
}

func sourceResourceEnabled(policy instructionpolicy.SourcePolicy, kind instructionpolicy.ResourceKind) bool {
	var value *bool
	switch kind {
	case instructionpolicy.ResourceContext:
		value = policy.Context
	case instructionpolicy.ResourceRules:
		value = policy.Rules
	case instructionpolicy.ResourceSkills:
		value = policy.Skills
	default:
		return true
	}
	return value == nil || *value
}

func sourceResourceLabel(kind instructionpolicy.ResourceKind) string {
	switch kind {
	case instructionpolicy.ResourceContext:
		return "Context"
	case instructionpolicy.ResourceRules:
		return "Rules"
	case instructionpolicy.ResourceSkills:
		return "Skills"
	default:
		return string(kind)
	}
}

func providerLabel(provider string) string {
	switch instructionpolicy.ProviderID(provider) {
	case "agents":
		return "Agents"
	case "claude":
		return "Claude"
	case "claudes":
		return "Claudes"
	case "cursor":
		return "Cursor"
	case "codex":
		return "Codex"
	default:
		return provider
	}
}

func sourceStateLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func boolPointer(value bool) *bool { return &value }
