package chatgptweb

const (
	ComposerSelector              = "[data-testid=\"prompt-textarea\"], #prompt-textarea, [contenteditable=\"true\"][data-lexical-editor=\"true\"], form[data-chatgpt-composer] [data-composer-markdown][contenteditable=\"true\"][role=\"textbox\"]"
	EffortControlSelector         = "button[aria-haspopup=\"menu\"][data-tone=\"neutral\"], button[data-testid=\"model-switcher-dropdown-button\"][aria-haspopup=\"menu\"], button[data-codex-intelligence-trigger=\"true\"][data-composer-navigation-target=\"reasoning\"][aria-haspopup=\"menu\"]"
	EffortMenuSelector            = "[data-testid=\"composer-intelligence-picker-content\"], [role=\"menu\"], [role=\"group\"]"
	EffortItemSelector            = "[role=\"menuitemradio\"]"
	EffortSliderContainerSelector = "[data-model-reasoning-effort-slider], [data-model-picker-power-slider]"
	SendButtonSelector            = "[data-testid=\"send-button\"], button[type=\"submit\"]"
	StopButtonSelector            = "[data-testid=\"stop-button\"], form[data-chatgpt-composer] button[type=\"button\"][aria-label=\"Stop\"]"
	CompletionActionSelector      = "button[data-testid=\"copy-turn-action-button\"], [data-turn-key] .turn-action-controls button"
	AssistantTurnSelector         = "[data-testid^=\"conversation-turn-\"][data-turn=\"assistant\"]:not([data-turn-key] *), [data-testid^=\"conversation-turn-\"][data-message-author-role=\"assistant\"]:not([data-turn-key] *), [data-testid^=\"conversation-turn-\"]:has([data-message-author-role=\"assistant\"]):not([data-turn-key] *), [data-turn-key]:has([data-conversation-role=\"assistant\"], [data-chatgpt-agent-turn-start])"
	UserTurnSelector              = "[data-testid^=\"conversation-turn-\"][data-turn=\"user\"]:not([data-turn-key] *), [data-testid^=\"conversation-turn-\"][data-message-author-role=\"user\"]:not([data-turn-key] *), [data-testid^=\"conversation-turn-\"]:has([data-message-author-role=\"user\"]):not([data-turn-key] *), [data-turn-key]:has([data-user-message-bubble])"
	ConnectorMenuRowSelector      = ".__menu-item[tabindex=\"0\"], [data-mention-list-scroll-area] button[data-list-navigation-item=\"true\"]"
	SelectedConnectorSelector     = "[data-id^=\"plugin:\"][data-keyword], [app-mention-path^=\"app://\"][app-mention-display-name][contenteditable=\"false\"]"
)
