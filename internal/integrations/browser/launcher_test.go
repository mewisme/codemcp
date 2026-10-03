package browser

import (
	"context"
	"strings"
	"testing"
)

func TestManagerLaunchArgsUseVisibleDedicatedWindowAndPrivateCDP(t *testing.T) {
	args := managerLaunchArgs("/tmp/codemcp-browser-profile", false)
	joined := strings.Join(args, " ")
	for _, required := range []string{
		"--new-window",
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=0",
		"--user-data-dir=/tmp/codemcp-browser-profile",
		"about:blank",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("launch args missing %q: %v", required, args)
		}
	}
	for _, forbidden := range []string{"--headless", "0.0.0.0", "--remote-allow-origins=*"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("launch args contain unsafe/headless correctness dependency %q: %v", forbidden, args)
		}
	}
}

func TestManagerLaunchArgsSupportMinimizedVisibleWindow(t *testing.T) {
	joined := strings.Join(managerLaunchArgs("/tmp/profile", true), " ")
	if !strings.Contains(joined, "--start-minimized") || strings.Contains(joined, "--headless") {
		t.Fatalf("minimized args=%s", joined)
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

func TestLaunchAdapterRejectsHeadlessManagedRequest(t *testing.T) {
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
