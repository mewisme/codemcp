package browser

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ProfileOptions struct {
	StateRoot string
	Candidate Candidate
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

func PrepareProfile(profile ProfileRef) error {
	path := strings.TrimSpace(profile.LocalPath)
	if path == "" {
		return errors.New("browser profile local path is required")
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return fmt.Errorf("create browser profile: %w", err)
	}
	if profile.Transport == TransportNative {
		if err := os.Chmod(path, 0700); err != nil {
			return fmt.Errorf("secure browser profile permissions: %w", err)
		}
	}
	return nil
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
