package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type execBrowserLauncher struct{}

func newExecBrowserLauncher() BrowserLauncher { return execBrowserLauncher{} }

func (execBrowserLauncher) Launch(ctx context.Context, request LaunchRequest) (BrowserProcess, BrowserEndpoint, error) {
	switch request.Candidate.Transport {
	case TransportNative:
		return launchNativeBrowser(ctx, request)
	case TransportWSLHost:
		return launchWSLHostBrowser(ctx, request)
	default:
		return nil, BrowserEndpoint{}, fmt.Errorf("unsupported browser transport %q", request.Candidate.Transport)
	}
}

func launchNativeBrowser(ctx context.Context, request LaunchRequest) (BrowserProcess, BrowserEndpoint, error) {
	if request.Profile.Transport != TransportNative {
		return nil, BrowserEndpoint{}, errors.New("native browser requires a native profile")
	}
	return launchExecBrowser(ctx, request)
}

func launchWSLHostBrowser(ctx context.Context, request LaunchRequest) (BrowserProcess, BrowserEndpoint, error) {
	if request.Profile.Transport != TransportWSLHost || request.Profile.HostPlatform != "windows" {
		return nil, BrowserEndpoint{}, errors.New("WSL-host browser requires a Windows-host profile")
	}
	if request.Candidate.HostPlatform != "windows" {
		return nil, BrowserEndpoint{}, errors.New("WSL-host browser candidate must target Windows")
	}
	return launchExecBrowser(ctx, request)
}

func launchExecBrowser(ctx context.Context, request LaunchRequest) (BrowserProcess, BrowserEndpoint, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !request.Visible {
		return nil, BrowserEndpoint{}, errors.New("managed browser launch requires a visible dedicated window")
	}
	executable := strings.TrimSpace(request.Candidate.LocalExecutable)
	if executable == "" {
		executable = strings.TrimSpace(request.Candidate.Executable)
	}
	if executable == "" {
		return nil, BrowserEndpoint{}, errors.New("browser executable is required")
	}
	profilePath := strings.TrimSpace(request.Profile.Path)
	localProfile := strings.TrimSpace(request.Profile.LocalPath)
	if profilePath == "" || localProfile == "" {
		return nil, BrowserEndpoint{}, errors.New("browser profile paths are required")
	}
	args := managerLaunchArgs(profilePath, request.Minimized)
	cmd := exec.Command(executable, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, BrowserEndpoint{}, err
	}
	var process BrowserProcess = newExecBrowserProcess(cmd)
	endpoint, relay, err := waitBrowserEndpoint(ctx, request.Candidate, localProfile, process)
	if err != nil {
		_ = process.Close(context.Background())
		return nil, BrowserEndpoint{}, err
	}
	if relay != nil {
		process = newRelayedBrowserProcess(process, relay)
	}
	return process, endpoint, nil
}

func managerLaunchArgs(profile string, minimized bool) []string {
	args := []string{
		"--no-first-run",
		"--no-default-browser-check",
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=0",
		"--user-data-dir=" + profile,
		"--profile-directory=" + managedProfileDirectory,
		"--new-window",
	}
	if minimized {
		args = append(args, "--start-minimized")
	}
	return append(args, "about:blank")
}

func waitBrowserEndpoint(ctx context.Context, candidate Candidate, localProfile string, process BrowserProcess) (BrowserEndpoint, *loopbackRelay, error) {
	portFile := filepath.Join(localProfile, "DevToolsActivePort")
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	processDone := process.Done()
	if candidate.Transport == TransportWSLHost {
		processDone = nil
	}
	for {
		select {
		case <-ctx.Done():
			return BrowserEndpoint{}, nil, ctx.Err()
		case <-processDone:
			if err := process.Err(); err != nil {
				return BrowserEndpoint{}, nil, fmt.Errorf("browser exited before CDP became ready: %w", err)
			}
			return BrowserEndpoint{}, nil, errors.New("browser exited before CDP became ready")
		case <-ticker.C:
			port, err := readDevToolsPort(portFile)
			if err != nil {
				continue
			}
			endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
			var relay *loopbackRelay
			if candidate.Transport == TransportWSLHost {
				relay, err = startWindowsLoopbackRelay(port)
				if err != nil {
					return BrowserEndpoint{}, nil, err
				}
				endpoint = relay.URL()
			}
			websocketURL, verifyErr := browserWebSocketURL(ctx, endpoint)
			if verifyErr == nil {
				if relay != nil {
					websocketURL, verifyErr = relayWebSocketURL(relay.URL(), websocketURL)
				}
				if verifyErr == nil {
					return BrowserEndpoint{URL: websocketURL}, relay, nil
				}
			}
			if relay != nil {
				_ = relay.Close()
			}
		}
	}
}

func verifyBrowserEndpoint(ctx context.Context, endpoint string) error {
	_, err := browserWebSocketURL(ctx, endpoint)
	return err
}

func browserWebSocketURL(ctx context.Context, endpoint string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/json/version", nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("CDP version endpoint returned %s", response.Status)
	}
	var payload struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&payload); err != nil {
		return "", err
	}
	websocketURL := strings.TrimSpace(payload.WebSocketDebuggerURL)
	if websocketURL == "" {
		return "", errors.New("CDP version endpoint has no browser websocket")
	}
	return websocketURL, nil
}

func relayWebSocketURL(relayEndpoint, remoteWebSocket string) (string, error) {
	relayURL, err := url.Parse(strings.TrimSpace(relayEndpoint))
	if err != nil || relayURL.Host == "" {
		return "", errors.New("invalid local browser relay endpoint")
	}
	websocketURL, err := url.Parse(strings.TrimSpace(remoteWebSocket))
	if err != nil || websocketURL.Host == "" || (websocketURL.Scheme != "ws" && websocketURL.Scheme != "wss") {
		return "", errors.New("invalid browser websocket endpoint")
	}
	websocketURL.Host = relayURL.Host
	return websocketURL.String(), nil
}

type execBrowserProcess struct {
	cmd  *exec.Cmd
	done chan struct{}

	mu  sync.Mutex
	err error
}

func newExecBrowserProcess(cmd *exec.Cmd) *execBrowserProcess {
	process := &execBrowserProcess{cmd: cmd, done: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		process.mu.Lock()
		process.err = err
		process.mu.Unlock()
		close(process.done)
	}()
	return process
}

func (process *execBrowserProcess) PID() int {
	if process == nil || process.cmd == nil || process.cmd.Process == nil {
		return 0
	}
	return process.cmd.Process.Pid
}

func (process *execBrowserProcess) Done() <-chan struct{} {
	if process == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return process.done
}

func (process *execBrowserProcess) Err() error {
	if process == nil {
		return nil
	}
	process.mu.Lock()
	defer process.mu.Unlock()
	return process.err
}

func (process *execBrowserProcess) Close(ctx context.Context) error {
	if process == nil || process.cmd == nil || process.cmd.Process == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-process.done:
		return nil
	default:
	}
	_ = process.cmd.Process.Signal(os.Interrupt)
	select {
	case <-process.done:
		return nil
	case <-ctx.Done():
		if err := process.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		select {
		case <-process.done:
			return nil
		case <-time.After(500 * time.Millisecond):
			return ctx.Err()
		}
	case <-time.After(500 * time.Millisecond):
		if err := process.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		select {
		case <-process.done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
