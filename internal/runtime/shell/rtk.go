package shell

import (
	"context"
	"path/filepath"
	"strings"

	"go.mewis.me/codemcp/internal/controlguard"
	"go.mewis.me/codemcp/internal/integrations/rtk"
)

type commandPlan struct {
	Requested string
	Effective string
	Security  string
	RTKPath   string
}

func (m *Manager) ConfigureRTK(enabled bool, configuredPath string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.rtk = rtk.New(rtk.Options{Enabled: enabled, ConfiguredPath: configuredPath})
	m.mu.Unlock()
}

func (m *Manager) prepareCommand(ctx context.Context, command string) (commandPlan, error) {
	requested := strings.TrimSpace(command)
	plan := commandPlan{Requested: requested, Effective: requested, Security: requested}
	if requested == "" || m == nil {
		return plan, nil
	}
	if _, approved := controlguard.ApprovalFromContext(ctx); approved {
		return plan, nil
	}
	m.mu.Lock()
	manager := m.rtk
	m.mu.Unlock()
	if manager == nil {
		return plan, nil
	}
	rewritten, err := manager.Rewrite(ctx, requested)
	if err != nil {
		return plan, err
	}
	if !rewritten.Rewritten {
		return plan, nil
	}
	plan.Effective = rewritten.Effective
	plan.RTKPath = rewritten.Executable
	return plan, nil
}

func commandSearchPath(plan commandPlan, configured []string) []string {
	result := make([]string, 0, len(configured)+1)
	if path := strings.TrimSpace(plan.RTKPath); path != "" {
		result = append(result, filepath.Dir(path))
	}
	for _, path := range configured {
		clean := filepath.Clean(strings.TrimSpace(path))
		if clean == "." || clean == "" {
			continue
		}
		duplicate := false
		for _, current := range result {
			if filepath.Clean(current) == clean {
				duplicate = true
				break
			}
		}
		if !duplicate {
			result = append(result, clean)
		}
	}
	return result
}
