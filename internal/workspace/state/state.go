package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"go.mewis.me/codemcp/internal/idgen"
	statepkg "go.mewis.me/codemcp/internal/state"
)

const (
	DirectoryName    = ".cm"
	identityFileName = "workspace.json"
	identityVersion  = 1
	identityKind     = "codemcp-workspace"
	maxIdentityBytes = 64 << 10
)

var identityInitMu sync.Mutex

type Identity struct {
	Version   int       `json:"version"`
	Kind      string    `json:"kind"`
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

type Store struct {
	WorkspaceRoot string
}

func New(root string) Store {
	return Store{WorkspaceRoot: filepath.Clean(root)}
}

func (s Store) Root() string           { return filepath.Join(s.WorkspaceRoot, DirectoryName) }
func (s Store) IdentityPath() string   { return filepath.Join(s.Root(), identityFileName) }
func (s Store) ConfigPath() string     { return filepath.Join(s.Root(), "config.json") }
func (s Store) StateRoot() string      { return filepath.Join(s.Root(), "state") }
func (s Store) MemoryRoot() string     { return filepath.Join(s.Root(), "memory") }
func (s Store) CheckpointRoot() string { return filepath.Join(s.Root(), "checkpoints") }
func (s Store) CacheRoot() string      { return filepath.Join(s.Root(), "cache") }
func (s Store) RuntimeRoot() string    { return filepath.Join(s.Root(), "runtime") }
func (s Store) RuntimeLockPath() string {
	return filepath.Join(s.RuntimeRoot(), "lock")
}
func (s Store) RulesRoot() string  { return filepath.Join(s.Root(), "rules") }
func (s Store) SkillsRoot() string { return filepath.Join(s.Root(), "skills") }
func (s Store) PromptRoot() string { return filepath.Join(s.Root(), "prompts") }

func (s Store) Join(parts ...string) (string, error) {
	root := s.Root()
	path := filepath.Join(append([]string{root}, parts...)...)
	if !contained(root, path) || !contained(s.WorkspaceRoot, path) {
		return "", fmt.Errorf("workspace local path escapes %s: %s", DirectoryName, strings.Join(parts, string(filepath.Separator)))
	}
	return path, nil
}

func (s Store) StatePath(name string) (string, error) {
	return s.Join("state", name)
}

func (s Store) EnsureIdentity(preferredID string) (Identity, bool, error) {
	identityInitMu.Lock()
	defer identityInitMu.Unlock()

	workspaceRoot, err := openWorkspaceRoot(s.WorkspaceRoot)
	if err != nil {
		return Identity{}, false, err
	}
	defer workspaceRoot.Close()

	localRoot, createdRoot, err := openOrCreateLocalRoot(workspaceRoot)
	if err != nil {
		return Identity{}, false, err
	}
	defer localRoot.Close()

	identity, err := loadIdentityRoot(localRoot)
	if err == nil {
		if preferred := strings.TrimSpace(preferredID); preferred != "" && identity.ID != preferred {
			return Identity{}, false, fmt.Errorf("workspace identity mismatch: local %s, expected %s", identity.ID, preferred)
		}
		return identity, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Identity{}, false, err
	}
	if !createdRoot {
		empty, emptyErr := rootIsEmpty(localRoot)
		if emptyErr != nil {
			return Identity{}, false, emptyErr
		}
		if !empty {
			return Identity{}, false, errors.New("workspace local .cm is missing the CodeMCP ownership marker")
		}
	}

	id := strings.TrimSpace(preferredID)
	if id == "" {
		id, err = idgen.New("ws", 8)
		if err != nil {
			return Identity{}, false, err
		}
	}
	identity = Identity{
		Version:   identityVersion,
		Kind:      identityKind,
		ID:        id,
		CreatedAt: time.Now().UTC(),
	}
	if err := validateIdentity(identity); err != nil {
		return Identity{}, false, err
	}
	identity, created, err := createIdentityAtomic(localRoot, identity)
	if err != nil {
		return Identity{}, false, err
	}
	if preferred := strings.TrimSpace(preferredID); preferred != "" && identity.ID != preferred {
		return Identity{}, false, fmt.Errorf("workspace identity mismatch: local %s, expected %s", identity.ID, preferred)
	}
	return identity, created, nil
}

func (s Store) LoadIdentity() (Identity, error) {
	workspaceRoot, err := openWorkspaceRoot(s.WorkspaceRoot)
	if err != nil {
		return Identity{}, err
	}
	defer workspaceRoot.Close()
	localRoot, err := openExistingLocalRoot(workspaceRoot)
	if err != nil {
		return Identity{}, err
	}
	defer localRoot.Close()
	return loadIdentityRoot(localRoot)
}

func validateIdentity(identity Identity) error {
	if identity.Version != identityVersion {
		return fmt.Errorf("unsupported workspace identity version: %d", identity.Version)
	}
	if identity.Kind != identityKind {
		return fmt.Errorf("invalid workspace ownership marker: %q", identity.Kind)
	}
	if err := ValidateIdentityID(identity.ID); err != nil {
		return err
	}
	if identity.CreatedAt.IsZero() {
		return errors.New("workspace identity contains invalid created_at")
	}
	return nil
}

func ValidateIdentityID(id string) error {
	if id != strings.TrimSpace(id) || !strings.HasPrefix(id, "ws_") || len(id) <= len("ws_") || len(id) > 128 || strings.IndexFunc(id, unicode.IsSpace) >= 0 {
		return errors.New("workspace identity contains invalid id")
	}
	for _, character := range strings.TrimPrefix(id, "ws_") {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '-' {
			continue
		}
		return errors.New("workspace identity contains unsafe id characters")
	}
	return nil
}

func NewIdentity(id string, createdAt time.Time) (Identity, error) {
	identity := Identity{
		Version:   identityVersion,
		Kind:      identityKind,
		ID:        strings.TrimSpace(id),
		CreatedAt: createdAt.UTC(),
	}
	if err := validateIdentity(identity); err != nil {
		return Identity{}, err
	}
	return identity, nil
}

func WriteIdentitySnapshot(localRoot string, identity Identity) error {
	if err := validateIdentity(identity); err != nil {
		return err
	}
	localRoot = filepath.Clean(strings.TrimSpace(localRoot))
	if localRoot == "." || localRoot == "" {
		return errors.New("workspace local root is required")
	}
	if info, err := os.Lstat(localRoot); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("workspace local root must be a real directory")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(localRoot, 0700); err != nil {
			return err
		}
	} else {
		return err
	}
	data, err := statepkg.MarshalJSON(identity)
	if err != nil {
		return err
	}
	return statepkg.WriteFileAtomic(filepath.Join(localRoot, identityFileName), data, 0600)
}

func openWorkspaceRoot(path string) (*os.Root, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("workspace root is not a stable directory: %s", absolute)
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !opened.IsDir() || !os.SameFile(info, opened) {
		_ = root.Close()
		if err != nil {
			return nil, fmt.Errorf("verify workspace root %s: %w", absolute, err)
		}
		return nil, fmt.Errorf("workspace root changed while opening: %s", absolute)
	}
	return root, nil
}

func openOrCreateLocalRoot(workspaceRoot *os.Root) (*os.Root, bool, error) {
	info, err := workspaceRoot.Lstat(DirectoryName)
	created := false
	if errors.Is(err, os.ErrNotExist) {
		if mkdirErr := workspaceRoot.Mkdir(DirectoryName, 0700); mkdirErr == nil {
			created = true
		} else if !errors.Is(mkdirErr, fs.ErrExist) {
			return nil, false, fmt.Errorf("create workspace local root: %w", mkdirErr)
		}
		info, err = workspaceRoot.Lstat(DirectoryName)
	}
	if err != nil {
		return nil, false, err
	}
	root, err := verifyLocalRoot(workspaceRoot, info)
	return root, created, err
}

func openExistingLocalRoot(workspaceRoot *os.Root) (*os.Root, error) {
	info, err := workspaceRoot.Lstat(DirectoryName)
	if err != nil {
		return nil, err
	}
	return verifyLocalRoot(workspaceRoot, info)
}

func verifyLocalRoot(workspaceRoot *os.Root, info fs.FileInfo) (*os.Root, error) {
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("workspace local .cm must be a real directory")
	}
	root, err := workspaceRoot.OpenRoot(DirectoryName)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !opened.IsDir() || !os.SameFile(info, opened) {
		_ = root.Close()
		if err != nil {
			return nil, fmt.Errorf("verify workspace local root: %w", err)
		}
		return nil, errors.New("workspace local .cm changed while opening")
	}
	return root, nil
}

func createIdentityAtomic(root *os.Root, identity Identity) (Identity, bool, error) {
	data, err := statepkg.MarshalJSON(identity)
	if err != nil {
		return Identity{}, false, err
	}
	token, err := idgen.New("workspace", 8)
	if err != nil {
		return Identity{}, false, err
	}
	tempName := "." + identityFileName + "-" + token + ".tmp"
	file, err := root.OpenFile(tempName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return Identity{}, false, fmt.Errorf("create workspace identity staging file: %w", err)
	}
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = root.Remove(tempName)
		}
	}()
	written, writeErr := file.Write(data)
	if writeErr == nil && written != len(data) {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return Identity{}, false, fmt.Errorf("write workspace identity staging file: %w", writeErr)
	}
	if err := root.Link(tempName, identityFileName); err != nil {
		if errors.Is(err, fs.ErrExist) {
			existing, loadErr := loadIdentityRoot(root)
			if loadErr != nil {
				return Identity{}, false, loadErr
			}
			return existing, false, nil
		}
		return Identity{}, false, fmt.Errorf("activate workspace identity: %w", err)
	}
	if err := root.Remove(tempName); err != nil {
		return Identity{}, false, fmt.Errorf("clean workspace identity staging file: %w", err)
	}
	removeTemp = false
	return identity, true, nil
}

func loadIdentityRoot(root *os.Root) (Identity, error) {
	info, err := root.Lstat(identityFileName)
	if err != nil {
		return Identity{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return Identity{}, errors.New("workspace identity marker must be a regular file")
	}
	if info.Size() > maxIdentityBytes {
		return Identity{}, errors.New("workspace identity marker exceeds size limit")
	}
	file, err := root.Open(identityFileName)
	if err != nil {
		return Identity{}, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return Identity{}, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return Identity{}, errors.New("workspace identity marker changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxIdentityBytes+1))
	if err != nil {
		return Identity{}, err
	}
	if len(data) > maxIdentityBytes {
		return Identity{}, errors.New("workspace identity marker exceeds size limit")
	}
	var identity Identity
	if err := decodeStrict(data, &identity); err != nil {
		return Identity{}, fmt.Errorf("decode workspace identity: %w", err)
	}
	if err := validateIdentity(identity); err != nil {
		return Identity{}, err
	}
	return identity, nil
}

func rootIsEmpty(root *os.Root) (bool, error) {
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func contained(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil {
		return false
	}
	return relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
