package product

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/state"
)

const identitySchema = 1

type identityState struct {
	Schema      int    `json:"schema"`
	AnonymousID string `json:"anonymous_id"`
}

type IdentityStore struct {
	Root  func() string
	NewID func() (string, error)
}

func NewIdentityStore() *IdentityStore {
	return &IdentityStore{
		Root:  configformat.RootPath,
		NewID: randomUUID,
	}
}

func (store *IdentityStore) Path() string {
	root := configformat.RootPath()
	if store != nil && store.Root != nil {
		root = store.Root()
	}
	return filepath.Join(root, "state", "product-telemetry.json")
}

func (store *IdentityStore) Read() (string, bool) {
	if store == nil {
		return "", false
	}
	data, err := os.ReadFile(store.Path())
	if err != nil {
		return "", false
	}
	var persisted identityState
	if json.Unmarshal(data, &persisted) != nil || persisted.Schema != identitySchema || !validUUID(persisted.AnonymousID) {
		return "", false
	}
	return persisted.AnonymousID, true
}

func (store *IdentityStore) Ensure(enabled bool, endpoint string) (string, bool, error) {
	if store == nil || !enabled || strings.TrimSpace(endpoint) == "" {
		return "", false, nil
	}
	if _, err := ParseEndpoint(endpoint); err != nil {
		return "", false, err
	}
	if id, ok := store.Read(); ok {
		return id, false, nil
	}
	newID := randomUUID
	if store.NewID != nil {
		newID = store.NewID
	}
	id, err := newID()
	if err != nil {
		return "", false, err
	}
	if !validUUID(id) {
		return "", false, errors.New("telemetry identity generator returned an invalid UUID")
	}
	if err := state.WriteJSONAtomic(store.Path(), identityState{Schema: identitySchema, AnonymousID: id}, 0600); err != nil {
		return "", false, err
	}
	return id, true, nil
}

func randomUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	hexValue := hex.EncodeToString(value[:])
	return hexValue[0:8] + "-" + hexValue[8:12] + "-" + hexValue[12:16] + "-" + hexValue[16:20] + "-" + hexValue[20:32], nil
}

func validUUID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	raw := strings.ReplaceAll(value, "-", "")
	if len(raw) != 32 {
		return false
	}
	_, err := hex.DecodeString(raw)
	return err == nil
}
