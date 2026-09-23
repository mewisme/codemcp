package install

import (
	"errors"
	"fmt"
	"os"
)

var ErrCanonicalConflict = errors.New("canonical command path is occupied by another file")

type CanonicalState string

const (
	CanonicalMissing   CanonicalState = "missing"
	CanonicalInstalled CanonicalState = "installed"
	CanonicalConflict  CanonicalState = "conflict"
)

type CanonicalStatus struct {
	State  CanonicalState
	Path   string
	Target string
}

func StatusCanonical(layout Layout) (CanonicalStatus, error) {
	return statusCanonicalPlatform(layout)
}

func InstallCanonical(layout Layout) (CanonicalStatus, error) {
	if err := ensureCurrentBinary(layout); err != nil {
		return CanonicalStatus{}, err
	}
	status, err := StatusCanonical(layout)
	if err != nil {
		return CanonicalStatus{}, err
	}
	switch status.State {
	case CanonicalInstalled:
		return status, nil
	case CanonicalConflict:
		return status, fmt.Errorf("%w: %s", ErrCanonicalConflict, status.Path)
	}
	if err := installCanonicalPlatform(layout); err != nil {
		return CanonicalStatus{}, err
	}
	return StatusCanonical(layout)
}

func RemoveCanonical(layout Layout) (CanonicalStatus, error) {
	status, err := StatusCanonical(layout)
	if err != nil {
		return CanonicalStatus{}, err
	}
	if status.State == CanonicalMissing {
		return status, nil
	}
	if status.State == CanonicalConflict {
		return status, fmt.Errorf("%w: %s", ErrCanonicalConflict, status.Path)
	}
	if err := removeCanonicalPlatform(layout); err != nil {
		return CanonicalStatus{}, err
	}
	return StatusCanonical(layout)
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
