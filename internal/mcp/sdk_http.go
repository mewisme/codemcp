package mcp

import (
	"net/http"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"go.mewis.me/codemcp/internal/tools"
)

func NewSDKHTTPHandler(toolRuntime *tools.Runtime, boundWorkspace string, enableSSE bool) (http.Handler, error) {
	streamableServer, err := NewSDKServerWithSession(toolRuntime, "http", "", boundWorkspace)
	if err != nil {
		return nil, err
	}
	streamable := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return streamableServer.Server }, &sdkmcp.StreamableHTTPOptions{SessionTimeout: 30 * time.Minute, PropagateRequestCancellation: true})
	mux := http.NewServeMux()
	mux.Handle("/mcp", streamable)
	if enableSSE {
		sseServer, err := NewSDKServerWithSession(toolRuntime, "sse", "", boundWorkspace)
		if err != nil {
			return nil, err
		}
		sse := sdkmcp.NewSSEHandler(func(*http.Request) *sdkmcp.Server { return sseServer.Server }, nil)
		mux.Handle("/mcp/sse", sse)
		mux.Handle("/mcp/sse/", sse)
	}
	return mux, nil
}
