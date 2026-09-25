package cli

import "github.com/spf13/cobra"

func notificationSettingsCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "notification", Short: "Manage notification delivery policy"}
	cmd.AddCommand(
		notificationApprovalCommand(),
		notificationProviderCommand("desktop", "notifications.approval.desktop_enabled"),
		notificationProviderCommand("telegram", "notifications.approval.telegram_enabled"),
	)
	return cmd
}

func notificationApprovalCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "approval", Short: "Manage approval notification policy"}
	cmd.AddCommand(
		scopedToggleCommand("enable", "Enable approval notifications", "Approval notifications enabled", "notifications.approval.enabled", true),
		scopedToggleCommand("disable", "Disable approval notifications", "Approval notifications disabled", "notifications.approval.enabled", false),
		notificationApprovalEventCommand("pending", "notifications.approval.pending"),
		notificationApprovalEventCommand("resolved", "notifications.approval.resolved"),
	)
	return cmd
}

func notificationApprovalEventCommand(name, key string) *cobra.Command {
	cmd := &cobra.Command{Use: name, Short: "Manage " + name + " approval notification delivery"}
	cmd.AddCommand(
		scopedToggleCommand("enable", "Enable "+name+" approval notifications", name+" approval notifications enabled", key, true),
		scopedToggleCommand("disable", "Disable "+name+" approval notifications", name+" approval notifications disabled", key, false),
	)
	return cmd
}

func notificationProviderCommand(name, key string) *cobra.Command {
	cmd := &cobra.Command{Use: name, Short: "Manage " + name + " approval notification delivery"}
	cmd.AddCommand(
		scopedToggleCommand("enable", "Enable "+name+" approval notifications", name+" approval notifications enabled", key, true),
		scopedToggleCommand("disable", "Disable "+name+" approval notifications", name+" approval notifications disabled", key, false),
	)
	return cmd
}
