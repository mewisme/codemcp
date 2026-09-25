package mcp

import (
	"net/http"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"go.mewis.me/codemcp/internal/tools"
)

func NewSDKHTTPHandler(toolRuntime *tools.Runtime, boundWorkspace string, enableSSE bool) (http.Handler, error) {
	return NewSDKHTTPHandlerWithProfile(toolRuntime, boundWorkspace, enableSSE, BaseProfile())
}

func NewSDKHTTPHandlerWithProfile(toolRuntime *tools.Runtime, boundWorkspace string, enableSSE bool, profile Profile) (http.Handler, error) {
	return NewSDKHTTPHandlerWithProfileAuth(toolRuntime, boundWorkspace, enableSSE, profile)
}

func NewSDKHTTPHandlerWithProfileAuth(toolRuntime *tools.Runtime, boundWorkspace string, enableSSE bool, profile Profile, authRequirements ...AuthRequirement) (http.Handler, error) {
	streamableServer, err := NewSDKServerWithProfileAuth(toolRuntime, "http", "", boundWorkspace, profile, authRequirements...)
	if err != nil {
		return nil, err
	}
	streamable := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return streamableServer.Server }, &sdkmcp.StreamableHTTPOptions{Stateless: true, SessionTimeout: 30 * time.Minute, PropagateRequestCancellation: true})
	mux := http.NewServeMux()
	mux.Handle("/mcp", streamable)
	if enableSSE {
		sseServer, err := NewSDKServerWithProfileAuth(toolRuntime, "sse", "", boundWorkspace, profile, authRequirements...)
		if err != nil {
			return nil, err
		}
		sse := sdkmcp.NewSSEHandler(func(*http.Request) *sdkmcp.Server { return sseServer.Server }, nil)
		mux.Handle("/mcp/sse", sse)
		mux.Handle("/mcp/sse/", sse)
	}
	return mux, nil
}
