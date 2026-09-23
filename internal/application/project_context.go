package application

import (
	"context"
	"time"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/instructioncontext"
)

func ProjectContextEnvironment() (bool, int) {
	cfg, err := config.Load()
	if err != nil {
		return false, 0
	}
	return cfg.Admin.Enabled, cfg.Admin.Port
}

func ProjectContextToolProfile(ctx context.Context) instructioncontext.ToolProfile {
	if ctx == nil {
		ctx = context.Background()
	}
	statusCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	status, running, err := RuntimeStatus(statusCtx)
	if err != nil || !running {
		return instructioncontext.ToolProfile{Name: "full"}
	}
	name := status.ToolProfile
	if name == "" {
		name = "full"
	}
	return instructioncontext.ToolProfile{Name: name, Count: status.ToolCount}
}
