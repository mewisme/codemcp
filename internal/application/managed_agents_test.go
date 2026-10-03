package application

import (
	"context"
	"strings"
	"testing"
	"time"

	managedagent "go.mewis.me/codemcp/internal/agent"
)

func TestManagedAgentServiceBoundsWaitBeforeManagerAccess(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	service := NewManagedAgentService(nil, nil)
	_, err := service.Wait(context.Background(), ManagedAgentWaitInput{
		AgentID:   "agent_0123456789abcdef",
		TimeoutMS: int(managedagent.MaxWaitDuration/time.Millisecond) + 1,
	})
	if err == nil || !strings.Contains(err.Error(), "timeout_ms must be between 0 and 10000") {
		t.Fatalf("unbounded application wait error=%v", err)
	}
}
