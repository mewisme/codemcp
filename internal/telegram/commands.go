package telegram

type Command struct {
	Name        string
	Description string
	Route       Route
}

type MenuButtonType string

const (
	MenuButtonCommands MenuButtonType = "commands"
	MenuButtonDefault  MenuButtonType = "default"
)

type MenuButton struct {
	Type MenuButtonType
}

var commandRegistry = []Command{
	{Name: "start", Description: "Open CodeMCP home", Route: RouteHome},
	{Name: "home", Description: "Open CodeMCP home", Route: RouteHome},
	{Name: "status", Description: "Show CodeMCP runtime status", Route: RouteStatus},
	{Name: "workspaces", Description: "Manage registered workspaces", Route: RouteWorkspaces},
	{Name: "requests", Description: "Review pending approval requests", Route: RouteRequests},
	{Name: "network", Description: "Manage tunnel and Upstream servers", Route: RouteNetwork},
	{Name: "integrations", Description: "Manage integrations", Route: RouteIntegrations},
	{Name: "settings", Description: "Browse and edit canonical settings", Route: RouteSettings},
	{Name: "system", Description: "Inspect runtime, diagnostics, version and updates", Route: RouteSystem},
	{Name: "instructions", Description: "Inspect project context, instructions and prompts", Route: RouteInstructions},
	{Name: "logs", Description: "Open Telegram Logs Mini App", Route: RouteLogs},
	{Name: "commands", Description: "Show available Telegram commands", Route: RouteCommands},
	{Name: "help", Description: "Show available Telegram commands", Route: RouteCommands},
}

func Commands() []Command {
	return append([]Command(nil), commandRegistry...)
}
