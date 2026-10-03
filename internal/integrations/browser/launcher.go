package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
	process := newExecBrowserProcess(cmd)
	endpoint, err := waitBrowserEndpoint(ctx, localProfile, process)
	if err != nil {
		_ = process.Close(context.Background())
		return nil, BrowserEndpoint{}, err
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
		"--new-window",
	}
	if minimized {
		args = append(args, "--start-minimized")
	}
	return append(args, "about:blank")
}

func waitBrowserEndpoint(ctx context.Context, localProfile string, process BrowserProcess) (BrowserEndpoint, error) {
	portFile := filepath.Join(localProfile, "DevToolsActivePort")
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return BrowserEndpoint{}, ctx.Err()
		case <-process.Done():
			if err := process.Err(); err != nil {
				return BrowserEndpoint{}, fmt.Errorf("browser exited before CDP became ready: %w", err)
			}
			return BrowserEndpoint{}, errors.New("browser exited before CDP became ready")
		case <-ticker.C:
			port, err := readDevToolsPort(portFile)
			if err != nil {
				continue
			}
			endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
			if err := verifyBrowserEndpoint(ctx, endpoint); err == nil {
				return BrowserEndpoint{URL: endpoint}, nil
			}
		}
	}
}

func verifyBrowserEndpoint(ctx context.Context, endpoint string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/json/version", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 500 * time.Millisecond}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("CDP version endpoint returned %s", response.Status)
	}
	var payload struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&payload); err != nil {
		return err
	}
	if strings.TrimSpace(payload.WebSocketDebuggerURL) == "" {
		return errors.New("CDP version endpoint has no browser websocket")
	}
	return nil
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
