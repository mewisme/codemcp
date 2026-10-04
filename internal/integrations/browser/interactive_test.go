package browser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeInteractiveLauncher struct {
	mu       sync.Mutex
	requests []InteractiveLaunchRequest
	process  *fakeProcess
	err      error
	started  chan struct{}
}

func (launcher *fakeInteractiveLauncher) Launch(_ context.Context, request InteractiveLaunchRequest) (BrowserProcess, error) {
	launcher.mu.Lock()
	defer launcher.mu.Unlock()
	launcher.requests = append(launcher.requests, request)
	if launcher.started != nil {
		select {
		case <-launcher.started:
		default:
			close(launcher.started)
		}
	}
	if launcher.err != nil {
		return nil, launcher.err
	}
	if launcher.process == nil {
		launcher.process = newFakeProcess(9001)
	}
	return launcher.process, nil
}

func (launcher *fakeInteractiveLauncher) count() int {
	launcher.mu.Lock()
	defer launcher.mu.Unlock()
	return len(launcher.requests)
}

func TestInteractiveBrowserOwnsProfileUntilObservedProcessExit(t *testing.T) {
	profile := testProfile(t.TempDir())
	capability := testCapability(profile)
	interactiveLauncher := &fakeInteractiveLauncher{process: newFakeProcess(9001)}
	process, err := StartInteractiveBrowser(context.Background(), InteractiveBrowserOptions{
		Capability: capability,
		URL:        "https://chatgpt.com/?temporary-chat=true",
		Launcher:   interactiveLauncher,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close(context.Background())

	busy, err := ProfileInUse(profile)
	if err != nil || !busy {
		t.Fatalf("interactive profile busy=%t err=%v", busy, err)
	}

	manager, err := NewManager(ManagerOptions{
		Capability: capability,
		Launcher:   &fakeLauncher{},
		Connector:  &fakeConnector{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(context.Background())
	if err := manager.EnsureRunning(context.Background()); !errors.Is(err, ErrProfileBusy) {
		t.Fatalf("managed runtime acquired interactive profile: %v", err)
	}

	interactiveLauncher.process.stop(nil)
	select {
	case <-process.Done():
	case <-time.After(time.Second):
		t.Fatal("interactive process did not release profile after observed exit")
	}
	busy, err = ProfileInUse(profile)
	if err != nil || busy {
		t.Fatalf("released interactive profile busy=%t err=%v", busy, err)
	}
	if err := manager.EnsureRunning(context.Background()); err != nil {
		t.Fatalf("managed runtime could not acquire released interactive profile: %v", err)
	}
}

func TestInteractiveBrowserCannotStartWhileManagedAgentOwnsProfile(t *testing.T) {
	profile := testProfile(t.TempDir())
	capability := testCapability(profile)
	manager, err := NewManager(ManagerOptions{
		Capability: capability,
		Launcher:   &fakeLauncher{},
		Connector:  &fakeConnector{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(context.Background())
	if _, err := manager.Acquire(context.Background(), "agent-a"); err != nil {
		t.Fatal(err)
	}

	interactiveLauncher := &fakeInteractiveLauncher{}
	if _, err := StartInteractiveBrowser(context.Background(), InteractiveBrowserOptions{
		Capability: capability,
		URL:        "https://chatgpt.com/?temporary-chat=true",
		Launcher:   interactiveLauncher,
	}); !errors.Is(err, ErrProfileBusy) {
		t.Fatalf("interactive browser acquired managed profile: %v", err)
	}
	if interactiveLauncher.count() != 0 {
		t.Fatalf("interactive launcher was called while profile busy: %d", interactiveLauncher.count())
	}
}

func TestRunInteractiveBrowserCancellationClosesOwnedProcess(t *testing.T) {
	profile := testProfile(t.TempDir())
	capability := testCapability(profile)
	launcher := &fakeInteractiveLauncher{
		process: newFakeProcess(9002),
		started: make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- RunInteractiveBrowser(ctx, InteractiveBrowserOptions{
			Capability: capability,
			URL:        "https://chatgpt.com/?temporary-chat=true",
			Launcher:   launcher,
			CloseTTL:   time.Second,
		})
	}()
	select {
	case <-launcher.started:
	case <-time.After(time.Second):
		t.Fatal("interactive browser did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("run error=%v want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("interactive browser did not stop after cancellation")
	}
	select {
	case <-launcher.process.Done():
	default:
		t.Fatal("owned interactive process remained alive after cancellation")
	}
	busy, err := ProfileInUse(profile)
	if err != nil || busy {
		t.Fatalf("profile remained busy after cancellation: busy=%t err=%v", busy, err)
	}
}

func TestNativeInteractiveLauncherObservesOwnedProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is for unix native process semantics")
	}
	root := t.TempDir()
	executable := filepath.Join(root, "browser-fixture")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\ntrap 'exit 0' INT TERM\nwhile :; do sleep 0.05; done\n"), 0700); err != nil {
		t.Fatal(err)
	}
	profile := testProfile(root)
	capability := testCapability(profile)
	capability.Candidate.Executable = executable
	capability.Candidate.LocalExecutable = executable
	capability.Executable = executable

	process, err := StartInteractiveBrowser(context.Background(), InteractiveBrowserOptions{
		Capability: capability,
		URL:        "https://chatgpt.com/?temporary-chat=true",
	})
	if err != nil {
		t.Fatal(err)
	}
	if process.PID() <= 0 {
		t.Fatalf("native interactive pid=%d", process.PID())
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := process.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-process.Done():
	case <-time.After(time.Second):
		t.Fatal("native interactive process did not finish")
	}
}

func TestWSLInteractiveHostObserverWaitsForProfileOwnerWithoutRelay(t *testing.T) {
	executable := `C:\Program Files\Google\Chrome\Application\chrome.exe`
	profile := `C:\Users\Mew User\AppData\Local\CodeMCP\Browser\ChatGPT`
	request := InteractiveLaunchRequest{
		Candidate: Candidate{
			Transport:       TransportWSLHost,
			HostPlatform:    "windows",
			Executable:      executable,
			LocalExecutable: "/mnt/c/Program Files/Google/Chrome/Application/chrome.exe",
		},
		Profile: ProfileRef{
			Transport:    TransportWSLHost,
			HostPlatform: "windows",
			Path:         profile,
			LocalPath:    "/mnt/c/Users/Mew User/AppData/Local/CodeMCP/Browser/ChatGPT",
		},
		URL: "https://chatgpt.com/?temporary-chat=true",
	}
	args, err := interactiveLaunchArgs(request)
	if err != nil {
		t.Fatal(err)
	}
	script := windowsHostInteractiveLaunchScript(executable, profile, args)
	for _, required := range []string{"Start-Process", "WaitForExit", "Get-CimInstance Win32_Process", "CommandLine.Contains($m)"} {
		if !strings.Contains(script, required) {
			t.Fatalf("host observer script missing %q: %s", required, script)
		}
	}
	for _, forbidden := range []string{"127.0.0.1", "TcpListener", "remote-debugging", "DevToolsActivePort"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("plain WSL login script contains relay/CDP primitive %q: %s", forbidden, script)
		}
	}
	commandLine := windowsCommandLine(args)
	if !strings.Contains(commandLine, `"--user-data-dir=C:\Users\Mew User\AppData\Local\CodeMCP\Browser\ChatGPT"`) {
		t.Fatalf("Windows profile path was not safely quoted: %s", commandLine)
	}
	if strings.Contains(commandLine, request.Profile.LocalPath) {
		t.Fatalf("Windows-host launch used WSL local profile path: %s", commandLine)
	}
}

func TestWSLInteractiveStopTargetsExactExecutableAndProfileMarker(t *testing.T) {
	executable := `C:\Program Files\Google\Chrome\Application\chrome.exe`
	profile := `C:\Users\Mew\AppData\Local\CodeMCP\Browser\ChatGPT`
	script := windowsHostInteractiveStopScript(executable, profile)
	for _, required := range []string{
		"ExecutablePath.Equals($e",
		"CommandLine.Contains($m)",
		"Stop-Process -Id $p.ProcessId",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("stop script missing ownership guard %q: %s", required, script)
		}
	}
	for _, forbidden := range []string{
		"Stop-Process -Name chrome",
		"Stop-Process -Name msedge",
		"taskkill",
	} {
		if strings.Contains(strings.ToLower(script), strings.ToLower(forbidden)) {
			t.Fatalf("stop script can target unrelated browsers via %q: %s", forbidden, script)
		}
	}
}

func TestWSLInteractiveProfileLockOutlivesUnrelatedInteropShimExit(t *testing.T) {
	root := t.TempDir()
	profile := ProfileRef{
		HostPlatform: "windows",
		Transport:    TransportWSLHost,
		Path:         `C:\Users\Mew\AppData\Local\CodeMCP\Browser\ChatGPT`,
		LocalPath:    filepath.Join(root, "CodeMCP", "Browser", "ChatGPT"),
		LockPath:     filepath.Join(root, "CodeMCP", "Browser", "chatgpt.lock"),
	}
	candidate := Candidate{
		Family:           FamilyChrome,
		Executable:       `C:\Program Files\Google\Chrome\Application\chrome.exe`,
		LocalExecutable:  "/mnt/c/Program Files/Google/Chrome/Application/chrome.exe",
		HostPlatform:     "windows",
		Transport:        TransportWSLHost,
		Source:           SourceStandard,
		LocalAppData:     root,
		HostLocalAppData: `C:\Users\Mew\AppData\Local`,
	}
	capability := Capability{
		State: StateAvailable, Enabled: true, Available: true, Launchable: true,
		Family: FamilyChrome, Executable: candidate.Executable,
		HostPlatform: "windows", Transport: TransportWSLHost, Graphical: true,
		ProfileHostPlatform: "windows", Profile: &profile, Candidate: &candidate,
	}
	observer := newFakeProcess(9100)
	launcher := &fakeInteractiveLauncher{process: observer}
	process, err := StartInteractiveBrowser(context.Background(), InteractiveBrowserOptions{
		Capability: capability,
		URL:        "https://chatgpt.com/?temporary-chat=true",
		Launcher:   launcher,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close(context.Background())

	shim := newFakeProcess(9101)
	shim.stop(nil)
	select {
	case <-process.Done():
		t.Fatal("profile ownership followed unrelated short-lived interop shim")
	default:
	}
	busy, err := ProfileInUse(profile)
	if err != nil || !busy {
		t.Fatalf("WSL profile lock released after shim exit: busy=%t err=%v", busy, err)
	}
	observer.stop(nil)
	select {
	case <-process.Done():
	case <-time.After(time.Second):
		t.Fatal("WSL profile lock did not release after host observer exit")
	}
}
