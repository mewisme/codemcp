package fanout

import (
	_ "embed"
	"strings"
)

//go:embed instructions.md
var baseInstructions string

func Instructions(mode Mode) string {
	if mode == Off {
		return ""
	}
	if normalized, ok := NormalizeRuntimeMode(string(mode)); ok {
		mode = normalized
	} else {
		mode = Auto
	}
	return "FANOUT MODE ACTIVE — level: " + string(mode) + "\n\n" +
		modeInstructions(mode) + "\n\n" + strings.TrimSpace(baseInstructions)
}

func modeInstructions(mode Mode) string {
	switch mode {
	case Conservative:
		return "## Strategy\n\nUse a high delegation threshold. Prefer direct work unless independent workstreams and payoff are clear. Favor read-only audit, research, and independent review; parallel mutation needs especially clear disjoint ownership."
	case Aggressive:
		return "## Strategy\n\nProactively decompose substantial work into useful independent workstreams. Use as much safe parallelism as current runtime capacity permits, but never duplicate trivial work, parallelize immediate dependencies, or overlap mutation ownership."
	default:
		return "## Strategy\n\nUse balanced delegation. Fan out when multiple meaningful workstreams are sufficiently independent that parallelism or specialization saves parent effort; otherwise work directly."
	}
}
