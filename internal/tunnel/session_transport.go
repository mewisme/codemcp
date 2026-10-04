package tunnel

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openai/tunnel-client/pkg/tunnelctx"
)

const sessionMetaKey = "go.mewis.me/codemcp/mcp-session-id"

type sessionTransport struct {
	base       sdkmcp.Transport
	onActivity func()
}

type sessionConnection struct {
	base       sdkmcp.Connection
	onActivity func()
}

func withSessionTransport(base sdkmcp.Transport) sdkmcp.Transport {
	return withSessionTransportActivity(base, nil)
}

func withSessionTransportActivity(base sdkmcp.Transport, onActivity func()) sdkmcp.Transport {
	if base == nil {
		return nil
	}
	return &sessionTransport{base: base, onActivity: onActivity}
}

func (t *sessionTransport) Connect(ctx context.Context) (sdkmcp.Connection, error) {
	conn, err := t.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &sessionConnection{base: conn, onActivity: t.onActivity}, nil
}

func (c *sessionConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	return c.base.Read(ctx)
}

func (c *sessionConnection) Write(ctx context.Context, msg jsonrpc.Message) error {
	request, isRequest := msg.(*jsonrpc.Request)
	if isRequest && request != nil && request.Method == "tools/call" {
		params := map[string]any{}
		if len(request.Params) > 0 && string(request.Params) != "null" {
			if err := json.Unmarshal(request.Params, &params); err != nil {
				return err
			}
		}
		meta, _ := params["_meta"].(map[string]any)
		if sessionID, ok := tunnelctx.SessionIDFromContext(ctx); ok {
			if meta == nil {
				meta = map[string]any{}
				params["_meta"] = meta
			}
			meta[sessionMetaKey] = sessionID
		} else if meta != nil {
			delete(meta, sessionMetaKey)
		} else {
			return c.write(ctx, msg, isRequest, request)
		}
		encoded, err := json.Marshal(params)
		if err != nil {
			return err
		}
		request.Params = encoded
	}
	return c.write(ctx, msg, isRequest, request)
}

func (c *sessionConnection) write(ctx context.Context, msg jsonrpc.Message, isRequest bool, request *jsonrpc.Request) error {
	err := c.base.Write(ctx, msg)
	if err == nil && isRequest && request != nil && c.onActivity != nil {
		c.onActivity()
	}
	return err
}

func (c *sessionConnection) Close() error      { return c.base.Close() }
func (c *sessionConnection) SessionID() string { return c.base.SessionID() }
