package browser

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type Manager struct {
	mu sync.Mutex

	capability Capability
	launcher   BrowserLauncher
	connector  BrowserConnector

	maxTabs        int
	agentIdleTTL   time.Duration
	browserWarmTTL time.Duration
	launchTTL      time.Duration
	minimizeTTL    time.Duration
	closeTTL       time.Duration
	now            func() time.Time

	closed     bool
	generation uint64
	starting   *startAttempt
	instance   *browserInstance
	leases     map[string]*leaseRecord
	pending    map[string]struct{}
	warmTimer  *time.Timer
}

type startAttempt struct {
	done chan struct{}
	err  error
}

type browserInstance struct {
	generation uint64
	process    BrowserProcess
	client     BrowserClient
	profile    ProfileRef
	lock       *ProfileLock
}

type leaseRecord struct {
	snapshot        LeaseSnapshot
	tab             BrowserTab
	timer           *time.Timer
	timerGeneration uint64
}

func NewManager(options ManagerOptions) (*Manager, error) {
	capability := options.Capability
	if capability.State != StateAvailable || !capability.Available || !capability.Launchable {
		return nil, errors.New("browser capability must be available and launchable")
	}
	if capability.Profile == nil {
		return nil, errors.New("browser capability profile is required")
	}
	if capability.Candidate == nil {
		return nil, errors.New("browser capability candidate is required")
	}
	if !capability.Graphical {
		return nil, errors.New("browser capability requires a graphical route")
	}
	if strings.TrimSpace(capability.Candidate.LocalExecutable) == "" && strings.TrimSpace(capability.Candidate.Executable) == "" {
		return nil, errors.New("browser capability candidate executable is required")
	}
	if strings.TrimSpace(capability.Profile.Path) == "" || strings.TrimSpace(capability.Profile.LocalPath) == "" {
		return nil, errors.New("browser capability profile paths are required")
	}
	if capability.Candidate.Transport != capability.Profile.Transport {
		return nil, errors.New("browser capability candidate/profile transport mismatch")
	}
	if options.MaxTabs == 0 {
		options.MaxTabs = DefaultMaxTabs
	}
	if options.MaxTabs < 1 {
		return nil, errors.New("browser max tabs must be positive")
	}
	if options.AgentIdleTTL == 0 {
		options.AgentIdleTTL = DefaultAgentIdleTTL
	}
	if options.AgentIdleTTL < 0 {
		return nil, errors.New("browser agent idle ttl cannot be negative")
	}
	if options.BrowserWarmTTL == 0 {
		options.BrowserWarmTTL = DefaultBrowserWarmTTL
	}
	if options.BrowserWarmTTL < 0 {
		return nil, errors.New("browser warm ttl cannot be negative")
	}
	if options.LaunchTTL == 0 {
		options.LaunchTTL = DefaultManagerLaunchTTL
	}
	if options.LaunchTTL <= 0 {
		return nil, errors.New("browser launch ttl must be positive")
	}
	if options.MinimizeTTL == 0 {
		options.MinimizeTTL = DefaultMinimizeTTL
	}
	if options.MinimizeTTL <= 0 {
		return nil, errors.New("browser minimize ttl must be positive")
	}
	if options.CloseTTL == 0 {
		options.CloseTTL = DefaultCloseTTL
	}
	if options.CloseTTL <= 0 {
		return nil, errors.New("browser close ttl must be positive")
	}
	if options.Launcher == nil {
		options.Launcher = newExecBrowserLauncher()
	}
	if options.Connector == nil {
		options.Connector = newChromedpConnector()
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Manager{
		capability: capability,
		launcher:   options.Launcher, connector: options.Connector,
		maxTabs: options.MaxTabs, agentIdleTTL: options.AgentIdleTTL,
		browserWarmTTL: options.BrowserWarmTTL, launchTTL: options.LaunchTTL,
		minimizeTTL: options.MinimizeTTL, closeTTL: options.CloseTTL,
		now: options.Now, leases: map[string]*leaseRecord{}, pending: map[string]struct{}{},
	}, nil
}

func (manager *Manager) EnsureRunning(ctx context.Context) error {
	if manager == nil {
		return errors.New("browser manager is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		manager.mu.Lock()
		if manager.closed {
			manager.mu.Unlock()
			return ErrManagerClosed
		}
		if manager.instance != nil {
			manager.mu.Unlock()
			return nil
		}
		if manager.starting != nil {
			attempt := manager.starting
			manager.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-attempt.done:
				if attempt.err != nil {
					return attempt.err
				}
				continue
			}
		}
		attempt := &startAttempt{done: make(chan struct{})}
		manager.starting = attempt
		manager.mu.Unlock()

		instance, err := manager.start(ctx)

		manager.mu.Lock()
		if err == nil && manager.closed {
			err = ErrManagerClosed
		}
		if err == nil {
			manager.generation++
			instance.generation = manager.generation
			manager.instance = instance
		}
		attempt.err = err
		if manager.starting == attempt {
			manager.starting = nil
		}
		close(attempt.done)
		manager.mu.Unlock()

		if err != nil {
			if instance != nil {
				manager.closeInstance(context.Background(), instance)
			}
			return err
		}
		go manager.watchBrowser(instance)
		return nil
	}
}

func (manager *Manager) start(ctx context.Context) (*browserInstance, error) {
	launchCtx, cancel := context.WithTimeout(ctx, manager.launchTTL)
	defer cancel()

	profile := *manager.capability.Profile
	if err := PrepareProfile(profile); err != nil {
		return nil, err
	}
	profileLock, ok, err := TryAcquireProfile(profile)
	if err != nil {
		return nil, fmt.Errorf("acquire browser profile lock: %w", err)
	}
	if !ok {
		return nil, ErrProfileBusy
	}
	instance := &browserInstance{profile: profile, lock: profileLock}
	process, endpoint, err := manager.launcher.Launch(launchCtx, LaunchRequest{
		Candidate: *manager.capability.Candidate,
		Profile:   profile,
		Visible:   true,
	})
	if err != nil {
		_ = profileLock.Release()
		return nil, fmt.Errorf("launch browser: %w", err)
	}
	instance.process = process
	client, err := manager.connector.Connect(launchCtx, endpoint)
	if err != nil {
		manager.closeInstance(context.Background(), instance)
		return nil, fmt.Errorf("connect browser CDP: %w", err)
	}
	instance.client = client
	return instance, nil
}

func (manager *Manager) Acquire(ctx context.Context, agentID string) (LeaseSnapshot, error) {
	if manager == nil {
		return LeaseSnapshot{}, errors.New("browser manager is unavailable")
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return LeaseSnapshot{}, errors.New("browser agent id is required")
	}
	if len(agentID) > 256 {
		return LeaseSnapshot{}, errors.New("browser agent id exceeds 256 bytes")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return LeaseSnapshot{}, ErrManagerClosed
	}
	if _, exists := manager.leases[agentID]; exists {
		manager.mu.Unlock()
		return LeaseSnapshot{}, ErrLeaseExists
	}
	if _, exists := manager.pending[agentID]; exists {
		manager.mu.Unlock()
		return LeaseSnapshot{}, ErrLeaseExists
	}
	if manager.inUseCountLocked() >= manager.maxTabs {
		manager.mu.Unlock()
		return LeaseSnapshot{}, ErrCapacity
	}
	manager.pending[agentID] = struct{}{}
	manager.stopWarmTimerLocked()
	manager.mu.Unlock()
	reserved := true
	defer func() {
		if reserved {
			manager.releasePending(agentID)
		}
	}()

	if err := manager.EnsureRunning(ctx); err != nil {
		return LeaseSnapshot{}, err
	}

	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return LeaseSnapshot{}, ErrManagerClosed
	}
	if _, exists := manager.pending[agentID]; !exists {
		manager.mu.Unlock()
		return LeaseSnapshot{}, errors.New("browser tab reservation disappeared")
	}
	instance := manager.instance
	if instance == nil {
		manager.mu.Unlock()
		return LeaseSnapshot{}, errors.New("browser became unavailable before tab acquisition")
	}
	generation := instance.generation
	client := instance.client
	manager.stopWarmTimerLocked()
	manager.mu.Unlock()

	tabCtx, tabCancel := context.WithTimeout(ctx, manager.launchTTL)
	tab, err := client.NewTab(tabCtx, "about:blank")
	tabCancel()
	if err != nil {
		return LeaseSnapshot{}, fmt.Errorf("create browser tab: %w", err)
	}
	if strings.TrimSpace(tab.ID()) == "" {
		_ = manager.closeTab(context.Background(), tab)
		return LeaseSnapshot{}, errors.New("browser tab id is empty")
	}

	manager.mu.Lock()
	if manager.closed || manager.instance == nil || manager.instance.generation != generation {
		manager.mu.Unlock()
		_ = manager.closeTab(context.Background(), tab)
		if manager.closed {
			return LeaseSnapshot{}, ErrManagerClosed
		}
		return LeaseSnapshot{}, errors.New("browser changed while acquiring tab")
	}
	if _, exists := manager.pending[agentID]; !exists {
		manager.mu.Unlock()
		_ = manager.closeTab(context.Background(), tab)
		return LeaseSnapshot{}, errors.New("browser tab reservation disappeared")
	}
	delete(manager.pending, agentID)
	reserved = false
	now := manager.now()
	record := &leaseRecord{
		snapshot: LeaseSnapshot{
			AgentID: agentID, TabID: tab.ID(), State: LeaseActive,
			AcquiredAt: now, LastActivity: now,
		},
		tab: tab,
	}
	manager.leases[agentID] = record
	manager.resetLeaseTimerLocked(agentID, record)
	snapshot := record.snapshot
	manager.mu.Unlock()

	go manager.watchTab(agentID, generation, tab)
	return snapshot, nil
}

func (manager *Manager) Touch(agentID string) error {
	if manager == nil {
		return errors.New("browser manager is unavailable")
	}
	agentID = strings.TrimSpace(agentID)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	record := manager.leases[agentID]
	if record == nil {
		return ErrLeaseNotFound
	}
	if record.snapshot.State != LeaseActive {
		return fmt.Errorf("browser tab lease is %s", record.snapshot.State)
	}
	record.snapshot.LastActivity = manager.now()
	manager.resetLeaseTimerLocked(agentID, record)
	return nil
}

func (manager *Manager) Lease(agentID string) (LeaseSnapshot, bool) {
	if manager == nil {
		return LeaseSnapshot{}, false
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	record := manager.leases[strings.TrimSpace(agentID)]
	if record == nil {
		return LeaseSnapshot{}, false
	}
	return record.snapshot, true
}

func (manager *Manager) Tab(agentID string) (BrowserTab, bool) {
	if manager == nil {
		return nil, false
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	record := manager.leases[strings.TrimSpace(agentID)]
	if record == nil || record.snapshot.State != LeaseActive || record.tab == nil {
		return nil, false
	}
	return record.tab, true
}

func (manager *Manager) Release(ctx context.Context, agentID string) error {
	if manager == nil {
		return errors.New("browser manager is unavailable")
	}
	agentID = strings.TrimSpace(agentID)
	manager.mu.Lock()
	record := manager.leases[agentID]
	if record == nil {
		manager.mu.Unlock()
		return ErrLeaseNotFound
	}
	delete(manager.leases, agentID)
	if record.timer != nil {
		record.timer.Stop()
	}
	tab := record.tab
	if manager.activeLeaseCountLocked() == 0 {
		manager.scheduleWarmShutdownLocked()
	}
	manager.mu.Unlock()
	if tab == nil {
		return nil
	}
	return manager.closeTab(ctx, tab)
}

func (manager *Manager) Minimize(ctx context.Context) error {
	if manager == nil {
		return errors.New("browser manager is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := manager.EnsureRunning(ctx); err != nil {
		return err
	}
	manager.mu.Lock()
	instance := manager.instance
	manager.mu.Unlock()
	if instance == nil || instance.client == nil {
		return errors.New("browser is not running")
	}
	minimizeCtx, cancel := context.WithTimeout(ctx, manager.minimizeTTL)
	defer cancel()
	return instance.client.Minimize(minimizeCtx)
}

func (manager *Manager) Snapshot() ManagerSnapshot {
	if manager == nil {
		return ManagerSnapshot{State: ManagerClosed}
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	state := ManagerStopped
	if manager.closed {
		state = ManagerClosed
	} else if manager.starting != nil {
		state = ManagerStarting
	} else if manager.instance != nil {
		state = ManagerRunning
	}
	snapshot := ManagerSnapshot{
		State: state, Running: manager.instance != nil,
		Generation: manager.generation, ActiveLeases: manager.activeLeaseCountLocked(),
		MaxTabs: manager.maxTabs,
	}
	if manager.capability.Profile != nil {
		profile := *manager.capability.Profile
		snapshot.Profile = &profile
	}
	if manager.instance != nil && manager.instance.process != nil {
		snapshot.PID = manager.instance.process.PID()
	}
	return snapshot
}

func (manager *Manager) Close(ctx context.Context) error {
	if manager == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return nil
	}
	manager.closed = true
	manager.stopWarmTimerLocked()
	var tabs []BrowserTab
	for id, record := range manager.leases {
		if record.timer != nil {
			record.timer.Stop()
		}
		if record.tab != nil {
			tabs = append(tabs, record.tab)
		}
		delete(manager.leases, id)
	}
	instance := manager.instance
	manager.instance = nil
	starting := manager.starting
	manager.mu.Unlock()

	for _, tab := range tabs {
		_ = manager.closeTab(ctx, tab)
	}
	var closeErr error
	if instance != nil {
		closeErr = manager.closeInstance(ctx, instance)
	}
	if starting != nil {
		select {
		case <-ctx.Done():
			if closeErr == nil {
				closeErr = ctx.Err()
			}
		case <-starting.done:
		}
	}
	return closeErr
}

func (manager *Manager) watchTab(agentID string, generation uint64, tab BrowserTab) {
	<-tab.Done()
	err := tab.Err()
	manager.mu.Lock()
	record := manager.leases[agentID]
	if record == nil || record.tab != tab || record.snapshot.State != LeaseActive ||
		manager.instance == nil || manager.instance.generation != generation {
		manager.mu.Unlock()
		return
	}
	if record.timer != nil {
		record.timer.Stop()
	}
	record.snapshot.State = LeaseFailed
	record.snapshot.Failure = boundedReason(tabFailureReason(err))
	record.snapshot.TabID = ""
	record.tab = nil
	if manager.activeLeaseCountLocked() == 0 {
		manager.scheduleWarmShutdownLocked()
	}
	manager.mu.Unlock()
}

func (manager *Manager) watchBrowser(instance *browserInstance) {
	var failure error
	select {
	case <-instance.client.Done():
		failure = instance.client.Err()
	case <-instance.process.Done():
		failure = instance.process.Err()
	}
	manager.handleBrowserDisconnect(instance, failure)
}

func (manager *Manager) handleBrowserDisconnect(instance *browserInstance, failure error) {
	manager.mu.Lock()
	if manager.closed || manager.instance != instance {
		manager.mu.Unlock()
		return
	}
	attempt := &startAttempt{done: make(chan struct{})}
	manager.starting = attempt
	manager.instance = nil
	manager.stopWarmTimerLocked()
	reason := boundedReason(browserFailureReason(failure))
	for _, record := range manager.leases {
		if record.snapshot.State != LeaseActive {
			continue
		}
		if record.timer != nil {
			record.timer.Stop()
		}
		record.snapshot.State = LeaseFailed
		record.snapshot.Failure = reason
		record.snapshot.TabID = ""
		record.tab = nil
	}
	manager.mu.Unlock()

	err := manager.closeInstance(context.Background(), instance)

	manager.mu.Lock()
	attempt.err = err
	if manager.starting == attempt {
		manager.starting = nil
	}
	close(attempt.done)
	manager.mu.Unlock()
}

func (manager *Manager) resetLeaseTimerLocked(agentID string, record *leaseRecord) {
	if record.timer != nil {
		record.timer.Stop()
	}
	if manager.agentIdleTTL <= 0 {
		return
	}
	record.timerGeneration++
	generation := record.timerGeneration
	tabID := record.snapshot.TabID
	record.timer = time.AfterFunc(manager.agentIdleTTL, func() {
		manager.expireLease(agentID, tabID, generation)
	})
}

func (manager *Manager) expireLease(agentID, tabID string, timerGeneration uint64) {
	manager.mu.Lock()
	record := manager.leases[agentID]
	if record == nil || record.snapshot.State != LeaseActive || record.snapshot.TabID != tabID ||
		record.timerGeneration != timerGeneration {
		manager.mu.Unlock()
		return
	}
	tab := record.tab
	record.tab = nil
	record.snapshot.State = LeaseExpired
	record.snapshot.Failure = boundedReason(fmt.Sprintf("browser tab lease expired after %s of inactivity", manager.agentIdleTTL))
	record.snapshot.TabID = ""
	if manager.activeLeaseCountLocked() == 0 {
		manager.scheduleWarmShutdownLocked()
	}
	manager.mu.Unlock()
	if tab != nil {
		_ = manager.closeTab(context.Background(), tab)
	}
}

func (manager *Manager) scheduleWarmShutdownLocked() {
	manager.stopWarmTimerLocked()
	if manager.browserWarmTTL <= 0 || manager.instance == nil || manager.closed || manager.inUseCountLocked() != 0 {
		return
	}
	generation := manager.instance.generation
	manager.warmTimer = time.AfterFunc(manager.browserWarmTTL, func() {
		manager.shutdownWarmBrowser(generation)
	})
}

func (manager *Manager) stopWarmTimerLocked() {
	if manager.warmTimer != nil {
		manager.warmTimer.Stop()
		manager.warmTimer = nil
	}
}

func (manager *Manager) shutdownWarmBrowser(generation uint64) {
	manager.mu.Lock()
	if manager.closed || manager.instance == nil || manager.instance.generation != generation ||
		manager.inUseCountLocked() != 0 {
		manager.mu.Unlock()
		return
	}
	instance := manager.instance
	attempt := &startAttempt{done: make(chan struct{})}
	manager.starting = attempt
	manager.instance = nil
	manager.warmTimer = nil
	manager.mu.Unlock()

	err := manager.closeInstance(context.Background(), instance)

	manager.mu.Lock()
	attempt.err = err
	if manager.starting == attempt {
		manager.starting = nil
	}
	close(attempt.done)
	manager.mu.Unlock()
}

func (manager *Manager) activeLeaseCountLocked() int {
	count := 0
	for _, record := range manager.leases {
		if record.snapshot.State == LeaseActive {
			count++
		}
	}
	return count
}

func (manager *Manager) inUseCountLocked() int {
	return manager.activeLeaseCountLocked() + len(manager.pending)
}

func (manager *Manager) releasePending(agentID string) {
	manager.mu.Lock()
	if _, exists := manager.pending[agentID]; exists {
		delete(manager.pending, agentID)
		if manager.inUseCountLocked() == 0 {
			manager.scheduleWarmShutdownLocked()
		}
	}
	manager.mu.Unlock()
}

func (manager *Manager) closeTab(ctx context.Context, tab BrowserTab) error {
	if tab == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	closeCtx, cancel := context.WithTimeout(ctx, manager.closeTTL)
	defer cancel()
	return tab.Close(closeCtx)
}

func (manager *Manager) closeInstance(ctx context.Context, instance *browserInstance) error {
	if instance == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	closeCtx, cancel := context.WithTimeout(ctx, manager.closeTTL)
	defer cancel()
	var result error
	if instance.client != nil {
		if err := instance.client.Close(closeCtx); err != nil && !errors.Is(err, context.Canceled) {
			result = err
		}
	}
	if instance.process != nil {
		if err := instance.process.Close(closeCtx); err != nil && result == nil {
			result = err
		}
	}
	if instance.lock != nil {
		if err := instance.lock.Release(); err != nil && result == nil {
			result = err
		}
	}
	return result
}

func tabFailureReason(err error) string {
	if err == nil {
		return "browser tab closed unexpectedly"
	}
	return "browser tab failed: " + err.Error()
}

func browserFailureReason(err error) string {
	if err == nil {
		return "browser disconnected"
	}
	return "browser disconnected: " + err.Error()
}
