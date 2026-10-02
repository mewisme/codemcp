package application

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/backgrounddelivery"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/checkpoint"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/doctor"
	agentcompletion "go.mewis.me/codemcp/internal/history/completion"
	codegraph "go.mewis.me/codemcp/internal/integrations/codegraph"
	typesafeintegration "go.mewis.me/codemcp/internal/integrations/typesafe"
	"go.mewis.me/codemcp/internal/llm"
	"go.mewis.me/codemcp/internal/mcp"
	released024 "go.mewis.me/codemcp/internal/migration/released024"
	"go.mewis.me/codemcp/internal/network"
	"go.mewis.me/codemcp/internal/notification"
	mcpoauth "go.mewis.me/codemcp/internal/oauth"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
	"go.mewis.me/codemcp/internal/secretinventory"
	"go.mewis.me/codemcp/internal/secretstore"
	managed "go.mewis.me/codemcp/internal/service"
	telegramcredential "go.mewis.me/codemcp/internal/telegram/credential"
	"go.mewis.me/codemcp/internal/tunnel"
	"go.mewis.me/codemcp/internal/upstream"
	"go.mewis.me/codemcp/internal/workspace"
)

type DoctorDependencies struct {
	InspectConfig        func() (config.Inspection, error)
	Workspaces           *workspace.Manager
	Checkpoints          *checkpoint.Store
	Completions          *agentcompletion.Service
	Approvals            *approval.Manager
	BackgroundDeliveries *backgrounddelivery.Broker
	Notifications        *notification.Coordinator
	TelegramHealth       func() TelegramHealthSnapshot
	Upstream             *upstream.Manager
	OAuth                *mcpoauth.Store
	Tunnel               *tunnel.Client
	LLM                  *LLMService
}

type TelegramHealthSnapshot struct {
	Enabled                  bool
	TokenConfigured          bool
	AuthorizationConfigured  bool
	Running                  bool
	PollingHealthy           bool
	Reconnecting             bool
	ReconnectCount           uint64
	DeliveryDegraded         bool
	DeliveryRateLimited      bool
	DeliveryFailures         uint64
	DeliveryRetryAfterMS     int64
	RichMessageSupported     bool
	RichMessageFallback      bool
	TopicsSupported          bool
	TopicsConfigured         bool
	TopicsEffective          bool
	TopicStoreHealthy        bool
	TopicReconcilePending    bool
	TopicCount               int
	TopicLastError           string
	CommandsPublished        bool
	CommandDrift             bool
	CommandCount             int
	MenuReconciled           bool
	MenuDriftCount           int
	AllowedUpdateCount       int
	MaxFileTransferBytes     int64
	LogsMiniAppEnabled       bool
	LogsMiniAppState         string
	LogsMiniAppDependency    bool
	LogsMiniAppListenerReady bool
	LogsMiniAppTunnelRunning bool
	LogsMiniAppIngressReady  bool
	LogsMiniAppGeneration    uint64
}

type DoctorService struct {
	collector *doctor.Collector
}

func NewDefaultDoctorService(additional ...doctor.Provider) (*DoctorService, error) {
	workspaces := workspace.NewManager(workspace.DefaultStorePath())
	deps := DoctorDependencies{
		InspectConfig: config.Inspect,
		Workspaces:    workspaces,
		Checkpoints:   checkpoint.NewWorkspaceStore(configformat.RootPath(), workspaces),
		Upstream:      upstream.NewManager(upstream.NewStore(upstream.Path())),
		OAuth:         mcpoauth.NewStore(mcpoauth.Path()),
		LLM:           NewLLMService(configformat.RootPath()),
	}
	return NewDoctorService(deps, additional...)
}

func NewDoctorService(deps DoctorDependencies, additional ...doctor.Provider) (*DoctorService, error) {
	if deps.InspectConfig == nil {
		deps.InspectConfig = config.Inspect
	}
	if deps.Workspaces == nil {
		deps.Workspaces = workspace.NewManager(workspace.DefaultStorePath())
	}
	if deps.LLM == nil {
		deps.LLM = NewLLMService(configformat.RootPath())
	}
	providers := defaultDoctorProviders(deps)
	providers = append(providers, additional...)
	if err := doctor.ValidateInventoryCoverage(providers); err != nil {
		return nil, err
	}
	return NewDoctorServiceWithProviders(providers...)
}

func NewDoctorServiceWithProviders(providers ...doctor.Provider) (*DoctorService, error) {
	collector, err := doctor.New(providers...)
	if err != nil {
		return nil, err
	}
	return &DoctorService{collector: collector}, nil
}

func (s *DoctorService) Run(ctx context.Context) (Result[doctor.Report], error) {
	return runOperation(ctx, "DOCTOR", capability.DoctorRead, "Diagnosing CodeMCP", nil, func() (doctor.Report, error) {
		if s == nil || s.collector == nil {
			return doctor.Report{}, errors.New("doctor service is unavailable")
		}
		return s.collector.Collect(ctx), nil
	})
}

func BindDoctorOperations(dispatcher *Dispatcher, service *DoctorService) error {
	if dispatcher == nil || service == nil {
		return errors.New("doctor operation dependencies are unavailable")
	}
	return dispatcher.Register(capability.DoctorRead, func(ctx context.Context, _ any) (any, error) {
		result, err := service.Run(ctx)
		return result.Value, err
	})
}

type doctorSnapshot struct {
	deps DoctorDependencies

	configOnce sync.Once
	config     config.Inspection
	configErr  error

	registryOnce sync.Once
	registry     workspace.RegistryInspection
	registryErr  error

	upstreamOnce sync.Once
	upstreams    []upstream.Server
	upstreamErr  error
}

func (s *doctorSnapshot) inspectConfig() (config.Inspection, error) {
	s.configOnce.Do(func() { s.config, s.configErr = s.deps.InspectConfig() })
	return s.config, s.configErr
}

func (s *doctorSnapshot) inspectRegistry() (workspace.RegistryInspection, error) {
	s.registryOnce.Do(func() { s.registry, s.registryErr = s.deps.Workspaces.InspectRegistry() })
	return s.registry, s.registryErr
}

func (s *doctorSnapshot) inspectUpstreams() ([]upstream.Server, error) {
	s.upstreamOnce.Do(func() {
		if s.deps.Upstream == nil {
			s.upstreamErr = errors.New("upstream manager is unavailable")
			return
		}
		statuses, err := s.deps.Upstream.InspectStatuses()
		if err != nil {
			s.upstreamErr = err
			return
		}
		s.upstreams = make([]upstream.Server, 0, len(statuses))
		for _, status := range statuses {
			server, ok := s.deps.Upstream.Get(status.ID)
			if ok {
				s.upstreams = append(s.upstreams, server)
				continue
			}
			s.upstreams = append(s.upstreams, upstream.Server{ID: status.ID, Enabled: status.Enabled, Transport: status.Transport})
		}
	})
	return append([]upstream.Server(nil), s.upstreams...), s.upstreamErr
}

func defaultDoctorProviders(deps DoctorDependencies) []doctor.Provider {
	snapshot := &doctorSnapshot{deps: deps}
	return []doctor.Provider{
		doctorProvider(doctor.ComponentInstallCurrent, func(context.Context) (doctor.Component, error) {
			overview, err := LoadInstallationOverview()
			if err != nil {
				return doctor.Component{}, err
			}
			return healthy("installation is detectable",
				doctor.Flag{ID: "managed", Value: overview.Managed},
				doctor.Flag{ID: "update_cached", Value: overview.CachedUpdate != nil},
			), nil
		}),
		doctorProvider(doctor.ComponentConfigOverview, func(context.Context) (doctor.Component, error) {
			inspection, err := snapshot.inspectConfig()
			if err != nil {
				return doctor.Component{}, err
			}
			if !inspection.Exists {
				return unavailable("configuration is not initialized", doctor.Remediation{
					ID: "initialize", Summary: "Initialize CodeMCP configuration", Operation: string(capability.ConfigInit),
				}), nil
			}
			return healthy("configuration is readable"), nil
		}),
		doctorProvider(doctor.ComponentWorkspaceRegistry, func(context.Context) (doctor.Component, error) {
			registry, err := snapshot.inspectRegistry()
			if err != nil {
				return doctor.Component{}, err
			}
			return doctor.Component{
				State: doctor.StateHealthy, Severity: doctor.SeverityInfo, Summary: "workspace registry is readable",
				Metrics: []doctor.Metric{{ID: "workspaces", Value: int64(len(registry.Workspaces))}, {ID: "containers", Value: int64(registry.Containers)}},
				Flags:   []doctor.Flag{{ID: "exists", Value: registry.Exists}},
			}, nil
		}),
		doctorProvider(doctor.ComponentWorkspaceLocalState, func(ctx context.Context) (doctor.Component, error) {
			registry, err := snapshot.inspectRegistry()
			if err != nil {
				return doctor.Component{}, err
			}
			diagnostics := make([]workspace.LocalStateDiagnostic, 0, len(registry.Workspaces))
			failures := 0
			for _, item := range registry.Workspaces {
				diagnostic, diagnoseErr := deps.Workspaces.Diagnose(ctx, item.ID)
				if diagnoseErr != nil {
					failures++
					continue
				}
				diagnostics = append(diagnostics, diagnostic)
			}
			return workspaceLocalStateDoctorComponent(diagnostics, failures), nil
		}),
		doctorProvider(doctor.ComponentSecretInventory, func(context.Context) (doctor.Component, error) {
			inspection, err := snapshot.inspectConfig()
			if err != nil {
				return doctor.Component{}, err
			}
			inventory, err := secretinventory.Inventory(configformat.RootPath())
			if err != nil {
				return doctor.Component{}, err
			}
			names := secretinventory.Names(inventory)
			status := secretstore.New(configformat.RootPath()).Inspect(names)
			requiredMissing := int64(0)
			for _, descriptor := range inventory {
				if descriptor.Configured && descriptor.RuntimeRequired && !descriptor.Present {
					requiredMissing++
				}
			}
			component := doctor.Component{
				State: doctor.StateHealthy, Severity: doctor.SeverityInfo, Summary: "secret inventory is readable",
				Metrics: []doctor.Metric{{ID: "managed", Value: int64(len(inventory))}, {ID: "checked", Value: int64(status.Checked)}, {ID: "configured", Value: int64(status.Configured)}, {ID: "missing", Value: int64(status.Missing)}, {ID: "required_missing", Value: requiredMissing}, {ID: "failed", Value: int64(status.Failed)}},
				Flags:   []doctor.Flag{{ID: "available", Value: status.Available}, {ID: "config_initialized", Value: inspection.Exists}},
			}
			if !status.Available || status.Failed > 0 || requiredMissing > 0 {
				component.State, component.Severity, component.Summary = doctor.StateDegraded, doctor.SeverityWarning, "secret inventory is partially unavailable"
			}
			return component, nil
		}),
		serviceDoctorProvider(doctor.ComponentServiceUser, managed.ScopeUser),
		serviceDoctorProvider(doctor.ComponentServiceSystem, managed.ScopeSystem),
		doctorProvider(doctor.ComponentRuntimeControl, func(ctx context.Context) (doctor.Component, error) {
			status, running, err := RuntimeStatus(ctx)
			if err != nil {
				return doctor.Component{}, err
			}
			return runtimeControlDoctorComponent(status, running), nil
		}),
		doctorProvider(doctor.ComponentRuntimeListeners, func(context.Context) (doctor.Component, error) {
			inspection, err := snapshot.inspectConfig()
			if err != nil {
				return doctor.Component{}, err
			}
			if !inspection.Exists {
				return disabled("listener configuration is not initialized"), nil
			}
			cfg := inspection.Config
			return doctor.Component{
				State: doctor.StateHealthy, Severity: doctor.SeverityInfo, Summary: "listener configuration is readable",
				Flags: []doctor.Flag{{ID: "mcp_http", Value: cfg.HTTP.MCP.Enabled}, {ID: "admin", Value: cfg.HTTP.Admin.Enabled}, {ID: "secure_tunnel", Value: cfg.Tunnel.Enabled}},
			}, nil
		}),
		doctorProvider(doctor.ComponentShellProvider, func(context.Context) (doctor.Component, error) {
			inspection, err := snapshot.inspectConfig()
			if err != nil {
				return doctor.Component{}, err
			}
			status := shellruntime.NewProviderResolver().Diagnose(inspection.Config.Shell.Path)
			return shellDoctorComponent(status), nil
		}),
		doctorProvider(doctor.ComponentIntegrationRTK, func(context.Context) (doctor.Component, error) {
			inspection, err := snapshot.inspectConfig()
			if err != nil {
				return doctor.Component{}, err
			}
			status, err := RTKStatusForConfig(inspection.Config)
			if err != nil {
				return doctor.Component{}, err
			}
			return rtkDoctorComponent(status), nil
		}),
		doctorProvider(doctor.ComponentIntegrationCodeGraph, func(context.Context) (doctor.Component, error) {
			inspection, err := snapshot.inspectConfig()
			if err != nil {
				return doctor.Component{}, err
			}
			cfg := inspection.Config.Integrations.CodeGraph
			status, err := codegraph.New(codegraph.Options{Enabled: cfg.Enabled, ConfiguredPath: cfg.Path}).Status()
			if err != nil {
				return doctor.Component{}, err
			}
			return codeGraphDoctorComponent(status), nil
		}),
		doctorProvider(doctor.ComponentIntegrationTypeSafe, func(context.Context) (doctor.Component, error) {
			inspection, err := snapshot.inspectConfig()
			if err != nil {
				return doctor.Component{}, err
			}
			cfg := inspection.Config.Integrations.TypeSafe
			credential, err := typesafeintegration.Credential(configformat.RootPath())
			if err != nil {
				return doctor.Component{}, err
			}
			return typeSafeDoctorComponent(cfg.Enabled, credential.Configured), nil
		}),
		doctorProvider(doctor.ComponentLLMProvider, func(ctx context.Context) (doctor.Component, error) {
			status, err := deps.LLM.Status(ctx)
			if err != nil {
				return doctor.Component{}, err
			}
			configured := int64(0)
			for _, provider := range status.Providers {
				if provider.Configured {
					configured++
				}
			}
			component := doctor.Component{
				State:    doctor.StateHealthy,
				Severity: doctor.SeverityInfo,
				Summary:  "active LLM provider configuration is readable; network readiness requires explicit probe",
				Metrics: []doctor.Metric{
					{ID: "providers", Value: int64(len(status.Providers))},
					{ID: "configured", Value: configured},
				},
				Flags: []doctor.Flag{
					{ID: "active_selected", Value: status.Active.Selected},
					{ID: "active_configured", Value: status.Active.Configured},
					{ID: "readiness_known", Value: status.Active.Readiness != llm.ReadinessUnknown},
				},
			}
			if !status.Active.Configured {
				component.State = doctor.StateDegraded
				component.Severity = doctor.SeverityWarning
				component.Summary = "active LLM provider configuration is incomplete"
				component.Remediations = []doctor.Remediation{{
					ID:        "llm_provider_configure",
					Summary:   "Configure the selected LLM provider",
					Operation: string(capability.LLMProviderConfigure),
				}}
			}
			return component, nil
		}),
		doctorProvider(doctor.ComponentUpstreamHealth, func(context.Context) (doctor.Component, error) {
			statuses, err := deps.Upstream.InspectStatuses()
			if err != nil {
				return doctor.Component{}, err
			}
			enabled, unreachable, unknown := int64(0), int64(0), int64(0)
			for _, status := range statuses {
				if status.Enabled {
					enabled++
				}
				if status.Health == upstream.HealthUnreachable {
					unreachable++
				}
				if status.Enabled && status.Health == upstream.HealthUnknown {
					unknown++
				}
			}
			component := doctor.Component{
				State: doctor.StateHealthy, Severity: doctor.SeverityInfo, Summary: "upstream runtime state is readable",
				Metrics: []doctor.Metric{{ID: "servers", Value: int64(len(statuses))}, {ID: "enabled", Value: enabled}, {ID: "unreachable", Value: unreachable}, {ID: "unknown", Value: unknown}},
			}
			if unreachable > 0 {
				component.State, component.Severity, component.Summary = doctor.StateDegraded, doctor.SeverityWarning, "one or more upstream servers are unreachable"
				component.Remediations = []doctor.Remediation{{ID: "upstream_review", Summary: "Inspect upstream server status", Operation: string(capability.UpstreamServerStatus)}}
			}
			return component, nil
		}),
		doctorProvider(doctor.ComponentOAuthStatus, func(context.Context) (doctor.Component, error) {
			servers, err := snapshot.inspectUpstreams()
			if err != nil {
				return doctor.Component{}, err
			}
			if deps.OAuth == nil {
				return disabled("OAuth diagnostics are not attached"), nil
			}
			configured, expired := int64(0), int64(0)
			for _, server := range servers {
				status, err := deps.OAuth.Status(server.ID)
				if err != nil {
					return doctor.Component{}, err
				}
				if status.Configured {
					configured++
				}
				if status.Expired {
					expired++
				}
			}
			component := doctor.Component{
				State: doctor.StateHealthy, Severity: doctor.SeverityInfo, Summary: "OAuth authorization state is readable",
				Metrics: []doctor.Metric{{ID: "configured", Value: configured}, {ID: "expired", Value: expired}},
			}
			if expired > 0 {
				component.State, component.Severity, component.Summary = doctor.StateDegraded, doctor.SeverityWarning, "one or more OAuth authorizations are expired"
			}
			return component, nil
		}),
		doctorProvider(doctor.ComponentNetworkExposure, func(context.Context) (doctor.Component, error) {
			inspection, err := snapshot.inspectConfig()
			if err != nil {
				return doctor.Component{}, err
			}
			if !inspection.Config.HTTP.MCP.Enabled {
				return disabled("MCP HTTP listener is disabled"), nil
			}
			hosts, addresses, err := network.ResolveCurrent(inspection.Config.HTTP.Exposure)
			if err != nil {
				return doctor.Component{}, err
			}
			return doctor.Component{
				State: doctor.StateHealthy, Severity: doctor.SeverityInfo, Summary: "network exposure is resolvable",
				Metrics: []doctor.Metric{{ID: "hosts", Value: int64(len(hosts))}, {ID: "addresses", Value: int64(len(addresses))}},
				Flags:   []doctor.Flag{{ID: "loopback_only", Value: inspection.Config.HTTP.Exposure.Mode == config.ExposureNone}},
			}, nil
		}),
		doctorProvider(doctor.ComponentMCPRegistry, func(context.Context) (doctor.Component, error) {
			inspection, err := snapshot.inspectConfig()
			if err != nil {
				return doctor.Component{}, err
			}
			if !inspection.Exists {
				return disabled("MCP transport configuration is not initialized"), nil
			}
			cfg := inspection.Config
			if !cfg.HTTP.MCP.Enabled && !cfg.Tunnel.Enabled {
				return degraded("no MCP transport is enabled", doctor.Remediation{ID: "mcp_transport", Summary: "Enable an MCP transport", Operation: string(capability.ConfigSet)}), nil
			}
			return doctor.Component{
				State: doctor.StateHealthy, Severity: doctor.SeverityInfo, Summary: "MCP transport configuration is available",
				Flags: []doctor.Flag{{ID: "http", Value: cfg.HTTP.MCP.Enabled}, {ID: "secure_tunnel", Value: cfg.Tunnel.Enabled}, {ID: "auth", Value: cfg.HTTP.MCP.Auth.Enabled}},
			}, nil
		}),
		doctorProvider(doctor.ComponentMCPOpenAIProfile, func(context.Context) (doctor.Component, error) {
			return doctor.Component{
				State: doctor.StateHealthy, Severity: doctor.SeverityInfo, Summary: "OpenAI MCP presentation profile is available",
				Flags: []doctor.Flag{{ID: "available", Value: mcp.OpenAIProfile().ID() != ""}},
			}, nil
		}),
		doctorProvider(doctor.ComponentTunnelSecureMCP, func(context.Context) (doctor.Component, error) {
			inspection, err := snapshot.inspectConfig()
			if err != nil {
				return doctor.Component{}, err
			}
			if deps.Tunnel == nil {
				return tunnelDoctorComponent(inspection, nil), nil
			}
			status := deps.Tunnel.Status()
			return tunnelDoctorComponent(inspection, &status), nil
		}),
		doctorProvider(doctor.ComponentCheckpointHistory, func(context.Context) (doctor.Component, error) {
			if deps.Checkpoints == nil {
				return disabled("checkpoint diagnostics are not attached"), nil
			}
			registry, err := snapshot.inspectRegistry()
			if err != nil {
				return doctor.Component{}, err
			}
			healths := make([]checkpoint.StorageHealth, 0, len(registry.Workspaces))
			for _, item := range registry.Workspaces {
				healths = append(healths, deps.Checkpoints.Diagnose(item.ID))
			}
			return checkpointHistoryDoctorComponent(healths), nil
		}),
		doctorProvider(doctor.ComponentCompletionHistory, func(context.Context) (doctor.Component, error) {
			if deps.Completions == nil {
				return disabled("completion history diagnostics are not attached"), nil
			}
			registry, err := snapshot.inspectRegistry()
			if err != nil {
				return doctor.Component{}, err
			}
			degraded := int64(0)
			for _, item := range registry.Workspaces {
				health := deps.Completions.Diagnose(item.ID)
				if health.Status != agentcompletion.HealthHealthy {
					degraded++
				}
			}
			component := doctor.Component{
				State: doctor.StateHealthy, Severity: doctor.SeverityInfo, Summary: "completion history is readable",
				Metrics: []doctor.Metric{{ID: "workspaces", Value: int64(len(registry.Workspaces))}, {ID: "attention", Value: degraded}},
			}
			if degraded > 0 {
				component.State, component.Severity, component.Summary = doctor.StateDegraded, doctor.SeverityWarning, "completion history requires attention"
				component.Remediations = []doctor.Remediation{{ID: "completion_review", Summary: "Review completion history diagnostics", Operation: string(capability.CompletionDoctor)}}
			}
			return component, nil
		}),
		doctorProvider(doctor.ComponentApprovalLifecycle, func(context.Context) (doctor.Component, error) {
			if deps.Approvals == nil {
				return disabled("approval lifecycle diagnostics are not attached"), nil
			}
			status := deps.Approvals.Diagnostics()
			component := doctor.Component{
				State: doctor.StateHealthy, Severity: doctor.SeverityInfo, Summary: "approval lifecycle is readable",
				Metrics: []doctor.Metric{{ID: "pending", Value: int64(status.Pending)}, {ID: "approved", Value: int64(status.Approved)}, {ID: "runtime_grants", Value: int64(status.RuntimeGrants)}, {ID: "stale", Value: int64(status.Stale)}},
			}
			if status.Stale > 0 {
				component.State, component.Severity, component.Summary = doctor.StateDegraded, doctor.SeverityWarning, "approval lifecycle contains stale active records"
			}
			return component, nil
		}),
		doctorProvider(doctor.ComponentBackgroundDelivery, func(context.Context) (doctor.Component, error) {
			if deps.BackgroundDeliveries == nil {
				return disabled("background delivery diagnostics are not attached"), nil
			}
			status := deps.BackgroundDeliveries.InspectDiagnostics()
			return backgroundDeliveryDoctorComponent(status), nil
		}),
		doctorProvider(doctor.ComponentNotificationsHealth, func(context.Context) (doctor.Component, error) {
			inspection, err := snapshot.inspectConfig()
			if err != nil {
				return doctor.Component{}, err
			}
			enabled := notificationEnabledMap(inspection.Config)
			if deps.Notifications == nil {
				if !anyEnabled(enabled) {
					return disabled("notifications are disabled"), nil
				}
				return doctor.Component{
					State: doctor.StateHealthy, Severity: doctor.SeverityInfo, Summary: "notification configuration is enabled",
					Flags: []doctor.Flag{{ID: "runtime_attached", Value: false}},
				}, nil
			}
			status := deps.Notifications.Status(enabled)
			degraded := int64(0)
			for _, provider := range status.Providers {
				if provider.Enabled && provider.Health != notification.ProviderHealthHealthy {
					degraded++
				}
			}
			component := doctor.Component{
				State: doctor.StateHealthy, Severity: doctor.SeverityInfo, Summary: "notification providers are healthy",
				Metrics: []doctor.Metric{{ID: "providers", Value: int64(len(status.Providers))}, {ID: "attention", Value: degraded}},
			}
			if degraded > 0 {
				component.State, component.Severity, component.Summary = doctor.StateDegraded, doctor.SeverityWarning, "one or more notification providers require attention"
			}
			return component, nil
		}),
		doctorProvider(doctor.ComponentTelegramHealth, func(context.Context) (doctor.Component, error) {
			inspection, err := snapshot.inspectConfig()
			if err != nil {
				return doctor.Component{}, err
			}
			if deps.TelegramHealth != nil {
				health := deps.TelegramHealth()
				return telegramDoctorComponent(health), nil
			}
			if !inspection.Config.Telegram.Enabled {
				return disabled("Telegram interface is disabled"), nil
			}
			token := secretstore.New(configformat.RootPath()).Inspect([]string{telegramcredential.BotTokenSecretName})
			authorized := len(inspection.Config.Telegram.AllowedUserIDs) > 0
			if token.Configured == 0 || !authorized {
				return degraded("Telegram interface configuration is incomplete", doctor.Remediation{ID: "telegram_setup", Summary: "Complete Telegram setup", Operation: string(capability.TelegramSetup)}), nil
			}
			return doctor.Component{
				State: doctor.StateHealthy, Severity: doctor.SeverityInfo, Summary: "Telegram interface is configured",
				Flags: []doctor.Flag{{ID: "runtime_attached", Value: false}},
			}, nil
		}),
		doctorProvider(doctor.ComponentUpdateAvailability, func(context.Context) (doctor.Component, error) {
			overview, err := LoadInstallationOverview()
			if err != nil {
				return doctor.Component{}, err
			}
			if overview.CachedUpdate == nil {
				return disabled("no fresh cached update check is available"), nil
			}
			return healthy("cached update state is available"), nil
		}),
		doctorProvider(doctor.ComponentMigrationReadiness, func(context.Context) (doctor.Component, error) {
			status, err := released024.InspectStatus(configformat.RootPath(), time.Now().UTC())
			if err != nil {
				return doctor.Component{}, err
			}
			if status.Transactions == 0 && status.Invalid == 0 {
				return disabled("no released-state migration transaction is present"), nil
			}
			component := doctor.Component{
				State: doctor.StateHealthy, Severity: doctor.SeverityInfo, Summary: "released-state migration history is consistent",
				Metrics: []doctor.Metric{
					{ID: "transactions", Value: int64(status.Transactions)},
					{ID: "pending", Value: int64(status.Pending)},
					{ID: "interrupted", Value: int64(status.Interrupted)},
					{ID: "retired", Value: int64(status.Retired)},
					{ID: "retained_backups", Value: int64(status.RetainedBackups)},
					{ID: "cleaned", Value: int64(status.Cleaned)},
					{ID: "invalid", Value: int64(status.Invalid)},
				},
				Flags: []doctor.Flag{
					{ID: "cleanup_due", Value: status.CleanupDue > 0},
					{ID: "cleanup_running", Value: status.CleanupRunning > 0},
					{ID: "missing_backup", Value: status.MissingBackups > 0},
				},
			}
			switch {
			case status.Invalid > 0 || status.Interrupted > 0 || status.CleanupRunning > 0 || status.MissingBackups > 0:
				component.State, component.Severity, component.Summary = doctor.StateDegraded, doctor.SeverityWarning, "released-state migration requires operator recovery"
			case status.Pending > 0:
				component.State, component.Severity, component.Summary = doctor.StateDegraded, doctor.SeverityWarning, "released-state migration is incomplete"
			case status.CleanupDue > 0:
				component.Summary = "released-state rollback backup is eligible for explicit cleanup"
			}
			return component, nil
		}),
	}
}

func doctorProvider(id doctor.ComponentID, run func(context.Context) (doctor.Component, error)) doctor.Provider {
	definition, ok := doctor.DefinitionFor(id)
	if !ok {
		panic(fmt.Sprintf("doctor definition missing for %s", id))
	}
	return doctor.ProviderFunc{Definition: definition.ProviderSpec(), Run: run}
}

func serviceDoctorProvider(id doctor.ComponentID, scope managed.Scope) doctor.Provider {
	return doctorProvider(id, func(context.Context) (doctor.Component, error) {
		if scope == managed.ScopeSystem && runtime.GOOS == "windows" {
			return disabled("system service scope is not supported on Windows"), nil
		}
		status := loadServiceOverview(scope)
		return serviceDoctorComponent(status), nil
	})
}

func healthy(summary string, flags ...doctor.Flag) doctor.Component {
	return doctor.Component{State: doctor.StateHealthy, Severity: doctor.SeverityInfo, Summary: summary, Flags: flags}
}

func disabled(summary string) doctor.Component {
	return doctor.Component{State: doctor.StateDisabled, Severity: doctor.SeverityInfo, Summary: summary}
}

func degraded(summary string, remediation doctor.Remediation) doctor.Component {
	return doctor.Component{State: doctor.StateDegraded, Severity: doctor.SeverityWarning, Summary: summary, Remediations: []doctor.Remediation{remediation}}
}

func unavailable(summary string, remediation doctor.Remediation) doctor.Component {
	return doctor.Component{State: doctor.StateUnavailable, Severity: doctor.SeverityWarning, Summary: summary, Remediations: []doctor.Remediation{remediation}}
}

func notificationEnabledMap(cfg config.Config) map[string]bool {
	return map[string]bool{
		"desktop":  cfg.Notifications.Approval.Enabled && cfg.Notifications.Approval.DesktopEnabled || cfg.Notifications.Completion.Enabled && cfg.Notifications.Completion.DesktopEnabled,
		"telegram": cfg.Notifications.Approval.Enabled && cfg.Notifications.Approval.TelegramEnabled || cfg.Notifications.Completion.Enabled && cfg.Notifications.Completion.TelegramEnabled,
	}
}

func anyEnabled(values map[string]bool) bool {
	for _, value := range values {
		if value {
			return true
		}
	}
	return false
}

func telegramDoctorComponent(health TelegramHealthSnapshot) doctor.Component {
	if !health.Enabled {
		return disabled("Telegram interface is disabled")
	}
	if !health.TokenConfigured || !health.AuthorizationConfigured {
		return degraded("Telegram interface configuration is incomplete", doctor.Remediation{ID: "telegram_setup", Summary: "Complete Telegram setup", Operation: string(capability.TelegramSetup)})
	}
	component := doctor.Component{
		State: doctor.StateHealthy, Severity: doctor.SeverityInfo, Summary: "Telegram interface is healthy",
		Flags: []doctor.Flag{
			{ID: "running", Value: health.Running}, {ID: "polling_healthy", Value: health.PollingHealthy}, {ID: "reconnecting", Value: health.Reconnecting},
			{ID: "delivery_degraded", Value: health.DeliveryDegraded}, {ID: "delivery_rate_limited", Value: health.DeliveryRateLimited},
			{ID: "rich_message_supported", Value: health.RichMessageSupported}, {ID: "rich_message_fallback", Value: health.RichMessageFallback},
			{ID: "topics_configured", Value: health.TopicsConfigured}, {ID: "topics_supported", Value: health.TopicsSupported},
			{ID: "topics_effective", Value: health.TopicsEffective}, {ID: "topic_store_healthy", Value: health.TopicStoreHealthy},
			{ID: "topic_reconcile_pending", Value: health.TopicReconcilePending},
			{ID: "commands_published", Value: health.CommandsPublished}, {ID: "command_drift", Value: health.CommandDrift}, {ID: "menu_reconciled", Value: health.MenuReconciled},
			{ID: "logs_mini_app_enabled", Value: health.LogsMiniAppEnabled}, {ID: "logs_mini_app_dependency", Value: health.LogsMiniAppDependency},
			{ID: "logs_mini_app_listener_ready", Value: health.LogsMiniAppListenerReady}, {ID: "logs_mini_app_tunnel_running", Value: health.LogsMiniAppTunnelRunning},
			{ID: "logs_mini_app_ingress_ready", Value: health.LogsMiniAppIngressReady},
		},
		Metrics: []doctor.Metric{
			{ID: "reconnects", Value: int64(health.ReconnectCount)}, {ID: "delivery_failures", Value: int64(health.DeliveryFailures)},
			{ID: "delivery_retry_after_ms", Value: health.DeliveryRetryAfterMS}, {ID: "command_count", Value: int64(health.CommandCount)},
			{ID: "menu_drift", Value: int64(health.MenuDriftCount)}, {ID: "allowed_updates", Value: int64(health.AllowedUpdateCount)},
			{ID: "max_file_transfer_bytes", Value: health.MaxFileTransferBytes}, {ID: "topic_count", Value: int64(health.TopicCount)},
			{ID: "logs_mini_app_generation", Value: int64(health.LogsMiniAppGeneration)},
		},
	}
	if health.Running && (!health.PollingHealthy || health.Reconnecting) {
		component.State, component.Severity, component.Summary = doctor.StateDegraded, doctor.SeverityWarning, "Telegram interface is reconnecting or unhealthy"
	}
	if health.DeliveryDegraded {
		component.State, component.Severity, component.Summary = doctor.StateDegraded, doctor.SeverityWarning, "Telegram delivery is degraded"
	}
	if health.Running && (!health.CommandsPublished || health.CommandDrift || !health.MenuReconciled) {
		component.State, component.Severity, component.Summary = doctor.StateDegraded, doctor.SeverityWarning, "Telegram command or menu publication requires attention"
	}
	if health.TopicsConfigured && health.TopicsSupported && strings.TrimSpace(health.TopicLastError) != "" {
		component.State, component.Severity, component.Summary = doctor.StateDegraded, doctor.SeverityWarning, "Telegram topic routing requires attention"
	}
	if health.LogsMiniAppEnabled && health.LogsMiniAppState == "degraded" {
		component.State, component.Severity, component.Summary = doctor.StateDegraded, doctor.SeverityWarning, "Telegram interface is healthy but the Logs Mini App is degraded"
		component.Remediations = append(component.Remediations, doctor.Remediation{ID: "telegram_logs_mini_app_dependency", Summary: "Install the verified managed asset with cm integration cf install, install cf-tunnel globally yourself, or disable telegram.logs_mini_app.enabled", Operation: string(capability.IntegrationCFInstall)})
	}
	return component
}
