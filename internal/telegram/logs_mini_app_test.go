package telegram

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/config"
)

type miniAppFakeProcess struct {
	events chan QuickTunnelEvent
	done   chan error
	once   sync.Once
}

func (process *miniAppFakeProcess) Events() <-chan QuickTunnelEvent { return process.events }
func (process *miniAppFakeProcess) Done() <-chan error              { return process.done }
func (process *miniAppFakeProcess) Stop() {
	process.once.Do(func() {
		process.done <- context.Canceled
		close(process.done)
		close(process.events)
	})
}

type miniAppFakeLauncher struct {
	mu        sync.Mutex
	processes []*miniAppFakeProcess
	err       error
	origins   []string
}

func (launcher *miniAppFakeLauncher) Start(_ context.Context, origin string) (QuickTunnelProcess, error) {
	launcher.mu.Lock()
	defer launcher.mu.Unlock()
	launcher.origins = append(launcher.origins, origin)
	if launcher.err != nil {
		return nil, launcher.err
	}
	if len(launcher.processes) == 0 {
		return nil, errors.New("no fake cf-tunnel process configured")
	}
	process := launcher.processes[0]
	launcher.processes = launcher.processes[1:]
	return process, nil
}

func signedTelegramInitData(t *testing.T, token string, userID int64, authDate time.Time) string {
	t.Helper()
	user, err := json.Marshal(map[string]any{"id": userID, "first_name": "Test"})
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{}
	values.Set("auth_date", strconv.FormatInt(authDate.Unix(), 10))
	values.Set("query_id", "AAE-test")
	values.Set("user", string(user))
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+values.Get(key))
	}
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	_, _ = secret.Write([]byte(token))
	signature := hmac.New(sha256.New, secret.Sum(nil))
	_, _ = signature.Write([]byte(strings.Join(parts, "\n")))
	values.Set("hash", hex.EncodeToString(signature.Sum(nil)))
	return values.Encode()
}

func TestValidateTelegramInitDataRejectsForgedStaleAndUnsignedPayloads(t *testing.T) {
	const token = "123456:test-token"
	now := time.Date(2026, 9, 28, 5, 0, 0, 0, time.UTC)
	valid := signedTelegramInitData(t, token, 42, now.Add(-time.Minute))
	if userID, err := validateTelegramInitData(valid, token, now); err != nil || userID != 42 {
		t.Fatalf("valid init data user=%d err=%v", userID, err)
	}
	for name, raw := range map[string]string{
		"forged":   valid + "x",
		"stale":    signedTelegramInitData(t, token, 42, now.Add(-miniAppAuthMaxAge-time.Second)),
		"unsigned": "auth_date=" + strconv.FormatInt(now.Unix(), 10) + "&user=%7B%22id%22%3A42%7D",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := validateTelegramInitData(raw, token, now); err == nil {
				t.Fatal("invalid Telegram init data was accepted")
			}
		})
	}
}

func TestLogsMiniAppIngressRequiresAuthorizedSessionAndDoesNotExposeAdminRoutes(t *testing.T) {
	const token = "123456:test-token"
	now := time.Date(2026, 9, 28, 5, 0, 0, 0, time.UTC)
	runtime := newLogsMiniAppRuntime(&miniAppFakeLauncher{})
	runtime.now = func() time.Time { return now }
	runtime.config = config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}, LogsMiniApp: config.TelegramLogsMiniAppConfig{Enabled: true}}
	runtime.botToken = token
	runtime.health = LogsMiniAppHealth{Enabled: true, State: MiniAppReady, Generation: 3}

	server := httptest.NewServer(runtime.handler())
	defer server.Close()
	shell, err := http.Get(server.URL + "/mini-app")
	if err != nil {
		t.Fatal(err)
	}
	shellBody, err := io.ReadAll(shell.Body)
	shell.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if shell.StatusCode != http.StatusOK || !strings.Contains(string(shellBody), "data-codemcp-mini-app-root") || strings.Contains(string(shellBody), "CodeMCP Admin") {
		t.Fatalf("dedicated logs shell status=%d body=%q", shell.StatusCode, string(shellBody))
	}

	for _, path := range []string{"/api/status", "/api/logs", "/admin", "/mcp"} {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("public Mini App route %s status=%d want=404", path, response.StatusCode)
		}
	}
	response, err := http.Get(server.URL + "/mini-app/api/logs/snapshot")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated logs snapshot status=%d want=401", response.StatusCode)
	}

	unauthorized := signedTelegramInitData(t, token, 99, now.Add(-time.Minute))
	response = postMiniAppAuth(t, server.URL, unauthorized)
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("valid but unauthorized Telegram user status=%d want=403", response.StatusCode)
	}

	authorized := signedTelegramInitData(t, token, 42, now.Add(-time.Minute))
	response = postMiniAppAuth(t, server.URL, authorized)
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("authorized Mini App authentication status=%d", response.StatusCode)
	}
	cookies := response.Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].Path != "/" || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("unsafe Mini App session cookie: %#v", cookies)
	}
}

func postMiniAppAuth(t *testing.T, baseURL, initData string) *http.Response {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"init_data": initData})
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(baseURL+"/mini-app/auth", "application/json", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestLogsMiniAppSessionIsRevokedByAllowlistAndGenerationChanges(t *testing.T) {
	runtime := newLogsMiniAppRuntime(&miniAppFakeLauncher{})
	runtime.now = time.Now
	runtime.config = config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}, LogsMiniApp: config.TelegramLogsMiniAppConfig{Enabled: true}}
	runtime.health = LogsMiniAppHealth{Enabled: true, State: MiniAppReady, Generation: 7}
	runtime.sessions["session"] = miniAppSession{UserID: 42, Generation: 7, ExpiresAt: time.Now().Add(time.Minute)}
	request := httptest.NewRequest(http.MethodGet, "/mini-app/api/logs/snapshot", nil)
	request.AddCookie(&http.Cookie{Name: miniAppCookieName, Value: "session"})
	if _, ok := runtime.authorizedSession(request); !ok {
		t.Fatal("current authorized session was rejected")
	}

	runtime.sessions["session"] = miniAppSession{UserID: 42, Generation: 7, ExpiresAt: time.Now().Add(time.Minute)}
	runtime.config.AllowedUserIDs = nil
	if _, ok := runtime.authorizedSession(request); ok {
		t.Fatal("allowlist removal did not revoke Mini App session")
	}

	runtime.config.AllowedUserIDs = []int64{42}
	runtime.sessions["session"] = miniAppSession{UserID: 42, Generation: 7, ExpiresAt: time.Now().Add(time.Minute)}
	runtime.health.Generation = 8
	if _, ok := runtime.authorizedSession(request); ok {
		t.Fatal("runtime generation change did not revoke Mini App session")
	}
}

func TestLogsMiniAppURLRotationAdvancesGenerationAndClearsSessions(t *testing.T) {
	runtime := newLogsMiniAppRuntime(&miniAppFakeLauncher{})
	runtime.health = LogsMiniAppHealth{Enabled: true, State: MiniAppReady, PublicURL: "https://first.trycloudflare.com/mini-app", Generation: 4}
	runtime.sessions["session"] = miniAppSession{UserID: 42, Generation: 4, ExpiresAt: time.Now().Add(time.Minute)}
	runtime.setReadyPublicURL("https://second.trycloudflare.com/mini-app")
	health := runtime.Health()
	if health.Generation != 5 || health.PublicURL != "https://second.trycloudflare.com/mini-app" {
		t.Fatalf("rotated health=%#v", health)
	}
	if len(runtime.sessions) != 0 {
		t.Fatalf("URL rotation retained %d stale sessions", len(runtime.sessions))
	}
}

func TestLogsMiniAppAbsentDependencyDoesNotDisableTelegramPolling(t *testing.T) {
	launcher := &miniAppFakeLauncher{err: fmt.Errorf("cf-tunnel is not installed: %w", exec.ErrNotFound)}
	api := &navigationFakeAPI{fakeAPI: &fakeAPI{}, menus: map[int64]MenuButton{}}
	root := t.TempDir()
	if err := SetToken(root, "123456:test-token"); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(Options{Root: root, Factory: func(string) API { return api }, PollTimeout: time.Millisecond, ReconnectDelay: func(int) time.Duration { return time.Millisecond }, StopTimeout: 100 * time.Millisecond, MiniAppLauncher: launcher})
	t.Cleanup(runtime.Stop)
	cfg := config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}, LogsMiniApp: config.TelegramLogsMiniAppConfig{Enabled: true}}
	if err := runtime.Reconcile(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for runtime.Health().LogsMiniApp.State != MiniAppDegraded && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	health := runtime.Health()
	if !health.Running || health.LogsMiniApp.State != MiniAppDegraded || health.LogsMiniApp.DependencyAvailable {
		t.Fatalf("Telegram runtime/mini app health=%#v", health)
	}
	if menu := api.menus[42]; menu.Type != MenuButtonCommands {
		t.Fatalf("Logs Mini App changed native command menu: %#v", menu)
	}
}

func TestLogsMiniAppRetriesMissingDependencyAndRecoversAfterInstall(t *testing.T) {
	launcher := &miniAppFakeLauncher{err: fmt.Errorf("cf-tunnel is not installed: %w", exec.ErrNotFound)}
	runtime := newLogsMiniAppRuntime(launcher)
	t.Cleanup(runtime.Stop)
	cfg := config.TelegramConfig{Enabled: true, AllowedUserIDs: []int64{42}, LogsMiniApp: config.TelegramLogsMiniAppConfig{Enabled: true}}
	if err := runtime.Reconcile(t.Context(), cfg, "123456:test-token"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for runtime.Health().State != MiniAppDegraded && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	process := &miniAppFakeProcess{events: make(chan QuickTunnelEvent, 1), done: make(chan error, 1)}
	process.events <- QuickTunnelEvent{State: "ready", URL: "https://recovered.trycloudflare.com"}
	launcher.mu.Lock()
	launcher.err = nil
	launcher.processes = append(launcher.processes, process)
	launcher.mu.Unlock()
	deadline = time.Now().Add(2 * time.Second)
	for runtime.Health().State != MiniAppReady && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	health := runtime.Health()
	if health.State != MiniAppReady || health.PublicURL != "https://recovered.trycloudflare.com/mini-app" || !health.DependencyAvailable {
		t.Fatalf("recovered health=%#v", health)
	}
}

func TestLogsScreenUsesCurrentReadyWebAppURLOnly(t *testing.T) {
	runtime := NewRuntime(Options{Root: t.TempDir()})
	runtime.logsMiniApp.mu.Lock()
	runtime.logsMiniApp.health = LogsMiniAppHealth{Enabled: true, State: MiniAppReady, DependencyAvailable: true, PublicURL: "https://first.trycloudflare.com/mini-app", Generation: 1}
	runtime.logsMiniApp.mu.Unlock()
	ui, err := NewInterface(InterfaceOptions{Runtime: runtime})
	if err != nil {
		t.Fatal(err)
	}
	owner := ViewOwner{ChatID: 42, UserID: 42, Generation: 1}
	first, err := ui.logsMiniAppScreen(owner)
	if err != nil {
		t.Fatal(err)
	}
	if got := webAppURL(first.Keyboard); got != "https://first.trycloudflare.com/mini-app" {
		t.Fatalf("first web_app URL=%q", got)
	}
	runtime.logsMiniApp.mu.Lock()
	runtime.logsMiniApp.health.PublicURL = "https://second.trycloudflare.com/mini-app"
	runtime.logsMiniApp.health.Generation = 2
	runtime.logsMiniApp.mu.Unlock()
	second, err := ui.logsMiniAppScreen(owner)
	if err != nil {
		t.Fatal(err)
	}
	if got := webAppURL(second.Keyboard); got != "https://second.trycloudflare.com/mini-app" {
		t.Fatalf("rotated web_app URL=%q", got)
	}
}

func webAppURL(rows [][]Button) string {
	for _, row := range rows {
		for _, button := range row {
			if button.WebAppURL != "" {
				return button.WebAppURL
			}
		}
	}
	return ""
}
