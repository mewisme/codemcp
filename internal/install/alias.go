package install

import (
	"context"
	"errors"
	"fmt"
	"os"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

var ErrAliasConflict = errors.New("alias path is occupied by another file")

type AliasState string

const (
	AliasMissing   AliasState = "missing"
	AliasInstalled AliasState = "installed"
	AliasConflict  AliasState = "conflict"
)

type AliasStatus struct {
	State  AliasState
	Path   string
	Target string
}

func StatusAlias(layout Layout) (AliasStatus, error) {
	return statusAliasPlatform(layout)
}

func StatusAliasContext(ctx context.Context, layout Layout) (status AliasStatus, err error) {
	span := tracepkg.Start(ctx, "INSTALL", "install.alias.status", "Inspecting command alias", tracepkg.String("path", layout.AliasPath))
	status, err = StatusAlias(layout)
	if err != nil {
		span.FailMessage("Command alias inspection failed", err, tracepkg.String("path", layout.AliasPath))
		return AliasStatus{}, err
	}
	span.EndMessage("Command alias inspected", tracepkg.String("state", string(status.State)), tracepkg.String("path", status.Path), tracepkg.String("target", status.Target))
	return status, nil
}

func InstallAlias(layout Layout) (AliasStatus, error) {
	if err := ensureCurrentBinary(layout); err != nil {
		return AliasStatus{}, err
	}
	status, err := StatusAlias(layout)
	if err != nil {
		return AliasStatus{}, err
	}
	switch status.State {
	case AliasInstalled:
		return status, nil
	case AliasConflict:
		return status, fmt.Errorf("%w: %s", ErrAliasConflict, status.Path)
	}
	if err := installAliasPlatform(layout); err != nil {
		return AliasStatus{}, err
	}
	return StatusAlias(layout)
}

func InstallAliasContext(ctx context.Context, layout Layout) (status AliasStatus, err error) {
	span := tracepkg.Start(ctx, "INSTALL", "install.alias.install-standalone", "Installing command alias", tracepkg.String("path", layout.AliasPath), tracepkg.String("target", layout.CurrentBinary))
	status, err = InstallAlias(layout)
	if err != nil {
		span.FailMessage("Command alias installation failed", err, tracepkg.String("path", layout.AliasPath), tracepkg.String("target", layout.CurrentBinary))
		return AliasStatus{}, err
	}
	span.EndMessage("Command alias installed", tracepkg.String("state", string(status.State)), tracepkg.String("path", status.Path), tracepkg.String("target", status.Target))
	return status, nil
}

func RemoveAlias(layout Layout) (AliasStatus, error) {
	status, err := StatusAlias(layout)
	if err != nil {
		return AliasStatus{}, err
	}
	if status.State == AliasMissing {
		return status, nil
	}
	if status.State == AliasConflict {
		return status, fmt.Errorf("%w: %s", ErrAliasConflict, status.Path)
	}
	if err := removeAliasPlatform(layout); err != nil {
		return AliasStatus{}, err
	}
	return StatusAlias(layout)
}

func RemoveAliasContext(ctx context.Context, layout Layout) (status AliasStatus, err error) {
	span := tracepkg.Start(ctx, "INSTALL", "install.alias.remove", "Removing command alias", tracepkg.String("path", layout.AliasPath))
	status, err = RemoveAlias(layout)
	if err != nil {
		span.FailMessage("Command alias removal failed", err, tracepkg.String("path", layout.AliasPath))
		return AliasStatus{}, err
	}
	span.EndMessage("Command alias removed", tracepkg.String("state", string(status.State)), tracepkg.String("path", status.Path), tracepkg.String("target", status.Target))
	return status, nil
}

func ensureCurrentBinary(layout Layout) error {
	if _, _, err := CurrentVersion(layout); err != nil {
		return err
	}
	info, err := os.Stat(layout.CurrentBinary)
	if err != nil {
		return fmt.Errorf("current binary unavailable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("current binary is not a regular file: %s", layout.CurrentBinary)
	}
	return nil
}
