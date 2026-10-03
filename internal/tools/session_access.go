package tools

import (
	"errors"
	"strings"
	"sync"
	"time"
)

const (
	defaultSessionWorkspaceAccessTTL  = 30 * 24 * time.Hour
	defaultSessionAccessMaxSessions   = 4096
	defaultSessionAccessMaxWorkspaces = 256
)

type SessionWorkspaceAccessDecision string

const (
	SessionWorkspaceAccessNew      SessionWorkspaceAccessDecision = "new"
	SessionWorkspaceAccessExisting SessionWorkspaceAccessDecision = "existing"
	SessionWorkspaceAccessClaimed  SessionWorkspaceAccessDecision = "claimed"
)

type WorkspaceAccess struct {
	WorkspaceID string
	GrantedAt   time.Time
	LastSeen    time.Time
}

type SessionWorkspaceAccess struct {
	Workspaces map[string]WorkspaceAccess
	CreatedAt  time.Time
	LastSeen   time.Time
}

type SessionWorkspaceAccessManager struct {
	mu            sync.Mutex
	sessions      map[string]SessionWorkspaceAccess
	now           func() time.Time
	ttl           time.Duration
	maxSessions   int
	maxWorkspaces int
}

func NewSessionWorkspaceAccessManager() *SessionWorkspaceAccessManager {
	return &SessionWorkspaceAccessManager{sessions: map[string]SessionWorkspaceAccess{}, now: time.Now, ttl: defaultSessionWorkspaceAccessTTL, maxSessions: defaultSessionAccessMaxSessions, maxWorkspaces: defaultSessionAccessMaxWorkspaces}
}

func (m *SessionWorkspaceAccessManager) CheckOrGrant(sessionID, workspaceID string) (WorkspaceAccess, SessionWorkspaceAccessDecision, int, error) {
	sessionID = strings.TrimSpace(sessionID)
	workspaceID = strings.TrimSpace(workspaceID)
	if sessionID == "" {
		return WorkspaceAccess{}, "", 0, errors.New("MCP session id is required")
	}
	if workspaceID == "" {
		return WorkspaceAccess{}, "", 0, errors.New("workspace id is required")
	}
	key := mcpSessionStateKey(sessionID)
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	m.purgeExpiredLocked(now)
	session, ok := m.sessions[key]
	if !ok {
		m.evictOldestSessionLocked()
		session = SessionWorkspaceAccess{Workspaces: map[string]WorkspaceAccess{}, CreatedAt: now}
	}
	if grant, exists := session.Workspaces[workspaceID]; exists {
		grant.LastSeen = now
		session.Workspaces[workspaceID] = grant
		session.LastSeen = now
		m.sessions[key] = session
		return grant, SessionWorkspaceAccessExisting, len(session.Workspaces), nil
	}
	m.evictOldestWorkspaceLocked(&session)
	grant := WorkspaceAccess{WorkspaceID: workspaceID, GrantedAt: now, LastSeen: now}
	session.Workspaces[workspaceID] = grant
	session.LastSeen = now
	m.sessions[key] = session
	return grant, SessionWorkspaceAccessNew, len(session.Workspaces), nil
}

func (m *SessionWorkspaceAccessManager) Lookup(sessionID string) (SessionWorkspaceAccess, bool) {
	if m == nil {
		return SessionWorkspaceAccess{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.purgeExpiredLocked(m.now())
	session, ok := m.sessions[mcpSessionStateKey(sessionID)]
	if !ok {
		return SessionWorkspaceAccess{}, false
	}
	session.Workspaces = cloneWorkspaceAccess(session.Workspaces)
	return session, true
}

func (m *SessionWorkspaceAccessManager) Delete(sessionID string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	delete(m.sessions, mcpSessionStateKey(sessionID))
	m.mu.Unlock()
}

func (m *SessionWorkspaceAccessManager) purgeExpiredLocked(now time.Time) int {
	if m.ttl <= 0 {
		return 0
	}
	removed := 0
	for sessionID, session := range m.sessions {
		if now.Sub(session.LastSeen) < m.ttl {
			continue
		}
		delete(m.sessions, sessionID)
		removed++
	}
	return removed
}

func (m *SessionWorkspaceAccessManager) PurgeExpired() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.purgeExpiredLocked(m.now())
}

func (m *SessionWorkspaceAccessManager) Count() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.purgeExpiredLocked(m.now())
	return len(m.sessions)
}

func (m *SessionWorkspaceAccessManager) evictOldestSessionLocked() {
	if m.maxSessions <= 0 || len(m.sessions) < m.maxSessions {
		return
	}
	oldestKey := ""
	var oldest time.Time
	for key, session := range m.sessions {
		if oldestKey == "" || session.LastSeen.Before(oldest) {
			oldestKey, oldest = key, session.LastSeen
		}
	}
	if oldestKey != "" {
		delete(m.sessions, oldestKey)
	}
}

func (m *SessionWorkspaceAccessManager) evictOldestWorkspaceLocked(session *SessionWorkspaceAccess) {
	if session == nil || m.maxWorkspaces <= 0 || len(session.Workspaces) < m.maxWorkspaces {
		return
	}
	oldestID := ""
	var oldest time.Time
	for workspaceID, grant := range session.Workspaces {
		if oldestID == "" || grant.LastSeen.Before(oldest) {
			oldestID, oldest = workspaceID, grant.LastSeen
		}
	}
	if oldestID != "" {
		delete(session.Workspaces, oldestID)
	}
}

func cloneWorkspaceAccess(values map[string]WorkspaceAccess) map[string]WorkspaceAccess {
	result := make(map[string]WorkspaceAccess, len(values))
	for id, value := range values {
		result[id] = value
	}
	return result
}
