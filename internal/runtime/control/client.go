package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/config"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

const FileName = ".runtime-control.json"

var requestHTTPClient = &http.Client{
	Timeout: 15 * time.Second,
	Transport: &http.Transport{
		Proxy:               nil,
		DialContext:         (&net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     30 * time.Second,
	},
}

type ReloadResult struct {
	PID              int                 `json:"pid"`
	NetworkRestarted bool                `json:"network_restarted"`
	ServerEnabled    bool                `json:"server_enabled"`
	ServerPort       int                 `json:"server_port"`
	AdminEnabled     bool                `json:"admin_enabled"`
	AdminPort        int                 `json:"admin_port"`
	Exposure         config.ExposureMode `json:"exposure"`
}

type WorkspaceReloadResult struct {
	PID   int `json:"pid"`
	Count int `json:"count"`
}

type UpstreamReloadResult struct {
	PID   int `json:"pid"`
	Count int `json:"count"`
}

type RuntimeStatus struct {
	PID               int                  `json:"pid"`
	RunID             string               `json:"run_id,omitempty"`
	Lifecycle         string               `json:"lifecycle,omitempty"`
	Starting          bool                 `json:"starting,omitempty"`
	Managed           bool                 `json:"managed"`
	ServiceID         string               `json:"service_id,omitempty"`
	ServiceScope      string               `json:"service_scope,omitempty"`
	StartedAt         time.Time            `json:"started_at"`
	ConfigRoot        string               `json:"config_root"`
	ConfigFingerprint string               `json:"config_fingerprint,omitempty"`
	ServerEnabled     bool                 `json:"server_enabled"`
	ServerPort        int                  `json:"server_port"`
	AdminEnabled      bool                 `json:"admin_enabled"`
	AdminPort         int                  `json:"admin_port"`
	Exposure          config.ExposureMode  `json:"exposure"`
	TunnelEnabled     bool                 `json:"tunnel_enabled"`
	TunnelConfigured  bool                 `json:"tunnel_configured"`
	TunnelRunning     bool                 `json:"tunnel_running"`
	TunnelReady       bool                 `json:"tunnel_ready"`
	TunnelRestarting  bool                 `json:"tunnel_restarting"`
	TunnelID          string               `json:"tunnel_id,omitempty"`
	TunnelLastError   string               `json:"tunnel_last_error,omitempty"`
	ToolProfile       string               `json:"tool_profile,omitempty"`
	ToolCount         int                  `json:"tool_count,omitempty"`
	Readiness         []ReadinessComponent `json:"readiness,omitempty"`
}

type ReadinessComponent struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Configured bool   `json:"configured"`
	Ready      bool   `json:"ready"`
}

type State struct {
	PID          int       `json:"pid"`
	Address      string    `json:"address"`
	Token        string    `json:"token"`
	RunID        string    `json:"run_id,omitempty"`
	Managed      bool      `json:"managed,omitempty"`
	ServiceID    string    `json:"service_id,omitempty"`
	ServiceScope string    `json:"service_scope,omitempty"`
	StartedAt    time.Time `json:"started_at,omitempty"`
	ConfigRoot   string    `json:"config_root"`
}

func Path() string { return PathAt(config.RootPath()) }

func PathAt(root string) string { return filepath.Join(filepath.Clean(root), FileName) }

func Load() (State, error) { return LoadContext(context.Background()) }

func LoadContext(ctx context.Context) (State, error) {
	return LoadContextAt(ctx, config.RootPath())
}

func LoadContextAt(ctx context.Context, root string) (State, error) {
	statePath := PathAt(root)
	readSpan := tracepkg.Start(ctx, "CONTROL", "runtime.control.state.read", "Reading runtime control state", tracepkg.String("state_file", statePath))
	data, retries, err := readStateFileAt(statePath)
	if err != nil {
		readSpan.FailMessage("Runtime control state read failed", err, tracepkg.Int("retries", retries))
		if os.IsNotExist(err) {
			return State{}, errors.New("no running server found for this config directory")
		}
		return State{}, err
	}
	readSpan.EndMessage("Runtime control state read", tracepkg.Int64("bytes", int64(len(data))), tracepkg.Int("retries", retries))
	if retries > 0 {
		tracepkg.Emit(ctx, "CONTROL", "runtime.control.state.read-retried", "Runtime control state read retried", tracepkg.String("state_file", statePath), tracepkg.Int("retries", retries))
	}
	decodeSpan := tracepkg.Start(ctx, "CONTROL", "runtime.control.state.decode", "Decoding runtime control state", tracepkg.String("state_file", statePath), tracepkg.Int64("bytes", int64(len(data))))
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		decodeSpan.FailMessage("Runtime control state decode failed", err)
		return State{}, fmt.Errorf("decode runtime control state: %w", err)
	}
	if state.PID <= 0 || strings.TrimSpace(state.Token) == "" {
		err := errors.New("runtime control state is invalid")
		decodeSpan.FailMessage("Runtime control state validation failed", err)
		return State{}, err
	}
	host, _, err := net.SplitHostPort(state.Address)
	if err != nil {
		decodeSpan.FailMessage("Runtime control state validation failed", errors.New("runtime control address is invalid"))
		return State{}, errors.New("runtime control address is invalid")
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil || !ip.IsLoopback() {
		decodeSpan.FailMessage("Runtime control state validation failed", errors.New("runtime control address is not loopback"))
		return State{}, errors.New("runtime control address is not loopback")
	}
	decodeSpan.EndMessage("Runtime control state decoded", tracepkg.Int("pid", state.PID), tracepkg.String("address", state.Address), tracepkg.Bool("managed", state.Managed), tracepkg.String("service", state.ServiceID), tracepkg.String("scope", state.ServiceScope))
	return state, nil
}

func readStateFileAt(path string) ([]byte, int, error) {
	data, err := os.ReadFile(path)
	if runtime.GOOS != "windows" || err == nil || os.IsNotExist(err) {
		return data, 0, err
	}
	retries := 0
	for range 5 {
		retries++
		time.Sleep(10 * time.Millisecond)
		data, err = os.ReadFile(path)
		if err == nil || os.IsNotExist(err) {
			return data, retries, err
		}
	}
	return data, retries, err
}

func Request(ctx context.Context, method, path string, input, output any) (State, error) {
	return RequestAt(ctx, config.RootPath(), method, path, input, output)
}

func RequestAt(ctx context.Context, root, method, path string, input, output any) (State, error) {
	state, err := LoadContextAt(ctx, root)
	if err != nil {
		return State{}, err
	}
	path = strings.TrimSpace(path)
	if !strings.HasPrefix(path, "/") {
		return State{}, errors.New("runtime control path must be absolute")
	}
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return State{}, fmt.Errorf("encode runtime control request: %w", err)
		}
		body = bytes.NewReader(data)
	}
	endpoint := "http://" + state.Address + path
	span := tracepkg.Start(ctx, "CONTROL", "runtime.control.request", "Runtime control request", tracepkg.String("state_file", PathAt(root)), tracepkg.Int("pid", state.PID), tracepkg.String("address", state.Address), tracepkg.String("method", method), tracepkg.URL("endpoint", endpoint))
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		span.FailMessage("Runtime control request construction failed", err)
		return State{}, err
	}
	request.Header.Set("Authorization", "Bearer "+state.Token)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := tracepkg.DoHTTP(requestHTTPClient, request)
	if err != nil {
		span.FailMessage("Runtime control request failed", err)
		return State{}, fmt.Errorf("running server control endpoint unavailable: %w", err)
	}
	defer response.Body.Close()
	reader := &countingReader{reader: response.Body}
	if response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(reader, 64*1024))
		var failure struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &failure) == nil && failure.Error != "" {
			err := errors.New(failure.Error)
			span.FailMessage("Runtime control request failed", err, tracepkg.Int("status", response.StatusCode), tracepkg.Int64("response_bytes", reader.bytes))
			return State{}, err
		}
		err := fmt.Errorf("runtime control request failed with HTTP %d", response.StatusCode)
		span.FailMessage("Runtime control request failed", err, tracepkg.Int("status", response.StatusCode), tracepkg.Int64("response_bytes", reader.bytes))
		return State{}, err
	}
	if output != nil {
		if err := json.NewDecoder(reader).Decode(output); err != nil {
			span.FailMessage("Runtime control response decode failed", err, tracepkg.Int("status", response.StatusCode), tracepkg.Int64("response_bytes", reader.bytes))
			return State{}, fmt.Errorf("decode runtime control response: %w", err)
		}
	}
	span.EndMessage("Runtime control response", tracepkg.Int("status", response.StatusCode), tracepkg.Int64("response_bytes", reader.bytes))
	return state, nil
}

type countingReader struct {
	reader io.Reader
	bytes  int64
}

func (reader *countingReader) Read(buffer []byte) (int, error) {
	n, err := reader.reader.Read(buffer)
	reader.bytes += int64(n)
	return n, err
}

func WaitStatusChange(ctx context.Context, lifecycle string) (RuntimeStatus, error) {
	return WaitStatusChangeAt(ctx, config.RootPath(), lifecycle)
}

func WaitStatusChangeAt(ctx context.Context, root, lifecycle string) (RuntimeStatus, error) {
	previous := strings.TrimSpace(lifecycle)
	span := tracepkg.Start(ctx, "CONTROL", "runtime.control.status-wait", "Waiting for runtime lifecycle change", tracepkg.String("previous_lifecycle", previous))
	var result RuntimeStatus
	path := "/status/wait?lifecycle=" + url.QueryEscape(strings.TrimSpace(lifecycle))
	state, err := RequestAt(ctx, root, http.MethodGet, path, nil, &result)
	if err != nil {
		span.FailMessage("Runtime lifecycle wait failed", err, tracepkg.String("previous_lifecycle", previous))
		return RuntimeStatus{}, err
	}
	if err := ValidatePID(ctx, state.PID, result.PID, "status-wait"); err != nil {
		span.FailMessage("Runtime lifecycle wait PID validation failed", err, tracepkg.String("previous_lifecycle", previous), tracepkg.String("current_lifecycle", result.Lifecycle))
		return RuntimeStatus{}, err
	}
	span.EndMessage("Runtime lifecycle changed", tracepkg.String("previous_lifecycle", previous), tracepkg.String("current_lifecycle", result.Lifecycle), tracepkg.Bool("changed", previous != strings.TrimSpace(result.Lifecycle)), tracepkg.Int("pid", result.PID), tracepkg.String("run_id", result.RunID))
	return result, nil
}

func StatusFingerprint(status RuntimeStatus) string {
	var value strings.Builder
	fmt.Fprintf(&value, "%d|%s|%s|%t|%t|%t|%t|%t", status.PID, status.RunID, status.Lifecycle, status.TunnelEnabled, status.TunnelConfigured, status.TunnelRunning, status.TunnelReady, status.TunnelRestarting)
	for _, component := range status.Readiness {
		fmt.Fprintf(&value, "|%s:%t:%t", component.ID, component.Configured, component.Ready)
	}
	sum := sha256.Sum256([]byte(value.String()))
	return fmt.Sprintf("%x", sum)
}

func WaitStatusUpdate(ctx context.Context, previous RuntimeStatus) (RuntimeStatus, error) {
	return WaitStatusUpdateAt(ctx, config.RootPath(), previous)
}

func WaitStatusUpdateAt(ctx context.Context, root string, previous RuntimeStatus) (RuntimeStatus, error) {
	fingerprint := StatusFingerprint(previous)
	span := tracepkg.Start(ctx, "CONTROL", "runtime.control.status-update-wait", "Waiting for runtime status update", tracepkg.String("previous_lifecycle", previous.Lifecycle))
	var result RuntimeStatus
	path := "/status/wait?fingerprint=" + url.QueryEscape(fingerprint)
	state, err := RequestAt(ctx, root, http.MethodGet, path, nil, &result)
	if err != nil {
		span.FailMessage("Runtime status update wait failed", err, tracepkg.String("previous_lifecycle", previous.Lifecycle))
		return RuntimeStatus{}, err
	}
	if err := ValidatePID(ctx, state.PID, result.PID, "status-update-wait"); err != nil {
		span.FailMessage("Runtime status update wait PID validation failed", err, tracepkg.String("previous_lifecycle", previous.Lifecycle), tracepkg.String("current_lifecycle", result.Lifecycle))
		return RuntimeStatus{}, err
	}
	span.EndMessage("Runtime status updated", tracepkg.String("previous_lifecycle", previous.Lifecycle), tracepkg.String("current_lifecycle", result.Lifecycle), tracepkg.Bool("changed", fingerprint != StatusFingerprint(result)), tracepkg.Int("pid", result.PID), tracepkg.String("run_id", result.RunID))
	return result, nil
}

func RequestStatusAt(ctx context.Context, root string) (RuntimeStatus, error) {
	var result RuntimeStatus
	state, err := RequestAt(ctx, root, http.MethodGet, "/status", nil, &result)
	if err != nil {
		return RuntimeStatus{}, err
	}
	if err := ValidatePID(ctx, state.PID, result.PID, "status"); err != nil {
		return RuntimeStatus{}, err
	}
	return result, nil
}

func RequestShutdownAt(ctx context.Context, root string) error {
	_, err := RequestAt(ctx, root, http.MethodPost, "/shutdown", nil, &map[string]bool{})
	return err
}

func ValidatePID(ctx context.Context, expected, actual int, operation string) error {
	operation = strings.TrimSpace(operation)
	span := tracepkg.Start(ctx, "CONTROL", "runtime.control.pid.validate", "Validating runtime control PID", tracepkg.String("operation", operation), tracepkg.Int("expected_pid", expected), tracepkg.Int("actual_pid", actual))
	if expected != actual {
		err := fmt.Errorf("runtime control PID mismatch: expected %d, got %d", expected, actual)
		span.FailMessage("Runtime control PID validation failed", err, tracepkg.String("operation", operation), tracepkg.Int("expected_pid", expected), tracepkg.Int("actual_pid", actual))
		return err
	}
	span.EndMessage("Runtime control PID validated", tracepkg.String("operation", operation), tracepkg.Int("pid", actual))
	return nil
}

func IsUnavailable(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no running server found") || strings.Contains(message, "control endpoint unavailable") || strings.Contains(message, "connection refused") || strings.Contains(message, "actively refused")
}
