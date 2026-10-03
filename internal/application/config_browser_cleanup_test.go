package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"go.mewis.me/codemcp/internal/integrations/browser"
)

func TestRemoveOwnedBrowserProfilesRemovesNativeAndWSLHostOnly(t *testing.T) {
	root := t.TempDir()
	hostRoot := t.TempDir()
	native := browser.ProfileRef{
		HostPlatform: "linux", Transport: browser.TransportNative,
		Path: filepath.Join(root, "browser", "chatgpt"), LocalPath: filepath.Join(root, "browser", "chatgpt"),
		LockPath: filepath.Join(root, "browser", "chatgpt.lock"),
	}
	host := browser.ProfileRef{
		HostPlatform: "windows", Transport: browser.TransportWSLHost,
		Path: filepath.Join(hostRoot, "CodeMCP", "Browser", "ChatGPT"), LocalPath: filepath.Join(hostRoot, "CodeMCP", "Browser", "ChatGPT"),
		LockPath: filepath.Join(hostRoot, "CodeMCP", "Browser", "chatgpt.lock"),
	}
	for _, profile := range []browser.ProfileRef{native, host} {
		if err := browser.PrepareProfile(profile); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(profile.LocalPath, "owned"), []byte("owned"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	personal := filepath.Join(hostRoot, "Google", "Chrome", "User Data", "Default")
	if err := os.MkdirAll(personal, 0700); err != nil {
		t.Fatal(err)
	}
	personalMarker := filepath.Join(personal, "keep")
	if err := os.WriteFile(personalMarker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}

	err := removeOwnedBrowserProfiles(context.Background(), root, func(context.Context, browser.OwnedProfileOptions) ([]browser.ProfileRef, error) {
		return []browser.ProfileRef{native, host}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []browser.ProfileRef{native, host} {
		if _, err := os.Stat(profile.LocalPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("owned profile remains %q: %v", profile.LocalPath, err)
		}
		if _, err := os.Stat(profile.LockPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("owned profile lock remains %q: %v", profile.LockPath, err)
		}
	}
	if _, err := os.Stat(personalMarker); err != nil {
		t.Fatalf("personal browser profile changed: %v", err)
	}
}
