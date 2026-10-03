package browser

import "time"

type State string

const (
	StateDisabled    State = "disabled"
	StateUnavailable State = "unavailable"
	StateAvailable   State = "available"
)

type Family string

const (
	FamilyChrome   Family = "chrome"
	FamilyChromium Family = "chromium"
	FamilyEdge     Family = "edge"
)

type Transport string

const (
	TransportNative  Transport = "native"
	TransportWSLHost Transport = "wsl-host"
)

type Source string

const (
	SourceConfigured Source = "configured"
	SourcePath       Source = "path"
	SourceStandard   Source = "standard"
)

type Candidate struct {
	Family           Family    `json:"family"`
	Executable       string    `json:"executable"`
	LocalExecutable  string    `json:"local_executable,omitempty"`
	HostPlatform     string    `json:"host_platform"`
	Transport        Transport `json:"transport"`
	Source           Source    `json:"source"`
	LocalAppData     string    `json:"-"`
	HostLocalAppData string    `json:"-"`
}

type ProbeResult struct {
	Usable    bool   `json:"usable"`
	Graphical bool   `json:"graphical"`
	Family    Family `json:"family,omitempty"`
	Version   string `json:"version,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type ProfileRef struct {
	HostPlatform string    `json:"host_platform"`
	Transport    Transport `json:"transport"`
	Path         string    `json:"path"`
	LocalPath    string    `json:"local_path,omitempty"`
	LockPath     string    `json:"-"`
}

type Capability struct {
	State               State       `json:"state"`
	Enabled             bool        `json:"enabled"`
	Available           bool        `json:"available"`
	Usable              bool        `json:"usable"`
	Family              Family      `json:"family,omitempty"`
	Executable          string      `json:"executable,omitempty"`
	Version             string      `json:"version,omitempty"`
	HostPlatform        string      `json:"host_platform,omitempty"`
	Transport           Transport   `json:"transport,omitempty"`
	Graphical           bool        `json:"graphical"`
	ProfileHostPlatform string      `json:"profile_host_platform,omitempty"`
	Profile             *ProfileRef `json:"profile,omitempty"`
	Reason              string      `json:"reason,omitempty"`
}

const (
	VersionProbeTimeout = 2 * time.Second
	LaunchProbeTimeout  = 5 * time.Second
	ReasonLimit         = 512
)
