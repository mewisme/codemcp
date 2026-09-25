package upstream

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

const toolsCacheTTL = 60 * time.Second

type Health string

const (
	HealthUnknown     Health = "unknown"
	HealthConnected   Health = "connected"
	HealthUnreachable Health = "unreachable"
	HealthDisabled    Health = "disabled"
)

type Status struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Enabled      bool     `json:"enabled"`
	Transport    string   `json:"transport"`
	Auth         string   `json:"auth"`
	Health       Health   `json:"health"`
	Connected    bool     `json:"connected"`
	ToolCount    int      `json:"tool_count"`
	Expose       string   `json:"expose"`
	ProxiedTools []string `json:"proxied_tools"`
	LastError    string   `json:"last_error,omitempty"`
	PID          *int     `json:"pid,omitempty"`
}

type toolCache struct {
	tools     []Tool
	expiresAt time.Time
}

type toolSubscription struct {
	cancel context.CancelFunc
}

type Manager struct {
	mu            sync.RWMutex
	store         *Store
	client        Client
	trace         tracepkg.Observer
	servers       map[string]Server
	cache         map[string]toolCache
	errors        map[string]string
	subscriptions map[string]*toolSubscription
	toolsChanged  func(context.Context, string) error
}

func NewManager(store *Store) *Manager {
	return NewManagerWithClient(store, NewNativeClient())
}

func NewManagerWithClient(store *Store, client Client) *Manager {
	return &Manager{
		store: store, client: client, servers: map[string]Server{}, cache: map[string]toolCache{}, errors: map[string]string{},
		subscriptions: map[string]*toolSubscription{},
	}
}

func (m *Manager) SetTraceObserver(observer tracepkg.Observer) *Manager {
	if m == nil {
		return m
	}
	m.trace = observer
	if client, ok := m.client.(interface{ SetTraceObserver(tracepkg.Observer) }); ok {
		client.SetTraceObserver(observer)
	}
	return m
}

func (m *Manager) Load() error {
	if m.store == nil {
		tracepkg.EmitObserver(m.trace, "UPSTREAM", "upstream.store.load.skipped", "Upstream store load skipped", tracepkg.Bool("configured", false))
		return nil
	}
	span := tracepkg.StartObserver(m.trace, "UPSTREAM", "upstream.store.load", "Loading Upstream store", tracepkg.String("path", m.store.Path))
	servers, err := m.store.Load()
	if err != nil {
		span.FailMessage("Upstream store load failed", err)
		return err
	}
	loadFields := []tracepkg.Field{tracepkg.String("path", m.store.Path), tracepkg.Int("count", len(servers))}
	if info, statErr := os.Stat(m.store.Path); statErr == nil {
		loadFields = append(loadFields, tracepkg.Int64("bytes", info.Size()))
	}
	span.EndMessage("Upstream store loaded", loadFields...)
	normalizeSpan := tracepkg.StartObserver(m.trace, "UPSTREAM", "upstream.store.normalize", "Normalizing Upstream servers", tracepkg.Int("count", len(servers)))
	next := make(map[string]Server, len(servers))
	for _, server := range servers {
		normalized, err := NormalizeServer(server)
		if err != nil {
			normalizeSpan.FailMessage("Upstream server normalization failed", err, tracepkg.String("server", server.ID))
			return err
		}
		next[normalized.ID] = normalized
	}
	normalizeSpan.EndMessage("Upstream servers normalized", tracepkg.Int("count", len(next)))
	m.mu.Lock()
	m.servers = next
	m.cache = map[string]toolCache{}
	m.errors = map[string]string{}
	m.mu.Unlock()
	tracepkg.EmitObserver(m.trace, "UPSTREAM", "upstream.cache.reset", "Upstream caches reset", tracepkg.Int("servers", len(next)))
	return nil
}

func (m *Manager) Reload(ctx context.Context) error {
	if err := m.Shutdown(ctx); err != nil {
		return err
	}
	return m.Load()
}

// RestoreRuntimeSnapshot restores the runtime manager state without writing the
// store. It is used when a runtime-side proxy refresh fails after Reload loaded
// a newly persisted snapshot; the caller remains responsible for rolling the
// persisted store back.
func (m *Manager) RestoreRuntimeSnapshot(ctx context.Context, servers []Server) error {
	next := make(map[string]Server, len(servers))
	for _, server := range servers {
		normalized, err := NormalizeServer(server)
		if err != nil {
			return err
		}
		if _, exists := next[normalized.ID]; exists {
			return fmt.Errorf("duplicate upstream server ID: %s", normalized.ID)
		}
		next[normalized.ID] = normalized
	}
	shutdownErr := m.Shutdown(ctx)
	m.mu.Lock()
	m.servers = next
	m.cache = map[string]toolCache{}
	m.errors = map[string]string{}
	m.mu.Unlock()
	return shutdownErr
}

func (m *Manager) Add(server Server) error {
	span := tracepkg.StartObserver(m.trace, "UPSTREAM", "upstream.server.save", "Saving Upstream server", upstreamServerTraceFields(server)...)
	normalized, err := NormalizeServer(server)
	if err != nil {
		span.FailMessage("Upstream server save failed", err)
		return err
	}
	m.mu.Lock()
	previous, existed := m.servers[normalized.ID]
	changed := changedServerFields(previous, normalized, existed)
	m.servers[normalized.ID] = normalized
	if err := m.persistLocked(); err != nil {
		if existed {
			m.servers[normalized.ID] = previous
		} else {
			delete(m.servers, normalized.ID)
		}
		m.mu.Unlock()
		span.FailMessage("Upstream server save failed", err, tracepkg.Bool("existing", existed), tracepkg.Any("changed_fields", changed))
		return err
	}
	delete(m.cache, normalized.ID)
	delete(m.errors, normalized.ID)
	m.mu.Unlock()
	tracepkg.EmitObserver(m.trace, "UPSTREAM", "upstream.cache.invalidated", "Upstream server cache invalidated", tracepkg.String("server", normalized.ID), tracepkg.Bool("tools", true), tracepkg.Bool("errors", true))
	if !existed {
		span.EndMessage("Upstream server saved", append(upstreamServerTraceFields(normalized), tracepkg.Bool("existing", false), tracepkg.Any("changed_fields", changed), tracepkg.Bool("connection_closed", false), tracepkg.Bool("oauth_cleanup_performed", false))...)
		return nil
	}
	m.stopToolsSubscription(normalized.ID)
	closeSpan := tracepkg.StartObserver(m.trace, "UPSTREAM", "upstream.connection.close", "Closing changed upstream connection", tracepkg.String("server", normalized.ID))
	closeErr := m.client.Close(context.Background(), normalized.ID)
	if closeErr != nil {
		closeErr = sanitizeRemoteError(previous, closeErr)
		closeSpan.FailMessage("Changed upstream connection close failed", closeErr)
	} else {
		closeSpan.EndMessage("Changed upstream connection closed")
	}
	var oauthErr error
	cleanupOAuth := oauthCredentialBindingChanged(previous, normalized)
	if cleanupOAuth {
		oauthSpan := tracepkg.StartObserver(m.trace, "OAUTH", "upstream.oauth.cleanup", "Clearing upstream OAuth credential after binding change", tracepkg.String("server", normalized.ID))
		oauthErr = m.clearOAuthCredential(normalized.ID)
		if oauthErr != nil {
			oauthSpan.FailMessage("Upstream OAuth credential cleanup failed", oauthErr)
		} else {
			oauthSpan.EndMessage("Upstream OAuth credential cleared")
		}
	}
	joined := errors.Join(closeErr, oauthErr)
	if joined != nil {
		span.FailMessage("Upstream server saved with cleanup failure", joined, tracepkg.Bool("existing", true), tracepkg.Any("changed_fields", changed), tracepkg.Bool("connection_closed", closeErr == nil), tracepkg.Bool("oauth_cleanup_performed", cleanupOAuth))
		return joined
	}
	span.EndMessage("Upstream server saved", append(upstreamServerTraceFields(normalized), tracepkg.Bool("existing", true), tracepkg.Any("changed_fields", changed), tracepkg.Bool("connection_closed", true), tracepkg.Bool("oauth_cleanup_performed", cleanupOAuth))...)
	return nil
}

func (m *Manager) CreateBatch(servers []Server) error {
	normalized := make([]Server, 0, len(servers))
	seen := map[string]bool{}
	for _, server := range servers {
		value, err := NormalizeServer(server)
		if err != nil {
			return err
		}
		if seen[value.ID] {
			return fmt.Errorf("duplicate upstream server ID: %s", value.ID)
		}
		seen[value.ID] = true
		normalized = append(normalized, value)
	}
	if len(normalized) == 0 {
		return errors.New("at least one upstream server is required")
	}
	m.mu.Lock()
	for _, server := range normalized {
		if _, exists := m.servers[server.ID]; exists {
			m.mu.Unlock()
			return fmt.Errorf("upstream server already exists: %s", server.ID)
		}
	}
	for _, server := range normalized {
		m.servers[server.ID] = server
	}
	if err := m.persistLocked(); err != nil {
		for _, server := range normalized {
			delete(m.servers, server.ID)
		}
		m.mu.Unlock()
		return err
	}
	for _, server := range normalized {
		delete(m.cache, server.ID)
		delete(m.errors, server.ID)
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager) Remove(id string) error {
	span := tracepkg.StartObserver(m.trace, "UPSTREAM", "upstream.server.remove", "Removing Upstream server", tracepkg.String("server", id))
	m.mu.Lock()
	previous, existed := m.servers[id]
	delete(m.servers, id)
	if err := m.persistLocked(); err != nil {
		if existed {
			m.servers[id] = previous
		}
		m.mu.Unlock()
		span.FailMessage("Upstream server removal failed", err, tracepkg.Bool("existing", existed))
		return err
	}
	delete(m.cache, id)
	delete(m.errors, id)
	m.mu.Unlock()
	tracepkg.EmitObserver(m.trace, "UPSTREAM", "upstream.cache.invalidated", "Upstream server cache invalidated", tracepkg.String("server", id), tracepkg.Bool("tools", true), tracepkg.Bool("errors", true))
	m.stopToolsSubscription(id)
	closeSpan := tracepkg.StartObserver(m.trace, "UPSTREAM", "upstream.connection.close", "Closing removed upstream connection", tracepkg.String("server", id))
	closeErr := m.client.Close(context.Background(), id)
	if closeErr != nil {
		closeErr = sanitizeRemoteError(previous, closeErr)
		closeSpan.FailMessage("Removed upstream connection close failed", closeErr)
	} else {
		closeSpan.EndMessage("Removed upstream connection closed")
	}
	oauthSpan := tracepkg.StartObserver(m.trace, "OAUTH", "upstream.oauth.cleanup", "Clearing removed upstream OAuth credential", tracepkg.String("server", id))
	oauthErr := m.clearOAuthCredential(id)
	if oauthErr != nil {
		oauthSpan.FailMessage("Removed upstream OAuth credential cleanup failed", oauthErr)
	} else {
		oauthSpan.EndMessage("Removed upstream OAuth credential cleared")
	}
	err := errors.Join(closeErr, oauthErr)
	if err != nil {
		span.FailMessage("Upstream server removal cleanup failed", err, tracepkg.Bool("existing", existed))
		return err
	}
	span.EndMessage("Upstream server removed", tracepkg.Bool("existing", existed), tracepkg.String("transport", previous.Transport), tracepkg.Bool("connection_closed", true), tracepkg.Bool("oauth_cleanup_performed", true))
	return nil
}

func (m *Manager) Get(id string) (Server, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	value, ok := m.servers[id]
	return value, ok
}

func (m *Manager) Disconnect(id string) error {
	server, _ := m.Get(id)
	m.mu.Lock()
	delete(m.cache, id)
	delete(m.errors, id)
	m.mu.Unlock()
	m.stopToolsSubscription(id)
	return sanitizeRemoteError(server, m.client.Close(context.Background(), id))
}

func (m *Manager) List() []Server {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.listLocked()
}

func (m *Manager) listLocked() []Server {
	out := make([]Server, 0, len(m.servers))
	for _, server := range m.servers {
		out = append(out, server)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (m *Manager) Tools(ctx context.Context, id string, force bool) ([]Tool, error) {
	server, ok := m.Get(id)
	if !ok {
		return nil, errors.New("unknown upstream server: " + id)
	}
	if !server.Enabled {
		return nil, errors.New("upstream server disabled: " + id)
	}
	span := tracepkg.Start(ctx, "UPSTREAM", "upstream.tools.discover", "Discovering Upstream tools", append(upstreamServerTraceFields(server), tracepkg.Bool("force_refresh", force))...)
	m.mu.RLock()
	cached, hasCache := m.cache[id]
	m.mu.RUnlock()
	if !force && hasCache && time.Now().Before(cached.expiresAt) {
		age := toolsCacheTTL - time.Until(cached.expiresAt)
		subscriptionStarted := m.ensureToolsSubscription(server)
		span.EndMessage("Upstream tools loaded from cache", tracepkg.Bool("cache_hit", true), tracepkg.Int64("cache_age_ms", max(0, age.Milliseconds())), tracepkg.Int64("cache_ttl_ms", toolsCacheTTL.Milliseconds()), tracepkg.Int("tool_count", len(cached.tools)), tracepkg.Bool("subscription_started", subscriptionStarted))
		return append([]Tool(nil), cached.tools...), nil
	}
	tracepkg.Emit(ctx, "UPSTREAM", "upstream.tools.cache-miss", "Upstream tools cache miss", tracepkg.String("server", id), tracepkg.Bool("force_refresh", force), tracepkg.Bool("cache_present", hasCache))
	if force {
		m.stopToolsSubscription(id)
		disconnectSpan := tracepkg.Start(ctx, "UPSTREAM", "upstream.connection.disconnect", "Disconnecting Upstream server for forced refresh", tracepkg.String("server", id))
		if err := m.client.Close(ctx, id); err != nil {
			err = sanitizeRemoteError(server, err)
			disconnectSpan.FailMessage("Forced upstream disconnect failed", err)
		} else {
			disconnectSpan.EndMessage("Upstream server disconnected for forced refresh")
		}
	}
	connectSpan := tracepkg.Start(ctx, "UPSTREAM", "upstream.connection.connect", "Connecting Upstream server", upstreamServerTraceFields(server)...)
	if err := m.client.Connect(ctx, server); err != nil {
		err = sanitizeRemoteError(server, err)
		connectSpan.FailMessage("Upstream connection failed", err)
		span.FailMessage("Upstream tool discovery failed", err, tracepkg.Bool("cache_hit", false))
		m.recordError(id, err)
		return nil, err
	}
	connectSpan.EndMessage("Upstream server connected", tracepkg.Int("pid", m.client.PID(id)))
	listSpan := tracepkg.Start(ctx, "UPSTREAM", "upstream.tools.list", "Requesting Upstream tool list", tracepkg.String("server", id))
	tools, err := m.client.Tools(ctx, id)
	if err != nil {
		err = sanitizeRemoteError(server, err)
		listSpan.FailMessage("Upstream tool list failed", err)
		span.FailMessage("Upstream tool discovery failed", err, tracepkg.Bool("cache_hit", false))
		m.recordError(id, err)
		return nil, err
	}
	listSpan.EndMessage("Upstream tool list received", tracepkg.Int("tool_count", len(tools)))
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	m.mu.Lock()
	m.cache[id] = toolCache{tools: append([]Tool(nil), tools...), expiresAt: time.Now().Add(toolsCacheTTL)}
	delete(m.errors, id)
	m.mu.Unlock()
	subscriptionStarted := m.ensureToolsSubscription(server)
	span.EndMessage("Upstream tools discovered", tracepkg.Bool("cache_hit", false), tracepkg.Int("tool_count", len(tools)), tracepkg.Int64("cache_ttl_ms", toolsCacheTTL.Milliseconds()), tracepkg.Bool("subscription_started", subscriptionStarted), tracepkg.Int("pid", m.client.PID(id)))
	return tools, nil
}

type inputRoundClient interface {
	CallWithInput(context.Context, string, string, map[string]any, string, map[string]any) (CallResult, error)
}

func (m *Manager) Call(ctx context.Context, id, tool string, args map[string]any) (CallResult, error) {
	return m.CallWithInput(ctx, id, tool, args, "", nil)
}

func (m *Manager) CallWithInput(ctx context.Context, id, tool string, args map[string]any, requestState string, inputResponses map[string]any) (CallResult, error) {
	server, _, err := m.resolveCallableTool(ctx, id, tool)
	if err != nil {
		return CallResult{}, err
	}
	if err := m.client.Connect(ctx, server); err != nil {
		err = sanitizeRemoteError(server, err)
		m.recordError(id, err)
		return CallResult{}, err
	}
	var result CallResult
	err = nil
	if client, ok := m.client.(inputRoundClient); ok {
		result, err = client.CallWithInput(ctx, id, tool, args, requestState, inputResponses)
	} else {
		if requestState != "" || inputResponses != nil {
			return CallResult{}, errors.New("upstream client does not support multi-round-trip input")
		}
		result, err = m.client.Call(ctx, id, tool, args)
	}
	if err != nil {
		err = sanitizeRemoteError(server, err)
		m.recordError(id, err)
		return CallResult{}, err
	}
	m.mu.Lock()
	delete(m.errors, id)
	m.mu.Unlock()
	return result, nil
}

func (m *Manager) CheckHealth(ctx context.Context, id string, force bool) Status {
	server, ok := m.Get(id)
	if !ok {
		return Status{ID: id, Health: HealthUnreachable, LastError: "unknown upstream server"}
	}
	if !server.Enabled {
		return m.buildStatus(server, HealthDisabled, false, nil, "")
	}
	tools, err := m.Tools(ctx, id, force)
	if err != nil {
		return m.buildStatus(server, HealthUnreachable, false, nil, err.Error())
	}
	return m.buildStatus(server, HealthConnected, true, tools, "")
}

func (m *Manager) ListStatuses(ctx context.Context, refresh bool) []Status {
	servers := m.List()
	enabled := 0
	for _, server := range servers {
		if server.Enabled {
			enabled++
		}
	}
	span := tracepkg.Start(ctx, "UPSTREAM", "upstream.status.list", "Refreshing Upstream status list", tracepkg.Int("server_count", len(servers)), tracepkg.Int("enabled_count", enabled), tracepkg.Bool("refresh", refresh))
	result := make([]Status, len(servers))
	var wg sync.WaitGroup
	for index, server := range servers {
		index, server := index, server
		wg.Add(1)
		go func() {
			defer wg.Done()
			result[index] = m.CheckHealth(ctx, server.ID, refresh)
		}()
	}
	wg.Wait()
	connected, unreachable := 0, 0
	for _, status := range result {
		switch status.Health {
		case HealthConnected:
			connected++
		case HealthUnreachable:
			unreachable++
		}
	}
	span.EndMessage("Upstream status list refreshed", tracepkg.Int("server_count", len(result)), tracepkg.Int("enabled_count", enabled), tracepkg.Int("connected_count", connected), tracepkg.Int("unreachable_count", unreachable), tracepkg.Bool("refresh", refresh))
	return result
}

func (m *Manager) ProxiedToolNames(server Server, tools []Tool) []string {
	result := make([]string, 0)
	for _, tool := range tools {
		if !ToolIsExposed(server, tool.Name) {
			continue
		}
		result = append(result, ProxyName(server.ToolPrefix, tool.Name))
	}
	sort.Strings(result)
	return result
}

func (m *Manager) resolveCallableTool(ctx context.Context, id, tool string) (Server, Tool, error) {
	id = strings.TrimSpace(id)
	tool = strings.TrimSpace(tool)
	server, ok := m.Get(id)
	if !ok {
		return Server{}, Tool{}, errors.New("unknown upstream server: " + id)
	}
	if !server.Enabled {
		return Server{}, Tool{}, errors.New("upstream server disabled: " + id)
	}
	if !ToolIsExposed(server, tool) {
		return Server{}, Tool{}, toolExposureError(server.ID, tool)
	}
	values, err := m.Tools(ctx, server.ID, false)
	if err != nil {
		return Server{}, Tool{}, err
	}
	for _, value := range values {
		if value.Name == tool {
			return server, value, nil
		}
	}
	return Server{}, Tool{}, fmt.Errorf("unknown upstream tool: %s:%s", server.ID, tool)
}

func (m *Manager) Shutdown(ctx context.Context) error {
	m.stopAllToolsSubscriptions()
	servers := m.List()
	var wg sync.WaitGroup
	errCh := make(chan error, len(servers))
	for _, server := range servers {
		server := server
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := m.client.Close(ctx, server.ID); err != nil {
				errCh <- fmt.Errorf("close upstream %s: %w", server.ID, sanitizeRemoteError(server, err))
			}
		}()
	}
	wg.Wait()
	close(errCh)
	var shutdownErr error
	for err := range errCh {
		shutdownErr = errors.Join(shutdownErr, err)
	}
	m.mu.Lock()
	m.cache = map[string]toolCache{}
	m.mu.Unlock()
	return shutdownErr
}

func (m *Manager) buildStatus(server Server, health Health, connected bool, tools []Tool, lastError string) Status {
	proxied := m.ProxiedToolNames(server, tools)
	auth := "none"
	if server.Transport == "http" {
		if len(server.Headers) > 0 || server.BearerTokenEnvVar != "" {
			auth = "static"
		} else if server.Auth.Type == "oauth" || server.Auth.Type == "auto" {
			auth = "oauth"
		}
	}
	status := Status{
		ID: server.ID, Name: server.Name, Enabled: server.Enabled, Transport: server.Transport,
		Auth: auth, Health: health, Connected: connected, ToolCount: len(tools),
		Expose: server.Expose, ProxiedTools: proxied, LastError: lastError,
	}
	if pid := m.client.PID(server.ID); pid > 0 {
		status.PID = &pid
	}
	return status
}

type toolsChangedSubscriptionClient interface {
	ListenToolsChanged(context.Context, string, func()) error
}

func (m *Manager) SetToolsChangedHandler(handler func(context.Context, string) error) {
	m.mu.Lock()
	m.toolsChanged = handler
	m.mu.Unlock()
}

func (m *Manager) ensureToolsSubscription(server Server) bool {
	client, ok := m.client.(toolsChangedSubscriptionClient)
	if !ok || !server.Enabled || server.Transport != "http" {
		return false
	}
	m.mu.Lock()
	if m.toolsChanged == nil || m.subscriptions[server.ID] != nil {
		m.mu.Unlock()
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	subscription := &toolSubscription{cancel: cancel}
	m.subscriptions[server.ID] = subscription
	m.mu.Unlock()

	go m.runToolsSubscription(ctx, server.ID, subscription, client)
	tracepkg.EmitObserver(m.trace, "UPSTREAM", "upstream.tools.subscription.started", "Upstream tools-change subscription started", tracepkg.String("server", server.ID))
	return true
}

func (m *Manager) runToolsSubscription(ctx context.Context, id string, subscription *toolSubscription, client toolsChangedSubscriptionClient) {
	defer func() {
		m.mu.Lock()
		if m.subscriptions[id] == subscription {
			delete(m.subscriptions, id)
		}
		m.mu.Unlock()
	}()
	backoff := time.Second
	for {
		err := client.ListenToolsChanged(ctx, id, func() { m.handleToolsChanged(id) })
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, ErrSubscriptionsUnsupported) {
			return
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
}

func (m *Manager) handleToolsChanged(id string) {
	m.mu.Lock()
	delete(m.cache, id)
	handler := m.toolsChanged
	m.mu.Unlock()
	tracepkg.EmitObserver(m.trace, "UPSTREAM", "upstream.tools.cache-invalidated", "Upstream tools cache invalidated by server notification", tracepkg.String("server", id))
	if handler == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	err := handler(ctx, id)
	cancel()
	if err != nil {
		m.recordError(id, err)
	}
}

func (m *Manager) stopToolsSubscription(id string) {
	m.mu.Lock()
	subscription := m.subscriptions[id]
	delete(m.subscriptions, id)
	m.mu.Unlock()
	if subscription != nil {
		subscription.cancel()
	}
}

func (m *Manager) stopAllToolsSubscriptions() {
	m.mu.Lock()
	values := make([]*toolSubscription, 0, len(m.subscriptions))
	for _, subscription := range m.subscriptions {
		values = append(values, subscription)
	}
	m.subscriptions = map[string]*toolSubscription{}
	m.mu.Unlock()
	for _, subscription := range values {
		subscription.cancel()
	}
}

type oauthCredentialCleaner interface {
	ClearOAuthCredential(string) error
}

func (m *Manager) clearOAuthCredential(id string) error {
	cleaner, ok := m.client.(oauthCredentialCleaner)
	if !ok {
		return nil
	}
	return cleaner.ClearOAuthCredential(id)
}

func oauthCredentialBindingChanged(previous, next Server) bool {
	previousManaged := usesManagedOAuth(previous)
	nextManaged := usesManagedOAuth(next)
	if previousManaged != nextManaged {
		return true
	}
	if !previousManaged {
		return false
	}
	return previous.URL != next.URL || previous.Auth.Scope != next.Auth.Scope
}

func usesManagedOAuth(server Server) bool {
	if server.Transport != "http" || server.Auth.Type == "none" || strings.TrimSpace(server.BearerTokenEnvVar) != "" {
		return false
	}
	for key := range server.Headers {
		if strings.EqualFold(key, "Authorization") {
			return false
		}
	}
	return true
}

func (m *Manager) recordError(id string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors[id] = err.Error()
}

func (m *Manager) persistLocked() error {
	if m.store == nil {
		tracepkg.EmitObserver(m.trace, "UPSTREAM", "upstream.store.persist.skipped", "Upstream store persistence skipped", tracepkg.Bool("configured", false), tracepkg.Int("count", len(m.servers)))
		return nil
	}
	servers := m.listLocked()
	span := tracepkg.StartObserver(m.trace, "UPSTREAM", "upstream.store.persist", "Persisting Upstream store", tracepkg.String("path", m.store.Path), tracepkg.Int("count", len(servers)), tracepkg.Bool("atomic", true))
	if err := m.store.Save(servers); err != nil {
		span.FailMessage("Upstream store persistence failed", err)
		return err
	}
	fields := []tracepkg.Field{tracepkg.String("path", m.store.Path), tracepkg.Int("count", len(servers))}
	if info, err := os.Stat(m.store.Path); err == nil {
		fields = append(fields, tracepkg.Int64("bytes", info.Size()))
	}
	span.EndMessage("Upstream store persisted", fields...)
	return nil
}

func upstreamServerTraceFields(server Server) []tracepkg.Field {
	fields := []tracepkg.Field{tracepkg.String("server", server.ID), tracepkg.String("transport", server.Transport), tracepkg.Bool("enabled", server.Enabled)}
	if server.Transport == "stdio" {
		fields = append(fields, tracepkg.String("command", server.Command), tracepkg.Int("arg_count", len(server.Args)), tracepkg.String("cwd", server.CWD), tracepkg.Int("env_count", len(server.Env)))
	} else {
		fields = append(fields, tracepkg.URL("endpoint", server.URL), tracepkg.Int("header_count", len(server.Headers)), tracepkg.Bool("bearer_env_configured", strings.TrimSpace(server.BearerTokenEnvVar) != ""))
	}
	return fields
}

func changedServerFields(previous, next Server, existed bool) []string {
	if !existed {
		return []string{"create"}
	}
	fields := []string{}
	checks := []struct {
		name  string
		left  any
		right any
	}{
		{"name", previous.Name, next.Name}, {"transport", previous.Transport, next.Transport}, {"enabled", previous.Enabled, next.Enabled},
		{"command", previous.Command, next.Command}, {"args", previous.Args, next.Args}, {"env", previous.Env, next.Env}, {"cwd", previous.CWD, next.CWD},
		{"url", previous.URL, next.URL}, {"headers", previous.Headers, next.Headers}, {"bearer_token_env", previous.BearerTokenEnvVar, next.BearerTokenEnvVar},
		{"auth", previous.Auth, next.Auth}, {"tool_prefix", previous.ToolPrefix, next.ToolPrefix}, {"expose", previous.Expose, next.Expose},
		{"tools", previous.Tools, next.Tools}, {"disabled_tools", previous.DisabledTools, next.DisabledTools}, {"idle_timeout", previous.IdleTimeoutSec, next.IdleTimeoutSec},
		{"allow_private_network", previous.AllowPrivateNetwork, next.AllowPrivateNetwork},
	}
	for _, check := range checks {
		if !reflect.DeepEqual(check.left, check.right) {
			fields = append(fields, check.name)
		}
	}
	return fields
}

func stringSet(values []string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			result[value] = true
		}
	}
	return result
}
