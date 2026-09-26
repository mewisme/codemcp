package mcp

const (
	SupportedProtocolVersion = "2026-07-28"
	defaultCacheTTLMS        = 0
	defaultCacheScope        = "private"
)

const serverInfoMetaKey = "io.modelcontextprotocol/serverInfo"

type DiscoverResult struct {
	ResultType        string         `json:"resultType"`
	SupportedVersions []string       `json:"supportedVersions"`
	Capabilities      Capabilities   `json:"capabilities"`
	Meta              map[string]any `json:"_meta,omitempty"`
	Instructions      string         `json:"instructions,omitempty"`
	TTLMS             int            `json:"ttlMs"`
	CacheScope        string         `json:"cacheScope"`
}

func serverInfo() ServerDescriptor {
	return DescribeProtocol(nil).Server
}

func BuildDiscoverResult(profile Profile) DiscoverResult {
	descriptor := DescribeProtocol(nil)
	return DiscoverResult{
		ResultType:        "complete",
		SupportedVersions: []string{SupportedProtocolVersion},
		Capabilities:      ProjectCapabilities(profile, descriptor.Capabilities, false),
		Meta:              map[string]any{serverInfoMetaKey: descriptor.Server},
		Instructions:      ProjectServerInstructions(profile),
		TTLMS:             defaultCacheTTLMS,
		CacheScope:        defaultCacheScope,
	}
}

func Discover() DiscoverResult {
	return BuildDiscoverResult(BaseProfile())
}

func cacheableCompleteResult(fields map[string]any) map[string]any {
	result := make(map[string]any, len(fields)+3)
	for key, value := range fields {
		result[key] = value
	}
	result["resultType"] = "complete"
	result["ttlMs"] = defaultCacheTTLMS
	result["cacheScope"] = defaultCacheScope
	return result
}
