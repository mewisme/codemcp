package approval

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/commandpattern"
	"go.mewis.me/codemcp/internal/controlguard"
	mcpconfigwire "go.mewis.me/codemcp/internal/mcpconfig/wire"
)

type ReviewDecision string

const (
	ReviewApprove ReviewDecision = "approve"
	ReviewDeny    ReviewDecision = "deny"
)

type ReviewInput struct {
	Request      string
	Decision     ReviewDecision
	ResolvedBy   string
	Reason       string
	AllowSimilar bool
}

type ReviewService struct {
	manager *Manager
}

func NewReviewService(manager *Manager) *ReviewService {
	return &ReviewService{manager: manager}
}

func (s *ReviewService) List(filter Filter) ([]Request, error) {
	if s == nil || s.manager == nil {
		return nil, errors.New("approval manager is unavailable")
	}
	return PublicRequests(s.manager.List(filter)), nil
}

func (s *ReviewService) View(reference string) (Request, error) {
	if s == nil || s.manager == nil {
		return Request{}, errors.New("approval manager is unavailable")
	}
	value, err := s.manager.Resolve(reference)
	return PublicRequest(value), err
}

func (s *ReviewService) Resolve(input ReviewInput) (Request, error) {
	if s == nil || s.manager == nil {
		return Request{}, errors.New("approval manager is unavailable")
	}
	input.Request = strings.TrimSpace(input.Request)
	input.ResolvedBy = strings.TrimSpace(input.ResolvedBy)
	input.Reason = strings.TrimSpace(input.Reason)
	switch input.Decision {
	case ReviewApprove, ReviewDeny:
	default:
		return Request{}, fmt.Errorf("unsupported approval review decision: %s", input.Decision)
	}
	if input.AllowSimilar && input.Decision != ReviewApprove {
		return Request{}, errors.New("similar-command grants require approval")
	}

	m := s.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC()
	m.purgeExpiredLocked(now)
	record, err := m.resolveRecordLocked(input.Request)
	if err != nil {
		return Request{}, err
	}
	if record.value.Status != StatusPending {
		return Request{}, fmt.Errorf("%w: %s", ErrRequestResolved, record.value.Status)
	}

	if input.Decision == ReviewApprove && input.AllowSimilar {
		if record.value.TargetTool == mcpconfigwire.SetToolName {
			return Request{}, errors.New("config_set approval requests do not support runtime session grants")
		}
		if record.value.GuardCode == controlguard.CodeDestructiveMutation {
			return Request{}, errors.New("destructive approval requests do not support similar-command runtime grants")
		}
		pattern, err := commandpattern.Parse(record.value.SimilarCommandPattern)
		if err != nil || strings.TrimSpace(record.value.Command) == "" {
			return Request{}, errors.New("approval request does not support a similar-command runtime grant")
		}
		ttl := m.runtimeGrantTTL
		if ttl <= 0 {
			ttl = DefaultRuntimeGrantTTL
		}
		if ttl > MaxRuntimeGrantTTL {
			ttl = MaxRuntimeGrantTTL
		}
		expiresAt := now.Add(ttl)
		record.value.Status, record.value.ResolvedAt, record.value.ResolvedBy, record.value.Reason = StatusApproved, now, input.ResolvedBy, input.Reason
		record.value.RetryUntil = time.Time{}
		record.value.RuntimeSessionGrant = true
		record.value.GrantExpiresAt = expiresAt
		m.runtimeGrants = append(m.runtimeGrants, runtimeGrant{
			requestID: record.value.ID, workspaceID: record.value.WorkspaceID, targetTool: record.value.TargetTool,
			pattern: pattern, createdAt: now, expiresAt: expiresAt,
		})
		m.clearActiveLocked(record.value)
		m.closeResolvedLocked(record)
		m.emitLocked(EventApproved, record.value)
		return PublicRequest(record.value), nil
	}

	status := StatusDenied
	if input.Decision == ReviewApprove {
		status = StatusApproved
	}
	record.value.Status, record.value.ResolvedAt, record.value.ResolvedBy, record.value.Reason = status, now, input.ResolvedBy, input.Reason
	if status == StatusApproved {
		record.value.RetryUntil = now.Add(m.retryTTL)
	} else {
		m.clearActiveLocked(record.value)
	}
	m.closeResolvedLocked(record)
	if status == StatusApproved {
		m.emitLocked(EventApproved, record.value)
	} else {
		m.emitLocked(EventDenied, record.value)
	}
	return PublicRequest(record.value), nil
}

func (s *ReviewService) ListGrants(workspaceID string) ([]Request, error) {
	if s == nil || s.manager == nil {
		return nil, errors.New("approval manager is unavailable")
	}
	return PublicRequests(s.manager.ListRuntimeGrants(strings.TrimSpace(workspaceID))), nil
}

func (s *ReviewService) RevokeGrant(reference string) (Request, error) {
	if s == nil || s.manager == nil {
		return Request{}, errors.New("approval manager is unavailable")
	}
	m := s.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC()
	m.purgeExpiredLocked(now)
	record, err := m.resolveRecordLocked(reference)
	if err != nil {
		return Request{}, err
	}
	if !record.value.RuntimeSessionGrant || record.value.Status != StatusApproved {
		return Request{}, ErrRuntimeGrantNotFound
	}
	m.removeRuntimeGrantLocked(record.value.ID)
	record.value.Status, record.value.ResolvedAt, record.value.Reason = StatusExpired, now, "runtime session grant revoked"
	record.value.GrantExpiresAt = now
	m.emitLockedWithSubject(EventRevoked, EventSubjectGrant, record.value)
	return PublicRequest(record.value), nil
}

func (s *ReviewService) RevokeGrants(workspaceID string) (int, error) {
	if s == nil || s.manager == nil {
		return 0, errors.New("approval manager is unavailable")
	}
	workspaceID = strings.TrimSpace(workspaceID)
	m := s.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC()
	m.purgeExpiredLocked(now)
	changed := 0
	for index := len(m.runtimeGrants) - 1; index >= 0; index-- {
		grant := m.runtimeGrants[index]
		if workspaceID != "" && grant.workspaceID != workspaceID {
			continue
		}
		record := m.requests[grant.requestID]
		m.runtimeGrants = append(m.runtimeGrants[:index], m.runtimeGrants[index+1:]...)
		if record != nil && record.value.RuntimeSessionGrant && record.value.Status == StatusApproved {
			record.value.Status, record.value.ResolvedAt, record.value.Reason = StatusExpired, now, "runtime session grant revoked"
			record.value.GrantExpiresAt = now
			m.emitLockedWithSubject(EventRevoked, EventSubjectGrant, record.value)
		}
		changed++
	}
	return changed, nil
}

func (m *Manager) resolveRecordLocked(reference string) (*requestRecord, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return nil, ErrRequestNotFound
	}
	if record := m.requests[reference]; record != nil {
		return record, nil
	}
	var matched *requestRecord
	for id, record := range m.requests {
		if !strings.HasPrefix(id, reference) {
			continue
		}
		if matched != nil {
			return nil, fmt.Errorf("%w: %s", ErrRequestAmbiguous, reference)
		}
		matched = record
	}
	if matched == nil {
		return nil, fmt.Errorf("%w: %s", ErrRequestNotFound, reference)
	}
	return matched, nil
}
