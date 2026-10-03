package config

type DefaultOnCapabilityContract struct {
	Key                         string
	FreshDefault                bool
	Prerequisite                string
	ConfiguredState             string
	EffectiveState              string
	MissingPrerequisiteBehavior string
	ExplicitDisableBehavior     string
}

var defaultOnCapabilityContracts = []DefaultOnCapabilityContract{
	{
		Key: "telegram.enabled", FreshDefault: true,
		Prerequisite:                "managed bot token and at least one authorized private user",
		ConfiguredState:             "Telegram credentials and authorization are configured",
		EffectiveState:              "Telegram bot runtime is running",
		MissingPrerequisiteBehavior: "retain enabled intent and report setup required without authorizing or starting the bot",
		ExplicitDisableBehavior:     "stop the Telegram runtime while preserving unrelated configuration",
	},
	{
		Key: "telegram.topics_enabled", FreshDefault: true,
		Prerequisite:                "effective Telegram runtime and Bot API private-topic support",
		ConfiguredState:             "Telegram is configured and topic routing is requested",
		EffectiveState:              "managed private-chat topic routing is available",
		MissingPrerequisiteBehavior: "fall back to General private chat without disabling Telegram",
		ExplicitDisableBehavior:     "route Telegram delivery through General chat without managed topics",
	},
	{
		Key: "telegram.logs_mini_app.enabled", FreshDefault: true,
		Prerequisite:                "authorized Telegram user and an available cf-tunnel integration",
		ConfiguredState:             "Logs Mini App ingress is requested",
		EffectiveState:              "logs-only listener and ephemeral public tunnel are running",
		MissingPrerequisiteBehavior: "keep only the Logs Mini App unavailable while the rest of Telegram remains usable",
		ExplicitDisableBehavior:     "do not expose the Logs Mini App listener or public tunnel",
	},
	{
		Key: "approval.semantic.enabled", FreshDefault: true,
		Prerequisite:                "configured semantic provider capable of risk classification",
		ConfiguredState:             "semantic classification policy is enabled",
		EffectiveState:              "eligible mutations can be classified by the selected provider",
		MissingPrerequisiteBehavior: "follow the configured fail mode without weakening deterministic approval policy",
		ExplicitDisableBehavior:     "use deterministic native approval policy without semantic classification",
	},
	{
		Key: "permissions.mcp_config_read", FreshDefault: true,
		Prerequisite:                "authenticated agent access to the existing MCP configuration tool surface",
		ConfiguredState:             "agent configuration-read eligibility is enabled",
		EffectiveState:              "bounded non-secret global setting projection is readable",
		MissingPrerequisiteBehavior: "remain enabled but expose nothing when the MCP tool surface is unavailable",
		ExplicitDisableBehavior:     "deny agent-facing global configuration reads",
	},
	{
		Key: "permissions.mcp_config_write", FreshDefault: true,
		Prerequisite:                "canonical local approval path for an eligible config_set mutation",
		ConfiguredState:             "agent configuration-write eligibility is enabled",
		EffectiveState:              "approved eligible settings can reach the canonical mutation owner",
		MissingPrerequisiteBehavior: "keep eligibility without bypassing approval, reviewer, or protected-secret requirements",
		ExplicitDisableBehavior:     "deny agent-facing global configuration mutations",
	},
	{
		Key: "notifications.approval.enabled", FreshDefault: true,
		Prerequisite:                "at least one available configured notification provider",
		ConfiguredState:             "approval notification delivery is enabled",
		EffectiveState:              "pending and resolved approval events are offered to available providers",
		MissingPrerequisiteBehavior: "preserve canonical approval state and report delivery as unavailable",
		ExplicitDisableBehavior:     "suppress approval notification delivery without hiding approval review state",
	},
	{
		Key: "notifications.approval.telegram_enabled", FreshDefault: true,
		Prerequisite:                "available authorized Telegram notification transport",
		ConfiguredState:             "Telegram approval delivery is enabled",
		EffectiveState:              "approval notifications can be delivered through Telegram",
		MissingPrerequisiteBehavior: "skip Telegram delivery without changing approval state",
		ExplicitDisableBehavior:     "exclude Telegram from approval notification delivery",
	},
	{
		Key: "notifications.completion.enabled", FreshDefault: true,
		Prerequisite:                "at least one available configured notification provider",
		ConfiguredState:             "completion notification delivery is enabled",
		EffectiveState:              "accepted completion records are offered to available providers",
		MissingPrerequisiteBehavior: "preserve durable completion truth and report delivery as unavailable",
		ExplicitDisableBehavior:     "suppress completion notification delivery without changing completion records",
	},
	{
		Key: "notifications.completion.telegram_enabled", FreshDefault: true,
		Prerequisite:                "available authorized Telegram notification transport",
		ConfiguredState:             "Telegram completion delivery is enabled",
		EffectiveState:              "completion notifications can be delivered through Telegram",
		MissingPrerequisiteBehavior: "skip Telegram delivery without changing completion truth",
		ExplicitDisableBehavior:     "exclude Telegram from completion notification delivery",
	},
	{
		Key: "integrations.typesafe.enabled", FreshDefault: true,
		Prerequisite:                "configured TypeSafe credential and reachable provider when a consumer makes a request",
		ConfiguredState:             "TypeSafe provider resolution is enabled",
		EffectiveState:              "semantic requests can reach the configured TypeSafe provider",
		MissingPrerequisiteBehavior: "report provider unavailability without preventing unrelated runtime operation",
		ExplicitDisableBehavior:     "do not select or invoke TypeSafe as a semantic provider",
	},
	{
		Key: "integrations.browser.enabled", FreshDefault: true,
		Prerequisite:                "a locally usable Chrome, Chromium, or Edge browser with a graphical route",
		ConfiguredState:             "browser capability detection is enabled",
		EffectiveState:              "a usable browser route is available to CodeMCP integrations",
		MissingPrerequisiteBehavior: "retain enabled intent and report browser capability unavailable without degrading unrelated runtime health",
		ExplicitDisableBehavior:     "skip browser discovery and browser-backed integrations without modifying installed browsers or profiles",
	},
	{
		Key: "integrations.chatgpt_web.enabled", FreshDefault: true,
		Prerequisite:                "usable browser capability, authenticated CodeMCP browser profile, and configured CodeMCP connector route",
		ConfiguredState:             "ChatGPT Web browser-agent integration is enabled",
		EffectiveState:              "authenticated Temporary Chat surfaces can be used by browser-backed agents",
		MissingPrerequisiteBehavior: "retain enabled intent and report setup required without degrading unrelated runtime health",
		ExplicitDisableBehavior:     "do not create or use ChatGPT Web browser-agent surfaces",
	},
	{
		Key: "tunnel.enabled", FreshDefault: true,
		Prerequisite:                "tunnel ID and managed runtime API key",
		ConfiguredState:             "Secure MCP Tunnel transport is requested",
		EffectiveState:              "remote tunnel session is connected and usable",
		MissingPrerequisiteBehavior: "retain enabled intent while the tunnel remains inactive and setup-required",
		ExplicitDisableBehavior:     "do not establish a Secure MCP Tunnel session",
	},
}

func DefaultOnCapabilityContracts() []DefaultOnCapabilityContract {
	return append([]DefaultOnCapabilityContract(nil), defaultOnCapabilityContracts...)
}
