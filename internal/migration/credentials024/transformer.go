package credentials024

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	currentformat "go.mewis.me/codemcp/internal/configformat"
	migrationformat "go.mewis.me/codemcp/internal/migration/configformat"
	"go.mewis.me/codemcp/internal/secretstore"
	"go.mewis.me/codemcp/internal/upstream"
)

const (
	SourceRelease            = "0.2.24"
	legacyServicePrefix      = "chatgpt-mcp"
	legacyEncryptedPrefix    = "cgmsecret1:"
	legacyMasterKeyName      = ".master.key"
	legacyMasterKeySize      = 32
	maxStructuredSourceBytes = 16 << 20
	maxLegacySecretBytes     = 1 << 20
)

type Input struct {
	SourceRoot      string
	DestinationRoot string
}

type AuthHashInventory struct {
	MCPConfigured   bool `json:"mcp_configured"`
	AdminConfigured bool `json:"admin_configured"`
}

type TunnelAdminState struct {
	Enabled        bool   `json:"enabled"`
	KeyConfigured  bool   `json:"key_configured"`
	OrganizationID string `json:"organization_id,omitempty"`
	WorkspaceID    string `json:"workspace_id,omitempty"`
	TenantID       string `json:"tenant_id,omitempty"`
	Verified       bool   `json:"verified"`
	ReadAccess     bool   `json:"read_access"`
	ManageAccess   bool   `json:"manage_access"`
}

type TunnelState struct {
	RuntimeKeyConfigured bool             `json:"runtime_key_configured"`
	Admin                TunnelAdminState `json:"admin"`
}

type Inventory struct {
	AuthHashes        AuthHashInventory `json:"auth_hashes"`
	Tunnel            int               `json:"tunnel"`
	OAuth             int               `json:"oauth"`
	Upstream          int               `json:"upstream"`
	Cluster           int               `json:"cluster"`
	Recoverable       int               `json:"recoverable"`
	InlinePlaintext   int               `json:"inline_plaintext"`
	LegacySecretFiles int               `json:"legacy_secret_files"`
	LegacyPlaintext   int               `json:"legacy_plaintext"`
	LegacyEncrypted   int               `json:"legacy_encrypted"`
}

type RollbackMetadata struct {
	SourceRoot            string `json:"source_root"`
	SourceRetained        bool   `json:"source_retained"`
	LegacyCredentialFiles int    `json:"legacy_credential_files"`
}

type Result struct {
	SourceRelease   string           `json:"source_release"`
	DestinationRoot string           `json:"destination_root"`
	Inventory       Inventory        `json:"inventory"`
	Tunnel          TunnelState      `json:"tunnel"`
	Migrated        int              `json:"migrated"`
	AlreadyApplied  bool             `json:"already_applied"`
	Rollback        RollbackMetadata `json:"rollback"`
}

type family string

const (
	familyTunnel   family = "tunnel"
	familyOAuth    family = "oauth"
	familyUpstream family = "upstream"
	familyCluster  family = "cluster"
)

type origin string

const (
	originInline          origin = "inline"
	originLegacyPlaintext origin = "legacy-plaintext"
	originLegacyEncrypted origin = "legacy-encrypted"
)

type candidate struct {
	account string
	value   string
	family  family
	origin  origin
}

type scanner struct {
	sourceRoot string
	service    string
	masterKey  []byte
	candidates map[string]candidate
	inventory  Inventory
	tunnel     TunnelState
}

func Transform(input Input) (Result, error) {
	input, err := normalizeInput(input)
	if err != nil {
		return Result{}, err
	}
	s := &scanner{
		sourceRoot: input.SourceRoot,
		service:    legacyService(input.SourceRoot),
		candidates: map[string]candidate{},
	}
	if err := s.scan(); err != nil {
		return Result{}, err
	}
	s.tunnel.RuntimeKeyConfigured = s.hasCandidate(secretstore.AccountName(secretstore.DomainTunnel, "runtime-key"))
	s.tunnel.Admin.KeyConfigured = s.hasCandidate(secretstore.AccountName(secretstore.DomainTunnel, "admin-key"))

	accounts := make([]string, 0, len(s.candidates))
	for account := range s.candidates {
		accounts = append(accounts, account)
	}
	sort.Strings(accounts)

	store := secretstore.New(input.DestinationRoot)
	changes := make([]secretstore.Change, 0, len(accounts))
	for _, account := range accounts {
		item := s.candidates[account]
		current, err := store.Get(account)
		switch {
		case errors.Is(err, secretstore.ErrNotFound):
			changes = append(changes, secretstore.Change{Name: account, Value: item.value})
		case err != nil:
			return Result{}, fmt.Errorf("inspect staged %s credential: %w", item.family, err)
		case current != item.value:
			return Result{}, fmt.Errorf("staged %s credential conflicts with released state", item.family)
		}
	}

	if err := store.Apply(changes); err != nil {
		return Result{}, fmt.Errorf("stage released credentials: %w", err)
	}
	for _, account := range accounts {
		item := s.candidates[account]
		current, err := store.Get(account)
		if err != nil {
			return Result{}, fmt.Errorf("verify staged %s credential: %w", item.family, err)
		}
		if current != item.value {
			return Result{}, fmt.Errorf("verify staged %s credential: content mismatch", item.family)
		}
	}

	return Result{
		SourceRelease:   SourceRelease,
		DestinationRoot: input.DestinationRoot,
		Inventory:       s.inventory,
		Tunnel:          s.tunnel,
		Migrated:        len(changes),
		AlreadyApplied:  len(changes) == 0,
		Rollback: RollbackMetadata{
			SourceRoot:            input.SourceRoot,
			SourceRetained:        true,
			LegacyCredentialFiles: s.inventory.LegacySecretFiles,
		},
	}, nil
}

func normalizeInput(input Input) (Input, error) {
	var err error
	input.SourceRoot, err = normalizeRoot(input.SourceRoot, "released credential source")
	if err != nil {
		return Input{}, err
	}
	input.DestinationRoot, err = normalizeRoot(input.DestinationRoot, "credential staging destination")
	if err != nil {
		return Input{}, err
	}
	if samePath(input.SourceRoot, input.DestinationRoot) {
		return Input{}, errors.New("credential migration source and staging destination must differ")
	}
	if activeRoot, activeErr := normalizeRoot(currentformat.RootPath(), "active config root"); activeErr == nil && samePath(input.DestinationRoot, activeRoot) {
		return Input{}, errors.New("credential migration staging destination cannot be the active config root")
	}
	if err := requireRealDirectory(input.SourceRoot, "released credential source"); err != nil {
		return Input{}, err
	}
	if info, err := os.Lstat(input.DestinationRoot); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return Input{}, errors.New("credential staging destination must be a real directory")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Input{}, fmt.Errorf("inspect credential staging destination: %w", err)
	}
	return input, nil
}

func normalizeRoot(value, label string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", label, err)
	}
	return filepath.Clean(abs), nil
}

func requireRealDirectory(path, label string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", label, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must be a real directory", label)
	}
	return nil
}

func samePath(left, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
}

func legacyService(root string) string {
	digest := sha256.Sum256([]byte(filepath.Clean(root)))
	return legacyServicePrefix + "/" + hex.EncodeToString(digest[:8])
}

func (s *scanner) scan() error {
	configRoot, _, found, err := readStructuredObject(s.sourceRoot, "config")
	if err != nil {
		return err
	}
	if found {
		if err := s.scanAuthHashes(configRoot); err != nil {
			return err
		}
	}
	tunnelRoot, _, tunnelFound, err := readStructuredObject(s.sourceRoot, "tunnel")
	if err != nil {
		return err
	}
	if err := s.scanTunnel(configRoot, found, tunnelRoot, tunnelFound); err != nil {
		return err
	}
	if err := s.scanOAuth(); err != nil {
		return err
	}
	if err := s.scanUpstream(); err != nil {
		return err
	}
	if err := s.scanClusterRelay(); err != nil {
		return err
	}
	s.inventory.Recoverable = len(s.candidates)
	return nil
}

func (s *scanner) scanAuthHashes(root map[string]any) error {
	auth, exists, err := optionalObject(root, "auth", "released auth configuration")
	if err != nil || !exists {
		return err
	}
	mcp, _, err := optionalString(auth, "mcp_token_hash", "released MCP token hash")
	if err != nil {
		return err
	}
	admin, _, err := optionalString(auth, "admin_token_hash", "released admin token hash")
	if err != nil {
		return err
	}
	// Authentication hashes are intentionally inventoried only. They remain opaque
	// config values and must never be converted into recoverable credentials.
	s.inventory.AuthHashes.MCPConfigured = mcp != ""
	s.inventory.AuthHashes.AdminConfigured = admin != ""
	return nil
}

func (s *scanner) scanTunnel(configRoot map[string]any, configFound bool, tunnelRoot map[string]any, tunnelFound bool) error {
	mainTunnel := map[string]any{}
	if configFound {
		value, exists, err := optionalObject(configRoot, "tunnel", "released tunnel configuration")
		if err != nil {
			return err
		}
		if exists {
			mainTunnel = value
		}
	}
	if !tunnelFound {
		tunnelRoot = map[string]any{}
	}
	if err := s.scanTunnelAdminState(mainTunnel, tunnelRoot); err != nil {
		return err
	}
	for _, spec := range []struct {
		field      string
		configured string
		account    string
		label      string
	}{
		{field: "api_key", configured: "runtime_key_configured", account: secretstore.AccountName(secretstore.DomainTunnel, "runtime-key"), label: "tunnel runtime credential"},
		{field: "admin_key", configured: "admin_key_configured", account: secretstore.AccountName(secretstore.DomainTunnel, "admin-key"), label: "tunnel admin credential"},
	} {
		fallback, fallbackExists, err := optionalString(mainTunnel, spec.field, spec.label)
		if err != nil {
			return err
		}
		side, sideExists, err := optionalString(tunnelRoot, spec.field, spec.label)
		if err != nil {
			return err
		}
		if sideExists {
			fallback, fallbackExists = side, true
		}
		configured, _, err := optionalBool(tunnelRoot, spec.configured, spec.label+" configured state")
		if err != nil {
			return err
		}
		marker := fallbackExists && secretstore.IsMarker(fallback)
		if marker {
			fallback = ""
		}
		if configured || marker {
			stored, source, exists, err := s.readLegacySecret(spec.account)
			if err != nil {
				return fmt.Errorf("read released %s: %w", spec.label, err)
			}
			if exists {
				if err := s.add(spec.account, stored, familyTunnel, source); err != nil {
					return err
				}
				continue
			}
			if fallback == "" {
				return fmt.Errorf("released %s is configured but its stored credential is missing", spec.label)
			}
		}
		if fallback != "" {
			if err := s.add(spec.account, fallback, familyTunnel, originInline); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *scanner) scanTunnelAdminState(mainTunnel, tunnelRoot map[string]any) error {
	enabled := true
	for _, source := range []map[string]any{mainTunnel, tunnelRoot} {
		value, exists, err := optionalBool(source, "admin_enabled", "released tunnel admin enabled state")
		if err != nil {
			return err
		}
		if exists {
			enabled = value
		}
	}
	state := TunnelAdminState{Enabled: enabled}
	for _, field := range []struct {
		name  string
		label string
		set   func(string)
	}{
		{name: "admin_organization_id", label: "released tunnel admin organization scope", set: func(value string) { state.OrganizationID = value }},
		{name: "admin_workspace_id", label: "released tunnel admin workspace scope", set: func(value string) { state.WorkspaceID = value }},
		{name: "admin_tenant_id", label: "released tunnel admin tenant scope", set: func(value string) { state.TenantID = value }},
	} {
		value, exists, err := optionalString(mainTunnel, field.name, field.label)
		if err != nil {
			return err
		}
		if side, sideExists, err := optionalString(tunnelRoot, field.name, field.label); err != nil {
			return err
		} else if sideExists {
			value, exists = side, true
		}
		if exists {
			field.set(strings.TrimSpace(value))
		}
	}
	scopes := 0
	for _, value := range []string{state.OrganizationID, state.WorkspaceID, state.TenantID} {
		if value != "" {
			scopes++
		}
	}
	if scopes > 1 {
		return errors.New("released tunnel admin state contains competing organization, workspace, and tenant scopes")
	}
	// Released verification/access flags are intentionally ignored. The staged
	// canonical state starts unverified and must be verified explicitly after cutover.
	s.tunnel.Admin = state
	return nil
}

func (s *scanner) scanOAuth() error {
	root, _, found, err := readStructuredObject(s.sourceRoot, "oauth")
	if err != nil || !found {
		return err
	}
	credentials, exists, err := optionalObject(root, "credentials", "released OAuth credentials")
	if err != nil || !exists {
		return err
	}
	ids := sortedKeys(credentials)
	for _, id := range ids {
		credential, ok := credentials[id].(map[string]any)
		if !ok {
			return fmt.Errorf("released OAuth credential %q must be an object", id)
		}
		for _, field := range []struct {
			jsonName string
			account  string
			label    string
		}{
			{jsonName: "client_secret", account: "client-secret", label: "OAuth client credential"},
			{jsonName: "access_token", account: "access-token", label: "OAuth access credential"},
			{jsonName: "refresh_token", account: "refresh-token", label: "OAuth refresh credential"},
		} {
			value, exists, err := optionalString(credential, field.jsonName, field.label)
			if err != nil || !exists || value == "" {
				if err != nil {
					return err
				}
				continue
			}
			account := secretstore.AccountName(secretstore.DomainOAuth, id, field.account)
			if err := s.addStoredOrInline(account, value, familyOAuth, field.label); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *scanner) scanUpstream() error {
	value, _, found, err := readStructured(s.sourceRoot, "upstream")
	if err != nil || !found {
		return err
	}
	var servers []any
	switch typed := value.(type) {
	case []any:
		servers = typed
	case map[string]any:
		raw, exists := typed["servers"]
		if !exists {
			return errors.New("released upstream state does not contain servers")
		}
		servers, _ = raw.([]any)
		if servers == nil {
			return errors.New("released upstream servers must be an array")
		}
	default:
		return errors.New("released upstream state must be an object or array")
	}
	for _, raw := range servers {
		server, ok := raw.(map[string]any)
		if !ok {
			return errors.New("released upstream server must be an object")
		}
		id, exists, err := optionalString(server, "id", "released upstream id")
		if err != nil {
			return err
		}
		if !exists || strings.TrimSpace(id) == "" {
			return errors.New("released upstream server id is required")
		}
		for _, field := range []struct {
			name string
			kind string
		}{
			{name: "headers", kind: "header"},
			{name: "env", kind: "env"},
		} {
			values, exists, err := optionalObject(server, field.name, "released upstream "+field.name)
			if err != nil || !exists {
				if err != nil {
					return err
				}
				continue
			}
			for _, key := range sortedKeys(values) {
				if !upstream.SensitiveConfigKey(key) {
					continue
				}
				value, ok := values[key].(string)
				if !ok {
					return fmt.Errorf("released upstream %s credential must be a string", field.kind)
				}
				if value == "" {
					continue
				}
				account := secretstore.AccountName(secretstore.DomainUpstream, id, field.kind, key)
				if err := s.addStoredOrInline(account, value, familyUpstream, "upstream "+field.kind+" credential"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *scanner) scanClusterRelay() error {
	account := secretstore.AccountName(secretstore.DomainCluster, "relay-token")
	value, source, exists, err := s.readLegacySecret(account)
	if err != nil {
		return fmt.Errorf("read released cluster relay credential: %w", err)
	}
	if !exists {
		return nil
	}
	return s.add(account, value, familyCluster, source)
}

func (s *scanner) addStoredOrInline(account, value string, group family, label string) error {
	if secretstore.IsMarker(value) {
		stored, source, exists, err := s.readLegacySecret(account)
		if err != nil {
			return fmt.Errorf("read released %s: %w", label, err)
		}
		if !exists {
			return fmt.Errorf("released %s references a missing stored credential", label)
		}
		return s.add(account, stored, group, source)
	}
	return s.add(account, value, group, originInline)
}

func (s *scanner) add(account, value string, group family, source origin) error {
	if value == "" {
		return nil
	}
	if previous, exists := s.candidates[account]; exists {
		if previous.value != value {
			return fmt.Errorf("released %s credential sources conflict", group)
		}
		return nil
	}
	s.candidates[account] = candidate{account: account, value: value, family: group, origin: source}
	switch group {
	case familyTunnel:
		s.inventory.Tunnel++
	case familyOAuth:
		s.inventory.OAuth++
	case familyUpstream:
		s.inventory.Upstream++
	case familyCluster:
		s.inventory.Cluster++
	}
	switch source {
	case originInline:
		s.inventory.InlinePlaintext++
	case originLegacyPlaintext:
		s.inventory.LegacySecretFiles++
		s.inventory.LegacyPlaintext++
	case originLegacyEncrypted:
		s.inventory.LegacySecretFiles++
		s.inventory.LegacyEncrypted++
	}
	return nil
}

func (s *scanner) hasCandidate(account string) bool {
	_, ok := s.candidates[account]
	return ok
}

func (s *scanner) readLegacySecret(account string) (string, origin, bool, error) {
	digest := sha256.Sum256([]byte(s.service + "\x00" + account))
	path := filepath.Join(s.sourceRoot, "state", "secrets", hex.EncodeToString(digest[:])+".secret")
	data, exists, err := readOptionalRegular(path, maxLegacySecretBytes, "released secret file")
	if err != nil || !exists {
		return "", "", exists, err
	}
	if !strings.HasPrefix(string(data), legacyEncryptedPrefix) {
		return string(data), originLegacyPlaintext, true, nil
	}
	plaintext, err := s.openLegacyEncrypted(data)
	if err != nil {
		return "", "", true, err
	}
	return string(plaintext), originLegacyEncrypted, true, nil
}

func (s *scanner) openLegacyEncrypted(data []byte) ([]byte, error) {
	payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(string(data), legacyEncryptedPrefix))
	if err != nil {
		return nil, errors.New("decode released encrypted credential")
	}
	key, err := s.legacyMasterKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("initialize released credential decryption")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("initialize released credential decryption")
	}
	if len(payload) < gcm.NonceSize()+gcm.Overhead() {
		return nil, errors.New("released encrypted credential is truncated")
	}
	nonce, ciphertext := payload[:gcm.NonceSize()], payload[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, errors.New("decrypt released credential")
	}
	return plaintext, nil
}

func (s *scanner) legacyMasterKey() ([]byte, error) {
	if len(s.masterKey) == legacyMasterKeySize {
		return s.masterKey, nil
	}
	path := filepath.Join(s.sourceRoot, "state", "secrets", legacyMasterKeyName)
	data, exists, err := readOptionalRegular(path, legacyMasterKeySize, "released secret master key")
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.New("released secret master key is missing")
	}
	if len(data) != legacyMasterKeySize {
		return nil, errors.New("released secret master key has invalid length")
	}
	s.masterKey = append([]byte(nil), data...)
	return s.masterKey, nil
}

func readStructuredObject(root, stem string) (map[string]any, migrationformat.Format, bool, error) {
	value, format, found, err := readStructured(root, stem)
	if err != nil || !found {
		return nil, format, found, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, format, true, fmt.Errorf("released %s state must be an object", stem)
	}
	return object, format, true, nil
}

func readStructured(root, stem string) (any, migrationformat.Format, bool, error) {
	path, format, found, err := discoverStructured(root, stem)
	if err != nil || !found {
		return nil, format, found, err
	}
	data, _, err := readOptionalRegular(path, maxStructuredSourceBytes, "released "+stem+" state")
	if err != nil {
		return nil, format, true, err
	}
	value, err := migrationformat.DecodeGeneric(format, data)
	if err != nil {
		return nil, format, true, fmt.Errorf("decode released %s state: %w", stem, err)
	}
	return value, format, true, nil
}

func discoverStructured(root, stem string) (string, migrationformat.Format, bool, error) {
	type match struct {
		path   string
		format migrationformat.Format
	}
	matches := []match{}
	for _, ext := range []string{".json", ".yaml", ".yml", ".toml"} {
		path := filepath.Join(root, stem+ext)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", "", false, fmt.Errorf("inspect released %s state: %w", stem, err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return "", "", false, fmt.Errorf("released %s state must be a regular non-symlink file", stem)
		}
		format, err := migrationformat.Detect(path)
		if err != nil {
			return "", "", false, err
		}
		matches = append(matches, match{path: path, format: format})
	}
	if len(matches) == 0 {
		return "", "", false, nil
	}
	if len(matches) > 1 {
		return "", "", false, fmt.Errorf("released %s state is ambiguous across multiple structured files", stem)
	}
	return matches[0].path, matches[0].format, true, nil
}

func readOptionalRegular(path string, limit int, label string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect %s: %w", label, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, false, fmt.Errorf("%s must be a regular non-symlink file", label)
	}
	if info.Size() > int64(limit) {
		return nil, false, fmt.Errorf("%s exceeds size limit", label)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", label, err)
	}
	if len(data) > limit {
		return nil, false, fmt.Errorf("%s exceeds size limit", label)
	}
	return data, true, nil
}

func optionalObject(root map[string]any, key, label string) (map[string]any, bool, error) {
	value, exists := root[key]
	if !exists || value == nil {
		return nil, false, nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, true, fmt.Errorf("%s must be an object", label)
	}
	return object, true, nil
}

func optionalString(root map[string]any, key, label string) (string, bool, error) {
	value, exists := root[key]
	if !exists || value == nil {
		return "", false, nil
	}
	text, ok := value.(string)
	if !ok {
		return "", true, fmt.Errorf("%s must be a string", label)
	}
	return text, true, nil
}

func optionalBool(root map[string]any, key, label string) (bool, bool, error) {
	value, exists := root[key]
	if !exists || value == nil {
		return false, false, nil
	}
	flag, ok := value.(bool)
	if !ok {
		return false, true, fmt.Errorf("%s must be a boolean", label)
	}
	return flag, true, nil
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
