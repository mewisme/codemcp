package telegram

import (
	"strings"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/productadapter"
)

func ProductReachabilityDescriptors() []productadapter.Descriptor {
	live := make([]productadapter.Descriptor, 0)
	for _, item := range capability.TelegramRolloutInventory() {
		if item.State != capability.TelegramRolloutLive || item.Operation == "" {
			continue
		}
		spec, ok := capability.Lookup(item.Operation)
		if !ok || (spec.Audience != capability.AudienceOperator && spec.Audience != capability.AudienceReviewer) {
			continue
		}
		contract, ok := spec.Surface(capability.SurfaceTelegram)
		if !ok || contract.State != capability.SurfaceRequired {
			continue
		}
		route := telegramOperationRoute(item.Operation)
		if route == "" {
			continue
		}
		command, ok := commandForRoute(route)
		if !ok {
			continue
		}
		discovery := []productadapter.EntryPoint{
			{Kind: productadapter.EntryCommand, Value: "/" + command.Name},
			{Kind: productadapter.EntryRoute, Value: string(route)},
		}
		dispatch := []productadapter.EntryPoint{
			{Kind: productadapter.EntryCallback, Value: "signed-state:" + string(item.Operation)},
			{Kind: productadapter.EntryDispatch, Value: "telegram.Interface.dispatch"},
		}
		if input := telegramOperationInput(item.Operation); input != "" {
			discovery = append(discovery, productadapter.EntryPoint{Kind: productadapter.EntryInput, Value: input})
			dispatch = append(dispatch, productadapter.EntryPoint{Kind: productadapter.EntryInput, Value: input})
		}
		if telegramMiniAppRead(item.Operation) {
			discovery = append(discovery, productadapter.EntryPoint{Kind: productadapter.EntryMiniAppRead, Value: "logs-mini-app"})
			dispatch = []productadapter.EntryPoint{{Kind: productadapter.EntryMiniAppRead, Value: "logs-mini-app"}}
		}
		descriptor := productadapter.Live(capability.SurfaceTelegram, item.Operation, discovery, dispatch)
		descriptor.Parent = productadapter.DefaultParent(item.Operation)
		if spec.Effects.Destructive && spec.Confirmation.Mode != capability.ConfirmationNone {
			descriptor.ConfirmationConsumed = requiresExplicitConfirmation(spec)
		}
		live = append(live, descriptor)
	}
	return productadapter.Complete(capability.SurfaceTelegram, live)
}

func telegramOperationRoute(operation capability.ID) Route {
	value := string(operation)
	switch {
	case operation == capability.StatusOverview:
		return RouteStatus
	case strings.HasPrefix(value, "workspace."), strings.HasPrefix(value, "process."):
		return RouteWorkspaces
	case strings.HasPrefix(value, "request."):
		return RouteRequests
	case strings.HasPrefix(value, "completion."):
		return RouteCompletions
	case strings.HasPrefix(value, "upstream."), strings.HasPrefix(value, "tunnel."), strings.HasPrefix(value, "network."):
		return RouteNetwork
	case strings.HasPrefix(value, "integration."):
		return RouteIntegrations
	case strings.HasPrefix(value, "llm."):
		return RouteLLM
	case strings.HasPrefix(value, "config."), strings.HasPrefix(value, "telemetry."), strings.HasPrefix(value, "auth."), strings.HasPrefix(value, "notification."):
		return RouteSettings
	case strings.HasPrefix(value, "runtime."), strings.HasPrefix(value, "update."), strings.HasPrefix(value, "install."),
		strings.HasPrefix(value, "doctor."), strings.HasPrefix(value, "health."), strings.HasPrefix(value, "version."),
		strings.HasPrefix(value, "tools."):
		return RouteSystem
	case strings.HasPrefix(value, "instructions."), strings.HasPrefix(value, "project.context."), strings.HasPrefix(value, "prompt."):
		return RouteInstructions
	case strings.HasPrefix(value, "logs."), strings.HasPrefix(value, "execution."), strings.HasPrefix(value, "activity."):
		return RouteLogs
	default:
		return ""
	}
}

func commandForRoute(route Route) (Command, bool) {
	for _, command := range Commands() {
		if command.Route == route {
			return command, true
		}
	}
	return Command{}, false
}

func telegramOperationInput(operation capability.ID) string {
	switch operation {
	case capability.WorkspaceRegister:
		return inputWorkspaceRegister
	case capability.WorkspaceRelocate:
		return inputWorkspaceRelocate
	case capability.WorkspaceAccessAdd:
		return inputWorkspaceAccessAdd
	case capability.LLMProviderAdd:
		return inputLLMProviderAdd
	case capability.LLMProviderConfigure:
		return inputLLMProviderConfigure
	case capability.LLMProviderModels:
		return inputLLMModelSearch
	case capability.LLMProviderCredentialSet:
		return inputLLMCredentialSet
	case capability.TunnelConfigure:
		return inputTunnelConfigure
	case capability.TunnelAdminKeySet:
		return inputTunnelAdminKey
	case capability.ConfigPatch:
		return inputConfigPatch
	case capability.UpstreamAuthLogin:
		return inputUpstreamOAuthLogin
	case capability.TunnelCreate:
		return inputManagedTunnelCreate
	case capability.TunnelUpdate:
		return inputManagedTunnelUpdate
	default:
		return ""
	}
}

func telegramMiniAppRead(operation capability.ID) bool {
	switch operation {
	case capability.LogsRead, capability.LogsFollow,
		capability.ExecutionList, capability.ExecutionView, capability.ExecutionFeed, capability.ExecutionStream,
		capability.ActivityStream, capability.ActivityView:
		return true
	default:
		return false
	}
}
