package update

import (
	"errors"
	"fmt"
	"strings"

	"go.mewis.me/codemcp/internal/install"
)

var ErrSelfUpdateUnavailable = errors.New("self-update unavailable for this installation")

type PolicyAction string

const (
	PolicySelfUpdate   PolicyAction = "self-update"
	PolicyDelegate     PolicyAction = "delegate"
	PolicyInstallFirst PolicyAction = "install-first"
	PolicyUnsupported  PolicyAction = "unsupported"
)

type InstallPolicy struct {
	Method  install.Method
	Action  PolicyAction
	Message string
	Command string
}

type PackageManagerCommand struct {
	Name string
	Args []string
}

type PackageManagerPlan struct {
	Method  install.Method
	Name    string
	Refresh PackageManagerCommand
	Apply   PackageManagerCommand
}

func PolicyForInstallation(detection install.Detection) InstallPolicy {
	policy := InstallPolicy{Method: detection.Method}
	switch detection.Method {
	case install.MethodDirect:
		if detection.Metadata == nil {
			policy.Action = PolicyInstallFirst
			policy.Message = "Direct installation is not managed yet"
			policy.Command = "cm install"
			return policy
		}
		if detection.Metadata.Method != install.MethodDirect {
			policy.Action = PolicyUnsupported
			policy.Message = fmt.Sprintf("Install metadata is managed as %s", detection.Metadata.Method)
			return policy
		}
		policy.Action = PolicySelfUpdate
		policy.Message = "Managed direct installation"
		return policy
	case install.MethodHomebrew:
		policy.Action = PolicyDelegate
		policy.Message = "Managed by Homebrew"
		policy.Command = "cm upgrade"
	case install.MethodScoop:
		policy.Action = PolicyDelegate
		policy.Message = "Managed by Scoop"
		policy.Command = "cm upgrade"
	case install.MethodGo:
		policy.Action = PolicyUnsupported
		policy.Message = "Self-update is unavailable for Go installations"
	case install.MethodDevelopment:
		policy.Action = PolicyUnsupported
		policy.Message = "Development builds cannot self-update"
	case install.MethodStandalone:
		policy.Action = PolicyInstallFirst
		policy.Message = "Standalone binary is not installed into the managed layout"
		policy.Command = "cm install"
	default:
		policy.Action = PolicyUnsupported
		policy.Message = "Unable to determine how chatgpt-mcp was installed"
	}
	return policy
}

func PackageManagerPlanFor(method install.Method) (PackageManagerPlan, bool) {
	switch method {
	case install.MethodHomebrew:
		return PackageManagerPlan{
			Method:  method,
			Name:    "Homebrew",
			Refresh: PackageManagerCommand{Name: "brew", Args: []string{"update"}},
			Apply:   PackageManagerCommand{Name: "brew", Args: []string{"upgrade", "--cask", "chatgpt-mcp"}},
		}, true
	case install.MethodScoop:
		return PackageManagerPlan{
			Method:  method,
			Name:    "Scoop",
			Refresh: PackageManagerCommand{Name: "scoop", Args: []string{"update"}},
			Apply:   PackageManagerCommand{Name: "scoop", Args: []string{"update", "mew/chatgpt-mcp"}},
		}, true
	default:
		return PackageManagerPlan{}, false
	}
}

func (p InstallPolicy) Error() error {
	if p.Action == PolicySelfUpdate || p.Action == PolicyDelegate {
		return nil
	}
	message := strings.TrimSpace(p.Message)
	if message == "" {
		message = string(p.Method)
	}
	if p.Command != "" {
		return fmt.Errorf("%w: %s; run: %s", ErrSelfUpdateUnavailable, message, p.Command)
	}
	return fmt.Errorf("%w: %s", ErrSelfUpdateUnavailable, message)
}
