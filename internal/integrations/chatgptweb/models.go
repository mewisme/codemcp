package chatgptweb

import (
	"fmt"
	"strings"
)

func normalizeEffort(value string) (int, string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return -1, "", nil
	case "instant", "low":
		return 0, "Instant", nil
	case "medium":
		return 1, "Medium", nil
	case "high":
		return 2, "High", nil
	case "extra high", "extra-high", "xhigh":
		return 3, "Extra High", nil
	case "pro", "max":
		return 4, "Pro", nil
	default:
		return -1, "", fmt.Errorf("unsupported ChatGPT reasoning effort %q", value)
	}
}
