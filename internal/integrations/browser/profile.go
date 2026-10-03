package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const managedProfileDirectory = "Default"

type ProfileOptions struct {
	StateRoot string
	Candidate Candidate
}

type OwnedProfileOptions struct {
	StateRoot string
	Runtime   Runtime
}

func ResolveProfile(options ProfileOptions) (ProfileRef, error) {
	root := strings.TrimSpace(options.StateRoot)
	if root == "" {
		return ProfileRef{}, errors.New("codemcp state root is required for browser profile resolution")
	}
	candidate := options.Candidate
	if candidate.Transport == TransportWSLHost {
		hostRoot := strings.TrimSpace(candidate.HostLocalAppData)
		localRoot := strings.TrimSpace(candidate.LocalAppData)
		if hostRoot == "" || localRoot == "" {
			return ProfileRef{}, errors.New("windows LocalAppData mapping is required for WSL-host browser profile")
		}
		hostPath := joinHostPath("windows", hostRoot, "CodeMCP", "Browser", "ChatGPT")
		localPath := filepath.Join(localRoot, "CodeMCP", "Browser", "ChatGPT")
		return ProfileRef{
			HostPlatform: "windows",
			Transport:    TransportWSLHost,
			Path:         hostPath,
			LocalPath:    localPath,
			LockPath:     filepath.Join(localRoot, "CodeMCP", "Browser", "chatgpt.lock"),
		}, nil
	}
	path := filepath.Join(root, "browser", "chatgpt")
	return ProfileRef{
		HostPlatform: candidate.HostPlatform,
		Transport:    TransportNative,
		Path:         path,
		LocalPath:    path,
		LockPath:     filepath.Join(root, "browser", "chatgpt.lock"),
	}, nil
}

func ResolveOwnedProfiles(ctx context.Context, options OwnedProfileOptions) ([]ProfileRef, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	runtime := normalizedRuntime(options.Runtime)
	native, err := ResolveProfile(ProfileOptions{
		StateRoot: options.StateRoot,
		Candidate: Candidate{HostPlatform: runtime.GOOS, Transport: TransportNative},
	})
	if err != nil {
		return nil, err
	}
	profiles := []ProfileRef{native}
	if !isWSL(runtime) {
		return profiles, nil
	}
	hostRoot, localRoot, err := windowsLocalAppData(ctx, runtime)
	if err != nil {
		return profiles, nil
	}
	host, err := ResolveProfile(ProfileOptions{
		StateRoot: options.StateRoot,
		Candidate: Candidate{
			HostPlatform: "windows", Transport: TransportWSLHost,
			HostLocalAppData: hostRoot, LocalAppData: localRoot,
		},
	})
	if err != nil {
		return profiles, nil
	}
	if filepath.Clean(host.LocalPath) != filepath.Clean(native.LocalPath) {
		profiles = append(profiles, host)
	}
	return profiles, nil
}

func PrepareProfile(profile ProfileRef) error {
	path := strings.TrimSpace(profile.LocalPath)
	if path == "" {
		return errors.New("browser profile local path is required")
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return fmt.Errorf("create browser profile: %w", err)
	}
	defaultProfile := filepath.Join(path, managedProfileDirectory)
	if err := os.MkdirAll(defaultProfile, 0700); err != nil {
		return fmt.Errorf("create managed browser Default profile: %w", err)
	}
	if profile.Transport == TransportNative {
		if err := os.Chmod(path, 0700); err != nil {
			return fmt.Errorf("secure browser profile permissions: %w", err)
		}
		if err := os.Chmod(defaultProfile, 0700); err != nil {
			return fmt.Errorf("secure managed browser Default profile permissions: %w", err)
		}
	}
	return nil
}

func ProfileInUse(profile ProfileRef) (bool, error) {
	lockPath := strings.TrimSpace(profile.LockPath)
	if lockPath == "" {
		return false, errors.New("browser profile lock path is required")
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Clean(lockPath)), 0700); err != nil {
		return false, fmt.Errorf("prepare browser profile lock directory: %w", err)
	}
	lock, ok, err := TryAcquireProfile(profile)
	if err != nil {
		return false, err
	}
	if !ok {
		return true, nil
	}
	if err := lock.Release(); err != nil {
		return false, err
	}
	return false, nil
}

func RemoveProfile(profile ProfileRef) error {
	path := filepath.Clean(strings.TrimSpace(profile.LocalPath))
	if path == "." || path == "" || !isOwnedChatGPTProfilePath(path, profile.Transport) {
		return fmt.Errorf("refusing to remove non-CodeMCP ChatGPT browser profile: %q", profile.LocalPath)
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove CodeMCP ChatGPT browser profile: %w", err)
	}
	return nil
}

func isOwnedChatGPTProfilePath(path string, transport Transport) bool {
	normalized := filepath.ToSlash(filepath.Clean(path))
	if transport == TransportWSLHost {
		return strings.HasSuffix(strings.ToLower(normalized), "/codemcp/browser/chatgpt")
	}
	return strings.HasSuffix(strings.ToLower(normalized), "/browser/chatgpt")
}

func joinHostPath(goos string, parts ...string) string {
	if goos != "windows" {
		return filepath.Join(parts...)
	}
	clean := make([]string, 0, len(parts))
	for index, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if index == 0 {
			part = strings.TrimRight(part, `\/`)
		} else {
			part = strings.Trim(part, `\/`)
		}
		clean = append(clean, part)
	}
	return strings.Join(clean, `\`)
}
