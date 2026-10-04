package agent

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	ClaimEntropyBytes = 32
)

var (
	DefaultClaimTTL     = 2 * time.Minute
	ErrClaimRejected    = errors.New("managed agent claim rejected")
	ErrClaimOutstanding = errors.New("managed agent already has an outstanding claim")
	ErrSessionBound     = errors.New("MCP session is already bound to another managed agent")
)

type ClaimCredential struct {
	token     string
	ExpiresAt time.Time
}

func (credential ClaimCredential) Token() string {
	return credential.token
}

func (ClaimCredential) String() string {
	return "<managed-agent-claim>"
}

func (ClaimCredential) GoString() string {
	return "agent.ClaimCredential{<redacted>}"
}

type ClaimBinding struct {
	AgentID     ID
	WorkspaceID string
	Backend     BackendID
	Active      bool
}

type claimState struct {
	digest    [sha256.Size]byte
	expiresAt time.Time
	issued    bool
}

type sessionClaimBinding struct {
	AgentID     ID
	WorkspaceID string
	Backend     BackendID
	Active      bool
}

func (manager *Manager) IssueClaim(id ID) (ClaimCredential, error) {
	if manager == nil {
		return ClaimCredential{}, errors.New("managed agent manager is unavailable")
	}
	if err := ValidateID(id); err != nil {
		return ClaimCredential{}, err
	}
	tokenBytes := make([]byte, ClaimEntropyBytes)
	if _, err := rand.Read(tokenBytes); err != nil {
		return ClaimCredential{}, fmt.Errorf("generate managed agent claim: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	digest := sha256.Sum256([]byte(token))
	now := manager.now().UTC()
	expiresAt := now.Add(manager.claimTTL)

	manager.mu.Lock()
	defer manager.mu.Unlock()
	item := manager.entries[id]
	if item == nil || item.record.State.Terminal() {
		return ClaimCredential{}, ErrClaimRejected
	}
	if item.hasClaimedSession {
		return ClaimCredential{}, ErrClaimRejected
	}
	if item.claim.issued && now.Before(item.claim.expiresAt) {
		return ClaimCredential{}, ErrClaimOutstanding
	}
	item.claim = claimState{digest: digest, expiresAt: expiresAt, issued: true}
	return ClaimCredential{token: token, ExpiresAt: expiresAt}, nil
}

func (manager *Manager) ConsumeClaim(id ID, token, sessionID string) (ClaimBinding, error) {
	if manager == nil {
		return ClaimBinding{}, errors.New("managed agent manager is unavailable")
	}
	if err := ValidateID(id); err != nil {
		return ClaimBinding{}, err
	}
	token = strings.TrimSpace(token)
	sessionID = strings.TrimSpace(sessionID)
	if token == "" || sessionID == "" {
		return ClaimBinding{}, ErrClaimRejected
	}
	tokenDigest := sha256.Sum256([]byte(token))
	sessionDigest := sha256.Sum256([]byte(sessionID))
	now := manager.now().UTC()

	manager.mu.Lock()
	defer manager.mu.Unlock()
	item := manager.entries[id]
	if item == nil || item.record.State.Terminal() {
		return ClaimBinding{}, ErrClaimRejected
	}
	if existing, ok := manager.claimedSessions[sessionDigest]; ok && existing.AgentID != id {
		return ClaimBinding{}, ErrSessionBound
	}

	stored := item.claim.digest
	valid := item.claim.issued &&
		now.Before(item.claim.expiresAt) &&
		subtle.ConstantTimeCompare(stored[:], tokenDigest[:]) == 1
	if !valid {
		if item.claim.issued && !now.Before(item.claim.expiresAt) {
			item.claim = claimState{}
		}
		return ClaimBinding{}, ErrClaimRejected
	}

	binding := sessionClaimBinding{
		AgentID: id, WorkspaceID: item.record.WorkspaceID,
		Backend: item.record.Backend, Active: true,
	}
	item.claim = claimState{}
	item.claimedSession = sessionDigest
	item.hasClaimedSession = true
	manager.claimedSessions[sessionDigest] = binding
	return ClaimBinding(binding), nil
}

func (manager *Manager) SessionBinding(sessionID string) (ClaimBinding, bool) {
	if manager == nil {
		return ClaimBinding{}, false
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ClaimBinding{}, false
	}
	key := sha256.Sum256([]byte(sessionID))
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	binding, ok := manager.claimedSessions[key]
	if !ok {
		return ClaimBinding{}, false
	}
	return ClaimBinding(binding), true
}

func (manager *Manager) PurgeExpiredClaims() int {
	if manager == nil {
		return 0
	}
	now := manager.now().UTC()
	manager.mu.Lock()
	defer manager.mu.Unlock()
	removed := 0
	for _, item := range manager.entries {
		if !item.claim.issued || now.Before(item.claim.expiresAt) {
			continue
		}
		item.claim = claimState{}
		removed++
	}
	return removed
}

func (manager *Manager) RevokeAllClaimsAndBindings() {
	if manager == nil {
		return
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for _, item := range manager.entries {
		item.claim = claimState{}
		item.hasClaimedSession = false
		item.claimedSession = [sha256.Size]byte{}
	}
	clear(manager.claimedSessions)
}

func (manager *Manager) revokeClaimAndBindingLocked(item *entry) {
	if item == nil {
		return
	}
	item.claim = claimState{}
	if item.hasClaimedSession {
		if binding, ok := manager.claimedSessions[item.claimedSession]; ok {
			binding.Active = false
			manager.claimedSessions[item.claimedSession] = binding
		}
	}
}

func (manager *Manager) forgetClaimBindingLocked(item *entry) {
	if item == nil || !item.hasClaimedSession {
		return
	}
	delete(manager.claimedSessions, item.claimedSession)
	item.hasClaimedSession = false
	item.claimedSession = [sha256.Size]byte{}
}
