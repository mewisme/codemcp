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
	defaultPollTimeout       = 30 * time.Second
	defaultReconnectMin      = 500 * time.Millisecond
	defaultReconnectMax      = 30 * time.Second
	defaultReconnectJitter   = 250 * time.Millisecond
	defaultStopTimeout       = time.Second
	defaultNavigationTimeout = 5 * time.Second
	maxUpdatesPerPoll        = 100
)

type Handler func(context.Context, Update)
type SetupHandler func(context.Context, Update) bool

type Health struct {
	Enabled                 bool      `json:"enabled"`
	TokenConfigured         bool      `json:"token_configured"`
	AuthorizationConfigured bool      `json:"authorization_configured"`
	TopicsConfigured        bool      `json:"topics_configured"`
	TopicsSupported         bool      `json:"topics_supported"`
	TopicsEffective         bool      `json:"topics_effective"`
	TopicCount              int       `json:"topic_count"`
	TopicLastError          string    `json:"topic_last_error,omitempty"`
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
	generation   uint64
	topics       *topicStore
}

func NewRuntime(options Options) *Runtime {
	pollTimeout := options.PollTimeout
	if pollTimeout <= 0 {
		pollTimeout = defaultPollTimeout
	}
	factory := options.Factory
	if factory == nil {
		factory = func(token string) API { return newAPIClientWithPollTimeout(token, pollTimeout) }
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
	if runtime.cancel == nil || !runtime.setupMode {
		runtime.mu.Unlock()
		return errors.New("telegram setup runtime is not active")
	}
	api := runtime.api
	topicsSupported := runtime.health.TopicsSupported
	runtime.config = cfg
	runtime.setupMode = false
	runtime.fingerprint = runtimeFingerprint(token, cfg, false)
	runtime.health.Enabled = true
	runtime.health.AuthorizationConfigured = true
	runtime.health.SetupMode = false
	runtime.mu.Unlock()
	runtime.reconcileNavigationBounded(context.Background(), api, cfg)
	runtime.reconcileTopicsBounded(context.Background(), api, cfg, topicsSupported)
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
	botUser, validateErr := api.GetMe(validateCtx)
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
	runtime.generation++
	runtime.api = api
	runtime.cancel = cancel
	runtime.done = done
	runtime.fingerprint = fingerprint
	runtime.health = Health{
		Enabled: cfg.Enabled, TokenConfigured: true, AuthorizationConfigured: authorizationConfigured,
		TopicsConfigured: cfg.TopicsEnabled, TopicsSupported: botUser.HasTopicsEnabled,
		TopicsEffective: cfg.TopicsEnabled && botUser.HasTopicsEnabled,
		Running:         true, SetupMode: setupMode,
	}
	runtime.mu.Unlock()
	if !setupMode {
		runtime.reconcileNavigationBounded(runCtx, api, cfg)
		runtime.reconcileTopicsBounded(runCtx, api, cfg, botUser.HasTopicsEnabled)
	}
	go runtime.supervise(runCtx, done, api)
	return nil
}

func (runtime *Runtime) Generation() uint64 {
	if runtime == nil {
		return 0
	}
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	return runtime.generation
}

func (runtime *Runtime) Authorizes(update Update) bool {
	if runtime == nil {
		return false
	}
	runtime.mu.RLock()
	cfg := runtime.config
	setupMode := runtime.setupMode
	runtime.mu.RUnlock()
	return !setupMode && authorizedUpdate(cfg, update)
}

func (runtime *Runtime) SendScreen(ctx context.Context, chatID int64, screen Screen) error {
	if runtime == nil || chatID <= 0 {
		return errors.New("telegram runtime is unavailable")
	}
	if err := validateKeyboard(screen.Keyboard); err != nil {
		return err
	}
	runtime.mu.RLock()
	api := runtime.api
	available := runtime.health.Running && !runtime.health.SetupMode
	runtime.mu.RUnlock()
	if !available || api == nil {
		return errors.New("telegram runtime is unavailable")
	}
	rich, ok := api.(ScreenAPI)
	if !ok {
		return errors.New("telegram rich screen API is unavailable")
	}
	return rich.SendScreen(ctx, chatID, screen)
}

func (runtime *Runtime) SendRichMessage(ctx context.Context, chatID int64, screen Screen, options RichMessageOptions) (int64, error) {
	if runtime == nil || chatID <= 0 {
		return 0, errors.New("telegram runtime is unavailable")
	}
	if err := validateKeyboard(screen.Keyboard); err != nil {
		return 0, err
	}
	runtime.mu.RLock()
	api := runtime.api
	available := runtime.health.Running && !runtime.health.SetupMode
	runtime.mu.RUnlock()
	if !available || api == nil {
		return 0, errors.New("telegram runtime is unavailable")
	}
	rich, ok := api.(RichMessageAPI)
	if !ok {
		return 0, errors.New("telegram rich message API is unavailable")
	}
	return rich.SendRichMessage(ctx, chatID, screen, options)
}

func (runtime *Runtime) SendChatAction(ctx context.Context, chatID int64, action string) error {
	if runtime == nil || chatID <= 0 || strings.TrimSpace(action) == "" {
		return errors.New("telegram runtime is unavailable")
	}
	runtime.mu.RLock()
	api := runtime.api
	available := runtime.health.Running && !runtime.health.SetupMode
	runtime.mu.RUnlock()
	if !available || api == nil {
		return errors.New("telegram runtime is unavailable")
	}
	rich, ok := api.(RichMessageAPI)
	if !ok {
		return errors.New("telegram chat action API is unavailable")
	}
	return rich.SendChatAction(ctx, chatID, action)
}

func (runtime *Runtime) SendChatActionToTopic(ctx context.Context, chatID int64, role TopicRole, action string) error {
	if runtime == nil || chatID <= 0 || strings.TrimSpace(action) == "" {
		return errors.New("telegram runtime is unavailable")
	}
	runtime.mu.RLock()
	api, topics := runtime.api, runtime.topics
	effective := runtime.health.TopicsEffective
	runtime.mu.RUnlock()
	if effective && topics != nil {
		if threadID := topics.get(chatID, role); threadID > 0 {
			if topicAPI, ok := api.(TopicAPI); ok {
				err := topicAPI.SendChatActionThread(ctx, chatID, threadID, action)
				if err == nil {
					return nil
				}
				if kind := transportErrorKind(err); kind != transportErrorBadRequest && kind != transportErrorNotFound {
					return err
				}
				topics.delete(chatID, role)
				runtime.setTopicError("telegram managed topic is missing; using General chat")
			}
		}
	}
	return runtime.SendChatAction(ctx, chatID, action)
}

func (runtime *Runtime) SendDocument(ctx context.Context, chatID int64, upload DocumentUpload) error {
	if runtime == nil || chatID <= 0 {
		return errors.New("telegram runtime is unavailable")
	}
	if err := ValidateDocumentUpload(upload); err != nil {
		return err
	}
	runtime.mu.RLock()
	api := runtime.api
	available := runtime.health.Running && !runtime.health.SetupMode
	runtime.mu.RUnlock()
	if !available || api == nil {
		return errors.New("telegram runtime is unavailable")
	}
	documents, ok := api.(DocumentAPI)
	if !ok {
		return errors.New("telegram document API is unavailable")
	}
	return documents.SendDocument(ctx, chatID, upload)
}

func (runtime *Runtime) SendDocumentToTopic(ctx context.Context, chatID int64, role TopicRole, upload DocumentUpload) error {
	if runtime == nil || chatID <= 0 {
		return errors.New("telegram runtime is unavailable")
	}
	if err := ValidateDocumentUpload(upload); err != nil {
		return err
	}
	runtime.mu.RLock()
	api, topics := runtime.api, runtime.topics
	effective := runtime.health.TopicsEffective
	runtime.mu.RUnlock()
	if effective && topics != nil {
		if threadID := topics.get(chatID, role); threadID > 0 {
			if topicAPI, ok := api.(TopicAPI); ok {
				err := topicAPI.SendDocumentThread(ctx, chatID, threadID, upload)
				if err == nil {
					return nil
				}
				if kind := transportErrorKind(err); kind != transportErrorBadRequest && kind != transportErrorNotFound {
					return err
				}
				topics.delete(chatID, role)
				runtime.setTopicError("telegram managed topic is missing; using General chat")
			}
		}
	}
	return runtime.SendDocument(ctx, chatID, upload)
}

func (runtime *Runtime) SendRichMessageToTopic(ctx context.Context, chatID int64, role TopicRole, screen Screen, options RichMessageOptions) (int64, error) {
	if runtime == nil || chatID <= 0 {
		return 0, errors.New("telegram runtime is unavailable")
	}
	if err := validateKeyboard(screen.Keyboard); err != nil {
		return 0, err
	}
	runtime.mu.RLock()
	api, topics := runtime.api, runtime.topics
	effective := runtime.health.TopicsEffective
	runtime.mu.RUnlock()
	if effective && topics != nil {
		if threadID := topics.get(chatID, role); threadID > 0 {
			if topicAPI, ok := api.(TopicAPI); ok {
				messageID, err := topicAPI.SendRichMessageThread(ctx, chatID, threadID, screen, options)
				if err == nil {
					return messageID, nil
				}
				if kind := transportErrorKind(err); kind != transportErrorBadRequest && kind != transportErrorNotFound {
					return 0, err
				}
				topics.delete(chatID, role)
				runtime.setTopicError("telegram managed topic is missing; using General chat")
			}
		}
	}
	return runtime.SendRichMessage(ctx, chatID, screen, options)
}

func (runtime *Runtime) DownloadDocument(ctx context.Context, document Document) ([]byte, error) {
	if runtime == nil {
		return nil, errors.New("telegram runtime is unavailable")
	}
	if err := ValidateDocument(document); err != nil {
		return nil, err
	}
	runtime.mu.RLock()
	api := runtime.api
	available := runtime.health.Running && !runtime.health.SetupMode
	runtime.mu.RUnlock()
	if !available || api == nil {
		return nil, errors.New("telegram runtime is unavailable")
	}
	documents, ok := api.(DocumentAPI)
	if !ok {
		return nil, errors.New("telegram document API is unavailable")
	}
	return documents.DownloadDocument(ctx, document)
}

func (runtime *Runtime) EditScreen(ctx context.Context, chatID, messageID int64, screen Screen) error {
	if runtime == nil || chatID <= 0 || messageID <= 0 {
		return errors.New("telegram runtime is unavailable")
	}
	if err := validateKeyboard(screen.Keyboard); err != nil {
		return err
	}
	runtime.mu.RLock()
	api := runtime.api
	available := runtime.health.Running && !runtime.health.SetupMode
	runtime.mu.RUnlock()
	if !available || api == nil {
		return errors.New("telegram runtime is unavailable")
	}
	rich, ok := api.(ScreenAPI)
	if !ok {
		return errors.New("telegram rich screen API is unavailable")
	}
	return rich.EditScreen(ctx, chatID, messageID, screen)
}

func (runtime *Runtime) AnswerCallback(ctx context.Context, callbackID, text string, alert bool) error {
	if runtime == nil || strings.TrimSpace(callbackID) == "" {
		return nil
	}
	runtime.mu.RLock()
	api := runtime.api
	runtime.mu.RUnlock()
	rich, ok := api.(ScreenAPI)
	if !ok {
		return errors.New("telegram rich screen API is unavailable")
	}
	return rich.AnswerCallback(ctx, callbackID, text, alert)
}

func (runtime *Runtime) DeleteMessage(ctx context.Context, chatID, messageID int64) error {
	if runtime == nil || chatID <= 0 || messageID <= 0 {
		return errors.New("telegram runtime is unavailable")
	}
	runtime.mu.RLock()
	api := runtime.api
	available := runtime.health.Running && !runtime.health.SetupMode
	runtime.mu.RUnlock()
	if !available || api == nil {
		return errors.New("telegram runtime is unavailable")
	}
	dismiss, ok := api.(MessageDismissAPI)
	if !ok {
		return errors.New("telegram message dismissal is unavailable")
	}
	return dismiss.DeleteMessage(ctx, chatID, messageID)
}

func (runtime *Runtime) SetChatMenuButton(ctx context.Context, userID int64, button MenuButton) error {
	navigation, err := runtime.authorizedNavigationAPI(userID)
	if err != nil {
		return err
	}
	if button.Type != MenuButtonCommands && button.Type != MenuButtonDefault {
		return errors.New("unsupported telegram chat menu button")
	}
	return navigation.SetChatMenuButton(ctx, userID, button)
}

func (runtime *Runtime) GetChatMenuButton(ctx context.Context, userID int64) (MenuButton, error) {
	navigation, err := runtime.authorizedNavigationAPI(userID)
	if err != nil {
		return MenuButton{}, err
	}
	return navigation.GetChatMenuButton(ctx, userID)
}

func (runtime *Runtime) authorizedNavigationAPI(userID int64) (NavigationAPI, error) {
	if runtime == nil || userID <= 0 {
		return nil, errors.New("telegram authorized private user is required")
	}
	runtime.mu.RLock()
	api := runtime.api
	cfg := runtime.config
	available := runtime.health.Running && runtime.health.AuthorizationConfigured && !runtime.health.SetupMode
	runtime.mu.RUnlock()
	if !available || api == nil || !containsUserID(cfg.AllowedUserIDs, userID) {
		return nil, errors.New("telegram authorized private user is required")
	}
	navigation, ok := api.(NavigationAPI)
	if !ok {
		return nil, errors.New("telegram bot menu API is unavailable")
	}
	return navigation, nil
}

func (runtime *Runtime) reconcileNavigationBounded(ctx context.Context, api API, cfg config.TelegramConfig) {
	if api == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, defaultNavigationTimeout)
	defer cancel()
	if err := reconcileNavigation(ctx, api, cfg); err != nil {
		runtime.mu.Lock()
		if runtime.health.Running && !runtime.health.SetupMode {
			runtime.health.LastError = "telegram command menu reconciliation failed"
		}
		runtime.mu.Unlock()
	}
}

func reconcileNavigation(ctx context.Context, api API, cfg config.TelegramConfig) error {
	navigation, ok := api.(NavigationAPI)
	if !ok {
		return nil
	}
	if err := navigation.SetCommands(ctx, Commands()); err != nil {
		return err
	}
	for _, userID := range cfg.AllowedUserIDs {
		if userID <= 0 {
			continue
		}
		if err := navigation.SetChatMenuButton(ctx, userID, MenuButton{Type: MenuButtonCommands}); err != nil {
			return err
		}
	}
	return nil
}

func containsUserID(values []int64, userID int64) bool {
	for _, value := range values {
		if value == userID {
			return true
		}
	}
	return false
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
	topicsEffective := runtime.health.TopicsEffective
	topics := runtime.topics
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
	role := topicRoleForNotification(message.Kind)
	for _, userID := range users {
		if topicsEffective && topics != nil {
			threadID := topics.get(userID, role)
			if threadID > 0 {
				if topicAPI, ok := api.(TopicAPI); ok {
					err := topicAPI.SendMessageThread(ctx, userID, threadID, text)
					if err == nil {
						continue
					}
					if kind := transportErrorKind(err); kind == transportErrorBadRequest || kind == transportErrorNotFound {
						topics.delete(userID, role)
						runtime.setTopicError("telegram managed topic is missing; using General chat")
					} else {
						result = errors.Join(result, err)
						continue
					}
				}
			}
		}
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
	if stream, ok := api.(streamingAPI); ok {
		runtime.superviseStream(ctx, stream)
		return
	}
	poller, ok := api.(pollingAPI)
	if !ok {
		runtime.markPollFailure(errors.New("telegram transport does not support long polling"))
		return
	}
	attempt := 0
	for {
		updates, err := poller.GetUpdates(ctx, runtime.offset(), maxUpdatesPerPoll, runtime.pollTimeout)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			runtime.markPollFailure(err)
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

func (runtime *Runtime) superviseStream(ctx context.Context, stream streamingAPI) {
	attempt := 0
	for {
		if ctx.Err() != nil {
			return
		}
		err := stream.StartUpdates(ctx, runtime.offset(), func(handlerCtx context.Context, update Update) {
			runtime.dispatchBatch(handlerCtx, []Update{update})
		}, func(event pollEvent) {
			if event.Success {
				runtime.markPollSuccess()
				return
			}
			if event.Err != nil {
				runtime.markPollFailure(event.Err)
			}
		})
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			err = errors.New("telegram long polling stopped")
		}
		runtime.markPollFailure(err)
		delay := runtime.reconnectDelay(attempt)
		attempt++
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
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
		if setupMode {
			if setupHandler != nil {
				setupHandler(ctx, update)
			}
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

func (runtime *Runtime) markPollFailure(err error) {
	runtime.mu.Lock()
	runtime.health.PollingHealthy = false
	runtime.health.Reconnecting = true
	runtime.health.ReconnectCount++
	switch transportErrorKind(err) {
	case transportErrorRateLimited:
		runtime.health.LastError = "telegram polling rate limited"
	case transportErrorForbidden:
		runtime.health.LastError = "telegram polling forbidden"
	case transportErrorBadRequest:
		runtime.health.LastError = "telegram polling bad request"
	case transportErrorUnauthorized:
		runtime.health.LastError = "telegram polling unauthorized"
	case transportErrorConflict:
		runtime.health.LastError = "telegram polling conflict"
	default:
		runtime.health.LastError = "telegram polling failed"
	}
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
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s|%t|%v|%t|%t", token, cfg.Enabled, cfg.AllowedUserIDs, cfg.TopicsEnabled, setupMode)))
	return hex.EncodeToString(hash[:])
}
