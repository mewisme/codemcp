package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openai/tunnel-client/pkg/tunnelctx"
)

const cancellationNotificationTimeout = time.Second

type cancellationSafeTransport struct {
	base sdkmcp.Transport
	mu   sync.Mutex
	conn *cancellationSafeConnection
}

type cancellationRoute struct {
	key      string
	callerID jsonrpc.ID
	wireID   jsonrpc.ID
	inbox    chan jsonrpc.Message
	done     <-chan struct{}
	complete bool
}

type cancellationSafeConnection struct {
	base        sdkmcp.Connection
	sequence    atomic.Uint64
	hardFailure atomic.Bool
	mu          sync.Mutex
	byKey       map[string]*cancellationRoute
	byWire      map[string]*cancellationRoute
	retired     map[string]time.Time
	global      chan jsonrpc.Message
	closed      chan struct{}
	readErr     error
	closeOnce   sync.Once
}

func newCancellationSafeInMemoryTransports() (sdkmcp.Transport, sdkmcp.Transport) {
	serverConn, tunnelConn := net.Pipe()
	server := &sdkmcp.IOTransport{Reader: serverConn, Writer: serverConn}
	tunnel := &cancellationSafeTransport{base: &sdkmcp.IOTransport{Reader: tunnelConn, Writer: tunnelConn}}
	return server, tunnel
}

func (t *cancellationSafeTransport) Connect(ctx context.Context) (sdkmcp.Connection, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.conn != nil {
		return t.conn, nil
	}
	base, err := t.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	conn := &cancellationSafeConnection{
		base:    base,
		byKey:   map[string]*cancellationRoute{},
		byWire:  map[string]*cancellationRoute{},
		retired: map[string]time.Time{},
		global:  make(chan jsonrpc.Message, 256),
		closed:  make(chan struct{}),
	}
	t.conn = conn
	go conn.readLoop(context.WithoutCancel(ctx))
	return conn, nil
}

func (c *cancellationSafeConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		route := c.routeForContext(ctx)
		if route != nil {
			select {
			case msg := <-route.inbox:
				c.finishRoute(route)
				return msg, nil
			default:
			}
		}

		if route == nil {
			select {
			case msg := <-c.global:
				return msg, nil
			case <-c.closed:
				return nil, c.connectionError()
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		select {
		case msg := <-route.inbox:
			c.finishRoute(route)
			return msg, nil
		case msg := <-c.global:
			return msg, nil
		case <-c.closed:
			select {
			case msg := <-route.inbox:
				c.finishRoute(route)
				return msg, nil
			default:
			}
			return nil, c.connectionError()
		case <-ctx.Done():
			if route.complete {
				c.finishRoute(route)
			}
			return nil, ctx.Err()
		}
	}
}

func (c *cancellationSafeConnection) Write(ctx context.Context, msg jsonrpc.Message) error {
	if ctx == nil {
		ctx = context.Background()
	}
	request, ok := msg.(*jsonrpc.Request)
	if !ok || request == nil || !request.ID.IsValid() {
		err := c.base.Write(ctx, msg)
		c.recordWriteError(ctx, err)
		return err
	}

	route, wireRequest, err := c.prepareRoute(ctx, request)
	if err != nil {
		return err
	}
	if err := c.base.Write(ctx, wireRequest); err != nil {
		c.removeRoute(route)
		c.recordWriteError(ctx, err)
		return err
	}
	if route.done != nil {
		go c.watchCancellation(context.WithoutCancel(ctx), route)
	}
	return nil
}

func (c *cancellationSafeConnection) Close() error {
	if !c.hardFailure.Load() {
		return nil
	}
	var err error
	c.closeOnce.Do(func() { err = c.base.Close() })
	return err
}

func (c *cancellationSafeConnection) SessionID() string { return c.base.SessionID() }

func (c *cancellationSafeConnection) prepareRoute(ctx context.Context, request *jsonrpc.Request) (*cancellationRoute, *jsonrpc.Request, error) {
	wireID, err := jsonrpc.MakeID(fmt.Sprintf("__cm_tunnel_%x", c.sequence.Add(1)))
	if err != nil {
		return nil, nil, err
	}
	key := cancellationRouteKey(ctx, request.ID)
	route := &cancellationRoute{key: key, callerID: request.ID, wireID: wireID, inbox: make(chan jsonrpc.Message, 1), done: ctx.Done()}
	wireRequest := *request
	wireRequest.ID = wireID

	c.mu.Lock()
	if previous := c.byKey[key]; previous != nil {
		c.mu.Unlock()
		return nil, nil, fmt.Errorf("duplicate active tunnel request route %q", key)
	}
	c.byKey[key] = route
	c.byWire[wireIDKey(wireID)] = route
	c.pruneRetiredLocked(time.Now())
	c.mu.Unlock()
	return route, &wireRequest, nil
}

func (c *cancellationSafeConnection) watchCancellation(ctx context.Context, route *cancellationRoute) {
	<-route.done
	if !c.retireRoute(route) {
		return
	}
	params, err := json.Marshal(sdkmcp.CancelledParams{RequestID: route.wireID.Raw(), Reason: "tunnel request context canceled"})
	if err != nil {
		return
	}
	notification := &jsonrpc.Request{Method: "notifications/cancelled", Params: params}
	ctx, cancel := context.WithTimeout(ctx, cancellationNotificationTimeout)
	defer cancel()
	if err := c.base.Write(ctx, notification); err != nil && !isContextError(ctx, err) {
		c.hardFailure.Store(true)
	}
}

func (c *cancellationSafeConnection) retireRoute(route *cancellationRoute) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	current := c.byKey[route.key]
	if current != route || route.complete {
		return false
	}
	delete(c.byKey, route.key)
	delete(c.byWire, wireIDKey(route.wireID))
	c.retired[wireIDKey(route.wireID)] = time.Now()
	c.pruneRetiredLocked(time.Now())
	return true
}

func (c *cancellationSafeConnection) removeRoute(route *cancellationRoute) {
	if route == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byKey[route.key] == route {
		delete(c.byKey, route.key)
	}
	if c.byWire[wireIDKey(route.wireID)] == route {
		delete(c.byWire, wireIDKey(route.wireID))
	}
}

func (c *cancellationSafeConnection) finishRoute(route *cancellationRoute) {
	c.mu.Lock()
	if c.byKey[route.key] == route {
		delete(c.byKey, route.key)
	}
	c.mu.Unlock()
}

func (c *cancellationSafeConnection) routeForContext(ctx context.Context) *cancellationRoute {
	requestID, _ := tunnelctx.RPCRequestIDFromContext(ctx)
	key := cancellationRouteKey(ctx, requestID)
	c.mu.Lock()
	route := c.byKey[key]
	c.mu.Unlock()
	return route
}

func (c *cancellationSafeConnection) readLoop(ctx context.Context) {
	for {
		msg, err := c.base.Read(ctx)
		if err != nil {
			c.mu.Lock()
			c.readErr = err
			c.mu.Unlock()
			c.hardFailure.Store(true)
			close(c.closed)
			return
		}
		if response, ok := msg.(*jsonrpc.Response); ok && response != nil && response.ID.IsValid() {
			if c.routeResponse(response) {
				continue
			}
			c.hardFailure.Store(true)
		}
		if request, ok := msg.(*jsonrpc.Request); ok && request != nil && request.ID.IsValid() {
			c.hardFailure.Store(true)
		}
		c.global <- msg
	}
}

func (c *cancellationSafeConnection) routeResponse(response *jsonrpc.Response) bool {
	key := wireIDKey(response.ID)
	c.mu.Lock()
	if _, retired := c.retired[key]; retired {
		delete(c.retired, key)
		c.mu.Unlock()
		return true
	}
	route := c.byWire[key]
	if route == nil {
		c.mu.Unlock()
		return false
	}
	delete(c.byWire, key)
	route.complete = true
	c.mu.Unlock()

	rewritten := *response
	rewritten.ID = route.callerID
	route.inbox <- &rewritten
	return true
}

func (c *cancellationSafeConnection) connectionError() error {
	c.mu.Lock()
	err := c.readErr
	c.mu.Unlock()
	if err == nil {
		return io.EOF
	}
	return err
}

func (c *cancellationSafeConnection) recordWriteError(ctx context.Context, err error) {
	if err != nil && !isContextError(ctx, err) {
		c.hardFailure.Store(true)
	}
}

func (c *cancellationSafeConnection) pruneRetiredLocked(now time.Time) {
	cutoff := now.Add(-10 * time.Minute)
	for key, retiredAt := range c.retired {
		if retiredAt.Before(cutoff) {
			delete(c.retired, key)
		}
	}
}

func cancellationRouteKey(ctx context.Context, requestID jsonrpc.ID) string {
	if id, ok := tunnelctx.ControlPlaneCommandRequestIDFromContext(ctx); ok {
		return "control-plane:" + string(id)
	}
	if id, ok := tunnelctx.RequestIDFromContext(ctx); ok {
		return "request:" + id
	}
	if requestID.IsValid() {
		return fmt.Sprintf("rpc:%T:%v", requestID.Raw(), requestID.Raw())
	}
	return ""
}

func wireIDKey(id jsonrpc.ID) string { return fmt.Sprintf("%T:%v", id.Raw(), id.Raw()) }

func isContextError(ctx context.Context, err error) bool {
	return err != nil && ctx != nil && ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded))
}
