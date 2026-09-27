package product

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"time"

	"go.mewis.me/codemcp/internal/version"
)

type Usage struct {
	Interface    Interface
	Command      string
	Feature      string
	ErrorCode    ErrorCode
	Duration     time.Duration
	Success      bool
	OmitDuration bool
	OmitSuccess  bool
}

type Recorder struct {
	mu       sync.Mutex
	enabled  bool
	endpoint string
	identity *IdentityStore
	client   *Client
	base     ClientFields
}

type RecorderOptions struct {
	Enabled  bool
	Endpoint string
	Identity *IdentityStore
	Client   *Client
	Version  string
	OS       string
	Arch     string
}

func NewRecorder(options RecorderOptions) (*Recorder, error) {
	endpoint := strings.TrimSpace(options.Endpoint)
	if _, err := ParseEndpoint(endpoint); err != nil {
		return nil, err
	}
	identity := options.Identity
	if identity == nil {
		identity = NewIdentityStore()
	}
	client := options.Client
	if client == nil {
		var err error
		client, err = NewClient(ClientOptions{Endpoint: endpoint, Enabled: options.Enabled})
		if err != nil {
			return nil, err
		}
	}
	versionValue := strings.TrimSpace(options.Version)
	if versionValue == "" {
		versionValue = version.Version
	}
	osValue := strings.TrimSpace(options.OS)
	if osValue == "" {
		osValue = runtime.GOOS
	}
	archValue := strings.TrimSpace(options.Arch)
	if archValue == "" {
		archValue = runtime.GOARCH
	}
	return &Recorder{
		enabled: options.Enabled, endpoint: endpoint, identity: identity, client: client,
		base: ClientFields{Version: versionValue, OS: osValue, Arch: archValue},
	}, nil
}

func (recorder *Recorder) SetEnabled(enabled bool) {
	if recorder == nil {
		return
	}
	recorder.mu.Lock()
	recorder.enabled = enabled
	client := recorder.client
	recorder.mu.Unlock()
	if client != nil {
		client.SetEnabled(enabled)
	}
}

func (recorder *Recorder) Record(_ context.Context, name EventName, usage Usage) bool {
	if recorder == nil {
		return false
	}
	recorder.mu.Lock()
	enabled := recorder.enabled
	endpoint := recorder.endpoint
	identity := recorder.identity
	client := recorder.client
	base := recorder.base
	recorder.mu.Unlock()
	if !enabled || endpoint == "" || identity == nil || client == nil {
		return false
	}
	anonymousID, _, err := identity.Ensure(true, endpoint)
	if err != nil {
		return false
	}
	base.AnonymousID = anonymousID
	var durationMS *int64
	if !usage.OmitDuration {
		value := usage.Duration.Milliseconds()
		if value < 0 {
			value = 0
		}
		durationMS = &value
	}
	var success *bool
	if !usage.OmitSuccess {
		value := usage.Success
		success = &value
	}
	event, err := NewEvent(name, base, EventFields{
		Interface:  usage.Interface,
		Command:    strings.TrimSpace(usage.Command),
		Feature:    strings.TrimSpace(usage.Feature),
		ErrorCode:  usage.ErrorCode,
		DurationMS: durationMS,
		Success:    success,
	})
	if err != nil {
		return false
	}
	return client.Enqueue(event)
}

func (recorder *Recorder) Flush(ctx context.Context) {
	if recorder == nil || recorder.client == nil {
		return
	}
	recorder.client.Flush(ctx)
}

func (recorder *Recorder) Close(ctx context.Context) {
	if recorder == nil || recorder.client == nil {
		return
	}
	recorder.client.Close(ctx)
}
