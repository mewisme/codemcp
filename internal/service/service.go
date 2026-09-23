package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type Scope string

const (
	ScopeUser   Scope = "user"
	ScopeSystem Scope = "system"
)

type Account struct {
	Username string
	UID      string
	GID      string
	HomeDir  string
}

type Spec struct {
	ID              string
	Scope           Scope
	ConfigRoot      string
	Binary          string
	EnvironmentHash string
	Account         Account
}

type Status struct {
	Installed bool
	Running   bool
	PID       int
	Backend   string
}

type Manager interface {
	Backend() string
	DefinitionMatches(Spec) (bool, error)
	Install(Spec) error
	Start(Spec) error
	Stop(Spec) error
	Uninstall(Spec) error
	Status(Spec) (Status, error)
}

func NewSpec(configRoot, binary string, scope Scope, account Account) (Spec, error) {
	return newSpecObserver(nil, configRoot, binary, scope, account)
}

func NewSpecContext(ctx context.Context, configRoot, binary string, scope Scope, account Account) (Spec, error) {
	return newSpecObserver(tracepkg.ObserverFromContext(ctx), configRoot, binary, scope, account)
}

func newSpecObserver(observer tracepkg.Observer, configRoot, binary string, scope Scope, account Account) (Spec, error) {
	span := tracepkg.StartObserver(observer, "SERVICE", "service.spec.resolve", "Resolving managed service specification", tracepkg.String("scope", string(scope)), tracepkg.String("config_root", configRoot), tracepkg.String("binary", binary), tracepkg.String("account", account.Username))
	if strings.TrimSpace(configRoot) == "" {
		err := errors.New("service config root is required")
		span.FailMessage("Managed service specification resolution failed", err)
		return Spec{}, err
	}
	absoluteRoot, err := filepath.Abs(configRoot)
	if err != nil {
		span.FailMessage("Managed service config root resolution failed", err)
		return Spec{}, err
	}
	binary, err = StableBinaryPath(binary)
	if err != nil {
		span.FailMessage("Managed service binary resolution failed", err)
		return Spec{}, err
	}
	spec := Spec{ID: ID(filepath.Clean(absoluteRoot), scope), Scope: scope, ConfigRoot: filepath.Clean(absoluteRoot), Binary: binary, Account: account}
	span.EndMessage("Managed service specification resolved", tracepkg.String("service", spec.ID), tracepkg.String("scope", string(spec.Scope)), tracepkg.String("config_root", spec.ConfigRoot), tracepkg.String("binary", spec.Binary), tracepkg.String("account", spec.Account.Username))
	return spec, nil
}

func InvokingAccountContext(ctx context.Context, scope Scope) (Account, error) {
	span := tracepkg.Start(ctx, "SERVICE", "service.account.resolve", "Resolving invoking account", tracepkg.String("scope", string(scope)))
	account, err := InvokingAccount(scope)
	if err != nil {
		span.FailMessage("Invoking account resolution failed", err)
		return Account{}, err
	}
	span.EndMessage("Invoking account resolved", tracepkg.String("scope", string(scope)), tracepkg.String("account", account.Username), tracepkg.String("home", account.HomeDir), tracepkg.String("uid", account.UID), tracepkg.String("gid", account.GID))
	return account, nil
}

func ID(configRoot string, scope Scope) string {
	sum := sha256.Sum256([]byte(filepath.Clean(configRoot)))
	return "cm-" + string(scope) + "-" + hex.EncodeToString(sum[:6])
}

func StableBinaryPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = os.Args[0]
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value), nil
	}
	resolved, err := exec.LookPath(value)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func PrepareManagedBinary(configRoot, value string) (string, error) {
	return prepareManagedBinaryObserver(nil, configRoot, value)
}

func PrepareManagedBinaryContext(ctx context.Context, configRoot, value string) (string, error) {
	return prepareManagedBinaryObserver(tracepkg.ObserverFromContext(ctx), configRoot, value)
}

func prepareManagedBinaryObserver(observer tracepkg.Observer, configRoot, value string) (string, error) {
	span := tracepkg.StartObserver(observer, "SERVICE", "service.binary.prepare", "Preparing managed service binary", tracepkg.String("requested_binary", value), tracepkg.String("config_root", configRoot))
	binary, err := StableBinaryPath(value)
	if err != nil {
		span.FailMessage("Managed service binary resolution failed", err)
		return "", err
	}
	if !transientGoBuildBinary(binary) {
		span.EndMessage("Managed service binary resolved", tracepkg.String("source", binary), tracepkg.String("destination", binary), tracepkg.Bool("transient", false), tracepkg.Bool("reused", true))
		return binary, nil
	}
	if strings.TrimSpace(configRoot) == "" {
		err := errors.New("service config root is required")
		span.FailMessage("Managed service binary preparation failed", err, tracepkg.String("source", binary), tracepkg.Bool("transient", true))
		return "", err
	}
	hash, err := fileSHA256(binary)
	if err != nil {
		span.FailMessage("Managed service binary hash failed", err, tracepkg.String("source", binary), tracepkg.Bool("transient", true))
		return "", err
	}
	name := filepath.Base(binary)
	destination := filepath.Join(configRoot, "runtime", "bin", "go-run", hash[:16], name)
	if info, statErr := os.Stat(destination); statErr == nil && !info.IsDir() {
		span.EndMessage("Managed service binary reused", tracepkg.String("source", binary), tracepkg.String("destination", filepath.Clean(destination)), tracepkg.Bool("transient", true), tracepkg.Bool("reused", true), tracepkg.Int64("bytes", info.Size()))
		return filepath.Clean(destination), nil
	} else if statErr != nil && !os.IsNotExist(statErr) {
		span.FailMessage("Managed service binary destination inspection failed", statErr, tracepkg.String("source", binary), tracepkg.String("destination", destination), tracepkg.Bool("transient", true))
		return "", statErr
	}
	if err := copyExecutableAtomic(binary, destination); err != nil {
		span.FailMessage("Managed service binary copy failed", err, tracepkg.String("source", binary), tracepkg.String("destination", destination), tracepkg.Bool("transient", true), tracepkg.Bool("reused", false))
		return "", err
	}
	fields := []tracepkg.Field{tracepkg.String("source", binary), tracepkg.String("destination", filepath.Clean(destination)), tracepkg.Bool("transient", true), tracepkg.Bool("reused", false), tracepkg.Bool("atomic", true)}
	if info, statErr := os.Stat(destination); statErr == nil {
		fields = append(fields, tracepkg.Int64("bytes", info.Size()))
	}
	span.EndMessage("Managed service binary prepared", fields...)
	return filepath.Clean(destination), nil
}

func transientGoBuildBinary(path string) bool {
	for _, part := range strings.FieldsFunc(filepath.Clean(path), func(r rune) bool { return r == '/' || r == '\\' }) {
		if strings.HasPrefix(strings.ToLower(part), "go-build") {
			return true
		}
	}
	return false
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func copyExecutableAtomic(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("managed service binary is a directory: %s", source)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(destination), ".cm-bin-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	sourceFile, err := os.Open(source)
	if err != nil {
		_ = temp.Close()
		return err
	}
	_, copyErr := io.Copy(temp, sourceFile)
	closeSourceErr := sourceFile.Close()
	if copyErr != nil {
		_ = temp.Close()
		return copyErr
	}
	if closeSourceErr != nil {
		_ = temp.Close()
		return closeSourceErr
	}
	mode := info.Mode().Perm()
	if mode&0111 == 0 {
		mode |= 0700
	}
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, destination); err != nil {
		if _, statErr := os.Stat(destination); statErr == nil {
			return nil
		}
		return err
	}
	return nil
}

func Args(spec Spec) []string {
	return []string{"--config-dir", spec.ConfigRoot, "_service", "run", "--service-id", spec.ID, "--service-scope", string(spec.Scope), "--service-environment-hash", spec.EnvironmentHash}
}
