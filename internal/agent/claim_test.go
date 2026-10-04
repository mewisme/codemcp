package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestClaimCredentialIs256BitSingleUseAndNotSerializable(t *testing.T) {
	backend := newManagerTestBackend("test", 5)
	manager := newTestManager(t, ManagerOptions{GlobalCapacity: Capacity{MaxParallel: 5}}, backend)
	owner, _ := NewMCPController("parent-session")
	spawned := spawnTestAgent(t, manager, owner, "")

	credential, err := manager.IssueClaim(spawned.ID)
	if err != nil {
		t.Fatal(err)
	}
	if credential.Token() == "" || len(credential.Token()) != 43 {
		t.Fatalf("unexpected claim token length=%d", len(credential.Token()))
	}
	if credential.String() == credential.Token() || credential.GoString() == credential.Token() {
		t.Fatal("claim credential string representation exposed token")
	}
	data, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), credential.Token()) {
		t.Fatalf("claim token serialized: %s", data)
	}
	if _, err := manager.IssueClaim(spawned.ID); !errors.Is(err, ErrClaimOutstanding) {
		t.Fatalf("second outstanding claim error=%v", err)
	}

	binding, err := manager.ConsumeClaim(spawned.ID, credential.Token(), "child-session")
	if err != nil {
		t.Fatal(err)
	}
	if binding.AgentID != spawned.ID || binding.WorkspaceID != "ws_test" || !binding.Active {
		t.Fatalf("binding=%#v", binding)
	}
	if _, err := manager.ConsumeClaim(spawned.ID, credential.Token(), "child-session"); !errors.Is(err, ErrClaimRejected) {
		t.Fatalf("claim replay error=%v", err)
	}
	if _, err := manager.IssueClaim(spawned.ID); !errors.Is(err, ErrClaimRejected) {
		t.Fatalf("claimed agent received second credential: %v", err)
	}
}

func TestClaimRejectsWrongAgentTokenAndSessionReuse(t *testing.T) {
	backend := newManagerTestBackend("test", 5)
	manager := newTestManager(t, ManagerOptions{GlobalCapacity: Capacity{MaxParallel: 5}}, backend)
	owner, _ := NewMCPController("parent-session")
	first := spawnTestAgent(t, manager, owner, "")
	second := spawnTestAgent(t, manager, owner, "")
	firstClaim, err := manager.IssueClaim(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondClaim, err := manager.IssueClaim(second.ID)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := manager.ConsumeClaim(second.ID, firstClaim.Token(), "session-a"); !errors.Is(err, ErrClaimRejected) {
		t.Fatalf("wrong-agent token error=%v", err)
	}
	if _, ok := manager.SessionBinding("session-a"); ok {
		t.Fatal("wrong-agent token created a session binding")
	}
	if _, err := manager.ConsumeClaim(first.ID, firstClaim.Token(), "session-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ConsumeClaim(second.ID, secondClaim.Token(), "session-a"); !errors.Is(err, ErrSessionBound) {
		t.Fatalf("same session claimed second agent: %v", err)
	}
	if _, err := manager.ConsumeClaim(second.ID, secondClaim.Token(), "session-b"); err != nil {
		t.Fatalf("independent session could not claim second agent: %v", err)
	}
}

func TestClaimExpiryAndPurge(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	backend := newManagerTestBackend("test", 5)
	manager := newTestManager(t, ManagerOptions{
		GlobalCapacity: Capacity{MaxParallel: 5},
		ClaimTTL:       2 * time.Minute,
		Now:            func() time.Time { return now },
	}, backend)
	owner, _ := NewMCPController("parent-session")
	spawned := spawnTestAgent(t, manager, owner, "")
	credential, err := manager.IssueClaim(spawned.ID)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2*time.Minute + time.Nanosecond)
	if _, err := manager.ConsumeClaim(spawned.ID, credential.Token(), "late-session"); !errors.Is(err, ErrClaimRejected) {
		t.Fatalf("expired claim error=%v", err)
	}
	if _, ok := manager.SessionBinding("late-session"); ok {
		t.Fatal("expired claim created a binding")
	}

	credential, err = manager.IssueClaim(spawned.ID)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * time.Minute)
	if removed := manager.PurgeExpiredClaims(); removed != 1 {
		t.Fatalf("purged claims=%d want=1", removed)
	}
	if _, err := manager.ConsumeClaim(spawned.ID, credential.Token(), "late-session"); !errors.Is(err, ErrClaimRejected) {
		t.Fatalf("purged claim error=%v", err)
	}
}

func TestClaimBindingBecomesInactiveOnTerminalState(t *testing.T) {
	backend := newManagerTestBackend("test", 5)
	manager := newTestManager(t, ManagerOptions{GlobalCapacity: Capacity{MaxParallel: 5}}, backend)
	owner, _ := NewMCPController("parent-session")
	spawned := spawnTestAgent(t, manager, owner, "")
	credential, err := manager.IssueClaim(spawned.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ConsumeClaim(spawned.ID, credential.Token(), "child-session"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Cancel(context.Background(), owner, spawned.ID); err != nil {
		t.Fatal(err)
	}
	binding, ok := manager.SessionBinding("child-session")
	if !ok || binding.Active {
		t.Fatalf("terminal binding=%#v ok=%t", binding, ok)
	}
	manager.RevokeAllClaimsAndBindings()
	if _, ok := manager.SessionBinding("child-session"); ok {
		t.Fatal("runtime-wide revocation retained child binding")
	}
}

func TestInactiveClaimBindingIsRemovedWithTerminalRetention(t *testing.T) {
	backend := newManagerTestBackend("test", 5)
	manager := newTestManager(t, ManagerOptions{GlobalCapacity: Capacity{MaxParallel: 5}, MaxTerminal: 1}, backend)
	owner, _ := NewMCPController("parent-session")

	spawnClaimCancel := func(session string) ID {
		spawned := spawnTestAgent(t, manager, owner, "")
		credential, err := manager.IssueClaim(spawned.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := manager.ConsumeClaim(spawned.ID, credential.Token(), session); err != nil {
			t.Fatal(err)
		}
		if _, err := manager.Cancel(context.Background(), owner, spawned.ID); err != nil {
			t.Fatal(err)
		}
		return spawned.ID
	}

	spawnClaimCancel("child-one")
	if binding, ok := manager.SessionBinding("child-one"); !ok || binding.Active {
		t.Fatalf("first terminal binding=%#v ok=%t", binding, ok)
	}
	spawnClaimCancel("child-two")
	if _, ok := manager.SessionBinding("child-one"); ok {
		t.Fatal("terminal retention pruned agent but retained inactive claim tombstone")
	}
	if binding, ok := manager.SessionBinding("child-two"); !ok || binding.Active {
		t.Fatalf("newest terminal binding=%#v ok=%t", binding, ok)
	}
}

func TestConcurrentClaimRaceHasExactlyOneWinner(t *testing.T) {
	backend := newManagerTestBackend("test", 5)
	manager := newTestManager(t, ManagerOptions{GlobalCapacity: Capacity{MaxParallel: 5}}, backend)
	owner, _ := NewMCPController("parent-session")
	spawned := spawnTestAgent(t, manager, owner, "")
	credential, err := manager.IssueClaim(spawned.ID)
	if err != nil {
		t.Fatal(err)
	}

	const contenders = 32
	var wg sync.WaitGroup
	winners := make(chan string, contenders)
	for i := range contenders {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			session := "child-" + string(rune('a'+index))
			if _, err := manager.ConsumeClaim(spawned.ID, credential.Token(), session); err == nil {
				winners <- session
			}
		}(i)
	}
	wg.Wait()
	close(winners)
	var winner string
	count := 0
	for session := range winners {
		winner = session
		count++
	}
	if count != 1 {
		t.Fatalf("claim winners=%d want=1", count)
	}
	if binding, ok := manager.SessionBinding(winner); !ok || binding.AgentID != spawned.ID || !binding.Active {
		t.Fatalf("winner binding=%#v ok=%t", binding, ok)
	}
}

func TestClaimSecretsDoNotAppearInPublicSnapshot(t *testing.T) {
	backend := newManagerTestBackend("test", 5)
	manager := newTestManager(t, ManagerOptions{GlobalCapacity: Capacity{MaxParallel: 5}}, backend)
	owner, _ := NewMCPController("parent-session")
	spawned := spawnTestAgent(t, manager, owner, "")
	credential, err := manager.IssueClaim(spawned.ID)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(credential.Token()))
	snapshot, err := manager.Get(t.Context(), owner, spawned.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), credential.Token()) || strings.Contains(string(data), fmtHex(digest[:])) {
		t.Fatalf("claim secret leaked in snapshot: %s", data)
	}
}

func fmtHex(data []byte) string {
	const alphabet = "0123456789abcdef"
	result := make([]byte, len(data)*2)
	for i, value := range data {
		result[i*2] = alphabet[value>>4]
		result[i*2+1] = alphabet[value&0x0f]
	}
	return string(result)
}
