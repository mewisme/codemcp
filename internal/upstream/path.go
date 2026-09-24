package upstream

import (
	"go.mewis.me/codemcp/internal/configformat"
)

func Path() string {
	return configformat.StructuredPath(configformat.RootPath(), "upstreams")
}
