package browser

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type execInteractiveBrowserLauncher struct{}

func newExecInteractiveBrowserLauncher() InteractiveBrowserLauncher {
	return execInteractiveBrowserLauncher{}
}

func (execInteractiveBrowserLauncher) Launch(ctx context.Context, request InteractiveLaunchRequest) (BrowserProcess, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	args, err := interactiveLaunchArgs(request)
	if err != nil {
		return nil, err
	}
	switch request.Candidate.Transport {
	case TransportNative:
		return launchNativeInteractiveBrowser(request, args)
	case TransportWSLHost:
		return launchWSLHostInteractiveBrowser(request, args)
	default:
		return nil, fmt.Errorf("unsupported interactive browser transport %q", request.Candidate.Transport)
	}
}

func StartInteractiveBrowser(ctx context.Context, options InteractiveBrowserOptions) (BrowserProcess, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateLaunchableCapability(options.Capability); err != nil {
		return nil, err
	}
	profile := *options.Capability.Profile
	if err := PrepareProfile(profile); err != nil {
		return nil, err
	}
	lock, ok, err := TryAcquireProfile(profile)
	if err != nil {
		return nil, fmt.Errorf("acquire browser profile lock: %w", err)
	}
	if !ok {
		return nil, ErrProfileBusy
	}
	if err := PruneProfileCaches(profile); err != nil {
		_ = lock.Release()
		return nil, err
	}
	launcher := options.Launcher
	if launcher == nil {
		launcher = newExecInteractiveBrowserLauncher()
	}
	process, err := launcher.Launch(ctx, InteractiveLaunchRequest{
		Candidate: *options.Capability.Candidate,
		Profile:   profile,
		URL:       options.URL,
	})
	if err != nil {
		_ = lock.Release()
		return nil, fmt.Errorf("launch interactive browser: %w", err)
	}
	if process == nil {
		_ = lock.Release()
		return nil, errors.New("interactive browser launcher returned no process")
	}
	return newProfileOwnedBrowserProcess(process, lock), nil
}

func RunInteractiveBrowser(ctx context.Context, options InteractiveBrowserOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	closeTTL := options.CloseTTL
	if closeTTL == 0 {
		closeTTL = DefaultCloseTTL
	}
	if closeTTL <= 0 {
		return errors.New("interactive browser close ttl must be positive")
	}
	process, err := StartInteractiveBrowser(ctx, options)
	if err != nil {
		return err
	}
	select {
	case <-process.Done():
		return process.Err()
	case <-ctx.Done():
		closeCtx, cancel := context.WithTimeout(context.Background(), closeTTL)
		closeErr := process.Close(closeCtx)
		cancel()
		if closeErr != nil {
			return errors.Join(ctx.Err(), closeErr)
		}
		return ctx.Err()
	}
}

func launchNativeInteractiveBrowser(request InteractiveLaunchRequest, args []string) (BrowserProcess, error) {
	if request.Profile.Transport != TransportNative {
		return nil, errors.New("native interactive browser requires a native profile")
	}
	executable := strings.TrimSpace(request.Candidate.LocalExecutable)
	if executable == "" {
		executable = strings.TrimSpace(request.Candidate.Executable)
	}
	if executable == "" {
		return nil, errors.New("interactive browser executable is required")
	}
	command := exec.Command(executable, args...)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return nil, err
	}
	return newExecBrowserProcess(command), nil
}

func launchWSLHostInteractiveBrowser(request InteractiveLaunchRequest, args []string) (BrowserProcess, error) {
	if request.Profile.Transport != TransportWSLHost || request.Profile.HostPlatform != "windows" {
		return nil, errors.New("WSL-host interactive browser requires a Windows-host profile")
	}
	if request.Candidate.HostPlatform != "windows" {
		return nil, errors.New("WSL-host interactive browser candidate must target Windows")
	}
	executable := strings.TrimSpace(request.Candidate.Executable)
	if executable == "" {
		return nil, errors.New("windows-host interactive browser executable is required")
	}
	powershell, err := exec.LookPath("powershell.exe")
	if err != nil {
		return nil, fmt.Errorf("windows-host interactive browser requires powershell.exe: %w", err)
	}
	script := windowsHostInteractiveLaunchScript(executable, request.Profile.Path, args)
	command := exec.Command(powershell, "-NoProfile", "-NonInteractive", "-Command", script)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return nil, err
	}
	base := newExecBrowserProcess(command)
	return newWSLInteractiveBrowserProcess(base, executable, request.Profile.Path), nil
}

func windowsHostInteractiveLaunchScript(executable, profilePath string, args []string) string {
	executableExpr := powershellBase64String(executable)
	argumentExpr := powershellBase64String(windowsCommandLine(args))
	profileExpr := powershellBase64String(profilePath)
	return "$ErrorActionPreference='Stop';" +
		"$e=" + executableExpr + ";" +
		"$a=" + argumentExpr + ";" +
		"$d=" + profileExpr + ";" +
		"$p=Start-Process -FilePath $e -ArgumentList $a -PassThru;" +
		"$p.WaitForExit();" +
		"$empty=0;while($empty -lt 3){" +
		"$owned=@(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue|" +
		"Where-Object{$_.ExecutablePath -and $_.CommandLine -and " +
		"$_.ExecutablePath.Equals($e,[StringComparison]::OrdinalIgnoreCase) -and " +
		"$_.CommandLine.Contains('--user-data-dir') -and $_.CommandLine.Contains($d)});" +
		"if($owned.Count -gt 0){$empty=0}else{$empty++};" +
		"Start-Sleep -Milliseconds 100};" +
		"exit $p.ExitCode"
}

func powershellBase64String(value string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(value))
	return "[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('" + encoded + "'))"
}

func windowsCommandLine(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, quoteWindowsCommandLineArg(arg))
	}
	return strings.Join(quoted, " ")
}

func quoteWindowsCommandLineArg(arg string) string {
	if arg == "" {
		return `""`
	}
	if !strings.ContainsAny(arg, " \t\n\v\"") {
		return arg
	}
	var builder strings.Builder
	builder.WriteByte('"')
	backslashes := 0
	for _, char := range arg {
		switch char {
		case '\\':
			backslashes++
		case '"':
			builder.WriteString(strings.Repeat("\\", backslashes*2+1))
			builder.WriteByte('"')
			backslashes = 0
		default:
			builder.WriteString(strings.Repeat("\\", backslashes))
			backslashes = 0
			builder.WriteRune(char)
		}
	}
	builder.WriteString(strings.Repeat("\\", backslashes*2))
	builder.WriteByte('"')
	return builder.String()
}

type wslInteractiveBrowserProcess struct {
	base        BrowserProcess
	executable  string
	profilePath string
}

func newWSLInteractiveBrowserProcess(base BrowserProcess, executable, profilePath string) BrowserProcess {
	if base == nil {
		return nil
	}
	return &wslInteractiveBrowserProcess{
		base:        base,
		executable:  strings.TrimSpace(executable),
		profilePath: strings.TrimSpace(profilePath),
	}
}

func (process *wslInteractiveBrowserProcess) PID() int {
	if process == nil || process.base == nil {
		return 0
	}
	select {
	case <-process.base.Done():
		return 0
	default:
		return process.base.PID()
	}
}

func (process *wslInteractiveBrowserProcess) Done() <-chan struct{} {
	if process == nil || process.base == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return process.base.Done()
}

func (process *wslInteractiveBrowserProcess) Err() error {
	if process == nil || process.base == nil {
		return nil
	}
	return process.base.Err()
}

func (process *wslInteractiveBrowserProcess) Close(ctx context.Context) error {
	if process == nil || process.base == nil {
		return nil
	}
	select {
	case <-process.base.Done():
		return nil
	default:
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := stopWindowsHostBrowser(ctx, process.executable, process.profilePath); err != nil {
		return err
	}
	select {
	case <-process.base.Done():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(300 * time.Millisecond):
		return process.base.Close(ctx)
	}
}

type profileOwnedBrowserProcess struct {
	base BrowserProcess
	lock *ProfileLock

	once sync.Once
	done chan struct{}
	mu   sync.Mutex
	err  error
}

func newProfileOwnedBrowserProcess(base BrowserProcess, lock *ProfileLock) BrowserProcess {
	process := &profileOwnedBrowserProcess{base: base, lock: lock, done: make(chan struct{})}
	go func() {
		<-base.Done()
		process.finish(base.Err())
	}()
	return process
}

func (process *profileOwnedBrowserProcess) finish(processErr error) {
	process.once.Do(func() {
		lockErr := process.lock.Release()
		process.mu.Lock()
		process.err = errors.Join(processErr, lockErr)
		process.mu.Unlock()
		close(process.done)
	})
}

func (process *profileOwnedBrowserProcess) PID() int {
	if process == nil || process.base == nil {
		return 0
	}
	select {
	case <-process.done:
		return 0
	default:
		return process.base.PID()
	}
}

func (process *profileOwnedBrowserProcess) Done() <-chan struct{} {
	if process == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return process.done
}

func (process *profileOwnedBrowserProcess) Err() error {
	if process == nil {
		return nil
	}
	process.mu.Lock()
	defer process.mu.Unlock()
	return process.err
}

func (process *profileOwnedBrowserProcess) Close(ctx context.Context) error {
	if process == nil || process.base == nil {
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
	if err := process.base.Close(ctx); err != nil {
		return err
	}
	select {
	case <-process.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
