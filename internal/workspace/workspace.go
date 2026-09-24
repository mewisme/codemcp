package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/idgen"
	"go.mewis.me/codemcp/internal/instance"
	"go.mewis.me/codemcp/internal/oslock"
	"go.mewis.me/codemcp/internal/state"
	tracepkg "go.mewis.me/codemcp/internal/trace"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

const storeVersion = 5
const LocalDirName = workspacestate.DirectoryName

var ErrNotFound = errors.New("workspace not found")

type Workspace struct {
	ID        string   `json:"id"`
	Path      string   `json:"path"`
	AllowDirs []string `json:"allow_dirs,omitempty"`
	LegacyIDs []string `json:"legacy_ids,omitempty"`
}

// WorkspaceContainer is an orchestration scope only. It groups concrete
// workspaces but never owns a filesystem root, shell cwd, memory, or rules.
type WorkspaceContainer struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	WorkspaceIDs []string `json:"workspace_ids,omitempty"`
}

type storeFile struct {
	Version    int                  `json:"version"`
	Workspaces []Workspace          `json:"workspaces"`
	Containers []WorkspaceContainer `json:"containers,omitempty"`
}

type Manager struct {
	path            string
	trace           tracepkg.Observer
	protectedRoot   string
	instanceStore   *instance.Store
	identityOnce    sync.Once
	identity        instance.Identity
	identityErr     error
	mu              sync.RWMutex
	loaded          bool
	items           map[string]Workspace
	containers      map[string]WorkspaceContainer
	aliases         map[string]string
	globalAllowDirs []string
	shellPath       []string
	runtimeMu       sync.Mutex
	runtime         *runtimeState
}

func DefaultStorePath() string {
	return configformat.StructuredPath(configformat.RootPath(), "workspaces")
}

func LocalDir(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, LocalDirName)
}

func NewManager(path string) *Manager {
	protectedRoot := ""
	storeRoot := canonicalRoot(filepath.Dir(path))
	configRoot := canonicalRoot(configformat.RootPath())
	if storeRoot != "" && configRoot != "" && storeRoot == configRoot {
		protectedRoot = configRoot
	}
	return &Manager{path: path, protectedRoot: protectedRoot, instanceStore: instance.NewStore(filepath.Dir(path)), items: map[string]Workspace{}, containers: map[string]WorkspaceContainer{}, aliases: map[string]string{}, runtime: newRuntimeState()}
}

func (m *Manager) SetTraceObserver(observer tracepkg.Observer) *Manager {
	if m != nil {
		m.trace = observer
	}
	return m
}

func NewManagerWithGlobalAllowDirs(path string, allowDirs []string) *Manager {
	manager := NewManager(path)
	manager.globalAllowDirs = normalizeRoots(allowDirs)
	return manager
}

func (m *Manager) Reload() error {
	if m == nil {
		return errors.New("workspace manager is unavailable")
	}
	m.runtimeMu.Lock()
	defer m.runtimeMu.Unlock()

	fresh := NewManager(m.path).SetTraceObserver(m.trace)
	if err := fresh.ensureLoaded(); err != nil {
		return err
	}
	fresh.mu.RLock()
	items := cloneWorkspaceItems(fresh.items)
	containers := cloneWorkspaceContainers(fresh.containers)
	aliases := cloneAliases(fresh.aliases)
	fresh.mu.RUnlock()

	m.mu.RLock()
	active := m.runtime != nil && m.runtime.active
	currentLocks := map[string]*oslock.Lock{}
	if active {
		for id, lock := range m.runtime.locks {
			currentLocks[id] = lock
		}
	}
	m.mu.RUnlock()

	nextLocks := map[string]*oslock.Lock{}
	nextRoots := map[string]string{}
	acquired := map[string]*oslock.Lock{}
	if active {
		ids := make([]string, 0, len(items))
		for id := range items {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			item := items[id]
			if err := validateActiveWorkspaceState(item); err != nil {
				_ = releaseRuntimeLocks(acquired)
				return err
			}
			if lock := currentLocks[id]; lock != nil {
				same, err := lock.SameFile(workspacestate.New(item.Path).RuntimeLockPath())
				if err == nil && same {
					nextLocks[id] = lock
					nextRoots[id] = item.Path
					continue
				}
			}
			lock, err := m.acquireRuntimeLock(item)
			if err != nil {
				_ = releaseRuntimeLocks(acquired)
				return err
			}
			acquired[id] = lock
			nextLocks[id] = lock
			nextRoots[id] = item.Path
		}
	}

	removed := map[string]*oslock.Lock{}
	m.mu.Lock()
	if active {
		for id, lock := range m.runtime.locks {
			if nextLocks[id] != lock {
				removed[id] = lock
			}
		}
		m.runtime.locks = nextLocks
		m.runtime.roots = nextRoots
	}
	m.items = items
	m.containers = containers
	m.aliases = aliases
	m.loaded = true
	m.mu.Unlock()
	return releaseRuntimeLocks(removed)
}

func (m *Manager) SetGlobalAllowDirs(allowDirs []string) {
	m.mu.Lock()
	m.globalAllowDirs = normalizeRoots(allowDirs)
	m.mu.Unlock()
}

func (m *Manager) SetShellPath(paths []string) {
	m.mu.Lock()
	m.shellPath = normalizeShellPaths(paths)
	m.mu.Unlock()
}

func (m *Manager) ShellPath() []string {
	if m == nil {
		return []string{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]string(nil), m.shellPath...)
}

func normalizeShellPaths(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || !filepath.IsAbs(value) {
			continue
		}
		value = filepath.Clean(value)
		key := value
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, value)
	}
	return result
}

func (m *Manager) Register(path string) (Workspace, error) {
	span := tracepkg.StartObserver(m.trace, "WORKSPACE", "workspace.register", "Registering workspace", tracepkg.String("input_path", path))
	m.runtimeMu.Lock()
	defer m.runtimeMu.Unlock()
	mutation, err := m.beginRegistryMutation()
	if err != nil {
		span.FailMessage("Workspace registration failed", err)
		return Workspace{}, err
	}
	defer mutation.Release()

	resolveSpan := tracepkg.StartObserver(m.trace, "WORKSPACE", "workspace.path.resolve", "Resolving workspace path", tracepkg.String("input_path", path))
	absolute, _ := filepath.Abs(path)
	root, err := canonicalExistingDirectory(path)
	if err != nil {
		resolveSpan.FailMessage("Workspace path resolution failed", err, tracepkg.String("absolute_path", absolute))
		span.FailMessage("Workspace registration failed", err)
		return Workspace{}, err
	}
	resolveSpan.EndMessage("Workspace path resolved", tracepkg.String("input_path", path), tracepkg.String("absolute_path", absolute), tracepkg.String("canonical_path", root))
	if m.protected(root) {
		err := fmt.Errorf("workspace root is inside protected control-plane state: %s", root)
		span.FailMessage("Workspace registration failed", err, tracepkg.String("canonical_path", root), tracepkg.Bool("protected", true))
		return Workspace{}, err
	}
	if m.workspaceLocalRootAliasesProtected(root) {
		err := fmt.Errorf("workspace local .cm root aliases protected global state: %s", root)
		span.FailMessage("Workspace registration failed", err, tracepkg.String("canonical_path", root), tracepkg.Bool("local_state_alias", true))
		return Workspace{}, err
	}

	preferredID := ""
	m.mu.RLock()
	for _, registered := range m.items {
		if sameCanonicalRoot(registered.Path, root) {
			preferredID = registered.ID
			break
		}
	}
	m.mu.RUnlock()
	identity, _, err := workspacestate.New(root).EnsureIdentity(preferredID)
	if err != nil {
		span.FailMessage("Workspace registration failed", err, tracepkg.String("canonical_path", root))
		return Workspace{}, err
	}
	item := Workspace{ID: identity.ID, Path: root, AllowDirs: []string{}}

	m.mu.RLock()
	existing, existed := m.items[item.ID]
	active := m.runtime != nil && m.runtime.active
	m.mu.RUnlock()

	var acquired *oslock.Lock
	if active {
		if existed {
			if err := m.validateRuntimeOwnership(item.ID); err != nil {
				span.FailMessage("Workspace registration failed", err)
				return Workspace{}, err
			}
		} else {
			acquired, err = m.acquireRuntimeLock(item)
			if err != nil {
				span.FailMessage("Workspace registration failed", err)
				return Workspace{}, err
			}
			defer func() {
				if acquired != nil {
					_ = acquired.Release()
				}
			}()
		}
	} else if err := probeWorkspaceRuntimeLock(item); err != nil {
		span.FailMessage("Workspace registration failed", err)
		return Workspace{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	existing, existed = m.items[item.ID]
	if existed && !sameCanonicalRoot(existing.Path, root) {
		err := fmt.Errorf("workspace identity %s is already registered at %s", item.ID, existing.Path)
		span.FailMessage("Workspace registration failed", err, tracepkg.String("workspace_id", item.ID), tracepkg.String("canonical_path", root))
		return Workspace{}, err
	}
	for id, registered := range m.items {
		if id != item.ID && sameCanonicalRoot(registered.Path, root) {
			err := fmt.Errorf("workspace path is already registered as %s", id)
			span.FailMessage("Workspace registration failed", err, tracepkg.String("workspace_id", item.ID), tracepkg.String("canonical_path", root))
			return Workspace{}, err
		}
	}
	if existed {
		item.AllowDirs = append([]string(nil), existing.AllowDirs...)
		item.LegacyIDs = append([]string(nil), existing.LegacyIDs...)
	}
	m.items[item.ID] = item
	if err := m.saveLocked(); err != nil {
		if existed {
			m.items[item.ID] = existing
		} else {
			delete(m.items, item.ID)
		}
		span.FailMessage("Workspace registration failed", err, tracepkg.String("workspace_id", item.ID), tracepkg.String("canonical_path", root), tracepkg.Bool("existing", existed))
		return Workspace{}, err
	}
	if acquired != nil {
		m.runtime.locks[item.ID] = acquired
		m.runtime.roots[item.ID] = item.Path
		acquired = nil
	}
	span.EndMessage("Workspace registered", tracepkg.String("workspace_id", item.ID), tracepkg.String("canonical_path", root), tracepkg.Bool("existing", existed), tracepkg.Bool("protected", false), tracepkg.Int("allow_dirs", len(item.AllowDirs)))
	return item, nil
}

func (m *Manager) AddAllowDir(id, path string) (Workspace, error) {
	span := tracepkg.StartObserver(m.trace, "WORKSPACE", "workspace.allow-dir.add", "Adding workspace allowed directory", tracepkg.String("workspace_id", id), tracepkg.String("input_path", path))
	mutation, err := m.beginRegistryMutation()
	if err != nil {
		span.FailMessage("Adding workspace allowed directory failed", err)
		return Workspace{}, err
	}
	defer mutation.Release()
	resolveSpan := tracepkg.StartObserver(m.trace, "WORKSPACE", "workspace.path.resolve", "Resolving allowed directory", tracepkg.String("workspace_id", id), tracepkg.String("input_path", path))
	absolute, _ := filepath.Abs(path)
	root, err := canonicalExistingDirectory(path)
	if err != nil {
		resolveSpan.FailMessage("Allowed directory resolution failed", err, tracepkg.String("absolute_path", absolute))
		span.FailMessage("Adding workspace allowed directory failed", err)
		return Workspace{}, err
	}
	resolveSpan.EndMessage("Allowed directory resolved", tracepkg.String("absolute_path", absolute), tracepkg.String("canonical_path", root))
	if m.protected(root) {
		err := fmt.Errorf("allowed directory is inside protected control-plane state: %s", root)
		span.FailMessage("Adding workspace allowed directory failed", err, tracepkg.String("canonical_path", root), tracepkg.Bool("protected", true))
		return Workspace{}, err
	}
	if err := m.ensureLoaded(); err != nil {
		span.FailMessage("Adding workspace allowed directory failed", err)
		return Workspace{}, err
	}
	if err := m.validateRuntimeOwnership(id); err != nil {
		span.FailMessage("Adding workspace allowed directory failed", err)
		return Workspace{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	canonical := m.canonicalIDLocked(id)
	item, ok := m.items[canonical]
	if !ok {
		err := fmt.Errorf("%w: %s", ErrNotFound, id)
		span.FailMessage("Adding workspace allowed directory failed", err)
		return Workspace{}, err
	}
	previousCount := len(item.AllowDirs)
	item.AllowDirs = normalizeRoots(append(item.AllowDirs, root))
	m.items[canonical] = item
	if err := m.saveLocked(); err != nil {
		span.FailMessage("Adding workspace allowed directory failed", err)
		return Workspace{}, err
	}
	span.EndMessage("Workspace allowed directory added", tracepkg.String("workspace_id", canonical), tracepkg.String("canonical_path", root), tracepkg.Bool("protected", false), tracepkg.Int("previous_count", previousCount), tracepkg.Int("count", len(item.AllowDirs)))
	return item, nil
}

func (m *Manager) RemoveAllowDir(id, path string) (Workspace, error) {
	span := tracepkg.StartObserver(m.trace, "WORKSPACE", "workspace.allow-dir.remove", "Removing workspace allowed directory", tracepkg.String("workspace_id", id), tracepkg.String("input_path", path))
	mutation, err := m.beginRegistryMutation()
	if err != nil {
		span.FailMessage("Removing workspace allowed directory failed", err)
		return Workspace{}, err
	}
	defer mutation.Release()
	absolute, err := filepath.Abs(path)
	if err != nil {
		span.FailMessage("Removing workspace allowed directory failed", err)
		return Workspace{}, err
	}
	root := filepath.Clean(absolute)
	if canonical, err := filepath.EvalSymlinks(root); err == nil {
		root = filepath.Clean(canonical)
	}
	if err := m.ensureLoaded(); err != nil {
		span.FailMessage("Removing workspace allowed directory failed", err)
		return Workspace{}, err
	}
	if err := m.validateRuntimeOwnership(id); err != nil {
		span.FailMessage("Removing workspace allowed directory failed", err)
		return Workspace{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	canonical := m.canonicalIDLocked(id)
	item, ok := m.items[canonical]
	if !ok {
		err := fmt.Errorf("%w: %s", ErrNotFound, id)
		span.FailMessage("Removing workspace allowed directory failed", err)
		return Workspace{}, err
	}
	previousCount := len(item.AllowDirs)
	filtered := item.AllowDirs[:0]
	removed := false
	for _, value := range item.AllowDirs {
		if filepath.Clean(value) == root {
			removed = true
			continue
		}
		filtered = append(filtered, value)
	}
	if !removed {
		err := fmt.Errorf("workspace allowed directory is not configured: %s", root)
		span.FailMessage("Removing workspace allowed directory failed", err, tracepkg.String("canonical_path", root))
		return Workspace{}, err
	}
	item.AllowDirs = normalizeRoots(filtered)
	m.items[canonical] = item
	if err := m.saveLocked(); err != nil {
		span.FailMessage("Removing workspace allowed directory failed", err)
		return Workspace{}, err
	}
	span.EndMessage("Workspace allowed directory removed", tracepkg.String("workspace_id", canonical), tracepkg.String("absolute_path", absolute), tracepkg.String("canonical_path", root), tracepkg.Int("previous_count", previousCount), tracepkg.Int("count", len(item.AllowDirs)))
	return item, nil
}

func (m *Manager) EffectiveRoots(id string) ([]string, error) {
	item, err := m.Get(id)
	if err != nil {
		return nil, err
	}
	m.mu.RLock()
	global := append([]string(nil), m.globalAllowDirs...)
	m.mu.RUnlock()
	roots := []string{item.Path}
	roots = append(roots, global...)
	roots = append(roots, item.AllowDirs...)
	result := make([]string, 0, len(roots))
	for _, root := range roots {
		root = filepath.Clean(root)
		result = appendUniqueRoot(result, root)
	}
	sort.Strings(result)
	return result, nil
}

func (m *Manager) Get(id string) (Workspace, error) {
	if err := m.ensureLoaded(); err != nil {
		return Workspace{}, err
	}
	m.mu.RLock()
	canonical := m.canonicalIDLocked(id)
	item, ok := m.items[canonical]
	m.mu.RUnlock()
	if !ok {
		return Workspace{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err := m.validateRuntimeOwnership(canonical); err != nil {
		return Workspace{}, err
	}
	return item, nil
}

func (m *Manager) CanonicalID(id string) (string, error) {
	if err := m.ensureLoaded(); err != nil {
		return "", err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	canonical := m.canonicalIDLocked(id)
	if _, ok := m.items[canonical]; !ok {
		return "", fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return canonical, nil
}

func (m *Manager) LocalState(id string) (workspacestate.Store, error) {
	item, err := m.Get(id)
	if err != nil {
		return workspacestate.Store{}, err
	}
	local := workspacestate.New(item.Path)
	identity, _, err := local.EnsureIdentity(item.ID)
	if err != nil {
		return workspacestate.Store{}, err
	}
	if identity.ID != item.ID {
		return workspacestate.Store{}, fmt.Errorf("workspace identity mismatch: local %s, expected %s", identity.ID, item.ID)
	}
	return local, nil
}

func (m *Manager) Instance() (instance.Identity, error) {
	m.identityOnce.Do(func() { m.identity, m.identityErr = m.instanceStore.LoadOrCreate() })
	return m.identity, m.identityErr
}

func (m *Manager) List() ([]Workspace, error) {
	if err := m.ensureLoaded(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]Workspace, 0, len(m.items))
	for _, item := range m.items {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	return items, nil
}

func (m *Manager) AdvertisedIDs() ([]string, error) {
	items, err := m.List()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = appendUniqueString(ids, item.ID)
		for _, legacyID := range item.LegacyIDs {
			ids = appendUniqueString(ids, legacyID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (m *Manager) ResolveDirectory(id, input string) (Workspace, string, error) {
	item, err := m.Get(id)
	if err != nil {
		return Workspace{}, "", err
	}
	if strings.TrimSpace(input) == "" {
		input = item.Path
	}
	candidate := input
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(item.Path, candidate)
	}
	canonical, err := canonicalExistingDirectory(candidate)
	if err != nil {
		return Workspace{}, "", err
	}
	if !m.allowed(item.ID, canonical) {
		return Workspace{}, "", fmt.Errorf("directory escapes workspace: %s", canonical)
	}
	return item, canonical, nil
}

func (m *Manager) ResolvePath(id, baseDirectory, input string, mustExist bool) (string, error) {
	item, cwd, err := m.ResolveDirectory(id, baseDirectory)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(input) == "" {
		return "", errors.New("path is required")
	}
	candidate := input
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(cwd, candidate)
	}
	canonical, err := canonicalForContainment(candidate, mustExist)
	if err != nil {
		return "", err
	}
	if !m.allowed(item.ID, canonical) {
		return "", fmt.Errorf("path escapes workspace: %s", canonical)
	}
	return canonical, nil
}

func (m *Manager) OpenRootForPath(id, path string) (*os.Root, string, error) {
	if strings.TrimSpace(path) == "" {
		return nil, "", errors.New("path is required")
	}
	absolute, err := canonicalForContainment(path, false)
	if err != nil {
		return nil, "", err
	}
	if m.protected(absolute) {
		return nil, "", fmt.Errorf("path is protected: %s", absolute)
	}
	roots, err := m.EffectiveRoots(id)
	if err != nil {
		return nil, "", err
	}
	selected := ""
	for _, root := range roots {
		root = filepath.Clean(root)
		if !within(root, absolute) {
			continue
		}
		if selected == "" || len(root) > len(selected) {
			selected = root
		}
	}
	if selected == "" {
		return nil, "", fmt.Errorf("path escapes workspace: %s", absolute)
	}
	relative, err := filepath.Rel(selected, absolute)
	if err != nil {
		return nil, "", err
	}
	info, err := os.Lstat(selected)
	if err != nil {
		return nil, "", fmt.Errorf("inspect workspace access root %s: %w", selected, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, "", fmt.Errorf("workspace access root is not a stable directory: %s", selected)
	}
	root, err := os.OpenRoot(selected)
	if err != nil {
		return nil, "", err
	}
	openedInfo, err := root.Stat(".")
	if err != nil || !openedInfo.IsDir() || !os.SameFile(info, openedInfo) {
		_ = root.Close()
		if err != nil {
			return nil, "", fmt.Errorf("verify workspace access root %s: %w", selected, err)
		}
		return nil, "", fmt.Errorf("workspace access root changed while opening: %s", selected)
	}
	return root, relative, nil
}

func (m *Manager) ensureLoaded() error {
	m.mu.RLock()
	if m.loaded {
		m.mu.RUnlock()
		return nil
	}
	m.mu.RUnlock()
	lock, err := acquireRegistryMutationLock(m.registryMutationLockPath(), registryMutationTimeout)
	if err != nil {
		return err
	}
	defer lock.Release()
	return m.ensureLoadedUnderRegistryLock()
}

func (m *Manager) ensureLoadedUnderRegistryLock() error {
	m.mu.RLock()
	if m.loaded {
		m.mu.RUnlock()
		return nil
	}
	m.mu.RUnlock()
	span := tracepkg.StartObserver(m.trace, "WORKSPACE", "workspace.registry.load", "Loading workspace registry", tracepkg.String("path", m.path))

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loaded {
		span.EndMessage("Workspace registry already loaded", tracepkg.Bool("cache_hit", true), tracepkg.Int("workspaces", len(m.items)), tracepkg.Int("containers", len(m.containers)))
		return nil
	}

	data, err := os.ReadFile(m.path)
	if errors.Is(err, os.ErrNotExist) {
		m.loaded = true
		span.EndMessage("Workspace registry not yet created", tracepkg.Bool("exists", false), tracepkg.Int64("bytes", 0), tracepkg.Int("workspaces", 0), tracepkg.Int("containers", 0))
		return nil
	}
	if err != nil {
		span.FailMessage("Workspace registry load failed", err)
		return fmt.Errorf("read workspace registry: %w", err)
	}

	var stored storeFile
	if err := configformat.Unmarshal(configformat.JSON, data, &stored); err != nil {
		span.FailMessage("Workspace registry decode failed", err, tracepkg.Int64("bytes", int64(len(data))))
		return fmt.Errorf("decode workspace registry: %w", err)
	}
	if stored.Version < 1 || stored.Version > storeVersion {
		err := fmt.Errorf("unsupported workspace registry version: %d", stored.Version)
		span.FailMessage("Workspace registry validation failed", err, tracepkg.Int64("bytes", int64(len(data))), tracepkg.Int("registry_version", stored.Version))
		return err
	}
	migrated := stored.Version != storeVersion
	migratedIDs, rewrittenFiles := 0, 0
	for _, item := range stored.Workspaces {
		if item.ID == "" || item.Path == "" {
			err := errors.New("workspace registry contains invalid entry")
			span.FailMessage("Workspace registry validation failed", err)
			return err
		}
		canonicalID := workspaceID(item.Path)
		if stored.Version < 5 && item.ID != canonicalID {
			previousID := item.ID
			item.ID = canonicalID
			item.LegacyIDs = appendUniqueString(item.LegacyIDs, previousID)
			stateRoot := filepath.Join(filepath.Dir(m.path), "workspaces")
			legacyStatePath := filepath.Join(stateRoot, previousID)
			canonicalStatePath := filepath.Join(stateRoot, canonicalID)
			migrationSpan := tracepkg.StartObserver(m.trace, "WORKSPACE", "workspace.state.migrate", "Migrating workspace state", tracepkg.String("legacy_workspace_id", previousID), tracepkg.String("workspace_id", canonicalID), tracepkg.String("legacy_state_path", legacyStatePath), tracepkg.String("canonical_state_path", canonicalStatePath))
			rewritten, err := m.migrateWorkspaceState(previousID, canonicalID)
			if err != nil {
				migrationSpan.FailMessage("Workspace state migration failed", err)
				span.FailMessage("Workspace registry migration failed", err)
				return err
			}
			migrationSpan.EndMessage("Workspace state migrated", tracepkg.String("legacy_state_path", legacyStatePath), tracepkg.String("canonical_state_path", canonicalStatePath), tracepkg.Int("rewritten_files", rewritten))
			migratedIDs++
			rewrittenFiles += rewritten
		}
		item.AllowDirs = normalizeRoots(item.AllowDirs)
		item.LegacyIDs = normalizeIDs(item.LegacyIDs, item.ID)
		if existing, ok := m.items[item.ID]; ok && filepath.Clean(existing.Path) != filepath.Clean(item.Path) {
			err := fmt.Errorf("workspace registry id collision: %s", item.ID)
			span.FailMessage("Workspace registry validation failed", err, tracepkg.String("workspace_id", item.ID))
			return err
		}
		m.items[item.ID] = item
		for _, alias := range item.LegacyIDs {
			if err := m.registerAliasLocked(alias, item.ID); err != nil {
				span.FailMessage("Workspace registry alias validation failed", err, tracepkg.String("workspace_id", item.ID), tracepkg.String("legacy_workspace_id", alias))
				return err
			}
		}
	}
	for _, container := range stored.Containers {
		container.ID = strings.TrimSpace(container.ID)
		container.Name = strings.TrimSpace(container.Name)
		if container.ID == "" || container.Name == "" || !strings.HasPrefix(container.ID, "wsc_") {
			err := errors.New("workspace registry contains invalid container entry")
			span.FailMessage("Workspace registry container validation failed", err, tracepkg.String("container_id", container.ID))
			return err
		}
		if _, exists := m.containers[container.ID]; exists {
			err := fmt.Errorf("workspace container id collision: %s", container.ID)
			span.FailMessage("Workspace registry container validation failed", err, tracepkg.String("container_id", container.ID))
			return err
		}
		container.WorkspaceIDs = normalizeContainerWorkspaceIDs(container.WorkspaceIDs, m.items)
		m.containers[container.ID] = container
	}
	m.loaded = true
	if migrated {
		if err := m.saveLocked(); err != nil {
			m.loaded = false
			span.FailMessage("Migrated workspace registry persistence failed", err, tracepkg.Int("registry_version", stored.Version), tracepkg.Int("target_version", storeVersion))
			return fmt.Errorf("persist migrated workspace registry: %w", err)
		}
	}
	span.EndMessage("Workspace registry loaded", tracepkg.Bool("exists", true), tracepkg.String("format", "json"), tracepkg.Int64("bytes", int64(len(data))), tracepkg.Int("registry_version", stored.Version), tracepkg.Int("current_version", storeVersion), tracepkg.Int("workspaces", len(m.items)), tracepkg.Int("containers", len(m.containers)), tracepkg.Bool("migrated", migrated), tracepkg.Int("migrated_workspace_ids", migratedIDs), tracepkg.Int("rewritten_state_files", rewrittenFiles))
	return nil
}

func (m *Manager) canonicalIDLocked(id string) string {
	if _, ok := m.items[id]; ok {
		return id
	}
	if canonical := m.aliases[id]; canonical != "" {
		return canonical
	}
	return id
}

func (m *Manager) registerAliasLocked(alias, canonical string) error {
	alias = strings.TrimSpace(alias)
	if alias == "" || alias == canonical {
		return nil
	}
	if existing, ok := m.items[alias]; ok && existing.ID != canonical {
		return fmt.Errorf("workspace legacy id collides with workspace id: %s", alias)
	}
	if existing := m.aliases[alias]; existing != "" && existing != canonical {
		return fmt.Errorf("workspace legacy id collision: %s", alias)
	}
	m.aliases[alias] = canonical
	return nil
}

func (m *Manager) migrateWorkspaceState(legacyID, canonicalID string) (int, error) {
	if legacyID == canonicalID {
		return 0, nil
	}
	root := filepath.Join(filepath.Dir(m.path), "workspaces")
	legacyPath := filepath.Join(root, legacyID)
	canonicalPath := filepath.Join(root, canonicalID)
	_, legacyErr := os.Stat(legacyPath)
	_, canonicalErr := os.Stat(canonicalPath)
	if errors.Is(legacyErr, os.ErrNotExist) {
		return 0, nil
	}
	if legacyErr != nil {
		return 0, fmt.Errorf("inspect legacy workspace state: %w", legacyErr)
	}
	if canonicalErr == nil {
		return 0, fmt.Errorf("cannot migrate workspace state: both %s and %s exist", legacyPath, canonicalPath)
	}
	if !errors.Is(canonicalErr, os.ErrNotExist) {
		return 0, fmt.Errorf("inspect migrated workspace state: %w", canonicalErr)
	}
	rewritten, err := rewriteWorkspaceStateIDs(legacyPath, legacyID, canonicalID)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return 0, err
	}
	if err := os.Rename(legacyPath, canonicalPath); err != nil {
		return 0, fmt.Errorf("migrate workspace state %s -> %s: %w", legacyID, canonicalID, err)
	}
	return rewritten, nil
}

func rewriteWorkspaceStateIDs(root, legacyID, canonicalID string) (int, error) {
	paths := []string{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		return 0, err
	}
	rewritten := 0
	for _, path := range paths {
		if !strings.EqualFold(filepath.Ext(path), ".json") {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return rewritten, err
		}
		decoded, err := configformat.DecodeGeneric(configformat.JSON, data)
		if err != nil {
			return rewritten, fmt.Errorf("decode workspace state %s: %w", path, err)
		}
		object, ok := decoded.(map[string]any)
		if !ok {
			continue
		}
		value, _ := object["workspace_id"].(string)
		if value == "" || value == canonicalID {
			continue
		}
		if value != legacyID {
			return rewritten, fmt.Errorf("workspace state %s belongs to unexpected workspace %s", path, value)
		}
		object["workspace_id"] = canonicalID
		encoded, err := configformat.EncodeGeneric(configformat.JSON, object)
		if err != nil {
			return rewritten, fmt.Errorf("encode workspace state %s: %w", path, err)
		}
		if err := state.WriteFileAtomic(path, encoded, 0600); err != nil {
			return rewritten, fmt.Errorf("rewrite workspace state %s: %w", path, err)
		}
		rewritten++
	}
	return rewritten, nil
}

func (m *Manager) allowed(id, candidate string) bool {
	if m.protected(candidate) {
		return false
	}
	roots, err := m.EffectiveRoots(id)
	if err != nil {
		return false
	}
	for _, root := range roots {
		if within(root, candidate) {
			return true
		}
	}
	return false
}

func (m *Manager) protected(candidate string) bool {
	return m.protectedRoot != "" && within(m.protectedRoot, candidate)
}

func (m *Manager) workspaceLocalRootAliasesProtected(workspaceRoot string) bool {
	if m == nil || m.protectedRoot == "" {
		return false
	}
	localRoot, err := canonicalForContainment(filepath.Join(workspaceRoot, LocalDirName), false)
	if err != nil {
		return false
	}
	return sameCanonicalRoot(localRoot, m.protectedRoot)
}

func sameCanonicalRoot(left, right string) bool {
	left, right = canonicalRoot(left), canonicalRoot(right)
	if left == "" || right == "" {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func canonicalRoot(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	root := filepath.Clean(absolute)
	if canonical, err := filepath.EvalSymlinks(root); err == nil {
		root = filepath.Clean(canonical)
	}
	return root
}

func normalizeRoots(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		absolute, err := filepath.Abs(value)
		if err != nil {
			continue
		}
		root := filepath.Clean(absolute)
		if canonical, err := filepath.EvalSymlinks(root); err == nil {
			root = filepath.Clean(canonical)
		}
		result = appendUniqueRoot(result, root)
	}
	sort.Strings(result)
	return result
}

func appendUniqueRoot(values []string, root string) []string {
	for _, value := range values {
		if filepath.Clean(value) == filepath.Clean(root) {
			return values
		}
	}
	return append(values, root)
}

func normalizeIDs(values []string, canonical string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || value == canonical {
			continue
		}
		result = appendUniqueString(result, value)
	}
	if len(result) == 0 {
		return nil
	}
	sort.Strings(result)
	return result
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func (m *Manager) saveLocked() error {
	span := tracepkg.StartObserver(m.trace, "WORKSPACE", "workspace.registry.persist", "Persisting workspace registry", tracepkg.String("path", m.path), tracepkg.Int("workspaces", len(m.items)), tracepkg.Int("containers", len(m.containers)), tracepkg.Bool("atomic", true))
	items := make([]Workspace, 0, len(m.items))
	for _, item := range m.items {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	containers := make([]WorkspaceContainer, 0, len(m.containers))
	for _, container := range m.containers {
		container.WorkspaceIDs = normalizeContainerWorkspaceIDs(container.WorkspaceIDs, m.items)
		containers = append(containers, container)
	}
	sort.Slice(containers, func(i, j int) bool {
		left, right := strings.ToLower(containers[i].Name), strings.ToLower(containers[j].Name)
		if left == right {
			return containers[i].ID < containers[j].ID
		}
		return left < right
	})
	stored := storeFile{Version: storeVersion, Workspaces: items, Containers: containers}
	data, err := state.MarshalJSON(stored)
	if err != nil {
		span.FailMessage("Workspace registry encoding failed", err)
		return err
	}
	if err := state.WriteFileAtomic(m.path, data, 0600); err != nil {
		span.FailMessage("Workspace registry persistence failed", err, tracepkg.Int64("bytes", int64(len(data))))
		return err
	}
	span.EndMessage("Workspace registry persisted", tracepkg.String("format", "json"), tracepkg.Int64("bytes", int64(len(data))), tracepkg.Int("registry_version", storeVersion), tracepkg.Int("workspaces", len(items)), tracepkg.Int("containers", len(containers)))
	return nil
}

func canonicalExistingDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory: %s", absolute)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	return filepath.Clean(canonical), nil
}

func canonicalForContainment(path string, mustExist bool) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	if mustExist {
		canonical, err := filepath.EvalSymlinks(absolute)
		if err != nil {
			return "", err
		}
		return filepath.Clean(canonical), nil
	}
	if canonical, err := filepath.EvalSymlinks(absolute); err == nil {
		return filepath.Clean(canonical), nil
	}

	current := absolute
	var suffix []string
	for {
		if _, err := os.Lstat(current); err == nil { // #nosec G703 -- current is an absolute path walked upward only to find the nearest existing ancestor.
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("cannot resolve path parent: %s", absolute)
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
	canonicalParent, err := filepath.EvalSymlinks(current)
	if err != nil {
		return "", err
	}
	for i := len(suffix) - 1; i >= 0; i-- {
		canonicalParent = filepath.Join(canonicalParent, suffix[i])
	}
	return filepath.Clean(canonicalParent), nil
}

func within(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative))
}

func normalizeForID(path string) string {
	clean := filepath.Clean(path)
	if filepath.Separator == '\\' {
		return strings.ToLower(clean)
	}
	return clean
}

func workspaceID(path string) string {
	sum := sha256.Sum256([]byte(normalizeForID(path)))
	return "ws_" + hex.EncodeToString(sum[:])[:16]
}

func workspaceContainerID() (string, error) {
	return idgen.New("wsc", 8)
}

func instanceScopedWorkspaceID(instanceID, path string) string {
	sum := sha256.Sum256([]byte(instanceID + "\x00" + normalizeForID(path)))
	return "ws_" + hex.EncodeToString(sum[:])[:16]
}
