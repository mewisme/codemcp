package telegram

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/logger"
)

const (
	MiniAppDisabled MiniAppState = "disabled"
	MiniAppStarting MiniAppState = "starting"
	MiniAppReady    MiniAppState = "ready"
	MiniAppDegraded MiniAppState = "degraded"
	MiniAppStopped  MiniAppState = "stopped"

	miniAppAuthMaxAge       = 5 * time.Minute
	miniAppSessionTTL       = 10 * time.Minute
	miniAppRestartDelay     = time.Second
	miniAppStopTimeout      = 2 * time.Second
	miniAppMaxInitDataBytes = 16 << 10
	miniAppMaxTail          = 500
	miniAppCookieName       = "__Host-cm_tg_logs"
)

type MiniAppState string

type LogsMiniAppHealth struct {
	Enabled             bool         `json:"enabled"`
	State               MiniAppState `json:"state"`
	DependencyAvailable bool         `json:"dependency_available"`
	Listener            string       `json:"listener,omitempty"`
	PublicURL           string       `json:"public_url,omitempty"`
	Generation          uint64       `json:"generation"`
	LastError           string       `json:"last_error,omitempty"`
}

type QuickTunnelEvent struct {
	State  string `json:"state"`
	Origin string `json:"origin,omitempty"`
	URL    string `json:"url,omitempty"`
	Error  string `json:"error,omitempty"`
}

type QuickTunnelProcess interface {
	Events() <-chan QuickTunnelEvent
	Done() <-chan error
	Stop()
}

type QuickTunnelLauncher interface {
	Start(context.Context, string) (QuickTunnelProcess, error)
}

type externalQuickTunnelLauncher struct {
	resolveExecutable func() (string, error)
}

type externalQuickTunnelProcess struct {
	cmd      *exec.Cmd
	events   chan QuickTunnelEvent
	done     chan error
	finished chan struct{}
	once     sync.Once
}

func (launcher externalQuickTunnelLauncher) Start(ctx context.Context, origin string) (QuickTunnelProcess, error) {
	if launcher.resolveExecutable == nil {
		return nil, errors.New("cf-tunnel executable resolver is unavailable")
	}
	path, err := launcher.resolveExecutable()
	if err != nil {
		return nil, fmt.Errorf("resolve cf-tunnel executable: %w", err)
	}
	cmd := exec.Command(path, "--origin", origin)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	process := &externalQuickTunnelProcess{cmd: cmd, events: make(chan QuickTunnelEvent, 16), done: make(chan error, 1), finished: make(chan struct{})}
	go process.scan(stdout)
	go io.Copy(io.Discard, stderr)
	go func() {
		err := cmd.Wait()
		process.done <- err
		close(process.done)
		close(process.finished)
	}()
	go func() {
		select {
		case <-ctx.Done():
			process.Stop()
		case <-process.finished:
		}
	}()
	return process, nil
}

func (process *externalQuickTunnelProcess) scan(reader io.Reader) {
	defer close(process.events)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 1024), 64<<10)
	for scanner.Scan() {
		var event QuickTunnelEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		select {
		case process.events <- event:
		default:
		}
	}
}

func (process *externalQuickTunnelProcess) Events() <-chan QuickTunnelEvent { return process.events }
func (process *externalQuickTunnelProcess) Done() <-chan error              { return process.done }
func (process *externalQuickTunnelProcess) Stop() {
	if process == nil || process.cmd == nil || process.cmd.Process == nil {
		return
	}
	process.once.Do(func() {
		_ = process.cmd.Process.Signal(os.Interrupt)
		go func() {
			timer := time.NewTimer(miniAppStopTimeout)
			defer timer.Stop()
			select {
			case <-process.finished:
			case <-timer.C:
				_ = process.cmd.Process.Kill()
			}
		}()
	})
}

type miniAppSession struct {
	UserID     int64
	Generation uint64
	ExpiresAt  time.Time
}

type LogsMiniAppRuntime struct {
	launcher QuickTunnelLauncher
	now      func() time.Time

	mu          sync.RWMutex
	config      config.TelegramConfig
	botToken    string
	health      LogsMiniAppHealth
	fingerprint string
	cancel      context.CancelFunc
	done        chan struct{}
	sessions    map[string]miniAppSession
}

func (ui *Interface) handleLogs(ctx context.Context, update Update) {
	owner, ok := ownerFromUpdate(ui.runtime, update)
	if !ok {
		return
	}
	screen, err := ui.logsMiniAppScreen(owner)
	if err != nil {
		screen = ErrorScreen(err)
	}
	_, _ = ui.runtime.SendRichMessageToTopic(ctx, owner.ChatID, TopicLogs, screen, RichMessageOptions{})
}

func (ui *Interface) logsMiniAppScreen(owner ViewOwner) (Screen, error) {
	if ui == nil || ui.runtime == nil {
		return Screen{}, errors.New("telegram runtime is unavailable")
	}
	health := ui.runtime.Health().LogsMiniApp
	back, err := ui.backButton(owner, RouteHome)
	if err != nil {
		return Screen{}, err
	}
	home, err := ui.homeButton(owner)
	if err != nil {
		return Screen{}, err
	}
	refresh, err := ui.refreshButton(owner, ActionState{Route: RouteLogs, Back: RouteHome})
	if err != nil {
		return Screen{}, err
	}
	statusText := string(health.State)
	if !health.Enabled {
		statusText = "disabled"
	}
	blocks := []RichBlock{
		{Kind: RichHeading, Title: "Logs", Text: "Read-only Telegram Mini App ingress"},
		{Kind: RichTable, Rows: [][]string{{"State", statusText}, {"cf-tunnel", boolState(health.DependencyAvailable)}, {"Generation", strconv.FormatUint(health.Generation, 10)}}},
	}
	primary := []Button{}
	if health.State == MiniAppReady && strings.TrimSpace(health.PublicURL) != "" {
		primary = append(primary, Button{Text: "Open Logs App", WebAppURL: health.PublicURL, Role: ButtonRolePrimary})
		blocks = append(blocks, RichBlock{Kind: RichDetails, Title: "Ephemeral ingress", Text: "The WebApp button is generated from the currently ready Quick Tunnel URL. Refresh this screen after a runtime or tunnel restart."})
	} else {
		primary = append(primary, Button{Text: "Open Logs App", Disabled: true, Role: ButtonRoleNeutral})
		message := strings.TrimSpace(health.LastError)
		if message == "" {
			if !health.Enabled {
				message = "Enable telegram.logs_mini_app.enabled to start the read-only Logs Mini App."
			} else if !health.DependencyAvailable {
				message = "Run cm integration cf install for the managed asset or install cf-tunnel globally yourself. Telegram polling and administration remain available without it."
			} else {
				message = "The Logs Mini App is not ready yet."
			}
		}
		blocks = append(blocks, RichBlock{Kind: RichDetails, Title: "Unavailable", Text: message})
	}
	return Screen{Rich: BuildRichPresentation(blocks...), Keyboard: BoundedActionGroups(ActionGroups{Primary: primary, Navigation: []Button{back, home, refresh}})}, nil
}

func newLogsMiniAppRuntime(launcher QuickTunnelLauncher, resolvers ...func() (string, error)) *LogsMiniAppRuntime {
	var resolveExecutable func() (string, error)
	if len(resolvers) > 0 {
		resolveExecutable = resolvers[0]
	}
	if launcher == nil {
		launcher = externalQuickTunnelLauncher{resolveExecutable: resolveExecutable}
	}
	return &LogsMiniAppRuntime{launcher: launcher, now: time.Now, sessions: map[string]miniAppSession{}, health: LogsMiniAppHealth{State: MiniAppDisabled}}
}

func (runtime *LogsMiniAppRuntime) Reconcile(ctx context.Context, cfg config.TelegramConfig, botToken string) error {
	if runtime == nil {
		return errors.New("telegram Logs Mini App runtime is unavailable")
	}
	botToken = strings.TrimSpace(botToken)
	fingerprint := miniAppFingerprint(cfg, botToken)
	runtime.mu.RLock()
	same := runtime.cancel != nil && runtime.fingerprint == fingerprint
	runtime.mu.RUnlock()
	if same {
		return nil
	}
	runtime.Stop()
	if !cfg.LogsMiniApp.Enabled {
		runtime.mu.Lock()
		runtime.config = cfg
		runtime.botToken = ""
		runtime.fingerprint = fingerprint
		runtime.health = LogsMiniAppHealth{Enabled: false, State: MiniAppDisabled}
		runtime.mu.Unlock()
		return nil
	}
	if !cfg.Enabled || botToken == "" || !validAuthorization(cfg.AllowedUserIDs) {
		runtime.mu.Lock()
		runtime.config = cfg
		runtime.botToken = ""
		runtime.fingerprint = fingerprint
		runtime.health = LogsMiniAppHealth{Enabled: true, State: MiniAppDegraded, LastError: "Telegram bot runtime, bot token, and authorized users are required before the Logs Mini App can start"}
		runtime.mu.Unlock()
		return nil
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		runtime.setDegraded(cfg, fingerprint, fmt.Errorf("start loopback Logs Mini App listener: %w", err))
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	server := &http.Server{Handler: runtime.handler(), ReadHeaderTimeout: 5 * time.Second}
	runtime.mu.Lock()
	runtime.config = cfg
	runtime.botToken = botToken
	runtime.fingerprint = fingerprint
	runtime.cancel = cancel
	runtime.done = done
	runtime.sessions = map[string]miniAppSession{}
	runtime.health = LogsMiniAppHealth{Enabled: true, State: MiniAppStarting, DependencyAvailable: true, Listener: listener.Addr().String(), Generation: runtime.health.Generation + 1}
	runtime.mu.Unlock()
	go func() {
		_ = server.Serve(listener)
	}()
	go runtime.supervise(runCtx, done, server, listener.Addr().String())
	return nil
}

func (runtime *LogsMiniAppRuntime) supervise(ctx context.Context, done chan struct{}, server *http.Server, address string) {
	defer close(done)
	defer server.Close()
	origin := "http://" + address
	attempt := 0
	for {
		if ctx.Err() != nil {
			return
		}
		if attempt > 0 {
			runtime.rotateGeneration()
		}
		runtime.updateHealth(func(health *LogsMiniAppHealth) {
			health.State = MiniAppStarting
			health.PublicURL = ""
			health.LastError = ""
		})
		process, err := runtime.launcher.Start(ctx, origin)
		if err != nil {
			runtime.updateHealth(func(health *LogsMiniAppHealth) {
				health.State = MiniAppDegraded
				health.DependencyAvailable = !errors.Is(err, exec.ErrNotFound)
				health.LastError = compactMiniAppError(err)
			})
			if !waitMiniAppRetry(ctx) {
				return
			}
			attempt++
			continue
		}
		runtime.updateHealth(func(health *LogsMiniAppHealth) { health.DependencyAvailable = true })
		ready := false
		events := process.Events()
		done := process.Done()
		for {
			select {
			case <-ctx.Done():
				process.Stop()
				return
			case event, ok := <-events:
				if !ok {
					events = nil
					continue
				}
				switch strings.ToLower(strings.TrimSpace(event.State)) {
				case "ready":
					publicURL, validationErr := miniAppPublicURL(event.URL)
					if validationErr != nil {
						process.Stop()
						runtime.updateHealth(func(health *LogsMiniAppHealth) {
							health.State = MiniAppDegraded
							health.LastError = validationErr.Error()
							health.PublicURL = ""
						})
						break
					}
					ready = true
					runtime.setReadyPublicURL(publicURL)
				case "stopped":
					if !ready {
						runtime.updateHealth(func(health *LogsMiniAppHealth) {
							health.State = MiniAppDegraded
							health.LastError = "cf-tunnel stopped before publishing a Quick Tunnel URL"
						})
					}
				default:
					if strings.TrimSpace(event.Error) != "" {
						runtime.updateHealth(func(health *LogsMiniAppHealth) { health.LastError = compactMiniAppError(errors.New(event.Error)) })
					}
				}
			case waitErr, ok := <-done:
				if ctx.Err() != nil {
					return
				}
				if !ok {
					done = nil
					continue
				}
				message := "cf-tunnel exited before shutdown"
				if ok && waitErr != nil {
					message = compactMiniAppError(waitErr)
				}
				runtime.updateHealth(func(health *LogsMiniAppHealth) {
					health.State = MiniAppDegraded
					health.PublicURL = ""
					health.LastError = message
				})
				process.Stop()
				if !waitMiniAppRetry(ctx) {
					return
				}
				attempt++
				goto nextAttempt
			}
		}
	nextAttempt:
	}
}

func waitMiniAppRetry(ctx context.Context) bool {
	timer := time.NewTimer(miniAppRestartDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (runtime *LogsMiniAppRuntime) rotateGeneration() {
	runtime.mu.Lock()
	runtime.health.Generation++
	runtime.sessions = map[string]miniAppSession{}
	runtime.mu.Unlock()
}

func (runtime *LogsMiniAppRuntime) updateHealth(update func(*LogsMiniAppHealth)) {
	runtime.mu.Lock()
	update(&runtime.health)
	runtime.mu.Unlock()
}

func (runtime *LogsMiniAppRuntime) setReadyPublicURL(publicURL string) {
	runtime.mu.Lock()
	if previous := strings.TrimSpace(runtime.health.PublicURL); previous != "" && previous != publicURL {
		runtime.health.Generation++
		runtime.sessions = map[string]miniAppSession{}
	}
	runtime.health.State = MiniAppReady
	runtime.health.PublicURL = publicURL
	runtime.health.LastError = ""
	runtime.mu.Unlock()
}

func (runtime *LogsMiniAppRuntime) setDegraded(cfg config.TelegramConfig, fingerprint string, err error) {
	runtime.mu.Lock()
	runtime.config = cfg
	runtime.fingerprint = fingerprint
	runtime.health = LogsMiniAppHealth{Enabled: cfg.LogsMiniApp.Enabled, State: MiniAppDegraded, LastError: compactMiniAppError(err)}
	runtime.mu.Unlock()
}

func (runtime *LogsMiniAppRuntime) Stop() {
	if runtime == nil {
		return
	}
	runtime.mu.Lock()
	cancel := runtime.cancel
	done := runtime.done
	runtime.cancel = nil
	runtime.done = nil
	runtime.botToken = ""
	runtime.sessions = map[string]miniAppSession{}
	if runtime.health.Enabled {
		runtime.health.State = MiniAppStopped
	}
	runtime.health.PublicURL = ""
	runtime.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if done != nil {
		timer := time.NewTimer(miniAppStopTimeout)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
		}
	}
}

func (runtime *LogsMiniAppRuntime) Health() LogsMiniAppHealth {
	if runtime == nil {
		return LogsMiniAppHealth{State: MiniAppDisabled}
	}
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	return runtime.health
}

func (runtime *LogsMiniAppRuntime) handler() http.Handler {
	mux := http.NewServeMux()
	assets := miniAppWebHandler()
	mux.HandleFunc("POST /api/auth", runtime.handleAuth)
	mux.HandleFunc("GET /api/logs/snapshot", runtime.handleSnapshot)
	mux.Handle("GET /", assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", miniAppContentSecurityPolicy())
		mux.ServeHTTP(w, r)
	})
}

func (runtime *LogsMiniAppRuntime) handleAuth(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, miniAppMaxInitDataBytes)
	var body struct {
		InitData string `json:"init_data"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		http.Error(w, "invalid Telegram Mini App authentication payload", http.StatusBadRequest)
		return
	}
	runtime.mu.RLock()
	token := runtime.botToken
	cfg := runtime.config
	generation := runtime.health.Generation
	now := runtime.now
	runtime.mu.RUnlock()
	userID, err := validateTelegramInitData(body.InitData, token, now().UTC())
	if err != nil {
		http.Error(w, "Telegram Mini App authentication failed", http.StatusUnauthorized)
		return
	}
	if !containsUserID(cfg.AllowedUserIDs, userID) {
		http.Error(w, "Telegram user is not authorized", http.StatusForbidden)
		return
	}
	sessionID, err := randomMiniAppSessionID()
	if err != nil {
		http.Error(w, "Mini App session unavailable", http.StatusInternalServerError)
		return
	}
	expires := now().UTC().Add(miniAppSessionTTL)
	runtime.mu.Lock()
	if generation != runtime.health.Generation || !containsUserID(runtime.config.AllowedUserIDs, userID) {
		runtime.mu.Unlock()
		http.Error(w, "Mini App runtime changed", http.StatusUnauthorized)
		return
	}
	runtime.sessions[sessionID] = miniAppSession{UserID: userID, Generation: generation, ExpiresAt: expires}
	runtime.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: miniAppCookieName, Value: sessionID, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, Expires: expires, MaxAge: int(miniAppSessionTTL.Seconds())})
	w.WriteHeader(http.StatusNoContent)
}

func (runtime *LogsMiniAppRuntime) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	if _, ok := runtime.authorizedSession(r); !ok {
		http.Error(w, "Mini App session is not authorized", http.StatusUnauthorized)
		return
	}
	tail := 200
	if raw := strings.TrimSpace(r.URL.Query().Get("tail")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || value > miniAppMaxTail {
			http.Error(w, fmt.Sprintf("tail must be between 0 and %d", miniAppMaxTail), http.StatusBadRequest)
			return
		}
		tail = value
	}
	options := application.LogsQueryOptions{Tail: tail, Level: r.URL.Query().Get("level"), Components: r.URL.Query().Get("components"), Workspace: r.URL.Query().Get("workspace"), Session: r.URL.Query().Get("session"), Event: r.URL.Query().Get("event"), Grep: r.URL.Query().Get("grep")}
	snapshot, err := application.LoadLogsContext(r.Context(), options, logger.VisibilityDefault, miniAppMaxTail, runtime.now().UTC())
	if err != nil {
		http.Error(w, "logs query failed", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snapshot)
}

func (runtime *LogsMiniAppRuntime) authorizedSession(r *http.Request) (miniAppSession, bool) {
	cookie, err := r.Cookie(miniAppCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return miniAppSession{}, false
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	session, ok := runtime.sessions[cookie.Value]
	if !ok || runtime.now().UTC().After(session.ExpiresAt) || session.Generation != runtime.health.Generation || !runtime.config.LogsMiniApp.Enabled || !containsUserID(runtime.config.AllowedUserIDs, session.UserID) {
		delete(runtime.sessions, cookie.Value)
		return miniAppSession{}, false
	}
	return session, true
}

func validateTelegramInitData(raw, botToken string, now time.Time) (int64, error) {
	if strings.TrimSpace(botToken) == "" {
		return 0, errors.New("telegram bot token is unavailable")
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return 0, errors.New("invalid Telegram init data")
	}
	providedHash, err := hex.DecodeString(values.Get("hash"))
	if err != nil || len(providedHash) != sha256.Size {
		return 0, errors.New("invalid Telegram init data hash")
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		if key != "hash" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+values.Get(key))
	}
	secretMAC := hmac.New(sha256.New, []byte("WebAppData"))
	_, _ = secretMAC.Write([]byte(botToken))
	checkMAC := hmac.New(sha256.New, secretMAC.Sum(nil))
	_, _ = checkMAC.Write([]byte(strings.Join(parts, "\n")))
	if subtle.ConstantTimeCompare(providedHash, checkMAC.Sum(nil)) != 1 {
		return 0, errors.New("telegram init data signature mismatch")
	}
	authUnix, err := strconv.ParseInt(values.Get("auth_date"), 10, 64)
	if err != nil {
		return 0, errors.New("telegram init data auth_date is invalid")
	}
	authTime := time.Unix(authUnix, 0).UTC()
	if authTime.After(now.Add(30*time.Second)) || now.Sub(authTime) > miniAppAuthMaxAge {
		return 0, errors.New("telegram init data is stale")
	}
	var user struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(values.Get("user")), &user); err != nil || user.ID <= 0 {
		return 0, errors.New("telegram init data user is invalid")
	}
	return user.ID, nil
}

func randomMiniAppSessionID() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func miniAppPublicURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || !strings.HasSuffix(strings.ToLower(parsed.Hostname()), ".trycloudflare.com") {
		return "", errors.New("cf-tunnel returned an invalid Quick Tunnel URL")
	}
	parsed.Path = "/"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func miniAppFingerprint(cfg config.TelegramConfig, token string) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%t|%t|%v|%s", cfg.Enabled, cfg.LogsMiniApp.Enabled, cfg.AllowedUserIDs, token)))
	return hex.EncodeToString(digest[:])
}

func compactMiniAppError(err error) string {
	if err == nil {
		return ""
	}
	value := strings.TrimSpace(err.Error())
	if len(value) > 240 {
		value = value[:240]
	}
	return value
}
