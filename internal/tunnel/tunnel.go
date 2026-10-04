package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	tunnelclient "github.com/openai/tunnel-client"
	tcadmin "github.com/openai/tunnel-client/pkg/controlplane/admin"

	"go.mewis.me/codemcp/internal/logger"
	"go.mewis.me/codemcp/internal/tools"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

const (
	ProviderOpenAI     = "openai"
	defaultStopTimeout = 5 * time.Second
	restartMinDelay    = time.Second
	restartMaxDelay    = 30 * time.Second
	idleReconnectDelay = 10 * time.Minute
	metadataTTL        = 5 * time.Minute
)

type Config struct {
	Enabled             bool        `json:"enabled"`
	ID                  string      `json:"id,omitempty"`
	APIKey              string      `json:"api_key,omitempty"`
	Admin               AdminConfig `json:"admin,omitempty"`
	ControlPlaneBaseURL string      `json:"control_plane_base_url,omitempty"`
	OrganizationID      string      `json:"organization_id,omitempty"`
}

type AdminConfig struct {
	Enabled        bool   `json:"enabled"`
	EnabledSet     bool   `json:"-"`
	Key            string `json:"key,omitempty"`
	OrganizationID string `json:"organization_id,omitempty"`
	WorkspaceID    string `json:"workspace_id,omitempty"`
	TenantID       string `json:"tenant_id,omitempty"`
	Verified       bool   `json:"verified,omitempty"`
	ReadAccess     bool   `json:"read_access,omitempty"`
	ManageAccess   bool   `json:"manage_access,omitempty"`
}

func (cfg *AdminConfig) UnmarshalJSON(data []byte) error {
	type adminConfigAlias AdminConfig
	var decoded adminConfigAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*cfg = AdminConfig(decoded)
	_, cfg.EnabledSet = fields["enabled"]
	return nil
}

type AdminAccess struct {
	Read   bool `json:"read"`
	Manage bool `json:"manage"`
}

type AdminScope struct {
	OrganizationID string `json:"organization_id,omitempty"`
	WorkspaceID    string `json:"workspace_id,omitempty"`
	TenantID       string `json:"tenant_id,omitempty"`
}

type AdminState struct {
	Enabled        bool   `json:"enabled"`
	KeyConfigured  bool   `json:"key_configured"`
	Configured     bool   `json:"configured"`
	OrganizationID string `json:"organization_id,omitempty"`
	WorkspaceID    string `json:"workspace_id,omitempty"`
	TenantID       string `json:"tenant_id,omitempty"`
	Verified       bool   `json:"verified"`
	ReadAccess     bool   `json:"read_access"`
	ManageAccess   bool   `json:"manage_access"`
}

func (state AdminState) Scope() AdminScope {
	return AdminScope{
		OrganizationID: state.OrganizationID,
		WorkspaceID:    state.WorkspaceID,
		TenantID:       state.TenantID,
	}
}

type Status struct {
	Provider            string     `json:"provider"`
	Enabled             bool       `json:"enabled"`
	Running             bool       `json:"running"`
	Ready               bool       `json:"ready"`
	Restarting          bool       `json:"restarting"`
	ID                  string     `json:"id,omitempty"`
	ControlPlaneBaseURL string     `json:"control_plane_base_url,omitempty"`
	OrganizationID      string     `json:"organization_id,omitempty"`
	StartedAt           time.Time  `json:"started_at,omitempty"`
	LastError           string     `json:"last_error,omitempty"`
	Metadata            *Metadata  `json:"metadata,omitempty"`
	MetadataError       string     `json:"metadata_error,omitempty"`
	Admin               AdminState `json:"admin"`
}

type RuntimeSnapshot struct {
	Configured bool   `json:"configured"`
	Status     Status `json:"status"`
}

type Metadata struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	Creator         string    `json:"creator,omitempty"`
	TenantIDs       []string  `json:"tenant_ids,omitempty"`
	WorkspaceIDs    []string  `json:"workspace_ids,omitempty"`
	OrganizationIDs []string  `json:"organization_ids,omitempty"`
	RequestID       string    `json:"request_id,omitempty"`
	FetchedAt       time.Time `json:"fetched_at"`
}

type CreateRequest struct {
	Name            string
	Description     string
	TenantIDs       []string
	WorkspaceIDs    []string
	OrganizationIDs []string
}

type metadataFetcher func(context.Context, Config) (Metadata, error)

type backend interface {
	Start(context.Context) error
	Stop(context.Context) error
	WaitUntilReady(context.Context) error
	Done() <-chan os.Signal
}

type backendFactory func(Config, sdkmcp.Transport) (backend, error)

type LifecycleState string

const (
	LifecycleConnecting   LifecycleState = "connecting"
	LifecycleReconnecting LifecycleState = "reconnecting"
	LifecycleReady        LifecycleState = "ready"
	LifecycleDegraded     LifecycleState = "degraded"
	LifecycleStopped      LifecycleState = "stopped"
)

type LifecycleEvent struct {
	State   LifecycleState
	ID      string
	Message string
	Attempt int
	RetryIn time.Duration
}

type LifecycleObserver func(LifecycleEvent)

type serverRun struct {
	done chan struct{}
	err  error
}

type Client struct {
	reconfigureMu  sync.Mutex
	lifecycleMu    sync.RWMutex
	metadataMu     sync.Mutex
	mu             sync.RWMutex
	config         Config
	runtime        *tools.Runtime
	factory        backendFactory
	backend        backend
	cancel         context.CancelFunc
	serverRun      *serverRun
	readyCh        chan struct{}
	doneCh         chan struct{}
	sessionCtx     context.Context
	sessionCancel  context.CancelFunc
	sessionReady   chan struct{}
	running        bool
	ready          bool
	stopping       bool
	recovering     bool
	restarting     bool
	session        uint64
	generation     uint64
	restartAttempt int
	restartDelay   func(int) time.Duration
	idleInterval   time.Duration
	lastActivity   time.Time
	idleWake       chan struct{}
	metadataFetch  metadataFetcher
	metadata       *Metadata
	metadataError  string
	metadataCheck  time.Time
	startedAt      time.Time
	lastError      string
	lifecycle      LifecycleObserver
}

func New(id, key string, runtime *tools.Runtime) *Client {
	return NewConfigured(Config{ID: id, APIKey: key}, runtime)
}

func NewConfigured(cfg Config, runtime *tools.Runtime) *Client {
	return NewConfiguredWithLogger(cfg, runtime, nil)
}

func NewConfiguredWithLogger(cfg Config, runtime *tools.Runtime, log *logger.Logger) *Client {
	return newConfigured(cfg, runtime, newOpenAIBackendFactory(log))
}

func newConfigured(cfg Config, runtime *tools.Runtime, factory backendFactory) *Client {
	if factory == nil {
		factory = newOpenAIBackendFactory(nil)
	}
	return &Client{config: cfg, runtime: runtime, factory: factory, restartDelay: defaultRestartDelay, idleInterval: idleReconnectDelay, metadataFetch: FetchMetadata}
}

func FetchMetadata(ctx context.Context, cfg Config) (Metadata, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	id := strings.TrimSpace(cfg.ID)
	span := tracepkg.Start(ctx, "TUNNEL", "tunnel.metadata.fetch", "Fetching tunnel metadata", tracepkg.String("tunnel_id", id), tracepkg.String("method", "GET"), tracepkg.URL("url", adminAPIURL(cfg, "/v1/tunnels/"+url.PathEscape(id), nil)), tracepkg.Bool("runtime_key_configured", strings.TrimSpace(cfg.APIKey) != ""))
	if strings.TrimSpace(cfg.ID) == "" {
		err := errors.New("OpenAI tunnel id is empty")
		span.FailMessage("Tunnel metadata fetch validation failed", err)
		return Metadata{}, err
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		err := errors.New("OpenAI tunnel API key is empty")
		span.FailMessage("Tunnel metadata fetch validation failed", err)
		return Metadata{}, err
	}
	client, err := adminTunnelClient(cfg, cfg.APIKey)
	if err != nil {
		span.FailMessage("Tunnel metadata client setup failed", err)
		return Metadata{}, err
	}
	value, err := client.GetTunnel(ctx, cfg.ID)
	if err != nil {
		span.FailMessage("Tunnel metadata fetch failed", safeAdminTraceError(err), adminRequestErrorTraceFields(err)...)
		return Metadata{}, err
	}
	metadata := metadataFromTunnel(value)
	span.EndMessage("Tunnel metadata fetched", tracepkg.String("tunnel_id", metadata.ID), tracepkg.Int("organization_count", len(metadata.OrganizationIDs)), tracepkg.Int("workspace_count", len(metadata.WorkspaceIDs)), tracepkg.Int("tenant_count", len(metadata.TenantIDs)))
	return metadata, nil
}

func createWithAdminKey(ctx context.Context, cfg Config, apiKey string, req CreateRequest) (Metadata, error) {
	if strings.TrimSpace(apiKey) == "" {
		return Metadata{}, errors.New("OpenAI tunnel admin API key is empty")
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Description = strings.TrimSpace(req.Description)
	if req.Name == "" {
		return Metadata{}, errors.New("tunnel name is required")
	}
	if req.Description == "" {
		return Metadata{}, errors.New("tunnel description is required")
	}
	if len(req.OrganizationIDs) == 0 && len(req.WorkspaceIDs) == 0 {
		return Metadata{}, errors.New("at least one organization or workspace id is required")
	}
	client, err := adminTunnelClient(cfg, apiKey)
	if err != nil {
		return Metadata{}, err
	}
	value, err := client.CreateTunnel(ctx, tcadmin.TunnelCreateRequest{
		Name: req.Name, Description: req.Description,
		TenantIDs: append([]string(nil), req.TenantIDs...), WorkspaceIDs: append([]string(nil), req.WorkspaceIDs...), OrganizationIDs: append([]string(nil), req.OrganizationIDs...),
	})
	if err != nil {
		return Metadata{}, err
	}
	return metadataFromTunnel(value), nil
}

func metadataFromTunnel(value *tcadmin.Tunnel) Metadata {
	if value == nil {
		return Metadata{}
	}
	return Metadata{
		ID: value.ID, Name: value.Name, Description: value.Description, Creator: value.Creator,
		TenantIDs: append([]string(nil), value.TenantIDs...), WorkspaceIDs: append([]string(nil), value.WorkspaceIDs...), OrganizationIDs: append([]string(nil), value.OrganizationIDs...),
		RequestID: value.RequestID, FetchedAt: time.Now().UTC(),
	}
}

func (c *Client) RefreshMetadata(ctx context.Context, force bool) (Metadata, error) {
	if c == nil {
		return Metadata{}, errors.New("tunnel client is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	span := tracepkg.Start(ctx, "TUNNEL", "tunnel.metadata.refresh", "Refreshing tunnel metadata", tracepkg.Bool("force_refresh", force))
	c.metadataMu.Lock()
	defer c.metadataMu.Unlock()
	c.mu.RLock()
	cfg := c.config
	if !force && c.metadata != nil && time.Since(c.metadata.FetchedAt) < metadataTTL {
		value := cloneMetadata(*c.metadata)
		c.mu.RUnlock()
		span.EndMessage("Tunnel metadata loaded from cache", tracepkg.String("tunnel_id", value.ID), tracepkg.Bool("cache_hit", true), tracepkg.Int64("cache_age_ms", time.Since(value.FetchedAt).Milliseconds()), tracepkg.Int64("cache_ttl_ms", metadataTTL.Milliseconds()))
		return value, nil
	}
	if !force && c.metadata == nil && c.metadataError != "" && time.Since(c.metadataCheck) < time.Minute {
		err := errors.New(c.metadataError)
		c.mu.RUnlock()
		span.FailMessage("Tunnel metadata negative cache hit", errors.New("cached tunnel metadata fetch failure"), tracepkg.Bool("cache_hit", true), tracepkg.Bool("negative_cache", true), tracepkg.Int64("cache_age_ms", time.Since(c.metadataCheck).Milliseconds()))
		return Metadata{}, err
	}
	fetch := c.metadataFetch
	c.mu.RUnlock()
	if !Configured(cfg) {
		err := errors.New("OpenAI tunnel is not configured")
		span.FailMessage("Tunnel metadata refresh validation failed", err, tracepkg.Bool("cache_hit", false))
		return Metadata{}, err
	}
	if fetch == nil {
		fetch = FetchMetadata
	}
	value, err := fetch(ctx, cfg)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.config.ID != cfg.ID || c.config.APIKey != cfg.APIKey || c.config.ControlPlaneBaseURL != cfg.ControlPlaneBaseURL {
		err := errors.New("OpenAI tunnel configuration changed while fetching metadata")
		span.FailMessage("Tunnel metadata refresh discarded", err, tracepkg.Bool("cache_hit", false))
		return Metadata{}, err
	}
	if err != nil {
		c.metadataCheck = time.Now().UTC()
		c.metadataError = err.Error()
		span.FailMessage("Tunnel metadata refresh failed", safeAdminTraceError(err), tracepkg.Bool("cache_hit", false))
		return Metadata{}, err
	}
	value = cloneMetadata(value)
	if value.FetchedAt.IsZero() {
		value.FetchedAt = time.Now().UTC()
	}
	c.metadata = &value
	c.metadataCheck = value.FetchedAt
	c.metadataError = ""
	span.EndMessage("Tunnel metadata refreshed", tracepkg.String("tunnel_id", value.ID), tracepkg.Bool("cache_hit", false), tracepkg.Int64("cache_ttl_ms", metadataTTL.Milliseconds()))
	return cloneMetadata(value), nil
}

func cloneMetadata(value Metadata) Metadata {
	value.TenantIDs = append([]string(nil), value.TenantIDs...)
	value.WorkspaceIDs = append([]string(nil), value.WorkspaceIDs...)
	value.OrganizationIDs = append([]string(nil), value.OrganizationIDs...)
	return value
}

func (c *Client) SeedMetadata(value Metadata) error {
	if c == nil {
		return errors.New("tunnel client is unavailable")
	}
	c.metadataMu.Lock()
	defer c.metadataMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if strings.TrimSpace(value.ID) == "" {
		value.ID = c.config.ID
	}
	if strings.TrimSpace(c.config.ID) == "" || value.ID != c.config.ID {
		return errors.New("tunnel metadata does not match the configured tunnel")
	}
	value = cloneMetadata(value)
	c.metadata = &value
	c.metadataCheck = value.FetchedAt
	c.metadataError = ""
	return nil
}

func (c *Client) SetLifecycleObserver(observer LifecycleObserver) {
	if c == nil {
		return
	}
	c.lifecycleMu.Lock()
	c.lifecycle = observer
	c.lifecycleMu.Unlock()
}

func TraceLifecycleObserver(observer tracepkg.Observer, next LifecycleObserver) LifecycleObserver {
	return func(event LifecycleEvent) {
		fields := []tracepkg.Field{tracepkg.String("state", string(event.State))}
		if strings.TrimSpace(event.ID) != "" {
			fields = append(fields, tracepkg.String("tunnel_id", event.ID))
		}
		if event.Attempt > 0 {
			fields = append(fields, tracepkg.Int("attempt", event.Attempt), tracepkg.DurationMS("retry_in", event.RetryIn))
		}
		switch event.State {
		case LifecycleConnecting:
			tracepkg.EmitPhaseObserver(observer, tracepkg.PhaseStart, "TUNNEL", "tunnel.connection", "Connecting tunnel", fields...)
		case LifecycleReconnecting:
			tracepkg.EmitPhaseObserver(observer, tracepkg.PhaseStart, "TUNNEL", "tunnel.connection", "Reconnecting tunnel", fields...)
		case LifecycleReady:
			tracepkg.EmitPhaseObserver(observer, tracepkg.PhaseEnd, "TUNNEL", "tunnel.connection", "Tunnel connected", fields...)
		case LifecycleDegraded:
			tracepkg.EmitPhaseObserver(observer, tracepkg.PhaseInfo, "TUNNEL", "tunnel.connection", "Tunnel degraded", fields...)
		case LifecycleStopped:
			tracepkg.EmitPhaseObserver(observer, tracepkg.PhaseEnd, "TUNNEL", "tunnel.stop", "Tunnel stopped", fields...)
		}
		if next != nil {
			next(event)
		}
	}
}

func (c *Client) emitLifecycle(state LifecycleState, id, message string) {
	c.emitLifecycleEvent(LifecycleEvent{State: state, ID: id, Message: message})
}

func (c *Client) emitLifecycleEvent(event LifecycleEvent) {
	if c == nil {
		return
	}
	c.lifecycleMu.RLock()
	observer := c.lifecycle
	c.lifecycleMu.RUnlock()
	if observer != nil {
		observer(event)
	}
}

func defaultRestartDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := restartMinDelay
	for i := 1; i < attempt && delay < restartMaxDelay; i++ {
		delay *= 2
		if delay > restartMaxDelay {
			delay = restartMaxDelay
		}
	}
	return delay
}

func newOpenAIBackendFactory(log *logger.Logger) backendFactory {
	if log == nil {
		log = logger.New(logger.Info)
	}
	return func(cfg Config, transport sdkmcp.Transport) (backend, error) {
		return newOpenAIBackend(cfg, transport, log.LineWriter("TUNNEL"))
	}
}

func newOpenAIBackend(cfg Config, transport sdkmcp.Transport, logWriter io.Writer) (backend, error) {
	return tunnelclient.New(tunnelclient.Config{
		TunnelID:            cfg.ID,
		APIKey:              cfg.APIKey,
		ControlPlaneBaseURL: cfg.ControlPlaneBaseURL,
		OrganizationID:      cfg.OrganizationID,
		PollTimeout:         2 * time.Second,
		LogWriter:           logWriter,
	}, transport)
}

func ValidateConfig(cfg Config) error {
	if cfg.ID != strings.TrimSpace(cfg.ID) {
		return errors.New("OpenAI tunnel id must not contain leading or trailing whitespace")
	}
	if cfg.APIKey != strings.TrimSpace(cfg.APIKey) {
		return errors.New("OpenAI tunnel API key must not contain leading or trailing whitespace")
	}
	if raw := strings.TrimSpace(cfg.ControlPlaneBaseURL); raw != "" {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return fmt.Errorf("invalid OpenAI tunnel control plane base URL %q", raw)
		}
		if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
			return fmt.Errorf("OpenAI tunnel control plane base URL must use HTTPS unless the host is loopback")
		}
	}
	return nil
}

func ValidateActivationConfig(cfg Config) error {
	if err := ValidateConfig(cfg); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.ID) == "" {
		return errors.New("OpenAI tunnel id is not configured")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return errors.New("OpenAI tunnel runtime API key is not configured")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func Configured(cfg Config) bool {
	return strings.TrimSpace(cfg.ID) != "" && strings.TrimSpace(cfg.APIKey) != ""
}

func ClearRuntimeConfig(cfg Config) Config {
	cfg.Enabled = false
	cfg.ID = ""
	cfg.APIKey = ""
	cfg.OrganizationID = ""
	return cfg
}

func StatusFromConfig(cfg Config, metadata *Metadata) Status {
	var copied *Metadata
	if metadata != nil {
		value := cloneMetadata(*metadata)
		copied = &value
	}
	return Status{
		Provider:            ProviderOpenAI,
		Enabled:             cfg.Enabled,
		ID:                  cfg.ID,
		ControlPlaneBaseURL: cfg.ControlPlaneBaseURL,
		OrganizationID:      cfg.OrganizationID,
		Metadata:            copied,
		Admin:               AdminStateFromConfig(cfg),
	}
}

func RuntimeConfigEqual(left, right Config) bool {
	left.Admin = AdminConfig{}
	right.Admin = AdminConfig{}
	return left == right
}

func (c *Client) SyncManagementConfig(cfg Config) error {
	if c == nil {
		return errors.New("tunnel client is unavailable")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !RuntimeConfigEqual(c.config, cfg) {
		return errors.New("cannot sync management config when runtime tunnel configuration differs")
	}
	c.config.Admin = cfg.Admin
	return nil
}

func (c *Client) Reconcile(cfg Config, metadata *Metadata, runtimeActive bool) error {
	if c == nil {
		return errors.New("tunnel client is unavailable")
	}
	if err := ValidateConfig(cfg); err != nil {
		return err
	}
	if metadata != nil {
		if strings.TrimSpace(metadata.ID) == "" || strings.TrimSpace(metadata.ID) != strings.TrimSpace(cfg.ID) {
			return errors.New("tunnel metadata does not match configured tunnel id")
		}
	}

	c.reconfigureMu.Lock()
	defer c.reconfigureMu.Unlock()

	current := c.Config()
	status := c.Status()
	if !RuntimeConfigEqual(current, cfg) {
		if runtimeActive {
			return c.reconcileRunningConfig(cfg, metadata)
		}
		if status.Running || status.Restarting {
			if err := c.Stop(); err != nil {
				return err
			}
		}
		if err := c.Configure(cfg); err != nil {
			return err
		}
		if metadata != nil {
			return c.SeedMetadata(*metadata)
		}
		return nil
	}

	if err := c.SyncManagementConfig(cfg); err != nil {
		return err
	}
	if metadata != nil {
		if err := c.SeedMetadata(*metadata); err != nil {
			return err
		}
	}
	status = c.Status()
	shouldRun := runtimeActive && cfg.Enabled && Configured(cfg)
	switch {
	case shouldRun && !status.Running && !status.Restarting:
		return c.Start()
	case !shouldRun && (status.Running || status.Restarting):
		return c.Stop()
	default:
		return nil
	}
}

func (c *Client) reconcileRunningConfig(cfg Config, metadata *Metadata) error {
	current := c.Config()
	status := c.Status()
	wasRunning := status.Running || status.Restarting
	previousMetadata := status.Metadata
	if err := c.Stop(); err != nil {
		return err
	}
	rollback := func(cause error) error {
		rollbackErr := c.Configure(current)
		if previousMetadata != nil {
			rollbackErr = errors.Join(rollbackErr, c.SeedMetadata(*previousMetadata))
		}
		if wasRunning {
			rollbackErr = errors.Join(rollbackErr, c.Start())
		}
		return errors.Join(cause, rollbackErr)
	}
	if err := c.Configure(cfg); err != nil {
		return rollback(err)
	}
	if metadata != nil {
		if err := c.SeedMetadata(*metadata); err != nil {
			return rollback(err)
		}
	}
	if cfg.Enabled && Configured(cfg) {
		if err := c.Start(); err != nil {
			return rollback(err)
		}
	}
	return nil
}

func (c *Client) Configure(cfg Config) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running || c.stopping || c.restarting || c.sessionCancel != nil {
		return errors.New("cannot configure a running tunnel")
	}
	changed := c.config.ID != cfg.ID || c.config.APIKey != cfg.APIKey || c.config.ControlPlaneBaseURL != cfg.ControlPlaneBaseURL
	c.config = cfg
	if changed {
		c.metadata = nil
		c.metadataError = ""
		c.metadataCheck = time.Time{}
	}
	c.lastError = ""
	return nil
}

func (c *Client) Reconfigure(cfg Config, persist func() error) error {
	return c.reconfigure(cfg, nil, persist)
}

func (c *Client) ReconfigureSeeded(cfg Config, metadata Metadata, persist func() error) error {
	return c.reconfigure(cfg, &metadata, persist)
}

func (c *Client) reconfigure(cfg Config, metadata *Metadata, persist func() error) error {
	if persist == nil {
		return errors.New("tunnel persistence callback is required")
	}
	if err := ValidateConfig(cfg); err != nil {
		return err
	}
	if cfg.Enabled {
		if err := ValidateActivationConfig(cfg); err != nil {
			return err
		}
	}
	c.reconfigureMu.Lock()
	defer c.reconfigureMu.Unlock()

	current := c.Config()
	status := c.Status()
	wasRunning := status.Running || status.Restarting
	if err := c.Stop(); err != nil {
		return err
	}
	if err := c.Configure(cfg); err != nil {
		return errors.Join(err, c.rollbackReconfigure(current, wasRunning))
	}
	if metadata != nil {
		if err := c.SeedMetadata(*metadata); err != nil {
			return errors.Join(err, c.rollbackReconfigure(current, wasRunning))
		}
	}
	if cfg.Enabled {
		if err := c.Start(); err != nil {
			return errors.Join(err, c.rollbackReconfigure(current, wasRunning))
		}
	}
	if err := persist(); err != nil {
		return errors.Join(err, c.rollbackReconfigure(current, wasRunning))
	}
	return nil
}

func (c *Client) rollbackReconfigure(cfg Config, restart bool) error {
	var rollbackErr error
	status := c.Status()
	if status.Running || status.Restarting {
		rollbackErr = errors.Join(rollbackErr, c.Stop())
	}
	rollbackErr = errors.Join(rollbackErr, c.Configure(cfg))
	if restart {
		rollbackErr = errors.Join(rollbackErr, c.Start())
	}
	return rollbackErr
}

func (c *Client) Config() Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.config
}

func (c *Client) Start() error { return c.StartContext(context.Background()) }

func (c *Client) StartContext(parent context.Context) error {
	if parent == nil {
		parent = context.Background()
	}
	c.mu.Lock()
	if !c.config.Enabled {
		c.mu.Unlock()
		return nil
	}
	if err := ValidateActivationConfig(c.config); err != nil {
		id := c.config.ID
		c.lastError = err.Error()
		c.mu.Unlock()
		c.emitLifecycle(LifecycleDegraded, id, err.Error())
		return err
	}
	if c.running || c.stopping || c.restarting || c.sessionCancel != nil {
		c.mu.Unlock()
		return errors.New("OpenAI tunnel already running")
	}
	sessionCtx, sessionCancel := context.WithCancel(parent)
	c.session++
	session := c.session
	c.sessionCtx = sessionCtx
	c.sessionCancel = sessionCancel
	c.sessionReady = make(chan struct{})
	c.restartAttempt = 0
	c.recovering = false
	c.restarting = false
	c.lastActivity = time.Now()
	c.idleWake = make(chan struct{}, 1)
	idleWake := c.idleWake
	c.mu.Unlock()

	if err := c.startGeneration(session, sessionCtx, true); err != nil {
		sessionCancel()
		c.mu.Lock()
		if c.session == session {
			c.sessionCtx = nil
			c.sessionCancel = nil
			c.sessionReady = nil
			c.lastActivity = time.Time{}
			c.idleWake = nil
		}
		c.mu.Unlock()
		return err
	}
	go c.watchSession(session, sessionCtx)
	go c.watchIdleReconnect(session, sessionCtx, idleWake)
	return nil
}

func (c *Client) startGeneration(session uint64, parent context.Context, initial bool) error {
	c.mu.Lock()
	id := c.config.ID
	if c.session != session || parent.Err() != nil {
		c.mu.Unlock()
		return context.Canceled
	}
	if c.runtime == nil || c.runtime.Registry == nil {
		err := errors.New("OpenAI tunnel requires an MCP tools runtime")
		c.lastError = err.Error()
		c.mu.Unlock()
		c.emitLifecycle(LifecycleDegraded, id, err.Error())
		return err
	}
	if err := ValidateActivationConfig(c.config); err != nil {
		c.lastError = err.Error()
		c.mu.Unlock()
		c.emitLifecycle(LifecycleDegraded, id, err.Error())
		return err
	}

	bridge, err := newSDKBridge(c.runtime)
	if err != nil {
		c.lastError = err.Error()
		c.mu.Unlock()
		c.emitLifecycle(LifecycleDegraded, id, err.Error())
		return err
	}
	serverTransport, tunnelTransport := newCancellationSafeInMemoryTransports()
	tunnelBackend, err := c.factory(c.config, withSessionTransportActivity(tunnelTransport, func() { c.markMCPActivity(session) }))
	if err != nil {
		err = redactConfigSecrets(err, c.config)
		c.lastError = err.Error()
		c.mu.Unlock()
		c.emitLifecycle(LifecycleDegraded, id, err.Error())
		return err
	}

	runCtx, cancel := context.WithCancel(parent)
	run := &serverRun{done: make(chan struct{})}
	go func() {
		run.err = bridge.Run(runCtx, serverTransport)
		close(run.done)
	}()

	if err := tunnelBackend.Start(runCtx); err != nil {
		err = redactConfigSecrets(err, c.config)
		cancel()
		c.lastError = err.Error()
		c.mu.Unlock()
		_ = waitRun(context.Background(), run, time.Second)
		c.emitLifecycle(LifecycleDegraded, id, err.Error())
		return err
	}

	c.generation++
	generation := c.generation
	c.backend = tunnelBackend
	c.cancel = cancel
	c.serverRun = run
	c.readyCh = make(chan struct{})
	c.doneCh = make(chan struct{})
	c.running = true
	c.ready = false
	c.stopping = false
	c.recovering = false
	c.startedAt = time.Now()
	if !c.restarting {
		c.lastError = ""
	}
	c.mu.Unlock()
	if initial {
		c.emitLifecycle(LifecycleConnecting, id, "waiting for control-plane readiness")
	}

	go c.watchReady(session, generation, tunnelBackend, runCtx, parent)
	go c.watchServer(session, generation, run, parent)
	go c.watchBackend(session, generation, tunnelBackend, runCtx, parent)
	return nil
}

func (c *Client) watchReady(session, generation uint64, tunnelBackend backend, ctx, parent context.Context) {
	err := tunnelBackend.WaitUntilReady(ctx)
	if err != nil {
		if ctx.Err() == nil {
			c.recoverGeneration(session, generation, parent, err.Error())
		}
		return
	}
	var id string
	becameReady := false
	c.mu.Lock()
	if c.session == session && c.generation == generation && c.running && !c.ready {
		c.ready = true
		c.restarting = false
		c.restartAttempt = 0
		c.lastError = ""
		id = c.config.ID
		becameReady = true
		close(c.readyCh)
		if c.sessionReady != nil {
			select {
			case <-c.sessionReady:
			default:
				close(c.sessionReady)
			}
		}
	}
	c.mu.Unlock()
	if becameReady {
		c.emitLifecycle(LifecycleReady, id, "control plane ready")
	}
}

func (c *Client) watchSession(session uint64, ctx context.Context) {
	<-ctx.Done()
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultStopTimeout)
	defer cancel()
	_ = c.stopSession(stopCtx, session, true)
}

func (c *Client) markMCPActivity(session uint64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.session != session || c.sessionCtx == nil {
		c.mu.Unlock()
		return
	}
	c.lastActivity = time.Now()
	wake := c.idleWake
	c.mu.Unlock()
	if wake != nil {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

func (c *Client) watchIdleReconnect(session uint64, ctx context.Context, wake <-chan struct{}) {
	for {
		c.mu.RLock()
		if c.session != session || c.idleWake == nil || c.idleWake != wake {
			c.mu.RUnlock()
			return
		}
		interval := c.idleInterval
		if interval <= 0 {
			interval = idleReconnectDelay
		}
		lastActivity := c.lastActivity
		c.mu.RUnlock()
		if lastActivity.IsZero() {
			lastActivity = time.Now()
		}
		delay := time.Until(lastActivity.Add(interval))
		if delay < 0 {
			delay = 0
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			continue
		case <-timer.C:
			c.reconnectIdle(session, ctx)
		}
	}
}

func (c *Client) reconnectIdle(session uint64, parent context.Context) {
	now := time.Now()
	c.mu.Lock()
	if c.session != session || parent.Err() != nil {
		c.mu.Unlock()
		return
	}
	interval := c.idleInterval
	if interval <= 0 {
		interval = idleReconnectDelay
	}
	if !c.lastActivity.IsZero() && now.Before(c.lastActivity.Add(interval)) {
		c.mu.Unlock()
		return
	}
	c.lastActivity = now
	generation := c.generation
	canRestart := c.config.Enabled && c.running && c.ready && !c.stopping && !c.recovering && !c.restarting
	c.mu.Unlock()
	if canRestart {
		c.restartGeneration(session, generation, parent, "MCP idle timeout reached", false)
	}
}

func (c *Client) watchServer(session, generation uint64, run *serverRun, parent context.Context) {
	<-run.done
	if run.err == nil || errors.Is(run.err, context.Canceled) {
		return
	}
	c.recoverGeneration(session, generation, parent, "embedded MCP server stopped: "+run.err.Error())
}

func (c *Client) watchBackend(session, generation uint64, tunnelBackend backend, ctx, parent context.Context) {
	done := tunnelBackend.Done()
	if done == nil {
		return
	}
	select {
	case <-ctx.Done():
		return
	case signal, ok := <-done:
		message := "tunnel runtime stopped"
		if ok && signal != nil {
			message += ": " + signal.String()
		}
		c.recoverGeneration(session, generation, parent, message)
	}
}

func (c *Client) recoverGeneration(session, generation uint64, parent context.Context, message string) {
	c.mu.RLock()
	cfg := c.config
	c.mu.RUnlock()
	c.restartGeneration(session, generation, parent, redactSecretText(message, cfg), true)
}

func redactConfigSecrets(err error, cfg Config) error {
	if err == nil {
		return nil
	}
	return errors.New(redactSecretText(err.Error(), cfg))
}

func redactSecretText(value string, cfg Config) string {
	for _, secret := range []string{strings.TrimSpace(cfg.APIKey), strings.TrimSpace(cfg.Admin.Key)} {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[redacted]")
		}
	}
	return value
}

func (c *Client) restartGeneration(session, generation uint64, parent context.Context, message string, degraded bool) {
	c.mu.Lock()
	if c.session != session || c.generation != generation || !c.running || c.stopping || c.recovering || parent.Err() != nil {
		c.mu.Unlock()
		return
	}
	if !degraded && !c.ready {
		c.mu.Unlock()
		return
	}
	c.recovering = true
	c.restarting = true
	if degraded {
		c.lastError = message
	} else {
		c.lastError = ""
	}
	id := c.config.ID
	c.mu.Unlock()
	if degraded {
		c.emitLifecycle(LifecycleDegraded, id, message)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), defaultStopTimeout)
	_ = c.stopGeneration(stopCtx, generation)
	cancel()

	c.mu.Lock()
	if c.session != session || parent.Err() != nil || !c.config.Enabled {
		c.recovering = false
		c.mu.Unlock()
		return
	}
	c.recovering = false
	c.mu.Unlock()
	go c.restartSession(session, parent)
}

func (c *Client) restartSession(session uint64, parent context.Context) {
	for {
		c.mu.Lock()
		if c.session != session || parent.Err() != nil || !c.config.Enabled {
			if c.session == session {
				c.restarting = false
			}
			c.mu.Unlock()
			return
		}
		c.restartAttempt++
		attempt := c.restartAttempt
		c.restarting = true
		id := c.config.ID
		delayFn := c.restartDelay
		if delayFn == nil {
			delayFn = defaultRestartDelay
		}
		delay := delayFn(attempt)
		c.mu.Unlock()

		c.emitLifecycleEvent(LifecycleEvent{State: LifecycleReconnecting, ID: id, Message: "retrying tunnel connection", Attempt: attempt, RetryIn: delay})
		timer := time.NewTimer(delay)
		select {
		case <-parent.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
		if err := c.startGeneration(session, parent, false); err != nil {
			if parent.Err() != nil {
				return
			}
			continue
		}
		return
	}
}

func (c *Client) WaitUntilReady(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.RLock()
	if c.ready {
		c.mu.RUnlock()
		return nil
	}
	if c.sessionCtx == nil || c.sessionReady == nil {
		c.mu.RUnlock()
		return errors.New("OpenAI tunnel is not running")
	}
	readyCh := c.sessionReady
	sessionDone := c.sessionCtx.Done()
	c.mu.RUnlock()

	select {
	case <-readyCh:
		return nil
	case <-sessionDone:
		return errors.New("OpenAI tunnel stopped before becoming ready")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultStopTimeout)
	defer cancel()
	return c.StopContext(ctx)
}

func (c *Client) StopContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return c.stopSession(ctx, 0, true)
}

func (c *Client) stopSession(ctx context.Context, expectedSession uint64, emitStopped bool) error {
	c.mu.Lock()
	if expectedSession != 0 && c.session != expectedSession {
		c.mu.Unlock()
		return nil
	}
	if c.sessionCancel == nil && !c.running && !c.restarting {
		c.mu.Unlock()
		return nil
	}
	cancel := c.sessionCancel
	hadSession := cancel != nil
	generation := c.generation
	running := c.running
	restarting := c.restarting
	id := c.config.ID
	c.session++
	c.sessionCtx = nil
	c.sessionCancel = nil
	c.sessionReady = nil
	c.restarting = false
	c.recovering = false
	c.restartAttempt = 0
	c.lastActivity = time.Time{}
	c.idleWake = nil
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if running {
		err := c.stopGeneration(ctx, generation)
		if emitStopped && !c.Status().Running {
			c.emitLifecycle(LifecycleStopped, id, "tunnel stopped")
		}
		return err
	}
	if emitStopped && (restarting || hadSession) {
		c.emitLifecycle(LifecycleStopped, id, "tunnel stopped")
	}
	return nil
}

func (c *Client) stopGeneration(ctx context.Context, generation uint64) error {
	c.mu.Lock()
	if !c.running || c.generation != generation {
		c.mu.Unlock()
		return nil
	}
	if c.stopping {
		doneCh := c.doneCh
		c.mu.Unlock()
		select {
		case <-doneCh:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.stopping = true
	tunnelBackend := c.backend
	cancel := c.cancel
	run := c.serverRun
	doneCh := c.doneCh
	cfg := c.config
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	var backendErr error
	if tunnelBackend != nil {
		backendErr = tunnelBackend.Stop(ctx)
	}
	serverErr := waitRun(ctx, run, 0)
	if errors.Is(serverErr, context.Canceled) {
		serverErr = nil
	}

	c.mu.Lock()
	if c.generation == generation {
		c.backend = nil
		c.cancel = nil
		c.serverRun = nil
		c.readyCh = nil
		c.doneCh = nil
		c.running = false
		c.ready = false
		c.stopping = false
		c.startedAt = time.Time{}
		close(doneCh)
	}
	c.mu.Unlock()
	return redactConfigSecrets(errors.Join(backendErr, serverErr), cfg)
}

func waitRun(ctx context.Context, run *serverRun, fallback time.Duration) error {
	if run == nil {
		return nil
	}
	if fallback > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, fallback)
		defer cancel()
	}
	select {
	case <-run.done:
		return run.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) Status() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.statusLocked()
}

func (c *Client) Snapshot() RuntimeSnapshot {
	if c == nil {
		return RuntimeSnapshot{Status: Status{Provider: ProviderOpenAI}}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return RuntimeSnapshot{Configured: Configured(c.config), Status: c.statusLocked()}
}

func (c *Client) statusLocked() Status {
	var metadata *Metadata
	if c.metadata != nil {
		value := cloneMetadata(*c.metadata)
		metadata = &value
	}
	admin := AdminStateFromConfig(c.config)
	return Status{
		Provider: ProviderOpenAI, Enabled: c.config.Enabled, Running: c.running, Ready: c.ready, Restarting: c.restarting, ID: c.config.ID,
		ControlPlaneBaseURL: c.config.ControlPlaneBaseURL, OrganizationID: c.config.OrganizationID,
		StartedAt: c.startedAt, LastError: c.lastError, Metadata: metadata, MetadataError: c.metadataError, Admin: admin,
	}
}
