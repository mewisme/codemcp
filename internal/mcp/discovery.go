package mcp

import (
	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/version"
)

const (
	SupportedProtocolVersion = "2026-07-28"
	defaultCacheTTLMS        = 0
	defaultCacheScope        = "private"
)

var ServerInstructions = instructioncontext.StaticServerInstructions()

func serverInfo() map[string]any {
	return map[string]any{"name": "chatgpt-mcp", "version": version.Version}
}

func Discover() map[string]any {
	return map[string]any{
		"supportedVersions": []string{SupportedProtocolVersion},
		"capabilities":      DefaultCapabilities(),
		"instructions":      ServerInstructions,
		"ttlMs":             defaultCacheTTLMS,
		"cacheScope":        defaultCacheScope,
		"resultType":        "complete",
		"_meta":             map[string]any{"io.modelcontextprotocol/serverInfo": serverInfo()},
	}
}
