package tui

import (
	"strings"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/interface/tui/action"
	"go.mewis.me/codemcp/internal/productadapter"
)

func ProductReachabilityDescriptors() []productadapter.Descriptor {
	registry := defaultActionRegistry()
	live := map[capability.ID]productadapter.Descriptor{}
	for _, item := range registry.All() {
		if item.Run == nil || strings.HasSuffix(item.ID, ".external") {
			continue
		}
		operations := tuiActionOperations(item)
		for _, operation := range operations {
			spec, ok := capability.Lookup(operation)
			if !ok || (spec.Audience != capability.AudienceOperator && spec.Audience != capability.AudienceReviewer) {
				continue
			}
			contract, ok := spec.Surface(capability.SurfaceTUI)
			if !ok || contract.State != capability.SurfaceRequired {
				continue
			}
			if spec.Kind == capability.KindMutation && item.Operation != operation {
				continue
			}
			descriptor := live[operation]
			if descriptor.Operation == "" {
				descriptor = productadapter.Live(
					capability.SurfaceTUI,
					operation,
					nil,
					nil,
				)
				descriptor.Parent = productadapter.DefaultParent(operation)
			}
			descriptor.Discovery = appendUniqueEntry(descriptor.Discovery,
				productadapter.EntryPoint{Kind: productadapter.EntryAction, Value: item.ID})
			if route := tuiActionRoute(item); route != "" {
				descriptor.Discovery = appendUniqueEntry(descriptor.Discovery,
					productadapter.EntryPoint{Kind: productadapter.EntryRoute, Value: route})
			}
			descriptor.Dispatch = appendUniqueEntry(descriptor.Dispatch,
				productadapter.EntryPoint{Kind: productadapter.EntryAction, Value: item.ID})
			if item.Operation == operation {
				descriptor.Dispatch = appendUniqueEntry(descriptor.Dispatch,
					productadapter.EntryPoint{Kind: productadapter.EntryDispatch, Value: "action.operation=" + string(operation)})
			} else {
				descriptor.Dispatch = appendUniqueEntry(descriptor.Dispatch,
					productadapter.EntryPoint{Kind: productadapter.EntryRoute, Value: tuiActionRoute(item)})
			}
			if spec.Effects.Destructive && spec.Confirmation.Mode != capability.ConfirmationNone {
				descriptor.ConfirmationConsumed = tuiConsumesConfirmation(operation)
			}
			live[operation] = descriptor
		}
	}
	values := make([]productadapter.Descriptor, 0, len(live))
	for _, descriptor := range live {
		spec, _ := capability.Lookup(descriptor.Operation)
		if spec.Effects.Destructive && spec.Confirmation.Mode != capability.ConfirmationNone && !descriptor.ConfirmationConsumed {
			continue
		}
		values = append(values, descriptor)
	}
	return productadapter.Complete(capability.SurfaceTUI, values)
}

func tuiActionOperations(item action.Action) []capability.ID {
	seen := map[capability.ID]bool{}
	out := make([]capability.ID, 0, 1+len(item.Capabilities))
	if item.Operation != "" {
		seen[item.Operation] = true
		out = append(out, item.Operation)
	}
	for _, operation := range item.Capabilities {
		if operation == "" || seen[operation] {
			continue
		}
		seen[operation] = true
		out = append(out, operation)
	}
	return out
}

func tuiActionRoute(item action.Action) string {
	if route := tuiNavigationActionRoute(item.ID); route != "" {
		return route
	}
	for _, ctx := range tuiReachabilityContexts() {
		if item.IsAvailable(ctx) {
			if strings.TrimSpace(ctx.Route) == "" {
				return string(RouteHome)
			}
			return strings.TrimSpace(ctx.Route)
		}
	}
	return ""
}

func tuiNavigationActionRoute(id string) string {
	switch id {
	case "app.go.workspaces":
		return string(RouteWorkspaces)
	case "app.go.upstreams":
		return string(RouteMCP)
	case "app.go.tunnel":
		return string(RouteTunnel)
	case "app.go.tools":
		return string(RouteTools)
	case "app.go.integrations":
		return string(RouteIntegrations)
	case "app.go.doctor":
		return string(RouteDoctor)
	case "app.go.requests":
		return string(RouteRequests)
	case "app.go.llm":
		return string(RouteLLM)
	case "app.go.completions":
		return string(RouteCompletions)
	case "app.go.logs":
		return string(RouteLogs)
	case "app.go.config":
		return string(RouteConfig)
	case "app.go.instruction":
		return string(RouteInstruction)
	case "app.go.prompts":
		return string(RoutePrompts)
	case "app.go.runtime":
		return string(RouteRuntime)
	case "app.go.about":
		return string(RouteAbout)
	default:
		return ""
	}
}

func tuiReachabilityContexts() []action.Context {
	return []action.Context{
		{Route: string(RouteHome)},
		{Route: string(RouteWorkspaces)},
		{Route: string(RouteWorkspaces), ResourceID: "resource"},
		{Route: string(RouteContainers), ResourceID: "resource"},
		{Route: string(RouteMCP)},
		{Route: string(RouteMCP), ResourceID: "resource"},
		{Route: string(RouteTunnel)},
		{Route: string(RouteTools)},
		{Route: string(RouteIntegrations)},
		{Route: string(RouteDoctor)},
		{Route: string(RouteRequests)},
		{Route: string(RouteRequests), ResourceID: "resource"},
		{Route: string(RouteLLM)},
		{Route: string(RouteLLM), ResourceID: "custom-provider"},
		{Route: string(RouteCompletions)},
		{Route: string(RouteLogs)},
		{Route: string(RouteConfig)},
		{Route: string(RouteInstruction)},
		{Route: string(RoutePrompts)},
		{Route: string(RouteRuntime)},
		{Route: string(RouteAbout)},
		{Route: string(RouteExecutions), Mode: "resource"},
		{Route: string(RouteProcesses), Mode: "resource"},
	}
}

func tuiConsumesConfirmation(operation capability.ID) bool {
	switch operation {
	case capability.LogsClear,
		capability.WorkspacePurge,
		capability.WorkspaceContainerDelete,
		capability.UpstreamServerRemove,
		capability.LLMProviderRemove:
		return true
	default:
		return false
	}
}

func appendUniqueEntry(entries []productadapter.EntryPoint, entry productadapter.EntryPoint) []productadapter.EntryPoint {
	if strings.TrimSpace(entry.Value) == "" {
		return entries
	}
	for _, current := range entries {
		if current == entry {
			return entries
		}
	}
	return append(entries, entry)
}
