package telegram

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPairingChallengeIsRootBoundOneShotAndSecretSafe(t *testing.T) {
	root := t.TempDir()
	store := NewPairingStore(root)
	challenge, err := store.Create(BotIdentity{ID: 1000, Username: "codemcp_test_bot"})
	if err != nil {
		t.Fatal(err)
	}
	if !ValidPairingCodeFormat(challenge.Code) || challenge.Generation == "" || challenge.DeepLink == "" {
		t.Fatalf("challenge=%#v", challenge)
	}
	if !strings.Contains(challenge.DeepLink, "?start="+challenge.Code) {
		t.Fatalf("deep link=%q", challenge.DeepLink)
	}
	statePath := filepath.Join(root, filepath.FromSlash(PairingStateFile))
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), challenge.Code) {
		t.Fatal("pairing state persisted the plaintext pairing code")
	}
	if strings.Contains(string(data), "SECRET_BOT_TOKEN") {
		t.Fatal("pairing state leaked bot token material")
	}
	presented, err := json.Marshal(challenge)
	if err != nil {
		t.Fatal(err)
	}
	var public map[string]any
	if err := json.Unmarshal(presented, &public); err != nil {
		t.Fatal(err)
	}
	if _, exists := public["Code"]; exists {
		t.Fatal("pairing challenge JSON exposed a plaintext code field")
	}
	if strings.Contains(string(presented), "SECRET_BOT_TOKEN") || strings.Contains(string(presented), "\"user_id\"") {
		t.Fatal("pairing challenge JSON exposed token or user identity state")
	}

	applied := 0
	state, err := store.Consume(challenge.Code, 42, 42, func(userID int64) error {
		if userID != 42 {
			t.Fatalf("userID=%d", userID)
		}
		applied++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != PairingStatusPaired || state.ChallengeHash != "" || applied != 1 {
		t.Fatalf("paired state=%#v applied=%d", state, applied)
	}
	if _, err := store.Consume(challenge.Code, 42, 42, func(int64) error {
		applied++
		return nil
	}); !errors.Is(err, ErrPairingNotPending) {
		t.Fatalf("replay err=%v", err)
	}
	if applied != 1 {
		t.Fatalf("pairing code replay applied authorization %d times", applied)
	}
}

func TestPairingConsumeInvalidatesCodeBeforeAuthorizationMutation(t *testing.T) {
	store := NewPairingStore(t.TempDir())
	challenge, err := store.Create(BotIdentity{ID: 1000})
	if err != nil {
		t.Fatal(err)
	}
	mutationErr := errors.New("authorization persistence failed")
	if _, err := store.Consume(challenge.Code, 42, 42, func(int64) error { return mutationErr }); !errors.Is(err, mutationErr) {
		t.Fatalf("consume err=%v", err)
	}
	state, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != PairingStatusPaired || state.ChallengeHash != "" {
		t.Fatalf("failed mutation left reusable challenge: %#v", state)
	}
	if _, err := store.Consume(challenge.Code, 99, 99, nil); !errors.Is(err, ErrPairingNotPending) {
		t.Fatalf("consumed challenge reused by another user: %v", err)
	}
}

func TestStalePairingDeepLinkCannotAuthorize(t *testing.T) {
	store := NewPairingStore(t.TempDir())
	now := time.Now().UTC()
	store.now = func() time.Time { return now }
	challenge, err := store.Create(BotIdentity{ID: 1000, Username: "codemcp_test_bot"})
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return challenge.ExpiresAt.Add(time.Second) }
	applied := false
	state, err := store.Consume(challenge.Code, 42, 42, func(int64) error {
		applied = true
		return nil
	})
	if !errors.Is(err, ErrPairingInvalid) {
		t.Fatalf("consume err=%v state=%#v", err, state)
	}
	if applied {
		t.Fatal("expired pairing challenge applied authorization")
	}
	status, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != PairingStatusExpired || status.ChallengeHash != "" {
		t.Fatalf("expired state=%#v", status)
	}
}

func TestPairingStateCannotCrossConfigRoots(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	storeA := NewPairingStore(rootA)
	challenge, err := storeA.Create(BotIdentity{ID: 1000, Username: "codemcp_test_bot"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(rootA, filepath.FromSlash(PairingStateFile)))
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(rootB, filepath.FromSlash(PairingStateFile))
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data, 0600); err != nil {
		t.Fatal(err)
	}
	storeB := NewPairingStore(rootB)
	if _, err := storeB.Consume(challenge.Code, 42, 42, nil); !errors.Is(err, ErrPairingRoot) {
		t.Fatalf("cross-root consume err=%v", err)
	}
}

func TestPairingBootstrapAcceptsOnlyPrivateStartCode(t *testing.T) {
	code := "ABCD-EFGH"
	cases := []struct {
		name   string
		update Update
		ok     bool
	}{
		{name: "private", update: Update{Message: &Message{From: &User{ID: 42}, Chat: Chat{ID: 42, Type: "private"}, Text: "/start " + code}}, ok: true},
		{name: "bot suffix", update: Update{Message: &Message{From: &User{ID: 42}, Chat: Chat{ID: 42, Type: "private"}, Text: "/start@codemcp_test_bot " + code}}, ok: true},
		{name: "group", update: Update{Message: &Message{From: &User{ID: 42}, Chat: Chat{ID: -100, Type: "group"}, Text: "/start " + code}}},
		{name: "mismatched private identity", update: Update{Message: &Message{From: &User{ID: 42}, Chat: Chat{ID: 99, Type: "private"}, Text: "/start " + code}}},
		{name: "bare start", update: Update{Message: &Message{From: &User{ID: 42}, Chat: Chat{ID: 42, Type: "private"}, Text: "/start"}}},
		{name: "other command", update: Update{Message: &Message{From: &User{ID: 42}, Chat: Chat{ID: 42, Type: "private"}, Text: "/status " + code}}},
		{name: "callback", update: Update{CallbackQuery: &CallbackQuery{From: User{ID: 42}, Message: &Message{Chat: Chat{ID: 42, Type: "private"}}, Data: code}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, ok := PairingCodeFromUpdate(test.update)
			if ok != test.ok {
				t.Fatalf("ok=%t want=%t code=%q", ok, test.ok, got)
			}
			if ok && got != code {
				t.Fatalf("code=%q want=%q", got, code)
			}
		})
	}
}

func TestPairingCancelInvalidatesChallenge(t *testing.T) {
	store := NewPairingStore(t.TempDir())
	challenge, err := store.Create(BotIdentity{ID: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Cancel("wrong-generation"); !errors.Is(err, ErrPairingGeneration) {
		t.Fatalf("wrong generation err=%v", err)
	}
	if err := store.Cancel(challenge.Generation); err != nil {
		t.Fatal(err)
	}
	state, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != PairingStatusCancelled || state.ChallengeHash != "" {
		t.Fatalf("cancelled state=%#v", state)
	}
	if _, err := store.Consume(challenge.Code, 42, 42, nil); !errors.Is(err, ErrPairingNotPending) {
		t.Fatalf("cancelled consume err=%v", err)
	}
}
