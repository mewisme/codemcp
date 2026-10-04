package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"

	statepkg "go.mewis.me/codemcp/internal/state"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

const codeMCPModulePath = "go.mewis.me/codemcp"

const developmentGoCacheMaxBytes int64 = 4 << 30

var (
	developmentLookPath = exec.LookPath
	developmentCommand  = runDevelopmentCommand
)

func PrepareManagedRestartBinaryContext(ctx context.Context, configRoot, value string) (string, error) {
	binary, err := StableBinaryPath(value)
	if err != nil {
		return "", err
	}
	sourceRoot, development, err := goRunDevelopmentSource(configRoot, binary)
	if err != nil {
		return "", err
	}
	if !development {
		return PrepareManagedBinaryContext(ctx, configRoot, binary)
	}
	if sourceRoot == "" {
		return "", errors.New("go-run development source root is unavailable; run restart once from the CodeMCP source tree")
	}
	return buildGoRunDevelopmentBinary(ctx, configRoot, binary, sourceRoot)
}

func IsGoRunDevelopmentBinary(configRoot, value string) bool {
	binary, err := StableBinaryPath(value)
	if err != nil {
		return false
	}
	if transientGoBuildBinary(binary) {
		return true
	}
	return stagedGoRunBinary(configRoot, binary)
}

func goRunDevelopmentSource(configRoot, binary string) (string, bool, error) {
	if transientGoBuildBinary(binary) {
		if root, ok := discoverCodeMCPSourceRoot(); ok {
			return root, true, nil
		}
		return "", true, nil
	}
	if !stagedGoRunBinary(configRoot, binary) {
		return "", false, nil
	}
	root, err := loadGoRunSourceRoot(configRoot)
	if err == nil {
		return root, true, nil
	}
	if root, ok := discoverCodeMCPSourceRoot(); ok {
		if persistErr := saveGoRunSourceRoot(configRoot, root); persistErr != nil {
			return "", true, persistErr
		}
		return root, true, nil
	}
	if os.IsNotExist(errors.Unwrap(err)) || os.IsNotExist(err) {
		return "", true, nil
	}
	return "", true, err
}

func stagedGoRunBinary(configRoot, binary string) bool {
	if strings.TrimSpace(configRoot) == "" || strings.TrimSpace(binary) == "" {
		return false
	}
	root := filepath.Clean(filepath.Join(configRoot, "runtime", "bin", "go-run"))
	rel, err := filepath.Rel(root, filepath.Clean(binary))
	if err != nil || rel == "." || rel == "" {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func goRunSourceRootPath(configRoot string) string {
	return filepath.Join(configRoot, "runtime", "bin", "go-run", "source-root")
}

func saveGoRunSourceRoot(configRoot, root string) error {
	root = filepath.Clean(strings.TrimSpace(root))
	if !validCodeMCPSourceRoot(root) {
		return fmt.Errorf("invalid CodeMCP development source root %q", root)
	}
	path := goRunSourceRootPath(configRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return statepkg.WriteFileAtomic(path, []byte(root+"\n"), 0600)
}

func loadGoRunSourceRoot(configRoot string) (string, error) {
	data, err := os.ReadFile(goRunSourceRootPath(configRoot))
	if err != nil {
		return "", err
	}
	root := filepath.Clean(strings.TrimSpace(string(data)))
	if !validCodeMCPSourceRoot(root) {
		return "", fmt.Errorf("stored CodeMCP development source root is invalid: %s", root)
	}
	return root, nil
}

func discoverCodeMCPSourceRoot() (string, bool) {
	candidates := []string{}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, cwd)
	}
	if pwd := strings.TrimSpace(os.Getenv("PWD")); pwd != "" {
		candidates = append(candidates, pwd)
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		current, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		for {
			current = filepath.Clean(current)
			if !seen[current] {
				seen[current] = true
				if validCodeMCPSourceRoot(current) {
					return current, true
				}
			}
			parent := filepath.Dir(current)
			if parent == current {
				break
			}
			current = parent
		}
	}
	return "", false
}

func validCodeMCPSourceRoot(root string) bool {
	if strings.TrimSpace(root) == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return false
	}
	module := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			module = strings.TrimSpace(strings.TrimPrefix(line, "module "))
			break
		}
	}
	if module != codeMCPModulePath {
		return false
	}
	info, err := os.Stat(filepath.Join(root, "frontend", "package.json"))
	return err == nil && !info.IsDir()
}

func buildGoRunDevelopmentBinary(ctx context.Context, configRoot, currentBinary, sourceRoot string) (string, error) {
	span := tracepkg.Start(ctx, "SERVICE", "service.development.build", "Preparing development runtime build", tracepkg.String("source_root", sourceRoot))
	if err := buildDevelopmentFrontend(ctx, sourceRoot); err != nil {
		span.FailMessage("Development frontend build failed", err)
		return "", err
	}

	buildDir := filepath.Join(configRoot, "runtime", "bin", "go-run", ".build")
	if err := os.MkdirAll(buildDir, 0700); err != nil {
		span.FailMessage("Development build directory creation failed", err)
		return "", err
	}
	temp, err := os.CreateTemp(buildDir, "cm-*")
	if err != nil {
		span.FailMessage("Development binary temporary file creation failed", err)
		return "", err
	}
	tempPath := temp.Name()
	if closeErr := temp.Close(); closeErr != nil {
		_ = os.Remove(tempPath)
		span.FailMessage("Development binary temporary file close failed", closeErr)
		return "", closeErr
	}
	_ = os.Remove(tempPath)
	defer os.Remove(tempPath)

	args := []string{"build", "-trimpath", "-o", tempPath}
	args = append(args, currentBuildFlags()...)
	args = append(args, ".")
	if output, err := developmentCommand(ctx, sourceRoot, "go", args...); err != nil {
		err = commandBuildError("go build", output, err)
		span.FailMessage("Development Go build failed", err)
		return "", err
	}

	name := filepath.Base(currentBinary)
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = "cm"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
	}
	prepared, _, err := stageGoRunBinary(configRoot, tempPath, name, sourceRoot)
	if err != nil {
		span.FailMessage("Development runtime staging failed", err)
		return "", err
	}
	_ = pruneDevelopmentGoCache(configRoot, developmentGoCacheMaxBytes)
	span.EndMessage("Development runtime build prepared", tracepkg.String("binary", prepared))
	return prepared, nil
}

func pruneDevelopmentGoCache(configRoot string, maxBytes int64) error {
	if maxBytes <= 0 {
		return nil
	}
	configured := strings.TrimSpace(os.Getenv("GOCACHE"))
	if configured == "" {
		return nil
	}
	cacheRoot := filepath.Clean(configured)
	ownedRoot := filepath.Clean(filepath.Join(configRoot, "runtime", "cache", "go-build"))
	if cacheRoot != ownedRoot {
		return nil
	}
	over, err := directoryExceedsBytes(cacheRoot, maxBytes)
	if err != nil || !over {
		return err
	}
	return os.RemoveAll(cacheRoot)
}

func directoryExceedsBytes(root string, maxBytes int64) (bool, error) {
	var total int64
	over := false
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() <= 0 {
			return nil
		}
		if info.Size() > maxBytes || total > maxBytes-info.Size() {
			over = true
			return filepath.SkipAll
		}
		total += info.Size()
		if total > maxBytes {
			over = true
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return over || total > maxBytes, nil
}

func buildDevelopmentFrontend(ctx context.Context, sourceRoot string) error {
	if pnpm, err := developmentLookPath("pnpm"); err == nil {
		output, runErr := developmentCommand(ctx, sourceRoot, pnpm, "--dir", "frontend", "build")
		if runErr != nil {
			return commandBuildError("pnpm frontend build", output, runErr)
		}
		return nil
	}
	if npm, err := developmentLookPath("npm"); err == nil {
		output, runErr := developmentCommand(ctx, sourceRoot, npm, "--prefix", "frontend", "run", "build")
		if runErr != nil {
			return commandBuildError("npm frontend build", output, runErr)
		}
		return nil
	}
	return errors.New("frontend build requires pnpm or npm in PATH")
}

func currentBuildFlags() []string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return nil
	}
	flags := []string{}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "-ldflags":
			if value := strings.TrimSpace(setting.Value); value != "" {
				flags = append(flags, "-ldflags", value)
			}
		case "-race":
			if setting.Value == "true" {
				flags = append(flags, "-race")
			}
		}
	}
	return flags
}

func runDevelopmentCommand(ctx context.Context, dir, name string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	return string(output), err
}

func commandBuildError(action, output string, err error) error {
	output = strings.TrimSpace(output)
	if len(output) > 2048 {
		output = output[len(output)-2048:]
	}
	if output == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %w: %s", action, err, output)
}
