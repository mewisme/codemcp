package browser

import (
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestManagerLaunchArgsUseVisibleDedicatedWindowAndPrivateCDP(t *testing.T) {
	args := managerLaunchArgs("/tmp/codemcp-browser-profile", false, 43123)
	joined := strings.Join(args, " ")
	for _, required := range []string{
		"--new-window",
		"--disable-background-mode",
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=43123",
		"--user-data-dir=/tmp/codemcp-browser-profile",
		"--profile-directory=Default",
		"about:blank",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("launch args missing %q: %v", required, args)
		}
	}
	for _, forbidden := range []string{"--headless", "0.0.0.0", "--remote-allow-origins=*", "--remote-debugging-port=0"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("launch args contain unsafe/headless correctness dependency %q: %v", forbidden, args)
		}
	}
}

func TestManagerLaunchArgsSupportMinimizedVisibleWindow(t *testing.T) {
	joined := strings.Join(managerLaunchArgs("/tmp/profile", true, 43124), " ")
	if !strings.Contains(joined, "--start-minimized") || strings.Contains(joined, "--headless") {
		t.Fatalf("minimized args=%s", joined)
	}
}

func TestAllocateNativeRemoteDebuggingPortReturnsNonZeroLoopbackPort(t *testing.T) {
	port, err := allocateRemoteDebuggingPort(context.Background(), Candidate{Transport: TransportNative})
	if err != nil {
		t.Fatal(err)
	}
	if port <= 0 || port > 65535 {
		t.Fatalf("port=%d", port)
	}
}

func TestInteractiveLaunchArgsUseIsolatedProfileWithoutAutomationTransport(t *testing.T) {
	tests := []struct {
		name    string
		request InteractiveLaunchRequest
		profile string
	}{
		{
			name: "native",
			request: InteractiveLaunchRequest{
				Candidate: Candidate{Transport: TransportNative, HostPlatform: "linux"},
				Profile: ProfileRef{
					Transport: TransportNative,
					Path:      "/tmp/codemcp-browser-profile",
					LocalPath: "/tmp/codemcp-browser-profile",
				},
				URL: "https://chatgpt.com/?temporary-chat=true",
			},
			profile: "/tmp/codemcp-browser-profile",
		},
		{
			name: "macos native",
			request: InteractiveLaunchRequest{
				Candidate: Candidate{Transport: TransportNative, HostPlatform: "darwin"},
				Profile: ProfileRef{
					HostPlatform: "darwin",
					Transport:    TransportNative,
					Path:         "/Users/mew/Library/Application Support/CodeMCP/browser/chatgpt",
					LocalPath:    "/Users/mew/Library/Application Support/CodeMCP/browser/chatgpt",
				},
				URL: "https://chatgpt.com/?temporary-chat=true",
			},
			profile: "/Users/mew/Library/Application Support/CodeMCP/browser/chatgpt",
		},
		{
			name: "windows native",
			request: InteractiveLaunchRequest{
				Candidate: Candidate{Transport: TransportNative, HostPlatform: "windows"},
				Profile: ProfileRef{
					HostPlatform: "windows",
					Transport:    TransportNative,
					Path:         `C:\Users\Mew\AppData\Local\CodeMCP\browser\chatgpt`,
					LocalPath:    `C:\Users\Mew\AppData\Local\CodeMCP\browser\chatgpt`,
				},
				URL: "https://chatgpt.com/?temporary-chat=true",
			},
			profile: `C:\Users\Mew\AppData\Local\CodeMCP\browser\chatgpt`,
		},
		{
			name: "wsl host",
			request: InteractiveLaunchRequest{
				Candidate: Candidate{Transport: TransportWSLHost, HostPlatform: "windows"},
				Profile: ProfileRef{
					HostPlatform: "windows",
					Transport:    TransportWSLHost,
					Path:         `C:\Users\Mew\AppData\Local\CodeMCP\Browser\ChatGPT`,
					LocalPath:    "/mnt/c/Users/Mew/AppData/Local/CodeMCP/Browser/ChatGPT",
				},
				URL: "https://chatgpt.com/?temporary-chat=true",
			},
			profile: `C:\Users\Mew\AppData\Local\CodeMCP\Browser\ChatGPT`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args, err := interactiveLaunchArgs(test.request)
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(args, " ")
			for _, required := range []string{
				"--new-window",
				"--disable-background-mode",
				"--user-data-dir=" + test.profile,
				"--profile-directory=Default",
				"https://chatgpt.com/?temporary-chat=true",
			} {
				if !strings.Contains(joined, required) {
					t.Fatalf("interactive args missing %q: %v", required, args)
				}
			}
			for _, forbidden := range []string{
				"--remote-debugging",
				"--headless",
				"--incognito",
				"--guest",
				"--proxy",
				"--load-extension",
				"--disable-extensions-except",
				"--remote-allow-origins",
				"--enable-automation",
			} {
				if strings.Contains(joined, forbidden) {
					t.Fatalf("interactive args contain forbidden %q: %v", forbidden, args)
				}
			}
		})
	}
}

func TestInteractiveLaunchArgsRejectCrossHostAndNonWebTargets(t *testing.T) {
	if _, err := interactiveLaunchArgs(InteractiveLaunchRequest{
		Candidate: Candidate{Transport: TransportWSLHost, HostPlatform: "windows"},
		Profile: ProfileRef{
			Transport: TransportNative, HostPlatform: "linux",
			Path: "/tmp/profile", LocalPath: "/tmp/profile",
		},
		URL: "https://chatgpt.com/",
	}); err == nil {
		t.Fatal("interactive launcher args accepted cross-host profile")
	}
	if _, err := interactiveLaunchArgs(InteractiveLaunchRequest{
		Candidate: Candidate{Transport: TransportNative, HostPlatform: "linux"},
		Profile: ProfileRef{
			Transport: TransportNative, HostPlatform: "linux",
			Path: "/tmp/profile", LocalPath: "/tmp/profile",
		},
		URL: "file:///tmp/page.html",
	}); err == nil {
		t.Fatal("interactive launcher args accepted non-web URL")
	}
}

func TestLaunchAdaptersRejectCrossHostProfileMismatch(t *testing.T) {
	if _, _, err := launchNativeBrowser(context.Background(), LaunchRequest{
		Candidate: Candidate{Transport: TransportNative},
		Profile:   ProfileRef{Transport: TransportWSLHost},
	}); err == nil {
		t.Fatal("native launcher accepted WSL-host profile")
	}
	if _, _, err := launchWSLHostBrowser(context.Background(), LaunchRequest{
		Candidate: Candidate{Transport: TransportWSLHost, HostPlatform: "windows"},
		Profile:   ProfileRef{Transport: TransportNative, HostPlatform: "linux"},
	}); err == nil {
		t.Fatal("WSL-host launcher accepted native profile")
	}
}

func TestLaunchAdapterRejectsMissingManagedLaunchMode(t *testing.T) {
	if _, _, err := launchExecBrowser(context.Background(), LaunchRequest{
		Candidate: Candidate{Transport: TransportNative, Executable: "/missing"},
		Profile: ProfileRef{
			Transport: TransportNative, Path: "/tmp/profile", LocalPath: "/tmp/profile",
		},
		Visible: false,
	}); err == nil {
		t.Fatal("managed browser launcher accepted a non-visible request")
	}
}

func TestLoopbackRelayBindsOnlyLocalhostAndForwardsHTTP(t *testing.T) {
	remote, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	go func() {
		for {
			connection, err := remote.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				buffer := make([]byte, 4096)
				if count, _ := connection.Read(buffer); count > 0 {
					_, _ = connection.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok"))
				}
			}()
		}
	}()

	bridge := func(connection net.Conn, port int) {
		upstream, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			return
		}
		defer upstream.Close()
		done := make(chan struct{}, 1)
		go func() {
			_, _ = io.Copy(upstream, connection)
			done <- struct{}{}
		}()
		_, _ = io.Copy(connection, upstream)
		<-done
	}
	relay, err := startLoopbackRelay(remote.Addr().(*net.TCPAddr).Port, bridge)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	if !strings.HasPrefix(relay.URL(), "http://127.0.0.1:") {
		t.Fatalf("relay URL=%q", relay.URL())
	}
	response, err := http.Get(relay.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("status=%s body=%q", response.Status, body)
	}
}

func TestRelayWebSocketURLKeepsBrowserPathOnLocalLoopback(t *testing.T) {
	got, err := relayWebSocketURL(
		"http://127.0.0.1:40123",
		"ws://127.0.0.1:59671/devtools/browser/abc123",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != "ws://127.0.0.1:40123/devtools/browser/abc123" {
		t.Fatalf("relay websocket=%q", got)
	}
}

func TestRelayedBrowserProcessIgnoresShortLivedWSLLauncherProcess(t *testing.T) {
	relay, err := startLoopbackRelay(9222, func(net.Conn, int) {})
	if err != nil {
		t.Fatal(err)
	}
	base := newFakeProcess(42)
	process := newRelayedBrowserProcess(base, relay, "", "")
	base.stop(nil)

	select {
	case <-process.Done():
		t.Fatal("relay process ended when the short-lived Windows launcher exited")
	default:
	}
	if process.PID() != 0 {
		t.Fatalf("stale launcher pid=%d", process.PID())
	}
	if err := process.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-process.Done():
	default:
		t.Fatal("relay process did not close")
	}
}

func TestRelayedBrowserProcessStopsExactWindowsHostOwnerOnClose(t *testing.T) {
	relay, err := startLoopbackRelay(9222, func(net.Conn, int) {})
	if err != nil {
		t.Fatal(err)
	}
	base := newFakeProcess(42)
	wrapped := newRelayedBrowserProcess(
		base,
		relay,
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Users\Mew\AppData\Local\CodeMCP\Browser\ChatGPT`,
	)
	process, ok := wrapped.(*relayedBrowserProcess)
	if !ok {
		t.Fatalf("wrapped process type=%T", wrapped)
	}
	var gotExecutable, gotProfile string
	process.stopHost = func(_ context.Context, executable, profile string) error {
		gotExecutable = executable
		gotProfile = profile
		return nil
	}
	if err := process.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotExecutable != `C:\Program Files\Google\Chrome\Application\chrome.exe` ||
		gotProfile != `C:\Users\Mew\AppData\Local\CodeMCP\Browser\ChatGPT` {
		t.Fatalf("host stop executable=%q profile=%q", gotExecutable, gotProfile)
	}
	select {
	case <-base.Done():
	default:
		t.Fatal("base launcher process was not closed")
	}
}
