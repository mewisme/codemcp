package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/logger"
	mcpnetwork "go.mewis.me/codemcp/internal/network"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type listenerPlan struct {
	Hosts     []string
	Addresses []mcpnetwork.Address
}

func addExposeFlag(cmd *cobra.Command) {
	cmd.Flags().String("expose", "", "network exposure for this run: none, all, 0.0.0.0, or comma-separated interface names")
	if flag := cmd.Flags().Lookup("expose"); flag != nil {
		flag.NoOptDefVal = "all"
	}
}

func applyExposeOverride(cmd *cobra.Command, cfg *config.Config) error {
	flag := cmd.Flags().Lookup("expose")
	if flag == nil || !flag.Changed {
		return nil
	}
	raw, err := cmd.Flags().GetString("expose")
	if err != nil {
		return err
	}
	exposure, err := config.ParseExposure(raw)
	if err != nil {
		return err
	}
	cfg.Server.Expose = exposure
	return nil
}

func resolveListenerPlan(exposure config.ExposureConfig) (listenerPlan, error) {
	hosts, addresses, err := mcpnetwork.ResolveCurrent(exposure)
	if err != nil {
		return listenerPlan{}, err
	}
	return listenerPlan{Hosts: hosts, Addresses: addresses}, nil
}

func listenOnHostsExactContext(ctx context.Context, component string, hosts []string, port int) ([]net.Listener, int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	listeners := make([]net.Listener, 0, len(hosts))
	for _, host := range hosts {
		span := tracepkg.Start(ctx, "NETWORK", "server.listener.bind", "Binding server listener", tracepkg.String("component", component), tracepkg.String("host", host), tracepkg.Int("configured_port", port), tracepkg.Int("attempted_port", port), tracepkg.Int("fallback_attempt", 0))
		listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			span.FailMessage("Server listener bind failed", err, tracepkg.String("component", component), tracepkg.String("host", host), tracepkg.Int("configured_port", port), tracepkg.Int("attempted_port", port), tracepkg.Int("fallback_attempt", 0), tracepkg.Bool("address_in_use", isAddressInUseError(err)))
			closeListeners(listeners)
			return nil, 0, fmt.Errorf("listen on %s:%d: %w", host, port, err)
		}
		listeners = append(listeners, listener)
		span.EndMessage("Server listener bound", tracepkg.String("component", component), tracepkg.String("host", host), tracepkg.Int("configured_port", port), tracepkg.Int("attempted_port", port), tracepkg.Int("fallback_attempt", 0), tracepkg.Int("bound_port", port), tracepkg.String("address", listener.Addr().String()))
	}
	tracepkg.Emit(ctx, "NETWORK", "server.listener.port-selected", "Selected server listener port", tracepkg.String("component", component), tracepkg.Int("configured_port", port), tracepkg.Int("selected_port", port), tracepkg.Int("fallback_attempt", 0), tracepkg.Int("listener_count", len(listeners)), tracepkg.Any("hosts", append([]string(nil), hosts...)))
	return listeners, port, nil
}

func listenOnHostsWithFallbackContext(ctx context.Context, component string, hosts []string, port int) ([]net.Listener, int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	configuredPort := port
	fallbackAttempt := 0
	for candidate := port; candidate <= 65535; candidate++ {
		listeners := make([]net.Listener, 0, len(hosts))
		var bindErr error
		for _, host := range hosts {
			span := tracepkg.Start(ctx, "NETWORK", "server.listener.bind", "Binding server listener", tracepkg.String("component", component), tracepkg.String("host", host), tracepkg.Int("configured_port", configuredPort), tracepkg.Int("attempted_port", candidate), tracepkg.Int("fallback_attempt", fallbackAttempt))
			listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(candidate)))
			if err != nil {
				span.FailMessage("Server listener bind failed", err, tracepkg.String("component", component), tracepkg.String("host", host), tracepkg.Int("configured_port", configuredPort), tracepkg.Int("attempted_port", candidate), tracepkg.Int("fallback_attempt", fallbackAttempt), tracepkg.Bool("address_in_use", isAddressInUseError(err)))
				bindErr = fmt.Errorf("listen on %s:%d: %w", host, candidate, err)
				break
			}
			listeners = append(listeners, listener)
			span.EndMessage("Server listener bound", tracepkg.String("component", component), tracepkg.String("host", host), tracepkg.Int("configured_port", configuredPort), tracepkg.Int("attempted_port", candidate), tracepkg.Int("fallback_attempt", fallbackAttempt), tracepkg.Int("bound_port", candidate), tracepkg.String("address", listener.Addr().String()))
		}
		if bindErr == nil {
			tracepkg.Emit(ctx, "NETWORK", "server.listener.port-selected", "Selected server listener port", tracepkg.String("component", component), tracepkg.Int("configured_port", configuredPort), tracepkg.Int("selected_port", candidate), tracepkg.Int("fallback_attempt", fallbackAttempt), tracepkg.Int("listener_count", len(listeners)), tracepkg.Any("hosts", append([]string(nil), hosts...)))
			return listeners, candidate, nil
		}
		closeListeners(listeners)
		err := bindErr
		if err == nil {
			return listeners, candidate, nil
		}
		if !isAddressInUseError(err) {
			return nil, 0, err
		}
		fallbackAttempt++
	}
	return nil, 0, fmt.Errorf("no available TCP port at or above %d", port)
}

func isAddressInUseError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	var errno syscall.Errno
	if errors.As(err, &errno) && errno == syscall.Errno(10048) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "address already in use") || strings.Contains(message, "only one usage of each socket address")
}

func endpointURL(host string, port int, endpointPath string) string {
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + endpointPath
}

func logReadyEndpoints(log *logger.Logger, cfg config.Config, plan listenerPlan) {
	mcpEndpoints := make([]string, 0, len(plan.Addresses))
	adminEndpoints := make([]string, 0, len(plan.Addresses))
	for _, address := range plan.Addresses {
		if cfg.Server.Enabled {
			mcpEndpoints = append(mcpEndpoints, endpointURL(address.Host, cfg.Server.Port, "/mcp"))
		}
		if cfg.Admin.Enabled {
			adminEndpoints = append(adminEndpoints, endpointURL(address.Host, cfg.Admin.Port, "/"))
		}
	}
	fields := []logger.Field{logger.With("mcp_http_enabled", cfg.Server.Enabled)}
	if cfg.Server.Enabled {
		fields = append(fields, logger.With("mcp", mcpEndpoints))
	}
	if cfg.Admin.Enabled {
		fields = append(fields, logger.With("admin", adminEndpoints))
	}
	fields = append(fields, logger.WithVerbose("expose", cfg.Server.Expose.Mode))
	switch cfg.Server.Expose.Mode {
	case config.ExposureAll:
		fields = append(fields, logger.WithVerbose("network_addresses", max(0, len(plan.Addresses)-1)))
	case config.ExposureWildcard:
		fields = append(fields, logger.WithVerbose("bind", mcpnetwork.WildcardHost), logger.WithVerbose("network_addresses", max(0, len(plan.Addresses)-1)))
	case config.ExposureInterfaces:
		fields = append(fields, logger.WithVerbose("interfaces", cfg.Server.Expose.Interfaces), logger.WithVerbose("network_addresses", max(0, len(plan.Addresses)-1)))
	}
	log.Ready("SERVER", "server.ready", "Server ready", fields...)
}

func logEndpointDetails(log *logger.Logger, cfg config.Config) {
	log.Detail("expose", cfg.Server.Expose.Mode)
	if len(cfg.Server.Expose.Interfaces) > 0 {
		log.Detail("interfaces", cfg.Server.Expose.Interfaces)
	}
	plan, err := resolveListenerPlan(cfg.Server.Expose)
	if err != nil {
		log.Detail("network", err.Error())
		return
	}
	for _, address := range plan.Addresses {
		if cfg.Server.Enabled {
			log.Detail(endpointDetailLabel("mcp", address), endpointURL(address.Host, cfg.Server.Port, "/mcp"))
		}
		if cfg.Admin.Enabled {
			log.Detail(endpointDetailLabel("admin", address), endpointURL(address.Host, cfg.Admin.Port, "/"))
		}
	}
	if !cfg.Server.Enabled {
		log.Detail("mcp http", "disabled")
	}
	if !cfg.Admin.Enabled {
		log.Detail("admin", "disabled")
	}
}

func endpointPresentationFields(cfg config.Config) []presentation.Field {
	fields := []presentation.Field{{Label: "expose", Value: cfg.Server.Expose.Mode}}
	if len(cfg.Server.Expose.Interfaces) > 0 {
		fields = append(fields, presentation.Field{Label: "interfaces", Value: strings.Join(cfg.Server.Expose.Interfaces, ", ")})
	}
	plan, err := resolveListenerPlan(cfg.Server.Expose)
	if err != nil {
		return append(fields, presentation.Field{Label: "network", Value: err.Error()})
	}
	for _, address := range plan.Addresses {
		if cfg.Server.Enabled {
			fields = append(fields, presentation.Field{Label: endpointDetailLabel("mcp", address), Value: endpointURL(address.Host, cfg.Server.Port, "/mcp")})
		}
		if cfg.Admin.Enabled {
			fields = append(fields, presentation.Field{Label: endpointDetailLabel("admin", address), Value: endpointURL(address.Host, cfg.Admin.Port, "/")})
		}
	}
	if !cfg.Server.Enabled {
		fields = append(fields, presentation.Field{Label: "mcp http", Value: "disabled"})
	}
	if !cfg.Admin.Enabled {
		fields = append(fields, presentation.Field{Label: "admin", Value: "disabled"})
	}
	return fields
}

func endpointDetailLabel(kind string, address mcpnetwork.Address) string {
	if address.Interface != "" {
		return kind + " " + address.Interface
	}
	return kind + " " + address.Scope
}
