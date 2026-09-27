package telegram

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/notification"
	"go.mewis.me/codemcp/internal/secretstore"
)

const (
	defaultPollTimeout     = 30 * time.Second
	defaultReconnectMin    = 500 * time.Millisecond
	defaultReconnectMax    = 30 * time.Second
	defaultReconnectJitter = 250 * time.Millisecond
	defaultStopTimeout     = time.Second
	maxUpdatesPerPoll      = 100
)

type Handler func(context.Context, Update)
type SetupHandler func(context.Context, Update) bool

type Health struct {
	Enabled                 bool      `json:"enabled"`
	TokenConfigured         bool      `json:"token_configured"`
	AuthorizationConfigured bool      `json:"authorization_configured"`
	Running                 bool      `json:"running"`
	PollingHealthy          bool      `json:"polling_healthy"`
	Reconnecting            bool      `json:"reconnecting"`
	ReconnectCount          uint64    `json:"reconnect_count"`
	NextOffset              int64     `json:"next_offset"`
	LastSuccess             time.Time `json:"last_success,omitempty"`
	LastError               string    `json:"last_error,omitempty"`
	SetupMode               bool      `json:"setup_mode"`
}

type Options struct {
	Root           string
	Factory        func(string) API
	PollTimeout    time.Duration
	ReconnectDelay func(int) time.Duration
	StopTimeout    time.Duration
}

type Runtime struct {
	root           string
	factory        func(string) API
	pollTimeout    time.Duration
	reconnectDelay func(int) time.Duration
	stopTimeout    time.Duration

	mu           sync.RWMutex
	config       config.TelegramConfig
	health       Health
	fingerprint  string
	api          API
	cancel       context.CancelFunc
	done         chan struct{}
	handler      Handler
	setupHandler SetupHandler
	setupMode    bool
}

func NewRuntime(options Options) *Runtime {
	factory := options.Factory
	if factory == nil {
		factory = newAPIClient
	}
	pollTimeout := options.PollTimeout
	if pollTimeout <= 0 {
		pollTimeout = defaultPollTimeout
	}
	reconnectDelay := options.ReconnectDelay
	if reconnectDelay == nil {
		reconnectDelay = func(attempt int) time.Duration {
			delay := defaultReconnectMin
			for i := 0; i < attempt && delay < defaultReconnectMax; i++ {
				delay *= 2
				if delay > defaultReconnectMax {
					delay = defaultReconnectMax
				}
			}
			jitter := time.Duration(time.Now().UnixNano() % int64(defaultReconnectJitter+1))
			if delay+jitter > defaultReconnectMax {
				return defaultReconnectMax
			}
			return delay + jitter
		}
	}
	stopTimeout := options.StopTimeout
	if stopTimeout <= 0 {
		stopTimeout = defaultStopTimeout
	}
	return &Runtime{root: options.Root, factory: factory, pollTimeout: pollTimeout, reconnectDelay: reconnectDelay, stopTimeout: stopTimeout}
}

func (runtime *Runtime) SetHandler(handler Handler) {
	if runtime == nil {
		return
	}
	runtime.mu.Lock()
	runtime.handler = handler
	runtime.mu.Unlock()
}

func (runtime *Runtime) SetSetupHandler(handler SetupHandler) {
	if runtime == nil {
		return
	}
	runtime.mu.Lock()
	runtime.setupHandler = handler
	runtime.mu.Unlock()
}

func (runtime *Runtime) Reconcile(ctx context.Context, cfg config.TelegramConfig) error {
	return runtime.reconcile(ctx, cfg, false)
}

func (runtime *Runtime) StartSetup(ctx context.Context, cfg config.TelegramConfig) error {
	if runtime == nil {
		return errors.New("telegram runtime is unavailable")
	}
	runtime.mu.RLock()
	hasSetupHandler := runtime.setupHandler != nil
	runtime.mu.RUnlock()
	if !hasSetupHandler {
		return errors.New("telegram setup handler is unavailable")
	}
	return runtime.reconcile(ctx, cfg, true)
}

func (runtime *Runtime) PromoteSetup(cfg config.TelegramConfig) error {
	if runtime == nil {
		return errors.New("telegram runtime is unavailable")
	}
	cfg = normalizeConfig(cfg)
	if !cfg.Enabled || !validAuthorization(cfg.AllowedUserIDs) {
		return errors.New("telegram authorization is incomplete")
	}
	token, err := loadToken(runtime.root)
	if err != nil {
		return err
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.cancel == nil || !runtime.setupMode {
		return errors.New("telegram setup runtime is not active")
	}
	runtime.config = cfg
	runtime.setupMode = false
	runtime.fingerprint = runtimeFingerprint(token, cfg, false)
	runtime.health.Enabled = true
	runtime.health.AuthorizationConfigured = true
	runtime.health.SetupMode = false
	return nil
}

func (runtime *Runtime) reconcile(ctx context.Context, cfg config.TelegramConfig, setupMode bool) error {
	if runtime == nil {
		return errors.New("telegram runtime is unavailable")
	}
	cfg = normalizeConfig(cfg)
	token, tokenErr := loadToken(runtime.root)
	tokenConfigured := tokenErr == nil && strings.TrimSpace(token) != ""
	authorizationConfigured := validAuthorization(cfg.AllowedUserIDs)

	if tokenErr != nil && !errors.Is(tokenErr, secretstore.ErrNotFound) {
		runtime.Stop()
		runtime.mu.Lock()
		runtime.config = cfg
		runtime.health = Health{Enabled: cfg.Enabled, TokenConfigured: false, AuthorizationConfigured: authorizationConfigured, SetupMode: setupMode, LastError: "telegram token is unavailable"}
		runtime.mu.Unlock()
		return tokenErr
	}
	if !tokenConfigured || (!setupMode && (!cfg.Enabled || !authorizationConfigured)) {
		runtime.Stop()
		runtime.mu.Lock()
		runtime.config = cfg
		runtime.setupMode = false
		runtime.health = Health{Enabled: cfg.Enabled, TokenConfigured: tokenConfigured, AuthorizationConfigured: authorizationConfigured}
		runtime.mu.Unlock()
		return nil
	}

	api := runtime.factory(token)
	validateCtx := ctx
	if validateCtx == nil {
		validateCtx = context.Background()
	}
	validateCtx, cancelValidate := context.WithTimeout(validateCtx, 10*time.Second)
	_, validateErr := api.GetMe(validateCtx)
	cancelValidate()
	if validateErr != nil {
		runtime.Stop()
		runtime.mu.Lock()
		runtime.config = cfg
		runtime.setupMode = false
		runtime.health = Health{Enabled: cfg.Enabled, TokenConfigured: true, AuthorizationConfigured: authorizationConfigured, LastError: "telegram bot token validation failed"}
		runtime.mu.Unlock()
		return errors.New("telegram bot token validation failed")
	}

	fingerprint := runtimeFingerprint(token, cfg, setupMode)
	runtime.mu.RLock()
	same := runtime.cancel != nil && runtime.fingerprint == fingerprint
	runtime.mu.RUnlock()
	if same {
		return nil
	}
	runtime.Stop()
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	runtime.mu.Lock()
	runtime.config = cfg
	runtime.setupMode = setupMode
	runtime.api = api
	runtime.cancel = cancel
	runtime.done = done
	runtime.fingerprint = fingerprint
	runtime.health = Health{Enabled: cfg.Enabled, TokenConfigured: true, AuthorizationConfigured: authorizationConfigured, Running: true, SetupMode: setupMode}
	runtime.mu.Unlock()
	go runtime.supervise(runCtx, done, api)
	return nil
}

func (runtime *Runtime) Stop() {
	if runtime == nil {
		return
	}
	runtime.mu.Lock()
	cancel := runtime.cancel
	done := runtime.done
	runtime.cancel = nil
	runtime.done = nil
	runtime.api = nil
	runtime.fingerprint = ""
	runtime.health.Running = false
	runtime.health.PollingHealthy = false
	runtime.health.Reconnecting = false
	runtime.health.SetupMode = false
	runtime.setupMode = false
	runtime.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if done != nil {
		timer := time.NewTimer(runtime.stopTimeout)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
		}
	}
}

func (runtime *Runtime) Health() Health {
	if runtime == nil {
		return Health{}
	}
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	return runtime.health
}

func (runtime *Runtime) Diagnostics() Health {
	return runtime.Health()
}

func (runtime *Runtime) Available() bool {
	health := runtime.Health()
	return health.Running && health.PollingHealthy && health.AuthorizationConfigured
}

func (runtime *Runtime) SendNotification(ctx context.Context, message notification.Message) error {
	if runtime == nil {
		return notification.ErrProviderUnavailable
	}
	runtime.mu.RLock()
	api := runtime.api
	users := append([]int64(nil), runtime.config.AllowedUserIDs...)
	available := runtime.health.Running && runtime.health.Enabled && runtime.health.AuthorizationConfigured && !runtime.health.SetupMode
	runtime.mu.RUnlock()
	if !available || api == nil {
		return notification.ErrProviderUnavailable
	}
	text := strings.TrimSpace(message.Title)
	if body := strings.TrimSpace(message.Body); body != "" {
		if text != "" {
			text += "\n\n"
		}
		text += body
	}
	if text == "" {
		text = string(message.Kind)
	}
	var result error
	for _, userID := range users {
		if err := api.SendMessage(ctx, userID, text); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (runtime *Runtime) SendSetupMessage(ctx context.Context, chatID int64, text string) error {
	if runtime == nil || chatID <= 0 {
		return errors.New("telegram setup transport is unavailable")
	}
	runtime.mu.RLock()
	api := runtime.api
	setupMode := runtime.setupMode && runtime.health.Running
	runtime.mu.RUnlock()
	if !setupMode || api == nil {
		return errors.New("telegram setup transport is unavailable")
	}
	return api.SendMessage(ctx, chatID, strings.TrimSpace(text))
}

func (runtime *Runtime) supervise(ctx context.Context, done chan struct{}, api API) {
	defer close(done)
	attempt := 0
	for {
		updates, err := api.GetUpdates(ctx, runtime.offset(), maxUpdatesPerPoll, runtime.pollTimeout)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			runtime.markPollFailure()
			delay := runtime.reconnectDelay(attempt)
			attempt++
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				continue
			}
		}
		attempt = 0
		runtime.markPollSuccess()
		runtime.dispatchBatch(ctx, updates)
	}
}

func (runtime *Runtime) dispatchBatch(ctx context.Context, updates []Update) {
	if len(updates) > maxUpdatesPerPoll {
		updates = updates[:maxUpdatesPerPoll]
	}
	slices.SortFunc(updates, func(left, right Update) int {
		if left.UpdateID < right.UpdateID {
			return -1
		}
		if left.UpdateID > right.UpdateID {
			return 1
		}
		return 0
	})
	for _, update := range updates {
		runtime.mu.Lock()
		if update.UpdateID < runtime.health.NextOffset {
			runtime.mu.Unlock()
			continue
		}
		runtime.health.NextOffset = update.UpdateID + 1
		cfg := runtime.config
		handler := runtime.handler
		setupHandler := runtime.setupHandler
		setupMode := runtime.setupMode
		runtime.mu.Unlock()
		if setupMode && setupHandler != nil && setupHandler(ctx, update) {
			continue
		}
		if handler == nil || !authorizedUpdate(cfg, update) {
			continue
		}
		handler(ctx, update)
	}
}

func (runtime *Runtime) offset() int64 {
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	return runtime.health.NextOffset
}

func (runtime *Runtime) markPollFailure() {
	runtime.mu.Lock()
	runtime.health.PollingHealthy = false
	runtime.health.Reconnecting = true
	runtime.health.ReconnectCount++
	runtime.health.LastError = "telegram polling failed"
	runtime.mu.Unlock()
}

func (runtime *Runtime) markPollSuccess() {
	runtime.mu.Lock()
	runtime.health.PollingHealthy = true
	runtime.health.Reconnecting = false
	runtime.health.LastSuccess = time.Now().UTC()
	runtime.health.LastError = ""
	runtime.mu.Unlock()
}

func authorizedUpdate(cfg config.TelegramConfig, update Update) bool {
	allowed := func(id int64) bool { return slices.Contains(cfg.AllowedUserIDs, id) }
	if update.Message != nil {
		message := update.Message
		return message.From != nil && message.Chat.Type == "private" && message.Chat.ID == message.From.ID && allowed(message.From.ID)
	}
	if update.CallbackQuery != nil {
		callback := update.CallbackQuery
		return callback.Message != nil && callback.Message.Chat.Type == "private" && callback.Message.Chat.ID == callback.From.ID && allowed(callback.From.ID)
	}
	return false
}

func normalizeConfig(cfg config.TelegramConfig) config.TelegramConfig {
	ids := append([]int64(nil), cfg.AllowedUserIDs...)
	slices.Sort(ids)
	cfg.AllowedUserIDs = slices.Compact(ids)
	return cfg
}

func validAuthorization(ids []int64) bool {
	if len(ids) == 0 {
		return false
	}
	for _, id := range ids {
		if id <= 0 {
			return false
		}
	}
	return true
}

func runtimeFingerprint(token string, cfg config.TelegramConfig, setupMode bool) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s|%t|%v|%t", token, cfg.Enabled, cfg.AllowedUserIDs, setupMode)))
	return hex.EncodeToString(hash[:])
}
