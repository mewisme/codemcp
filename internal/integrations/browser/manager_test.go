package browser

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeLauncher struct {
	mu        sync.Mutex
	launches  []LaunchRequest
	processes []*fakeProcess
	nextPID   int
}

func (launcher *fakeLauncher) Launch(_ context.Context, request LaunchRequest) (BrowserProcess, BrowserEndpoint, error) {
	launcher.mu.Lock()
	defer launcher.mu.Unlock()
	launcher.nextPID++
	process := newFakeProcess(1000 + launcher.nextPID)
	launcher.launches = append(launcher.launches, request)
	launcher.processes = append(launcher.processes, process)
	return process, BrowserEndpoint{URL: fmt.Sprintf("fake://browser/%d", launcher.nextPID)}, nil
}

func (launcher *fakeLauncher) count() int {
	launcher.mu.Lock()
	defer launcher.mu.Unlock()
	return len(launcher.launches)
}

func (launcher *fakeLauncher) request(index int) LaunchRequest {
	launcher.mu.Lock()
	defer launcher.mu.Unlock()
	return launcher.launches[index]
}

type fakeConnector struct {
	mu      sync.Mutex
	clients []*fakeClient
}

func (connector *fakeConnector) Connect(_ context.Context, _ BrowserEndpoint) (BrowserClient, error) {
	connector.mu.Lock()
	defer connector.mu.Unlock()
	client := newFakeClient()
	connector.clients = append(connector.clients, client)
	return client, nil
}

func (connector *fakeConnector) latest() *fakeClient {
	connector.mu.Lock()
	defer connector.mu.Unlock()
	if len(connector.clients) == 0 {
		return nil
	}
	return connector.clients[len(connector.clients)-1]
}

type fakeProcess struct {
	pid  int
	done chan struct{}
	once sync.Once
	mu   sync.Mutex
	err  error
}

func newFakeProcess(pid int) *fakeProcess          { return &fakeProcess{pid: pid, done: make(chan struct{})} }
func (process *fakeProcess) PID() int              { return process.pid }
func (process *fakeProcess) Done() <-chan struct{} { return process.done }
func (process *fakeProcess) Err() error {
	process.mu.Lock()
	defer process.mu.Unlock()
	return process.err
}
func (process *fakeProcess) stop(err error) {
	process.once.Do(func() {
		process.mu.Lock()
		process.err = err
		process.mu.Unlock()
		close(process.done)
	})
}
func (process *fakeProcess) Close(context.Context) error { process.stop(nil); return nil }

type fakeClient struct {
	mu        sync.Mutex
	done      chan struct{}
	once      sync.Once
	err       error
	nextTab   int
	tabs      map[string]*fakeTab
	minimized int
}

func newFakeClient() *fakeClient {
	return &fakeClient{done: make(chan struct{}), tabs: map[string]*fakeTab{}}
}

func (client *fakeClient) NewTab(_ context.Context, _ string) (BrowserTab, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.nextTab++
	id := fmt.Sprintf("tab-%d", client.nextTab)
	tab := newFakeTab(id)
	client.tabs[id] = tab
	return tab, nil
}

func (client *fakeClient) Minimize(context.Context) error {
	client.mu.Lock()
	client.minimized++
	client.mu.Unlock()
	return nil
}

func (client *fakeClient) Done() <-chan struct{} { return client.done }
func (client *fakeClient) Err() error {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.err
}
func (client *fakeClient) Close(context.Context) error {
	client.crash(nil)
	return nil
}
func (client *fakeClient) crash(err error) {
	client.once.Do(func() {
		client.mu.Lock()
		client.err = err
		client.mu.Unlock()
		close(client.done)
	})
}
func (client *fakeClient) tab(id string) *fakeTab {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.tabs[id]
}

type fakeTab struct {
	id     string
	done   chan struct{}
	once   sync.Once
	mu     sync.Mutex
	err    error
	closed bool
	url    string
}

func newFakeTab(id string) *fakeTab        { return &fakeTab{id: id, done: make(chan struct{})} }
func (tab *fakeTab) ID() string            { return tab.id }
func (tab *fakeTab) Done() <-chan struct{} { return tab.done }
func (tab *fakeTab) Err() error {
	tab.mu.Lock()
	defer tab.mu.Unlock()
	return tab.err
}
func (tab *fakeTab) Navigate(_ context.Context, url string) error {
	tab.mu.Lock()
	tab.url = url
	tab.mu.Unlock()
	return nil
}
func (tab *fakeTab) Evaluate(_ context.Context, _ string, _ any) error { return nil }
func (tab *fakeTab) Close(context.Context) error {
	tab.mu.Lock()
	tab.closed = true
	tab.mu.Unlock()
	tab.finish(nil)
	return nil
}
func (tab *fakeTab) crash(err error) { tab.finish(err) }
func (tab *fakeTab) finish(err error) {
	tab.once.Do(func() {
		tab.mu.Lock()
		tab.err = err
		tab.mu.Unlock()
		close(tab.done)
	})
}
func (tab *fakeTab) isClosed() bool {
	tab.mu.Lock()
	defer tab.mu.Unlock()
	return tab.closed
}

func TestBrowserManagerSharesOneBrowserAcrossDistinctAgentTabs(t *testing.T) {
	manager, launcher, connector := newFakeManager(t, 5, time.Minute, time.Minute)
	defer manager.Close(context.Background())

	var leases []LeaseSnapshot
	for _, id := range []string{"agent-a", "agent-b", "agent-c"} {
		lease, err := manager.Acquire(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, lease)
	}
	if launcher.count() != 1 {
		t.Fatalf("browser launches=%d want=1", launcher.count())
	}
	seen := map[string]bool{}
	for _, lease := range leases {
		if seen[lease.TabID] {
			t.Fatalf("duplicate tab id %q", lease.TabID)
		}
		seen[lease.TabID] = true
	}
	if got := manager.Snapshot().ActiveLeases; got != 3 {
		t.Fatalf("active leases=%d want=3", got)
	}
	if connector.latest() == nil || connector.latest().nextTab != 3 {
		t.Fatal("expected three tabs on one CDP client")
	}
}

func TestBrowserManagerMinimizedLaunchRemainsVisible(t *testing.T) {
	root := t.TempDir()
	launcher := &fakeLauncher{}
	connector := &fakeConnector{}
	manager, err := NewManager(ManagerOptions{
		Capability:  testCapability(testProfile(root)),
		MaxTabs:     1,
		Minimized:   true,
		LaunchTTL:   time.Second,
		CloseTTL:    time.Second,
		MinimizeTTL: time.Second,
		Launcher:    launcher,
		Connector:   connector,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(context.Background())
	if _, err := manager.Acquire(context.Background(), "agent-minimized"); err != nil {
		t.Fatal(err)
	}
	request := launcher.request(0)
	if !request.Visible || !request.Minimized {
		t.Fatalf("minimized launch request=%#v", request)
	}
	if connector.latest() == nil || connector.latest().minimized != 1 {
		t.Fatal("managed minimized mode did not enforce minimized window after CDP connect")
	}
}

func TestBrowserManagerExposesOnlyActiveOwnedTab(t *testing.T) {
	manager, _, _ := newFakeManager(t, 1, time.Minute, time.Minute)
	defer manager.Close(context.Background())
	lease, err := manager.Acquire(context.Background(), "agent-a")
	if err != nil {
		t.Fatal(err)
	}
	tab, ok := manager.Tab("agent-a")
	if !ok || tab.ID() != lease.TabID {
		t.Fatalf("tab=%v ok=%t lease=%#v", tab, ok, lease)
	}
	if err := manager.Release(context.Background(), "agent-a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.Tab("agent-a"); ok {
		t.Fatal("released tab remained visible through manager")
	}
}

func TestBrowserManagerReleaseDoesNotAffectSiblingAgents(t *testing.T) {
	manager, _, connector := newFakeManager(t, 5, time.Minute, time.Minute)
	defer manager.Close(context.Background())
	a, _ := manager.Acquire(context.Background(), "agent-a")
	b, _ := manager.Acquire(context.Background(), "agent-b")
	c, _ := manager.Acquire(context.Background(), "agent-c")
	client := connector.latest()

	if err := manager.Release(context.Background(), "agent-b"); err != nil {
		t.Fatal(err)
	}
	if !client.tab(b.TabID).isClosed() {
		t.Fatal("released tab was not closed")
	}
	for _, item := range []LeaseSnapshot{a, c} {
		lease, ok := manager.Lease(item.AgentID)
		if !ok || lease.State != LeaseActive || client.tab(item.TabID).isClosed() {
			t.Fatalf("sibling lease changed: %#v", lease)
		}
	}
}

func TestBrowserManagerTabCrashFailsOnlyOwner(t *testing.T) {
	manager, _, connector := newFakeManager(t, 5, time.Minute, time.Minute)
	defer manager.Close(context.Background())
	a, _ := manager.Acquire(context.Background(), "agent-a")
	b, _ := manager.Acquire(context.Background(), "agent-b")
	connector.latest().tab(a.TabID).crash(errors.New("renderer crashed"))

	eventually(t, time.Second, func() bool {
		lease, ok := manager.Lease("agent-a")
		return ok && lease.State == LeaseFailed && lease.TabID == ""
	})
	leaseB, ok := manager.Lease("agent-b")
	if !ok || leaseB.State != LeaseActive || leaseB.TabID != b.TabID {
		t.Fatalf("sibling lease=%#v", leaseB)
	}
}

func TestBrowserManagerBrowserCrashFailsAllActiveAndCanRelaunch(t *testing.T) {
	manager, launcher, connector := newFakeManager(t, 5, time.Minute, time.Minute)
	defer manager.Close(context.Background())
	_, _ = manager.Acquire(context.Background(), "agent-a")
	_, _ = manager.Acquire(context.Background(), "agent-b")
	connector.latest().crash(errors.New("browser connection lost"))

	eventually(t, time.Second, func() bool {
		a, oka := manager.Lease("agent-a")
		b, okb := manager.Lease("agent-b")
		return oka && okb && a.State == LeaseFailed && b.State == LeaseFailed &&
			a.TabID == "" && b.TabID == "" && manager.Snapshot().ActiveLeases == 0
	})
	if _, err := manager.Acquire(context.Background(), "agent-c"); err != nil {
		t.Fatal(err)
	}
	if launcher.count() != 2 {
		t.Fatalf("browser launches=%d want=2 after crash", launcher.count())
	}
}

func TestBrowserManagerCapacityFailsWithoutOpeningExtraTab(t *testing.T) {
	manager, launcher, connector := newFakeManager(t, 2, time.Minute, time.Minute)
	defer manager.Close(context.Background())
	_, _ = manager.Acquire(context.Background(), "agent-a")
	_, _ = manager.Acquire(context.Background(), "agent-b")
	if _, err := manager.Acquire(context.Background(), "agent-c"); !errors.Is(err, ErrCapacity) {
		t.Fatalf("capacity error=%v", err)
	}
	if launcher.count() != 1 || connector.latest().nextTab != 2 {
		t.Fatalf("launches=%d tabs=%d", launcher.count(), connector.latest().nextTab)
	}
}

func TestBrowserManagerIdleLeaseExpiresAndClosesTab(t *testing.T) {
	manager, _, connector := newFakeManager(t, 5, 25*time.Millisecond, time.Minute)
	defer manager.Close(context.Background())
	lease, err := manager.Acquire(context.Background(), "agent-a")
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, time.Second, func() bool {
		current, ok := manager.Lease("agent-a")
		return ok && current.State == LeaseExpired && current.TabID == ""
	})
	if !connector.latest().tab(lease.TabID).isClosed() {
		t.Fatal("expired tab was not closed")
	}
}

func TestBrowserManagerTouchInvalidatesStaleIdleTimer(t *testing.T) {
	manager, _, _ := newFakeManager(t, 5, 60*time.Millisecond, time.Minute)
	defer manager.Close(context.Background())
	if _, err := manager.Acquire(context.Background(), "agent-a"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	if err := manager.Touch("agent-a"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(35 * time.Millisecond)
	lease, ok := manager.Lease("agent-a")
	if !ok || lease.State != LeaseActive {
		t.Fatalf("stale idle timer expired refreshed lease: %#v", lease)
	}
	eventually(t, time.Second, func() bool {
		lease, ok := manager.Lease("agent-a")
		return ok && lease.State == LeaseExpired
	})
}

func TestBrowserManagerConcurrentAcquireUsesOneProcessAndHardCapacity(t *testing.T) {
	manager, launcher, _ := newFakeManager(t, 5, time.Minute, time.Minute)
	defer manager.Close(context.Background())
	var success atomic.Int32
	var capacity atomic.Int32
	var wg sync.WaitGroup
	for index := range 12 {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, err := manager.Acquire(context.Background(), fmt.Sprintf("agent-%02d", index))
			switch {
			case err == nil:
				success.Add(1)
			case errors.Is(err, ErrCapacity):
				capacity.Add(1)
			default:
				t.Errorf("acquire %d: %v", index, err)
			}
		}(index)
	}
	wg.Wait()
	if success.Load() != 5 || capacity.Load() != 7 {
		t.Fatalf("success=%d capacity=%d", success.Load(), capacity.Load())
	}
	if launcher.count() != 1 {
		t.Fatalf("browser launches=%d want=1", launcher.count())
	}
}

func TestBrowserManagerWarmShutdownReopensSameProfile(t *testing.T) {
	manager, launcher, _ := newFakeManager(t, 5, time.Minute, 25*time.Millisecond)
	defer manager.Close(context.Background())
	if _, err := manager.Acquire(context.Background(), "agent-a"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Release(context.Background(), "agent-a"); err != nil {
		t.Fatal(err)
	}
	eventually(t, time.Second, func() bool { return manager.Snapshot().State == ManagerStopped })
	if _, err := manager.Acquire(context.Background(), "agent-b"); err != nil {
		t.Fatal(err)
	}
	if launcher.count() != 2 {
		t.Fatalf("launches=%d want=2", launcher.count())
	}
	if launcher.request(0).Profile.Path != launcher.request(1).Profile.Path {
		t.Fatalf("profile changed across relaunch: %q != %q", launcher.request(0).Profile.Path, launcher.request(1).Profile.Path)
	}
}

func TestBrowserManagerProfileLockPreventsConcurrentOwner(t *testing.T) {
	root := t.TempDir()
	profile := testProfile(root)
	capability := testCapability(profile)
	oneLauncher, oneConnector := &fakeLauncher{}, &fakeConnector{}
	twoLauncher, twoConnector := &fakeLauncher{}, &fakeConnector{}
	one, err := NewManager(ManagerOptions{Capability: capability, Launcher: oneLauncher, Connector: oneConnector})
	if err != nil {
		t.Fatal(err)
	}
	two, err := NewManager(ManagerOptions{Capability: capability, Launcher: twoLauncher, Connector: twoConnector})
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close(context.Background())
	defer two.Close(context.Background())
	if err := one.EnsureRunning(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := two.EnsureRunning(context.Background()); !errors.Is(err, ErrProfileBusy) {
		t.Fatalf("second owner error=%v", err)
	}
	if err := one.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := two.EnsureRunning(context.Background()); err != nil {
		t.Fatalf("second owner could not acquire released profile: %v", err)
	}
}

func TestBrowserManagerMinimizeUsesBoundedCDPClientOperation(t *testing.T) {
	manager, _, connector := newFakeManager(t, 5, time.Minute, time.Minute)
	defer manager.Close(context.Background())
	if err := manager.Minimize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if connector.latest().minimized != 1 {
		t.Fatalf("minimize calls=%d", connector.latest().minimized)
	}
}

func TestBrowserManagerAcceptsLaunchableCapabilityWithoutActiveProbeEvidence(t *testing.T) {
	profile := testProfile(t.TempDir())
	capability := testCapability(profile)
	capability.Usable = false
	manager, err := NewManager(ManagerOptions{
		Capability: capability,
		Launcher:   &fakeLauncher{},
		Connector:  &fakeConnector{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(context.Background())
	if err := manager.EnsureRunning(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestBrowserManagerRejectsStructurallyIncompleteLaunchableCapability(t *testing.T) {
	profile := testProfile(t.TempDir())
	tests := []struct {
		name       string
		capability Capability
	}{
		{name: "not launchable", capability: func() Capability {
			value := testCapability(profile)
			value.Launchable = false
			return value
		}()},
		{name: "missing candidate", capability: func() Capability {
			value := testCapability(profile)
			value.Candidate = nil
			return value
		}()},
		{name: "missing profile", capability: func() Capability {
			value := testCapability(profile)
			value.Profile = nil
			return value
		}()},
		{name: "no graphical route", capability: func() Capability {
			value := testCapability(profile)
			value.Graphical = false
			return value
		}()},
		{name: "transport mismatch", capability: func() Capability {
			value := testCapability(profile)
			value.Profile = &ProfileRef{
				HostPlatform: "windows",
				Transport:    TransportWSLHost,
				Path:         `C:\Users\Mew\AppData\Local\CodeMCP\Browser\ChatGPT`,
				LocalPath:    "/mnt/c/Users/Mew/AppData/Local/CodeMCP/Browser/ChatGPT",
			}
			return value
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewManager(ManagerOptions{Capability: test.capability}); err == nil {
				t.Fatalf("NewManager accepted capability=%#v", test.capability)
			}
		})
	}
}

func newFakeManager(t *testing.T, maxTabs int, idleTTL, warmTTL time.Duration) (*Manager, *fakeLauncher, *fakeConnector) {
	t.Helper()
	root := t.TempDir()
	launcher := &fakeLauncher{}
	connector := &fakeConnector{}
	manager, err := NewManager(ManagerOptions{
		Capability: testCapability(testProfile(root)),
		MaxTabs:    maxTabs, AgentIdleTTL: idleTTL, BrowserWarmTTL: warmTTL,
		LaunchTTL: time.Second, CloseTTL: time.Second, MinimizeTTL: time.Second,
		Launcher: launcher, Connector: connector,
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager, launcher, connector
}

func testProfile(root string) ProfileRef {
	return ProfileRef{
		HostPlatform: "linux", Transport: TransportNative,
		Path:      filepath.Join(root, "browser", "chatgpt"),
		LocalPath: filepath.Join(root, "browser", "chatgpt"),
		LockPath:  filepath.Join(root, "browser", "chatgpt.lock"),
	}
}

func testCapability(profile ProfileRef) Capability {
	candidate := Candidate{
		Family: FamilyChromium, Executable: "/usr/bin/chromium", LocalExecutable: "/usr/bin/chromium",
		HostPlatform: "linux", Transport: TransportNative, Source: SourceConfigured,
	}
	return Capability{
		State: StateAvailable, Enabled: true, Available: true, Launchable: true, Usable: true,
		Family: FamilyChromium, Executable: candidate.Executable,
		HostPlatform: "linux", Transport: TransportNative, Graphical: true,
		ProfileHostPlatform: profile.HostPlatform, Profile: &profile, Candidate: &candidate,
	}
}

func eventually(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}
