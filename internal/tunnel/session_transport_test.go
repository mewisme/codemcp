package tunnel

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openai/tunnel-client/pkg/tunnelctx"
)

type captureTransport struct{ conn *captureConnection }
type captureConnection struct{ written jsonrpc.Message }

func (t *captureTransport) Connect(context.Context) (sdkmcp.Connection, error) { return t.conn, nil }
func (c *captureConnection) Read(context.Context) (jsonrpc.Message, error) {
	return nil, context.Canceled
}
func (c *captureConnection) Write(_ context.Context, msg jsonrpc.Message) error {
	c.written = msg
	return nil
}
func (c *captureConnection) Close() error      { return nil }
func (c *captureConnection) SessionID() string { return "" }

func TestSessionTransportInjectsTunnelSessionIntoToolMeta(t *testing.T) {
	base := &captureTransport{conn: &captureConnection{}}
	conn, err := withSessionTransport(base).Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	request := &jsonrpc.Request{Method: "tools/call", Params: json.RawMessage(`{"name":"probe","arguments":{},"_meta":{"client":"keep"}}`)}
	ctx := tunnelctx.ContextWithSessionID(context.Background(), "session-production")
	if err := conn.Write(ctx, request); err != nil {
		t.Fatal(err)
	}
	written, ok := base.conn.written.(*jsonrpc.Request)
	if !ok || written == nil {
		t.Fatalf("written = %#v", base.conn.written)
	}
	var params map[string]any
	if err := json.Unmarshal(written.Params, &params); err != nil {
		t.Fatal(err)
	}
	meta, _ := params["_meta"].(map[string]any)
	if meta[sessionMetaKey] != "session-production" || meta["client"] != "keep" {
		t.Fatalf("meta = %#v", meta)
	}
}

func TestSessionTransportDoesNotInjectIntoNonToolRequest(t *testing.T) {
	base := &captureTransport{conn: &captureConnection{}}
	conn, err := withSessionTransport(base).Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	request := &jsonrpc.Request{Method: "tools/list", Params: json.RawMessage(`{"_meta":{"client":"keep"}}`)}
	ctx := tunnelctx.ContextWithSessionID(context.Background(), "session-production")
	if err := conn.Write(ctx, request); err != nil {
		t.Fatal(err)
	}
	if string(request.Params) != `{"_meta":{"client":"keep"}}` {
		t.Fatalf("params changed: %s", request.Params)
	}
}

func TestSessionTransportStripsUntrustedSessionMetaWithoutTunnelSession(t *testing.T) {
	base := &captureTransport{conn: &captureConnection{}}
	conn, err := withSessionTransport(base).Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	request := &jsonrpc.Request{Method: "tools/call", Params: json.RawMessage(`{"name":"probe","arguments":{},"_meta":{"go.mewis.me/codemcp/mcp-session-id":"spoofed","client":"keep"}}`)}
	if err := conn.Write(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	written, ok := base.conn.written.(*jsonrpc.Request)
	if !ok || written == nil {
		t.Fatalf("written = %#v", base.conn.written)
	}
	var params map[string]any
	if err := json.Unmarshal(written.Params, &params); err != nil {
		t.Fatal(err)
	}
	meta, _ := params["_meta"].(map[string]any)
	if _, exists := meta[sessionMetaKey]; exists {
		t.Fatalf("untrusted session metadata survived: %#v", meta)
	}
	if meta["client"] != "keep" {
		t.Fatalf("client metadata changed: %#v", meta)
	}
}

func TestSessionTransportReportsMCPRequestActivity(t *testing.T) {
	base := &captureTransport{conn: &captureConnection{}}
	activity := 0
	conn, err := withSessionTransportActivity(base, func() { activity++ }).Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	request := &jsonrpc.Request{Method: "tools/list", Params: json.RawMessage(`{}`)}
	if err := conn.Write(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if activity != 1 {
		t.Fatalf("activity callbacks=%d want=1", activity)
	}
}
