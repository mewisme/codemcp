package reconcile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"

	"go.mewis.me/codemcp/internal/checkpoint"
	agentcompletion "go.mewis.me/codemcp/internal/history/completion"
	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/memory"
	plandoc "go.mewis.me/codemcp/internal/plan"
	"go.mewis.me/codemcp/internal/rules"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
	"go.mewis.me/codemcp/internal/skills"
	statepkg "go.mewis.me/codemcp/internal/state"
	"go.mewis.me/codemcp/internal/workspace"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

var ErrUnsupportedWorkspaceState = errors.New("unsupported workspace local state")
var ErrDurableStateConflict = errors.New("workspace durable state merge conflict")

func MergeDuplicateState(request workspace.DuplicateMergeRequest) error {
	registeredStore, err := storeForLocalRoot(request.RegisteredStateRoot)
	if err != nil {
		return err
	}
	destinationStore, err := storeForLocalRoot(request.DestinationStateRoot)
	if err != nil {
		return err
	}
	outputStore, err := storeForLocalRoot(request.OutputStateRoot)
	if err != nil {
		return err
	}
	if err := classifyLocalTree(request.RegisteredStateRoot); err != nil {
		return fmt.Errorf("registered workspace state: %w", err)
	}
	if err := classifyLocalTree(request.DestinationStateRoot); err != nil {
		return fmt.Errorf("destination workspace state: %w", err)
	}
	registeredIdentity, err := registeredStore.LoadIdentity()
	if err != nil {
		return err
	}
	destinationIdentity, err := destinationStore.LoadIdentity()
	if err != nil {
		return err
	}
	if registeredIdentity.ID != request.WorkspaceID || destinationIdentity.ID != request.WorkspaceID {
		return fmt.Errorf("%w: workspace identities are not compatible", ErrDurableStateConflict)
	}
	if err := os.MkdirAll(request.OutputStateRoot, 0700); err != nil {
		return err
	}
	identityData, err := os.ReadFile(registeredStore.IdentityPath())
	if err != nil {
		return err
	}
	if err := statepkg.WriteFileAtomic(outputStore.IdentityPath(), identityData, 0600); err != nil {
		return err
	}
	if hygiene := workspace.EnsureLocalStateGitHygiene(outputStore.WorkspaceRoot); hygiene.Error() != nil {
		return fmt.Errorf("regenerate workspace git hygiene: %w", hygiene.Error())
	}

	if err := mergeMemoryState(registeredStore.MemoryRoot(), destinationStore.MemoryRoot(), outputStore.MemoryRoot()); err != nil {
		return fmt.Errorf("%w: memory: %v", ErrDurableStateConflict, err)
	}
	if err := mergeValidatedTree("rules", registeredStore.RulesRoot(), destinationStore.RulesRoot(), outputStore.RulesRoot(), rules.ValidateWorkspaceState, nil); err != nil {
		return err
	}
	if err := mergeValidatedTree("skills", registeredStore.SkillsRoot(), destinationStore.SkillsRoot(), outputStore.SkillsRoot(), skills.ValidateWorkspaceState, nil); err != nil {
		return err
	}
	if err := mergeValidatedTree("prompts", registeredStore.PromptRoot(), destinationStore.PromptRoot(), outputStore.PromptRoot(), instructioncontext.ValidateWorkspacePromptState, jsonEquivalent); err != nil {
		return err
	}
	if err := mergeValidatedTree("plans", registeredStore.PlansRoot(), destinationStore.PlansRoot(), outputStore.PlansRoot(), plandoc.ValidateWorkspaceState, plandoc.EquivalentDocuments); err != nil {
		return err
	}
	if err := agentcompletion.MergeWorkspaceState(registeredStore, destinationStore, outputStore, request.WorkspaceID); err != nil {
		return fmt.Errorf("%w: completion history: %v", ErrDurableStateConflict, err)
	}
	if err := checkpoint.MergeWorkspaceState(
		registeredStore.CheckpointRoot(),
		destinationStore.CheckpointRoot(),
		outputStore.CheckpointRoot(),
		request.WorkspaceID,
		request.RegisteredRoot,
		request.DestinationRoot,
	); err != nil {
		return fmt.Errorf("%w: checkpoints: %v", ErrDurableStateConflict, err)
	}
	registeredShell, _ := registeredStore.StatePath("shell.json")
	destinationShell, _ := destinationStore.StatePath("shell.json")
	outputShell, _ := outputStore.StatePath("shell.json")
	if err := shellruntime.MergeSessionState(
		registeredShell,
		destinationShell,
		outputShell,
		request.WorkspaceID,
		request.RegisteredRoot,
		request.DestinationRoot,
		request.AllowedRoots,
	); err != nil {
		return fmt.Errorf("%w: shell state: %v", ErrDurableStateConflict, err)
	}
	if err := validateMergedTree(outputStore, request); err != nil {
		return err
	}
	return nil
}

func storeForLocalRoot(root string) (workspacestate.Store, error) {
	root = filepath.Clean(root)
	if filepath.Base(root) != workspacestate.DirectoryName {
		return workspacestate.Store{}, fmt.Errorf("workspace local state root must end in %s", workspacestate.DirectoryName)
	}
	return workspacestate.New(filepath.Dir(root)), nil
}

func classifyLocalTree(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	known := map[string]string{
		"workspace.json": "identity",
		".gitignore":     "derived-hygiene",
		"config.json":    "unsupported-config",
		"memory":         "durable-user",
		"rules":          "durable-user",
		"skills":         "durable-user",
		"prompts":        "durable-user",
		"plans":          "durable-user",
		"checkpoints":    "durable-history",
		"state":          "typed-state",
		"runtime":        "transient-runtime",
		"cache":          "derived-cache",
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symlink %s", ErrUnsupportedWorkspaceState, entry.Name())
		}
		if _, ok := known[entry.Name()]; !ok {
			return fmt.Errorf("%w: unclassified top-level entry %s", ErrUnsupportedWorkspaceState, entry.Name())
		}
		switch entry.Name() {
		case "workspace.json":
			if !entry.Type().IsRegular() {
				return fmt.Errorf("%w: identity is not a regular file", ErrUnsupportedWorkspaceState)
			}
		case ".gitignore":
			if !entry.Type().IsRegular() {
				return fmt.Errorf("%w: local git hygiene is not a regular file", ErrUnsupportedWorkspaceState)
			}
		case "config.json":
			return fmt.Errorf("%w: workspace config has no canonical merge owner", ErrUnsupportedWorkspaceState)
		case "state":
			if err := classifyStateDirectory(filepath.Join(root, entry.Name())); err != nil {
				return err
			}
		case "runtime":
			if err := classifyRuntimeDirectory(filepath.Join(root, entry.Name())); err != nil {
				return err
			}
		default:
			if !entry.IsDir() {
				return fmt.Errorf("%w: %s must be a directory", ErrUnsupportedWorkspaceState, entry.Name())
			}
		}
	}
	return nil
}

func classifyStateDirectory(root string) error {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	known := map[string]bool{
		"shell.json":                      true,
		"codegraph.json":                  true,
		"agent-completions.json":          true,
		"agent-completions.archive.jsonl": true,
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() || !known[entry.Name()] {
			return fmt.Errorf("%w: unclassified state entry %s", ErrUnsupportedWorkspaceState, entry.Name())
		}
	}
	return nil
}

func classifyRuntimeDirectory(root string) error {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() || (entry.Name() != "lock" && entry.Name() != "codegraph.lock") {
			return fmt.Errorf("%w: unclassified runtime entry %s", ErrUnsupportedWorkspaceState, entry.Name())
		}
	}
	return nil
}

func mergeMemoryState(registeredRoot, destinationRoot, outputRoot string) error {
	load := func(root string) (memory.Document, error) {
		path := filepath.Join(root, "MEMORY.md")
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			if entries, readErr := os.ReadDir(root); readErr == nil && len(entries) > 0 {
				return memory.Document{}, fmt.Errorf("%w: unclassified memory state", ErrUnsupportedWorkspaceState)
			}
			return memory.Document{}, nil
		}
		if err != nil {
			return memory.Document{}, err
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			return memory.Document{}, err
		}
		if len(entries) != 1 || entries[0].Name() != "MEMORY.md" {
			return memory.Document{}, fmt.Errorf("%w: unclassified memory state", ErrUnsupportedWorkspaceState)
		}
		return memory.Parse(string(data)), nil
	}
	registered, err := load(registeredRoot)
	if err != nil {
		return err
	}
	destination, err := load(destinationRoot)
	if err != nil {
		return err
	}
	merged, err := memory.MergeDocuments(registered, destination)
	if err != nil {
		return err
	}
	if len(merged.Entries) == 0 {
		return nil
	}
	if err := os.MkdirAll(outputRoot, 0700); err != nil {
		return err
	}
	return statepkg.WriteFileAtomic(filepath.Join(outputRoot, "MEMORY.md"), []byte(memory.Render(merged)), 0600)
}

type treeValidator func(string) error
type equivalentFile func([]byte, []byte) (bool, error)

func mergeValidatedTree(domain, registeredRoot, destinationRoot, outputRoot string, validate treeValidator, equivalent equivalentFile) error {
	if err := validate(registeredRoot); err != nil {
		return fmt.Errorf("registered %s state: %w", domain, err)
	}
	if err := validate(destinationRoot); err != nil {
		return fmt.Errorf("destination %s state: %w", domain, err)
	}
	registered, err := collectTreeFiles(registeredRoot)
	if err != nil {
		return err
	}
	destination, err := collectTreeFiles(destinationRoot)
	if err != nil {
		return err
	}
	paths := map[string]struct{}{}
	for path := range registered {
		paths[path] = struct{}{}
	}
	for path := range destination {
		paths[path] = struct{}{}
	}
	for relative := range paths {
		left, leftOK := registered[relative]
		right, rightOK := destination[relative]
		selected := left
		if rightOK {
			selected = right
		}
		if leftOK && rightOK && !bytes.Equal(left, right) {
			equal := false
			if equivalent != nil {
				equal, err = equivalent(left, right)
				if err != nil {
					return fmt.Errorf("%s %s: %w", domain, relative, err)
				}
			}
			if !equal {
				return fmt.Errorf("%w: divergent %s path %s", ErrDurableStateConflict, domain, relative)
			}
		}
		target := filepath.Join(outputRoot, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		if err := statepkg.WriteFileAtomic(target, selected, 0600); err != nil {
			return err
		}
	}
	return validate(outputRoot)
}

func collectTreeFiles(root string) (map[string][]byte, error) {
	result := map[string][]byte{}
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("state root is not a real directory: %s", root)
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root || entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("state contains symlink: %s", path)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("state contains non-regular file: %s", path)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(filepath.Clean(relative))] = data
		return nil
	})
	return result, err
}

func jsonEquivalent(left, right []byte) (bool, error) {
	var l, r any
	if err := json.Unmarshal(left, &l); err != nil {
		return false, err
	}
	if err := json.Unmarshal(right, &r); err != nil {
		return false, err
	}
	return reflect.DeepEqual(l, r), nil
}

func validateMergedTree(store workspacestate.Store, request workspace.DuplicateMergeRequest) error {
	if err := classifyLocalTree(store.Root()); err != nil {
		return err
	}
	identity, err := store.LoadIdentity()
	if err != nil {
		return err
	}
	if identity.ID != request.WorkspaceID {
		return errors.New("merged workspace identity mismatch")
	}
	for _, transient := range []string{store.RuntimeRoot(), store.CacheRoot()} {
		if _, err := os.Lstat(transient); !errors.Is(err, os.ErrNotExist) {
			if err == nil {
				return fmt.Errorf("merged state retained transient tree: %s", filepath.Base(transient))
			}
			return err
		}
	}
	stateEntries, err := os.ReadDir(store.StateRoot())
	if err == nil {
		for _, entry := range stateEntries {
			if entry.Name() == "codegraph.json" {
				return errors.New("merged state retained derived codegraph metadata")
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
