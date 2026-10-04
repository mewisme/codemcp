package browser

import (
	"context"
	"errors"
	"time"
)

var (
	ErrManagerClosed = errors.New("browser manager is closed")
	ErrCapacity      = errors.New("browser tab capacity reached")
	ErrLeaseExists   = errors.New("browser tab lease already exists")
	ErrLeaseNotFound = errors.New("browser tab lease not found")
	ErrProfileBusy   = errors.New("browser profile is already owned by another process")
)

type ManagerState string

const (
	ManagerStopped  ManagerState = "stopped"
	ManagerStarting ManagerState = "starting"
	ManagerRunning  ManagerState = "running"
	ManagerClosed   ManagerState = "closed"
)

type LeaseState string

const (
	LeaseActive  LeaseState = "active"
	LeaseFailed  LeaseState = "failed"
	LeaseExpired LeaseState = "expired"
)

type LaunchRequest struct {
	Candidate Candidate
	Profile   ProfileRef
	Visible   bool
	Minimized bool
}

type BrowserEndpoint struct {
	URL string
}

type BrowserProcess interface {
	PID() int
	Done() <-chan struct{}
	Err() error
	Close(context.Context) error
}

type BrowserLauncher interface {
	Launch(context.Context, LaunchRequest) (BrowserProcess, BrowserEndpoint, error)
}

type InteractiveLaunchRequest struct {
	Candidate Candidate
	Profile   ProfileRef
	URL       string
}

type InteractiveBrowserLauncher interface {
	Launch(context.Context, InteractiveLaunchRequest) (BrowserProcess, error)
}

type InteractiveBrowserOptions struct {
	Capability Capability
	URL        string
	Launcher   InteractiveBrowserLauncher
	CloseTTL   time.Duration
}

type BrowserTab interface {
	ID() string
	Navigate(context.Context, string) error
	Evaluate(context.Context, string, any) error
	Done() <-chan struct{}
	Err() error
	Close(context.Context) error
}

type BrowserClient interface {
	NewTab(context.Context, string) (BrowserTab, error)
	Minimize(context.Context) error
	Done() <-chan struct{}
	Err() error
	Close(context.Context) error
}

type BrowserConnector interface {
	Connect(context.Context, BrowserEndpoint) (BrowserClient, error)
}

type ManagerOptions struct {
	Capability Capability

	MaxTabs        int
	Minimized      bool
	AgentIdleTTL   time.Duration
	BrowserWarmTTL time.Duration
	LaunchTTL      time.Duration
	MinimizeTTL    time.Duration
	CloseTTL       time.Duration

	Launcher  BrowserLauncher
	Connector BrowserConnector
	Now       func() time.Time
}

type LeaseSnapshot struct {
	AgentID      string     `json:"agent_id"`
	TabID        string     `json:"tab_id,omitempty"`
	State        LeaseState `json:"state"`
	AcquiredAt   time.Time  `json:"acquired_at"`
	LastActivity time.Time  `json:"last_activity"`
	Failure      string     `json:"failure,omitempty"`
}

type ManagerSnapshot struct {
	State        ManagerState `json:"state"`
	Running      bool         `json:"running"`
	PID          int          `json:"pid,omitempty"`
	Generation   uint64       `json:"generation"`
	ActiveLeases int          `json:"active_leases"`
	MaxTabs      int          `json:"max_tabs"`
	Profile      *ProfileRef  `json:"profile,omitempty"`
}
