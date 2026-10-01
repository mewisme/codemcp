//go:build !windows

package install

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	packageOwnerProbeTimeout = 2 * time.Second
	maxPackageOwnerOutput    = 32 << 10
)

type packageOwnership struct {
	Method  Method
	Package string
}

type boundedPackageOutput struct {
	mu   sync.Mutex
	data []byte
}

func (buffer *boundedPackageOutput) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	remaining := maxPackageOwnerOutput - len(buffer.data)
	if remaining > 0 {
		if len(value) < remaining {
			remaining = len(value)
		}
		buffer.data = append(buffer.data, value[:remaining]...)
	}
	return len(value), nil
}

func (buffer *boundedPackageOutput) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return string(buffer.data)
}

func linuxPackageOwnership(path string) packageOwnership {
	if strings.TrimSpace(path) == "" {
		return packageOwnership{}
	}
	return linuxPackageOwnershipWith(path, exec.LookPath, TrustedSystemExecutable, runPackageOwnerProbe)
}

func linuxPackageOwnershipWith(path string, lookup func(string) (string, error), trust func(string) bool, run func(context.Context, string, ...string) (string, error)) packageOwnership {
	checks := []struct {
		method  Method
		command string
		args    []string
		parse   func(string) string
	}{
		{MethodDebian, "dpkg-query", []string{"--search", path}, parseDebianPackageOwner},
		{MethodRPM, "rpm", []string{"-qf", "--qf", "%{NAME}\n", path}, parseRPMPackageOwner},
	}
	for _, check := range checks {
		command, err := lookup(check.command)
		if err != nil || trust == nil || !trust(command) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), packageOwnerProbeTimeout)
		output, err := run(ctx, command, check.args...)
		cancel()
		if err != nil {
			continue
		}
		if owner := check.parse(output); owner != "" {
			return packageOwnership{Method: check.method, Package: owner}
		}
	}
	return packageOwnership{}
}

func TrustedSystemExecutable(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	resolved = filepath.Clean(resolved)
	dir := filepath.Dir(resolved)
	trustedDir := false
	for _, candidate := range []string{"/usr/bin", "/usr/sbin", "/bin", "/sbin"} {
		canonical, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			continue
		}
		if dir == filepath.Clean(canonical) {
			trustedDir = true
			break
		}
	}
	if !trustedDir {
		return false
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 || info.Mode().Perm()&0022 != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == 0
}

func runPackageOwnerProbe(ctx context.Context, command string, args ...string) (string, error) {
	process := exec.CommandContext(ctx, command, args...)
	var stdout, stderr boundedPackageOutput
	process.Stdout = &stdout
	process.Stderr = &stderr
	if err := process.Run(); err != nil {
		return strings.TrimSpace(stdout.String()), err
	}
	return strings.TrimSpace(stdout.String()), nil
}

func parseDebianPackageOwner(output string) string {
	for _, line := range strings.Split(output, "\n") {
		owner, _, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		owner = strings.TrimSpace(owner)
		if owner == "" {
			continue
		}
		if index := strings.IndexByte(owner, ':'); index >= 0 {
			owner = owner[:index]
		}
		return strings.TrimSpace(owner)
	}
	return ""
}

func parseRPMPackageOwner(output string) string {
	fields := strings.Fields(output)
	if len(fields) == 0 {
		return ""
	}
	return strings.TrimSpace(fields[0])
}

func currentLinuxPackageMethod(path string) Method {
	if runtime.GOOS != "linux" {
		return MethodUnknown
	}
	ownership := linuxPackageOwnership(path)
	if ownership.Package != PackageName {
		return MethodUnknown
	}
	return ownership.Method
}

const PackageName = "codemcp"
