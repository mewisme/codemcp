package telegram

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.mewis.me/codemcp/internal/oslock"
	"go.mewis.me/codemcp/internal/state"
)

const (
	PairingCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	PairingCodeSymbols  = 8
	PairingCodeGroup    = 4
	PairingDefaultTTL   = 5 * time.Minute
	PairingStateFile    = "state/telegram-pairing.json"
	pairingStateVersion = 1
)

var (
	ErrPairingNotPending = errors.New("telegram pairing is not pending")
	ErrPairingInvalid    = errors.New("telegram pairing code is invalid or expired")
	ErrPairingGeneration = errors.New("telegram pairing generation is stale")
	ErrPairingRoot       = errors.New("telegram pairing belongs to a different config root")
)

type PairingStatus string

const (
	PairingStatusPending   PairingStatus = "pending"
	PairingStatusPaired    PairingStatus = "paired"
	PairingStatusExpired   PairingStatus = "expired"
	PairingStatusCancelled PairingStatus = "cancelled"
)

type PairingState struct {
	Version       int           `json:"version"`
	RootHash      string        `json:"root_hash"`
	ChallengeHash string        `json:"challenge_hash,omitempty"`
	CreatedAt     time.Time     `json:"created_at"`
	ExpiresAt     time.Time     `json:"expires_at"`
	BotID         int64         `json:"bot_id"`
	BotUsername   string        `json:"bot_username,omitempty"`
	Generation    string        `json:"generation"`
	Status        PairingStatus `json:"status"`
}

type PairingChallenge struct {
	Code       string    `json:"-"`
	Generation string    `json:"generation"`
	ExpiresAt  time.Time `json:"expires_at"`
	BotID      int64     `json:"bot_id"`
	Username   string    `json:"username,omitempty"`
	DeepLink   string    `json:"deep_link,omitempty"`
}

type PairingStore struct {
	root     string
	rootHash string
	path     string
	lockPath string
	now      func() time.Time
	mu       sync.Mutex
}

func NewPairingStore(root string) *PairingStore {
	absolute, _ := filepath.Abs(strings.TrimSpace(root))
	absolute = filepath.Clean(absolute)
	sum := sha256.Sum256([]byte(absolute))
	path := filepath.Join(absolute, filepath.FromSlash(PairingStateFile))
	return &PairingStore{root: absolute, rootHash: hex.EncodeToString(sum[:]), path: path, lockPath: path + ".lock", now: time.Now}
}

func (store *PairingStore) Create(identity BotIdentity) (PairingChallenge, error) {
	if store == nil || identity.ID <= 0 {
		return PairingChallenge{}, errors.New("validated telegram bot identity is required")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	lock, err := oslock.Acquire(store.lockPath, oslock.Exclusive)
	if err != nil {
		return PairingChallenge{}, err
	}
	defer lock.Release()
	code, err := generatePairingCode()
	if err != nil {
		return PairingChallenge{}, err
	}
	generation, err := randomHex(16)
	if err != nil {
		return PairingChallenge{}, err
	}
	now := store.now().UTC()
	username := strings.TrimPrefix(strings.TrimSpace(identity.Username), "@")
	state := PairingState{
		Version: pairingStateVersion, RootHash: store.rootHash, ChallengeHash: pairingCodeHash(code),
		CreatedAt: now, ExpiresAt: now.Add(PairingDefaultTTL), BotID: identity.ID,
		BotUsername: username, Generation: generation, Status: PairingStatusPending,
	}
	if err := store.write(state); err != nil {
		return PairingChallenge{}, err
	}
	challenge := PairingChallenge{Code: code, Generation: generation, ExpiresAt: state.ExpiresAt, BotID: identity.ID, Username: username}
	if username != "" {
		challenge.DeepLink = "https://t.me/" + username + "?start=" + code
	}
	return challenge, nil
}

func (store *PairingStore) Status() (PairingState, error) {
	if store == nil {
		return PairingState{}, ErrPairingNotPending
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	lock, err := oslock.Acquire(store.lockPath, oslock.Exclusive)
	if err != nil {
		return PairingState{}, err
	}
	defer lock.Release()
	state, err := store.load()
	if err != nil {
		return PairingState{}, err
	}
	if state.Status == PairingStatusPending && !store.now().UTC().Before(state.ExpiresAt) {
		state.Status = PairingStatusExpired
		state.ChallengeHash = ""
		if err := store.write(state); err != nil {
			return PairingState{}, err
		}
	}
	return state, nil
}

func (store *PairingStore) Cancel(generation string) error {
	if store == nil {
		return ErrPairingNotPending
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	lock, err := oslock.Acquire(store.lockPath, oslock.Exclusive)
	if err != nil {
		return err
	}
	defer lock.Release()
	state, err := store.load()
	if err != nil {
		return err
	}
	if value := strings.TrimSpace(generation); value != "" && state.Generation != value {
		return ErrPairingGeneration
	}
	if state.Status != PairingStatusPending {
		return nil
	}
	state.Status = PairingStatusCancelled
	state.ChallengeHash = ""
	return store.write(state)
}

func (store *PairingStore) Consume(code string, chatID, userID int64, apply func(int64) error) (PairingState, error) {
	if store == nil {
		return PairingState{}, ErrPairingNotPending
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	lock, err := oslock.Acquire(store.lockPath, oslock.Exclusive)
	if err != nil {
		return PairingState{}, err
	}
	defer lock.Release()
	state, err := store.load()
	if err != nil {
		return PairingState{}, err
	}
	if state.RootHash != store.rootHash {
		return PairingState{}, ErrPairingRoot
	}
	now := store.now().UTC()
	if state.Status != PairingStatusPending {
		return state, ErrPairingNotPending
	}
	if !now.Before(state.ExpiresAt) || !verifyPairingCodeHash(code, state.ChallengeHash) {
		if !now.Before(state.ExpiresAt) {
			state.Status = PairingStatusExpired
			state.ChallengeHash = ""
			_ = store.write(state)
		}
		return state, ErrPairingInvalid
	}
	if chatID <= 0 || userID <= 0 || chatID != userID {
		return state, ErrPairingInvalid
	}
	state.Status = PairingStatusPaired
	state.ChallengeHash = ""
	if err := store.write(state); err != nil {
		return PairingState{}, err
	}
	if apply != nil {
		if err := apply(userID); err != nil {
			return state, err
		}
	}
	return state, nil
}

func (store *PairingStore) load() (PairingState, error) {
	data, err := os.ReadFile(store.path)
	if err != nil {
		if os.IsNotExist(err) {
			return PairingState{}, ErrPairingNotPending
		}
		return PairingState{}, err
	}
	var state PairingState
	if err := json.Unmarshal(data, &state); err != nil {
		return PairingState{}, err
	}
	if state.Version != pairingStateVersion || state.RootHash == "" || state.Generation == "" || state.ExpiresAt.IsZero() {
		return PairingState{}, errors.New("invalid telegram pairing state")
	}
	if state.RootHash != store.rootHash {
		return PairingState{}, ErrPairingRoot
	}
	if state.Status == PairingStatusPending && state.ChallengeHash == "" {
		return PairingState{}, errors.New("invalid telegram pairing state")
	}
	return state, nil
}

func (store *PairingStore) write(value PairingState) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return state.WriteFileAtomic(store.path, append(data, '\n'), 0600)
}

func PairingCodeFromUpdate(update Update) (string, bool) {
	if update.Message == nil || update.Message.From == nil {
		return "", false
	}
	message := update.Message
	if message.Chat.Type != "private" || message.Chat.ID <= 0 || message.Chat.ID != message.From.ID {
		return "", false
	}
	fields := strings.Fields(strings.TrimSpace(message.Text))
	if len(fields) != 2 || commandName(fields[0]) != "start" || !ValidPairingCodeFormat(fields[1]) {
		return "", false
	}
	return strings.ToUpper(fields[1]), true
}

func ValidPairingCodeFormat(value string) bool {
	value = strings.ToUpper(strings.TrimSpace(value))
	if len(value) != PairingCodeSymbols+1 || value[PairingCodeGroup] != '-' {
		return false
	}
	for index, char := range value {
		if index == PairingCodeGroup {
			continue
		}
		if !strings.ContainsRune(PairingCodeAlphabet, char) {
			return false
		}
	}
	return true
}

func commandName(value string) string {
	value = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "/")
	if at := strings.IndexByte(value, '@'); at >= 0 {
		value = value[:at]
	}
	return value
}

func generatePairingCode() (string, error) {
	raw := make([]byte, PairingCodeSymbols)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	var builder strings.Builder
	for index, value := range raw {
		if index == PairingCodeGroup {
			builder.WriteByte('-')
		}
		builder.WriteByte(PairingCodeAlphabet[int(value)&31])
	}
	return builder.String(), nil
}

func randomHex(byteCount int) (string, error) {
	if byteCount <= 0 {
		return "", fmt.Errorf("random byte count must be positive")
	}
	value := make([]byte, byteCount)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func pairingCodeHash(code string) string {
	sum := sha256.Sum256([]byte(strings.ToUpper(strings.TrimSpace(code))))
	return hex.EncodeToString(sum[:])
}

func verifyPairingCodeHash(code, encoded string) bool {
	expected, err := hex.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(expected) != sha256.Size {
		return false
	}
	sum := sha256.Sum256([]byte(strings.ToUpper(strings.TrimSpace(code))))
	return subtle.ConstantTimeCompare(sum[:], expected) == 1
}
