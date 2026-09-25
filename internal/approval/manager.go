package approval

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"go.mewis.me/codemcp/internal/commandpattern"
	"go.mewis.me/codemcp/internal/idgen"
)

type challengeRecord struct {
	value Challenge
}

type requestRecord struct {
	value    Request
	resolved chan struct{}
	closed   bool
}

type cliCapabilityRecord struct {
	requestID string
	args      []string
	expiresAt time.Time
}

type runtimeGrant struct {
	requestID   string
	workspaceID string
	targetTool  string
	pattern     commandpattern.Pattern
	createdAt   time.Time
	expiresAt   time.Time
}

type Manager struct {
	mu                    sync.Mutex
	instanceID            string
	challenges            map[string]*challengeRecord
	challengeByTarget     map[string]string
	requests              map[string]*requestRecord
	activeByCaller        map[string]string
	cliCapabilities       map[string]*cliCapabilityRecord
	runtimeGrants         []runtimeGrant
	now                   func() time.Time
	newID                 func(string) (string, error)
	challengeTTL          time.Duration
	requestTTL            time.Duration
	retryTTL              time.Duration
	runtimeGrantTTL       time.Duration
	pendingLimit          int
	workspacePendingLimit int
	events                *EventStream
}

func NewManager(instanceID string) *Manager {
	return &Manager{
		instanceID: strings.TrimSpace(instanceID), challenges: map[string]*challengeRecord{}, challengeByTarget: map[string]string{}, requests: map[string]*requestRecord{}, activeByCaller: map[string]string{},
		cliCapabilities: map[string]*cliCapabilityRecord{}, runtimeGrants: []runtimeGrant{}, now: time.Now, newID: randomID, challengeTTL: DefaultChallengeTTL, requestTTL: DefaultRequestTTL, retryTTL: DefaultRetryTTL, runtimeGrantTTL: DefaultRuntimeGrantTTL, pendingLimit: DefaultPendingLimit, workspacePendingLimit: DefaultWorkspacePendingLimit,
		events: newEventStream(),
	}
}

func (m *Manager) Events() *EventStream {
	if m == nil {
		return nil
	}
	return m.events
}

func (m *Manager) CreateChallenge(input ChallengeInput) (Challenge, bool, error) {
	if m == nil {
		return Challenge{}, false, errors.New("approval manager is unavailable")
	}
	input.CallerID = strings.TrimSpace(input.CallerID)
	input.RequestCorrelationID = strings.TrimSpace(input.RequestCorrelationID)
	input.SessionHash = strings.TrimSpace(input.SessionHash)
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Source = strings.TrimSpace(input.Source)
	input.TargetTool = strings.TrimSpace(input.TargetTool)
	input.GuardReason = strings.TrimSpace(input.GuardReason)
	input.Title = strings.TrimSpace(input.Title)
	input.Command = strings.TrimSpace(input.Command)
	input.SimilarCommandPattern = strings.TrimSpace(input.SimilarCommandPattern)
	callerID := input.CallerID
	digest, arguments, err := CanonicalTargetDigest(m.instanceID, Target{CallerID: callerID, WorkspaceID: input.WorkspaceID, Source: input.Source, TargetTool: input.TargetTool, Arguments: input.Arguments, GuardCode: input.GuardCode})
	if err != nil {
		return Challenge{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC()
	m.purgeExpiredLocked(now)
	key := challengeTargetKey(callerID, digest)
	if id := m.challengeByTarget[key]; id != "" {
		if record := m.challenges[id]; record != nil {
			if request := m.requests[record.value.requestID]; request == nil || request.value.Status == StatusPending || request.value.Status == StatusApproved {
				record.value.ExpiresAt = now.Add(m.challengeTTL)
				return cloneChallenge(record.value), false, nil
			}
		}
		delete(m.challengeByTarget, key)
	}
	id, err := m.newID("chg")
	if err != nil {
		return Challenge{}, false, err
	}
	value := Challenge{
		ID: id, SessionHash: input.SessionHash, WorkspaceID: input.WorkspaceID, Source: input.Source, TargetTool: input.TargetTool, Arguments: arguments, Digest: digest,
		GuardCode: input.GuardCode, GuardReason: input.GuardReason, Title: input.Title, Command: input.Command, SimilarCommandPattern: input.SimilarCommandPattern, CreatedAt: now, ExpiresAt: now.Add(m.challengeTTL), callerID: callerID, requestCorrelationID: input.RequestCorrelationID,
	}
	m.challenges[id] = &challengeRecord{value: value}
	m.challengeByTarget[key] = id
	m.emitChallengeLocked(EventCreated, value)
	return cloneChallenge(value), true, nil
}

func (m *Manager) CreateRequest(challengeID, callerID, workspaceID string) (Request, bool, error) {
	return m.CreateRequestWithTitle(challengeID, callerID, workspaceID, "")
}

func (m *Manager) CreateRequestWithTitle(challengeID, callerID, workspaceID, title string) (Request, bool, error) {
	return m.CreateRequestWithCorrelation(challengeID, callerID, workspaceID, title)
}

func (m *Manager) CreateRequestWithCorrelation(challengeID, callerID, workspaceID, title string) (Request, bool, error) {
	if m == nil {
		return Request{}, false, errors.New("approval manager is unavailable")
	}
	challengeID, callerID, workspaceID, title = strings.TrimSpace(challengeID), strings.TrimSpace(callerID), strings.TrimSpace(workspaceID), strings.TrimSpace(title)
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC()
	challenge := m.challenges[challengeID]
	if challenge == nil {
		m.purgeExpiredLocked(now)
		return Request{}, false, ErrChallengeNotFound
	}
	if !now.Before(challenge.value.ExpiresAt) {
		m.removeChallengeLocked(challenge.value)
		m.purgeExpiredLocked(now)
		return Request{}, false, ErrChallengeExpired
	}
	m.purgeExpiredLocked(now)
	challenge = m.challenges[challengeID]
	if challenge == nil {
		return Request{}, false, ErrChallengeNotFound
	}
	if challenge.value.callerID != callerID || challenge.value.WorkspaceID != workspaceID {
		return Request{}, false, ErrChallengeMismatch
	}
	if title == "" {
		title = challenge.value.Title
	}
	if title == "" {
		return Request{}, false, errors.New("approval request title is required")
	}
	if utf8.RuneCountInString(title) > 120 {
		return Request{}, false, errors.New("approval request title must be at most 120 characters")
	}
	if challenge.value.requestID != "" {
		if request := m.requests[challenge.value.requestID]; request != nil {
			return cloneRequest(request.value), false, nil
		}
	}
	if activeID := m.activeByCaller[callerID]; activeID != "" {
		if active := m.requests[activeID]; active != nil && (active.value.Status == StatusPending || active.value.Status == StatusApproved) {
			if active.value.Digest != challenge.value.Digest {
				return Request{}, false, ErrSessionRequestActive
			}
			challenge.value.requestID = active.value.ID
			return cloneRequest(active.value), false, nil
		}
		delete(m.activeByCaller, callerID)
	}
	if m.pendingLimit > 0 && m.pendingCountLocked("") >= m.pendingLimit {
		return Request{}, false, ErrPendingLimit
	}
	if m.workspacePendingLimit > 0 && m.pendingCountLocked(workspaceID) >= m.workspacePendingLimit {
		return Request{}, false, fmt.Errorf("%w for workspace %s", ErrPendingLimit, workspaceID)
	}
	id, err := m.newID("req")
	if err != nil {
		return Request{}, false, err
	}
	value := Request{
		ID: id, Status: StatusPending, WorkspaceID: challenge.value.WorkspaceID, SessionHash: challenge.value.SessionHash, Source: challenge.value.Source, TargetTool: challenge.value.TargetTool,
		Arguments: cloneRaw(challenge.value.Arguments), Digest: challenge.value.Digest, GuardCode: challenge.value.GuardCode, GuardReason: challenge.value.GuardReason, Title: title, Command: challenge.value.Command, SimilarCommandPattern: challenge.value.SimilarCommandPattern,
		CreatedAt: now, ExpiresAt: now.Add(m.requestTTL), callerID: callerID, challengeID: challenge.value.ID,
	}
	m.requests[id] = &requestRecord{value: value, resolved: make(chan struct{})}
	m.activeByCaller[callerID] = id
	challenge.value.requestID = id
	m.emitLocked(EventPending, value)
	return cloneRequest(value), true, nil
}

func (m *Manager) Approve(id, resolvedBy, reason string) (Request, error) {
	return NewReviewService(m).Resolve(ReviewInput{Request: id, Decision: ReviewApprove, ResolvedBy: resolvedBy, Reason: reason})
}

func (m *Manager) ApproveRuntimeSession(id, resolvedBy, reason string) (Request, error) {
	return NewReviewService(m).Resolve(ReviewInput{Request: id, Decision: ReviewApprove, ResolvedBy: resolvedBy, Reason: reason, AllowSimilar: true})
}

func (m *Manager) RevokeRuntimeGrant(id string) (Request, error) {
	return NewReviewService(m).RevokeGrant(id)
}

func (m *Manager) RevokeRuntimeGrants(workspaceID string) int {
	changed, _ := NewReviewService(m).RevokeGrants(workspaceID)
	return changed
}

func (m *Manager) ListRuntimeGrants(workspaceID string) []Request {
	if m == nil {
		return nil
	}
	workspaceID = strings.TrimSpace(workspaceID)
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC()
	m.purgeExpiredLocked(now)
	values := make([]Request, 0, len(m.runtimeGrants))
	for _, grant := range m.runtimeGrants {
		if workspaceID != "" && grant.workspaceID != workspaceID {
			continue
		}
		record := m.requests[grant.requestID]
		if record == nil || !record.value.RuntimeSessionGrant || record.value.Status != StatusApproved {
			continue
		}
		values = append(values, cloneRequest(record.value))
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].GrantExpiresAt.Equal(values[j].GrantExpiresAt) {
			return values[i].ID < values[j].ID
		}
		return values[i].GrantExpiresAt.Before(values[j].GrantExpiresAt)
	})
	return values
}

func (m *Manager) Deny(id, resolvedBy, reason string) (Request, error) {
	return NewReviewService(m).Resolve(ReviewInput{Request: id, Decision: ReviewDeny, ResolvedBy: resolvedBy, Reason: reason})
}

func (m *Manager) Cancel(id, resolvedBy, reason string) (Request, error) {
	return m.resolve(id, StatusCancelled, resolvedBy, reason)
}

func (m *Manager) resolve(id string, status Status, resolvedBy, reason string) (Request, error) {
	if m == nil {
		return Request{}, errors.New("approval manager is unavailable")
	}
	id, resolvedBy, reason = strings.TrimSpace(id), strings.TrimSpace(resolvedBy), strings.TrimSpace(reason)
	if status != StatusApproved && status != StatusDenied && status != StatusCancelled {
		return Request{}, fmt.Errorf("unsupported approval resolution status: %s", status)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC()
	m.purgeExpiredLocked(now)
	record := m.requests[id]
	if record == nil {
		return Request{}, ErrRequestNotFound
	}
	if record.value.Status == status {
		return cloneRequest(record.value), nil
	}
	if record.value.Status != StatusPending {
		return Request{}, fmt.Errorf("%w: %s", ErrRequestResolved, record.value.Status)
	}
	record.value.Status, record.value.ResolvedAt, record.value.ResolvedBy, record.value.Reason = status, now, resolvedBy, reason
	if status == StatusApproved {
		record.value.RetryUntil = now.Add(m.retryTTL)
	} else {
		m.clearActiveLocked(record.value)
	}
	m.closeResolvedLocked(record)
	switch status {
	case StatusApproved:
		m.emitLocked(EventApproved, record.value)
	case StatusDenied:
		m.emitLocked(EventDenied, record.value)
	case StatusCancelled:
		m.emitLocked(EventCancelled, record.value)
	}
	return cloneRequest(record.value), nil
}

func (m *Manager) Wait(ctx context.Context, id string) (Request, error) {
	if m == nil {
		return Request{}, errors.New("approval manager is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	now := m.now().UTC()
	m.purgeExpiredLocked(now)
	record := m.requests[strings.TrimSpace(id)]
	if record == nil {
		m.mu.Unlock()
		return Request{}, ErrRequestNotFound
	}
	if record.value.Status != StatusPending {
		value := cloneRequest(record.value)
		m.mu.Unlock()
		return value, nil
	}
	resolved, expiresAt := record.resolved, record.value.ExpiresAt
	m.mu.Unlock()
	duration := expiresAt.Sub(now)
	if duration < 0 {
		duration = 0
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-resolved:
		value, ok := m.Get(id)
		if !ok {
			return Request{}, ErrRequestNotFound
		}
		return value, nil
	case <-timer.C:
		m.PurgeExpired()
		value, ok := m.Get(id)
		if !ok {
			return Request{}, ErrRequestNotFound
		}
		return value, nil
	case <-ctx.Done():
		value, ok := m.Get(id)
		if !ok {
			return Request{}, ErrRequestNotFound
		}
		return value, ctx.Err()
	}
}

func (m *Manager) MatchApproved(input RetryInput) (Request, bool, error) {
	if m == nil {
		return Request{}, false, errors.New("approval manager is unavailable")
	}
	input = normalizeRetryInput(input)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.purgeExpiredLocked(m.now().UTC())
	return m.matchApprovedLocked(input)
}

func (m *Manager) MatchRuntimeGrant(input RetryInput) (Request, bool) {
	if m == nil {
		return Request{}, false
	}
	input.WorkspaceID, input.TargetTool, input.Command = strings.TrimSpace(input.WorkspaceID), strings.TrimSpace(input.TargetTool), strings.TrimSpace(input.Command)
	if input.WorkspaceID == "" || input.TargetTool == "" || input.Command == "" {
		return Request{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC()
	m.purgeExpiredLocked(now)
	for index := len(m.runtimeGrants) - 1; index >= 0; index-- {
		grant := m.runtimeGrants[index]
		if grant.workspaceID != input.WorkspaceID || grant.targetTool != input.TargetTool {
			continue
		}
		if !now.Before(grant.expiresAt) {
			continue
		}
		argv, err := commandpattern.CommandWords(input.Command)
		if err != nil || !grant.pattern.Match(argv) {
			continue
		}
		record := m.requests[grant.requestID]
		if record == nil || record.value.Status != StatusApproved || !record.value.RuntimeSessionGrant {
			continue
		}
		return cloneRequest(record.value), true
	}
	return Request{}, false
}

func (m *Manager) ClaimApproved(input RetryInput) (Request, bool, error) {
	request, _, matched, err := m.ClaimApprovedCLI(input, CLIInvocation{})
	return request, matched, err
}

func (m *Manager) ClaimApprovedCLI(input RetryInput, cli CLIInvocation) (Request, string, bool, error) {
	if m == nil {
		return Request{}, "", false, errors.New("approval manager is unavailable")
	}
	input = normalizeRetryInput(input)
	cli.Program = strings.TrimSpace(cli.Program)
	cli.Args = append([]string(nil), cli.Args...)
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC()
	m.purgeExpiredLocked(now)
	request, matched, err := m.matchApprovedLocked(input)
	if err != nil || !matched {
		return request, "", matched, err
	}
	record := m.requests[request.ID]
	if record == nil || record.value.Status != StatusApproved {
		return Request{}, "", false, ErrRequestNotApproved
	}
	capability := ""
	if cli.Program != "" || len(cli.Args) > 0 {
		capability, err = m.newID("cap")
		if err != nil {
			return Request{}, "", false, err
		}
		expiresAt := now.Add(DefaultCLICapabilityTTL)
		if !record.value.RetryUntil.IsZero() && record.value.RetryUntil.Before(expiresAt) {
			expiresAt = record.value.RetryUntil
		}
		m.cliCapabilities[capability] = &cliCapabilityRecord{requestID: record.value.ID, args: append([]string(nil), cli.Args...), expiresAt: expiresAt}
	}
	record.value.Status, record.value.ConsumedAt = StatusConsumed, now
	m.clearActiveLocked(record.value)
	m.emitLocked(EventClaimed, record.value)
	return cloneRequest(record.value), capability, true, nil
}

func (m *Manager) ConsumeCLI(capability string, args []string) (string, error) {
	if m == nil {
		return "", errors.New("approval manager is unavailable")
	}
	capability = strings.TrimSpace(capability)
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC()
	record := m.cliCapabilities[capability]
	if record == nil {
		m.purgeExpiredLocked(now)
		return "", ErrCapabilityNotFound
	}
	if !now.Before(record.expiresAt) {
		delete(m.cliCapabilities, capability)
		m.purgeExpiredLocked(now)
		return "", ErrCapabilityExpired
	}
	m.purgeExpiredLocked(now)
	if len(record.args) != len(args) {
		return "", &CapabilityMismatchError{Expected: append([]string(nil), record.args...), Actual: append([]string(nil), args...)}
	}
	for index := range record.args {
		if record.args[index] != args[index] {
			return "", &CapabilityMismatchError{Expected: append([]string(nil), record.args...), Actual: append([]string(nil), args...)}
		}
	}
	delete(m.cliCapabilities, capability)
	return record.requestID, nil
}

func (m *Manager) matchApprovedLocked(input RetryInput) (Request, bool, error) {
	active := m.requests[m.activeByCaller[input.CallerID]]
	if active == nil || active.value.Status != StatusApproved {
		return Request{}, false, nil
	}
	if active.value.TargetTool != input.TargetTool {
		return Request{}, false, nil
	}
	digest, actual, err := CanonicalTargetDigest(m.instanceID, Target{CallerID: input.CallerID, WorkspaceID: input.WorkspaceID, Source: input.Source, TargetTool: input.TargetTool, Arguments: input.Arguments, GuardCode: active.value.GuardCode})
	if err != nil {
		return Request{}, false, err
	}
	if digest != active.value.Digest {
		return Request{}, false, &MismatchError{RequestID: active.value.ID, TargetTool: active.value.TargetTool, Expected: cloneRaw(active.value.Arguments), Actual: actual}
	}
	return cloneRequest(active.value), true, nil
}

func (m *Manager) Consume(id string) (Request, error) {
	if m == nil {
		return Request{}, errors.New("approval manager is unavailable")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC()
	m.purgeExpiredLocked(now)
	record := m.requests[strings.TrimSpace(id)]
	if record == nil {
		return Request{}, ErrRequestNotFound
	}
	if record.value.Status != StatusApproved {
		return Request{}, fmt.Errorf("%w: %s", ErrRequestNotApproved, record.value.Status)
	}
	record.value.Status, record.value.ConsumedAt = StatusConsumed, now
	m.clearActiveLocked(record.value)
	m.emitLocked(EventClaimed, record.value)
	return cloneRequest(record.value), nil
}

func (m *Manager) Get(id string) (Request, bool) {
	if m == nil {
		return Request{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.purgeExpiredLocked(m.now().UTC())
	record := m.requests[strings.TrimSpace(id)]
	if record == nil {
		return Request{}, false
	}
	return cloneRequest(record.value), true
}

func (m *Manager) Resolve(value string) (Request, error) {
	if m == nil {
		return Request{}, errors.New("approval manager is unavailable")
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return Request{}, ErrRequestNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.purgeExpiredLocked(m.now().UTC())
	record, err := m.resolveRecordLocked(value)
	if err != nil {
		return Request{}, err
	}
	return cloneRequest(record.value), nil
}

func (m *Manager) List(filter Filter) []Request {
	if m == nil {
		return nil
	}
	filter.WorkspaceID = strings.TrimSpace(filter.WorkspaceID)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.purgeExpiredLocked(m.now().UTC())
	values := make([]Request, 0, len(m.requests))
	for _, record := range m.requests {
		if filter.WorkspaceID != "" && record.value.WorkspaceID != filter.WorkspaceID {
			continue
		}
		if filter.Status != "" && record.value.Status != filter.Status {
			continue
		}
		values = append(values, cloneRequest(record.value))
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].CreatedAt.Equal(values[j].CreatedAt) {
			return values[i].ID < values[j].ID
		}
		return values[i].CreatedAt.After(values[j].CreatedAt)
	})
	return values
}

func (m *Manager) PurgeExpired() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.purgeExpiredLocked(m.now().UTC())
}

func (m *Manager) purgeExpiredLocked(now time.Time) int {
	changed := 0
	for _, record := range m.requests {
		switch record.value.Status {
		case StatusPending:
			if now.Before(record.value.ExpiresAt) {
				continue
			}
			record.value.Status, record.value.ResolvedAt, record.value.Reason = StatusExpired, now, "approval request expired"
			m.clearActiveLocked(record.value)
			m.closeResolvedLocked(record)
			m.emitLocked(EventExpired, record.value)
			changed++
		case StatusApproved:
			if record.value.RuntimeSessionGrant {
				if record.value.GrantExpiresAt.IsZero() || now.Before(record.value.GrantExpiresAt) {
					continue
				}
				m.removeRuntimeGrantLocked(record.value.ID)
				record.value.Status, record.value.ResolvedAt, record.value.Reason = StatusExpired, now, "runtime session grant expired"
				m.emitLockedWithSubject(EventExpired, EventSubjectGrant, record.value)
				changed++
				continue
			}
			if record.value.RetryUntil.IsZero() || now.Before(record.value.RetryUntil) {
				continue
			}
			record.value.Status, record.value.ResolvedAt, record.value.Reason = StatusExpired, now, "approved retry window expired"
			m.clearActiveLocked(record.value)
			m.emitLocked(EventExpired, record.value)
			changed++
		}
	}
	kept := m.runtimeGrants[:0]
	for _, grant := range m.runtimeGrants {
		if now.Before(grant.expiresAt) {
			kept = append(kept, grant)
			continue
		}
		changed++
	}
	m.runtimeGrants = kept
	for _, record := range m.challenges {
		if now.Before(record.value.ExpiresAt) {
			continue
		}
		m.emitChallengeLocked(EventExpired, record.value)
		m.removeChallengeLocked(record.value)
		changed++
	}
	for token, record := range m.cliCapabilities {
		if !now.Before(record.expiresAt) {
			delete(m.cliCapabilities, token)
			changed++
		}
	}
	return changed
}

func (m *Manager) removeRuntimeGrantLocked(requestID string) {
	kept := m.runtimeGrants[:0]
	for _, grant := range m.runtimeGrants {
		if grant.requestID == requestID {
			continue
		}
		kept = append(kept, grant)
	}
	m.runtimeGrants = kept
}

func (m *Manager) emitLocked(name string, request Request) {
	m.emitLockedWithSubject(name, EventSubjectRequest, request)
}

func (m *Manager) emitLockedWithSubject(name string, subject EventSubject, request Request) {
	if m == nil || name == "" {
		return
	}
	event := Event{
		Name: name, Subject: subject, ChallengeID: request.challengeID, RequestID: request.ID, WorkspaceID: request.WorkspaceID, SessionHash: request.SessionHash, Source: request.Source,
		TargetTool: request.TargetTool, Status: request.Status, CreatedAt: request.CreatedAt, ExpiresAt: request.ExpiresAt, RetryUntil: request.RetryUntil, GrantExpiresAt: request.GrantExpiresAt, Timestamp: m.now().UTC(),
	}
	if m.events != nil {
		m.events.Publish(event)
	}
}

func (m *Manager) emitChallengeLocked(name string, challenge Challenge) {
	if m == nil || name == "" {
		return
	}
	event := Event{
		Name: name, Subject: EventSubjectChallenge, ChallengeID: challenge.ID, WorkspaceID: challenge.WorkspaceID, SessionHash: challenge.SessionHash, Source: challenge.Source,
		TargetTool: challenge.TargetTool, CreatedAt: challenge.CreatedAt, ExpiresAt: challenge.ExpiresAt, Timestamp: m.now().UTC(),
	}
	if m.events != nil {
		m.events.Publish(event)
	}
}

func (m *Manager) pendingCountLocked(workspaceID string) int {
	count := 0
	for _, record := range m.requests {
		if record.value.Status == StatusPending && (workspaceID == "" || record.value.WorkspaceID == workspaceID) {
			count++
		}
	}
	return count
}

func (m *Manager) clearActiveLocked(value Request) {
	if m.activeByCaller[value.callerID] == value.ID {
		delete(m.activeByCaller, value.callerID)
	}
}

func (m *Manager) closeResolvedLocked(record *requestRecord) {
	if record == nil || record.closed {
		return
	}
	close(record.resolved)
	record.closed = true
}

func (m *Manager) removeChallengeLocked(value Challenge) {
	delete(m.challenges, value.ID)
	key := challengeTargetKey(value.callerID, value.Digest)
	if m.challengeByTarget[key] == value.ID {
		delete(m.challengeByTarget, key)
	}
}

func challengeTargetKey(callerID, digest string) string { return callerID + "\x00" + digest }

func normalizeRetryInput(input RetryInput) RetryInput {
	input.CallerID = strings.TrimSpace(input.CallerID)
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Source = strings.TrimSpace(input.Source)
	input.TargetTool = strings.TrimSpace(input.TargetTool)
	input.Command = strings.TrimSpace(input.Command)
	return input
}

func cloneChallenge(value Challenge) Challenge {
	value.Arguments = cloneRaw(value.Arguments)
	return value
}

func cloneRequest(value Request) Request {
	value.Arguments = cloneRaw(value.Arguments)
	return value
}

func randomID(prefix string) (string, error) {
	return idgen.New(prefix, 12)
}
