package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	toolLoopHistoryLimit = 32
	toolLoopSessionTTL   = 30 * time.Minute
	toolLoopMaxSessions  = 4096
)

type toolLoopClass string

const (
	toolLoopClassContext  toolLoopClass = "context"
	toolLoopClassRead     toolLoopClass = "read"
	toolLoopClassMutation toolLoopClass = "mutation"
	toolLoopClassExempt   toolLoopClass = "exempt"
)

type ToolLoopGuard struct {
	mu          sync.Mutex
	sessions    map[string]*toolLoopSession
	now         func() time.Time
	maxSessions int
}

type toolLoopSession struct {
	history             []toolLoopRecord
	progress            uint64
	lastMutation        string
	lastMutationRepeats int
	lastSeen            time.Time
}

type toolLoopRecord struct {
	tool        string
	fingerprint string
	progress    uint64
}

type toolLoopDecision struct {
	blocked bool
	warn    bool
	reason  string
	repeats int
}

func NewToolLoopGuard() *ToolLoopGuard {
	return &ToolLoopGuard{sessions: map[string]*toolLoopSession{}, now: time.Now, maxSessions: toolLoopMaxSessions}
}

func (g *ToolLoopGuard) Check(sessionID, name string, args map[string]any, class toolLoopClass) toolLoopDecision {
	if g == nil || strings.TrimSpace(sessionID) == "" || class == toolLoopClassExempt {
		return toolLoopDecision{}
	}
	fingerprint := toolCallFingerprint(name, args)
	now := g.clock()()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.purgeLocked(now)
	session := g.sessionLocked(sessionID, now)
	if class == toolLoopClassMutation {
		repeats := 1
		if session.lastMutation == fingerprint {
			repeats = session.lastMutationRepeats + 1
		}
		decision := toolLoopDecision{warn: repeats == 2, repeats: repeats}
		if repeats >= 3 {
			decision.blocked = true
			decision.reason = "duplicate_mutation"
		}
		return decision
	}
	record := toolLoopRecord{tool: name, fingerprint: fingerprint, progress: session.progress}
	consecutive := consecutiveFingerprintCount(session.history, record) + 1
	exactLimit := 4
	if class == toolLoopClassContext {
		exactLimit = 3
	}
	decision := toolLoopDecision{warn: consecutive == exactLimit-1, repeats: consecutive}
	if consecutive >= exactLimit {
		decision.blocked = true
		decision.reason = "exact_duplicate"
		return decision
	}
	prospective := append(append([]toolLoopRecord(nil), session.history...), record)
	if cycleLength, ok := repeatedTailCycle(prospective, session.progress); ok {
		decision.blocked = true
		decision.reason = fmt.Sprintf("repeated_cycle_%d", cycleLength)
		decision.repeats = 3
		return decision
	}
	session.history = append(session.history, record)
	if len(session.history) > toolLoopHistoryLimit {
		session.history = append([]toolLoopRecord(nil), session.history[len(session.history)-toolLoopHistoryLimit:]...)
	}
	return decision
}

func (g *ToolLoopGuard) MarkMutationSuccess(sessionID, name string, args map[string]any) {
	if g == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	fingerprint := toolCallFingerprint(name, args)
	now := g.clock()()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.purgeLocked(now)
	session := g.sessionLocked(sessionID, now)
	if session.lastMutation == fingerprint {
		session.lastMutationRepeats++
	} else {
		session.lastMutation = fingerprint
		session.lastMutationRepeats = 1
	}
	session.progress++
	session.history = nil
}

func (g *ToolLoopGuard) MarkProgress(sessionID string) {
	if g == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	now := g.clock()()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.purgeLocked(now)
	session := g.sessionLocked(sessionID, now)
	session.progress++
	session.history = nil
	session.lastMutation = ""
	session.lastMutationRepeats = 0
}

func (g *ToolLoopGuard) Delete(sessionID string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	delete(g.sessions, mcpSessionStateKey(sessionID))
	g.mu.Unlock()
}

func (g *ToolLoopGuard) clock() func() time.Time {
	if g.now != nil {
		return g.now
	}
	return time.Now
}

func (g *ToolLoopGuard) sessionLocked(sessionID string, now time.Time) *toolLoopSession {
	key := mcpSessionStateKey(sessionID)
	session := g.sessions[key]
	if session == nil {
		g.evictOldestSessionLocked()
		session = &toolLoopSession{}
		g.sessions[key] = session
	}
	session.lastSeen = now
	return session
}

func (g *ToolLoopGuard) purgeLocked(now time.Time) {
	for id, session := range g.sessions {
		if session == nil || now.Sub(session.lastSeen) >= toolLoopSessionTTL {
			delete(g.sessions, id)
		}
	}
}

func (g *ToolLoopGuard) evictOldestSessionLocked() {
	if g.maxSessions <= 0 || len(g.sessions) < g.maxSessions {
		return
	}
	oldestKey := ""
	var oldest time.Time
	for key, session := range g.sessions {
		if session == nil {
			delete(g.sessions, key)
			return
		}
		if oldestKey == "" || session.lastSeen.Before(oldest) {
			oldestKey, oldest = key, session.lastSeen
		}
	}
	if oldestKey != "" {
		delete(g.sessions, oldestKey)
	}
}

func consecutiveFingerprintCount(history []toolLoopRecord, record toolLoopRecord) int {
	count := 0
	for index := len(history) - 1; index >= 0; index-- {
		item := history[index]
		if item.progress != record.progress || item.fingerprint != record.fingerprint {
			break
		}
		count++
	}
	return count
}

func repeatedTailCycle(history []toolLoopRecord, progress uint64) (int, bool) {
	start := 0
	for index := len(history) - 1; index >= 0; index-- {
		if history[index].progress != progress {
			start = index + 1
			break
		}
	}
	history = history[start:]
	for cycleLength := 2; cycleLength <= 4; cycleLength++ {
		need := cycleLength * 3
		if len(history) < need {
			continue
		}
		tail := history[len(history)-need:]
		match := true
		for index := cycleLength; index < len(tail); index++ {
			if tail[index].fingerprint != tail[index%cycleLength].fingerprint {
				match = false
				break
			}
		}
		if match {
			return cycleLength, true
		}
	}
	return 0, false
}

func toolCallFingerprint(name string, args map[string]any) string {
	data, err := json.Marshal(args)
	if err != nil {
		data = []byte(fmt.Sprintf("%v", args))
	}
	sum := sha256.Sum256(append([]byte(strings.TrimSpace(name)+"\n"), data...))
	return hex.EncodeToString(sum[:])
}

func toolLoopClassFor(name string, schema Schema, args map[string]any) toolLoopClass {
	switch strings.TrimSpace(name) {
	case "process_status", "process_output", "workspace_status", "shell_status", "agent_status", "get_version", "request_control_approval", AgentCompleteToolName, AgentClaimToolName:
		return toolLoopClassExempt
	case "project_context", "load_path_rules", "list_skills", "load_skill":
		return toolLoopClassContext
	case "git_branch", "git_stash":
		if toolAction(args, "list") == "list" {
			return toolLoopClassRead
		}
		return toolLoopClassMutation
	case "rewind":
		switch toolAction(args, "list") {
		case "list", "status", "preview":
			return toolLoopClassRead
		default:
			return toolLoopClassMutation
		}
	case "node_repl":
		if toolAction(args, "eval") == "status" {
			return toolLoopClassRead
		}
		return toolLoopClassMutation
	}
	if readOnly, _ := schema.Annotations["readOnlyHint"].(bool); readOnly {
		return toolLoopClassRead
	}
	return toolLoopClassMutation
}

func toolAction(args map[string]any, fallback string) string {
	if value, ok := args["action"].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func toolLoopBlockedResult(name string, decision toolLoopDecision) Result {
	message := fmt.Sprintf("Tool loop detected: %s is repeating without an intervening state-changing action. Reuse the previous result or choose a different action.", name)
	if decision.reason == "duplicate_mutation" {
		message = fmt.Sprintf("Duplicate mutation blocked: %s has already completed with the same arguments twice without a different mutation in between. Inspect current state before retrying or change the action.", name)
	}
	result := ErrorResult(fmt.Errorf("%s", message))
	result.Meta = map[string]any{"loopGuard": map[string]any{"blocked": true, "reason": decision.reason, "repeats": decision.repeats, "tool": name}}
	return result
}

func addToolLoopWarning(result Result, name string, decision toolLoopDecision) Result {
	if !decision.warn || decision.blocked {
		return result
	}
	if result.Meta == nil {
		result.Meta = map[string]any{}
	} else {
		result.Meta = cloneMap(result.Meta)
	}
	result.Meta["loopGuard"] = map[string]any{"warning": true, "repeats": decision.repeats, "tool": name}
	return result
}
