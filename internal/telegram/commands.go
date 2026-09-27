package telegram

type Command struct {
	Name        string
	Description string
	Route       Route
}

var commandRegistry = []Command{
	{Name: "start", Description: "Open CodeMCP home", Route: RouteHome},
	{Name: "home", Description: "Open CodeMCP home", Route: RouteHome},
	{Name: "status", Description: "Show CodeMCP runtime status", Route: RouteStatus},
	{Name: "commands", Description: "Show available Telegram commands", Route: RouteCommands},
	{Name: "help", Description: "Show available Telegram commands", Route: RouteCommands},
}

func Commands() []Command {
	return append([]Command(nil), commandRegistry...)
}
