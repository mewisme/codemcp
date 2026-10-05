package skills

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.mewis.me/codemcp/internal/idgen"
	statepkg "go.mewis.me/codemcp/internal/state"
)

const (
	ManagedSourcesFile     = ".cm-sources.json"
	managedSourcesSchema   = 1
	maxManagedSourcesBytes = 1 << 20
	managedStageNamePrefix = ".cm-skill-stage-"
)

type ManagedSource struct {
	Source      string `json:"source"`
	Ref         string `json:"ref,omitempty"`
	Revision    string `json:"revision"`
	Path        string `json:"path"`
	ContentHash string `json:"content_hash,omitempty"`
}

type ManagedSources struct {
	Schema int                      `json:"schema"`
	Skills map[string]ManagedSource `json:"skills"`
}

type StagedManagedSkill struct {
	Name        string
	StageRel    string
	ContentHash string
}

func EmptyManagedSources() ManagedSources {
	return ManagedSources{Schema: managedSourcesSchema, Skills: map[string]ManagedSource{}}
}

func CloneManagedSources(value ManagedSources) ManagedSources {
	result := ManagedSources{Schema: value.Schema, Skills: make(map[string]ManagedSource, len(value.Skills))}
	for name, source := range value.Skills {
		result.Skills[name] = source
	}
	return result
}

func ReadManagedSources(skillsRoot string) (ManagedSources, error) {
	path := filepath.Join(skillsRoot, ManagedSourcesFile)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return EmptyManagedSources(), nil
	}
	if err != nil {
		return ManagedSources{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ManagedSources{}, errors.New("managed skill metadata must be a regular non-symlink file")
	}
	if info.Size() > maxManagedSourcesBytes {
		return ManagedSources{}, errors.New("managed skill metadata exceeds size limit")
	}
	// #nosec G304 -- path is the fixed managed metadata filename under the caller-selected skills root and was lstat-checked above.
	file, err := os.Open(path)
	if err != nil {
		return ManagedSources{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxManagedSourcesBytes+1))
	if err != nil {
		return ManagedSources{}, err
	}
	if len(data) > maxManagedSourcesBytes {
		return ManagedSources{}, errors.New("managed skill metadata exceeds size limit")
	}
	var value ManagedSources
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return ManagedSources{}, fmt.Errorf("decode managed skill metadata: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return ManagedSources{}, errors.New("managed skill metadata contains trailing data")
		}
		return ManagedSources{}, fmt.Errorf("decode managed skill metadata: %w", err)
	}
	if err := ValidateManagedSources(value); err != nil {
		return ManagedSources{}, err
	}
	return value, nil
}

func ValidateManagedSources(value ManagedSources) error {
	if value.Schema != managedSourcesSchema {
		return fmt.Errorf("unsupported managed skill metadata schema %d", value.Schema)
	}
	if value.Skills == nil {
		return errors.New("managed skill metadata skills map is required")
	}
	for name, source := range value.Skills {
		if _, err := ValidateNativeSkillName(name); err != nil {
			return fmt.Errorf("managed skill metadata name %q: %w", name, err)
		}
		if _, err := ParseGitHubIdentity(source.Source); err != nil {
			return fmt.Errorf("managed skill %q source is not canonical", name)
		}
		if err := ValidateGitRevision(source.Revision); err != nil {
			return fmt.Errorf("managed skill %q revision: %w", name, err)
		}
		if err := validateRepositoryRelativePath(source.Path); err != nil {
			return fmt.Errorf("managed skill %q path: %w", name, err)
		}
		if err := validateManagedSourceRef(source.Ref); err != nil {
			return fmt.Errorf("managed skill %q ref: %w", name, err)
		}
		if source.ContentHash != "" {
			if err := ValidateSkillContentHash(source.ContentHash); err != nil {
				return fmt.Errorf("managed skill %q content hash: %w", name, err)
			}
		}
	}
	return nil
}

func validateManagedSourceRef(value string) error {
	if value == "" {
		return nil
	}
	if value != strings.TrimSpace(value) || len(value) > 255 || strings.ContainsAny(value, "\x00\r\n") {
		return errors.New("git ref is invalid")
	}
	return nil
}

func ValidateGitRevision(value string) error {
	if len(value) != 40 && len(value) != 64 {
		return errors.New("git revision must be a 40 or 64 character lowercase hexadecimal commit id")
	}
	for _, r := range value {
		if r >= '0' && r <= '9' || r >= 'a' && r <= 'f' {
			continue
		}
		return errors.New("git revision must be lowercase hexadecimal")
	}
	return nil
}

func validateRepositoryRelativePath(value string) error {
	if value == "" {
		return nil
	}
	clean, err := ValidateSupportingPath(value)
	if err != nil {
		return err
	}
	if clean != value {
		return fmt.Errorf("repository-relative path is not canonical: %q", value)
	}
	return nil
}

func StageManagedSkills(skillsRoot string, candidates []RepositorySkillCandidate) ([]StagedManagedSkill, error) {
	if len(candidates) == 0 {
		return nil, errors.New("no skills selected for staging")
	}
	root, _, parentPath, err := openOrCreateManagedSkillTransactionRoot(skillsRoot)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	staged := make([]StagedManagedSkill, 0, len(candidates))
	cleanup := func() {
		for _, item := range staged {
			_ = root.RemoveAll(item.StageRel)
		}
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		name := candidate.Skill.Name
		if _, err := ValidateNativeSkillName(name); err != nil {
			cleanup()
			return nil, err
		}
		if seen[name] {
			cleanup()
			return nil, fmt.Errorf("repository contains duplicate skill name %q", name)
		}
		seen[name] = true
		stageRel, err := uniqueStageName(root)
		if err != nil {
			cleanup()
			return nil, err
		}
		if err := root.Mkdir(stageRel, 0o700); err != nil {
			cleanup()
			return nil, fmt.Errorf("create skill staging directory: %w", err)
		}
		staged = append(staged, StagedManagedSkill{Name: name, StageRel: stageRel})
		if err := copyCandidateIntoStage(root, stageRel, candidate); err != nil {
			cleanup()
			return nil, err
		}
		validated, err := ValidateNativeSkillRoot(filepath.Join(parentPath, stageRel))
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("validate staged skill %q: %w", name, err)
		}
		if validated.Skill.Name != name {
			cleanup()
			return nil, fmt.Errorf("staged skill identity changed from %q to %q", name, validated.Skill.Name)
		}
		hash, err := HashValidatedNativeSkill(validated)
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("hash staged skill %q: %w", name, err)
		}
		staged[len(staged)-1].ContentHash = hash
	}
	return staged, nil
}

func CleanupStagedManagedSkills(skillsRoot string, staged []StagedManagedSkill) {
	root, _, _, err := openManagedSkillTransactionRoot(skillsRoot)
	if err != nil {
		return
	}
	defer root.Close()
	for _, item := range staged {
		_ = root.RemoveAll(item.StageRel)
	}
}

func CommitStagedManagedSkills(skillsRoot string, staged []StagedManagedSkill, metadata ManagedSources, beforeMetadata func() error) error {
	if len(staged) == 0 {
		return errors.New("no staged skills to commit")
	}
	if err := ValidateManagedSources(metadata); err != nil {
		return err
	}
	root, skillsDir, _, err := openManagedSkillTransactionRoot(skillsRoot)
	if err != nil {
		return err
	}
	defer root.Close()

	for _, item := range staged {
		if _, err := ValidateNativeSkillName(item.Name); err != nil {
			return err
		}
		stageInfo, err := root.Lstat(item.StageRel)
		if err != nil {
			return fmt.Errorf("inspect staged skill %q: %w", item.Name, err)
		}
		if !stageInfo.IsDir() || stageInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("staged skill %q is not a real directory", item.Name)
		}
		destinationRel := filepath.Join(skillsDir, item.Name)
		if _, err := root.Lstat(destinationRel); err == nil {
			return fmt.Errorf("skill %q already exists", item.Name)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	activated := make([]StagedManagedSkill, 0, len(staged))
	rollback := func(cause error) error {
		var rollbackErr error
		for i := len(activated) - 1; i >= 0; i-- {
			item := activated[i]
			destinationRel := filepath.Join(skillsDir, item.Name)
			if err := root.Rename(destinationRel, item.StageRel); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("rollback skill %q: %w", item.Name, err))
			}
		}
		if rollbackErr != nil {
			return errors.Join(cause, rollbackErr)
		}
		return cause
	}

	for _, item := range staged {
		destinationRel := filepath.Join(skillsDir, item.Name)
		if err := root.Rename(item.StageRel, destinationRel); err != nil {
			return rollback(fmt.Errorf("activate skill %q: %w", item.Name, err))
		}
		activated = append(activated, item)
	}
	if beforeMetadata != nil {
		if err := beforeMetadata(); err != nil {
			return rollback(err)
		}
	}
	data, err := marshalManagedSources(metadata)
	if err != nil {
		return rollback(err)
	}
	if err := statepkg.WriteFileAtomicRoot(root, filepath.Join(skillsDir, ManagedSourcesFile), data, 0o600); err != nil {
		return rollback(fmt.Errorf("write managed skill metadata: %w", err))
	}
	return nil
}

func ReplaceStagedManagedSkill(skillsRoot string, staged StagedManagedSkill, metadata ManagedSources, beforeMetadata func() error) error {
	if _, err := ValidateNativeSkillName(staged.Name); err != nil {
		return err
	}
	if staged.ContentHash == "" {
		return errors.New("staged managed skill content hash is required")
	}
	if err := ValidateSkillContentHash(staged.ContentHash); err != nil {
		return err
	}
	if err := ValidateManagedSources(metadata); err != nil {
		return err
	}
	root, skillsDir, _, err := openManagedSkillTransactionRoot(skillsRoot)
	if err != nil {
		return err
	}
	defer root.Close()

	stageInfo, err := root.Lstat(staged.StageRel)
	if err != nil {
		return fmt.Errorf("inspect staged skill %q: %w", staged.Name, err)
	}
	if !stageInfo.IsDir() || stageInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("staged skill %q is not a real directory", staged.Name)
	}
	destinationRel := filepath.Join(skillsDir, staged.Name)
	destinationInfo, err := root.Lstat(destinationRel)
	if err != nil {
		return fmt.Errorf("inspect installed skill %q: %w", staged.Name, err)
	}
	if !destinationInfo.IsDir() || destinationInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("installed skill %q is not a real directory", staged.Name)
	}

	backupRel, err := uniqueStageName(root)
	if err != nil {
		return err
	}
	if err := root.Rename(destinationRel, backupRel); err != nil {
		return fmt.Errorf("backup installed skill %q: %w", staged.Name, err)
	}
	oldMoved, newMoved := true, false
	rollback := func(cause error) error {
		var rollbackErr error
		if newMoved {
			if err := root.Rename(destinationRel, staged.StageRel); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("rollback replacement skill %q: %w", staged.Name, err))
			} else {
				newMoved = false
			}
		}
		if oldMoved {
			if err := root.Rename(backupRel, destinationRel); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore installed skill %q: %w", staged.Name, err))
			} else {
				oldMoved = false
			}
		}
		if rollbackErr != nil {
			return errors.Join(cause, rollbackErr)
		}
		return cause
	}

	if err := root.Rename(staged.StageRel, destinationRel); err != nil {
		return rollback(fmt.Errorf("activate replacement skill %q: %w", staged.Name, err))
	}
	newMoved = true
	if err := writeManagedSourcesRoot(root, skillsDir, metadata, beforeMetadata); err != nil {
		return rollback(err)
	}
	oldMoved = false
	_ = root.RemoveAll(backupRel)
	return nil
}

func WriteManagedSources(skillsRoot string, metadata ManagedSources, beforeMetadata func() error) error {
	if err := ValidateManagedSources(metadata); err != nil {
		return err
	}
	root, skillsDir, _, err := openOrCreateManagedSkillTransactionRoot(skillsRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	return writeManagedSourcesRoot(root, skillsDir, metadata, beforeMetadata)
}

func CommitManagedSkillRemoval(skillsRoot, name string, metadata ManagedSources, beforeMetadata func() error) error {
	if _, err := ValidateNativeSkillName(name); err != nil {
		return err
	}
	if err := ValidateManagedSources(metadata); err != nil {
		return err
	}
	root, skillsDir, _, err := openManagedSkillTransactionRoot(skillsRoot)
	if err != nil {
		return err
	}
	defer root.Close()

	destinationRel := filepath.Join(skillsDir, name)
	info, err := root.Lstat(destinationRel)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("installed skill %q is not a real directory", name)
	}
	backupRel, err := uniqueStageName(root)
	if err != nil {
		return err
	}
	if err := root.Rename(destinationRel, backupRel); err != nil {
		return fmt.Errorf("stage removal of skill %q: %w", name, err)
	}
	if err := writeManagedSourcesRoot(root, skillsDir, metadata, beforeMetadata); err != nil {
		if restoreErr := root.Rename(backupRel, destinationRel); restoreErr != nil {
			return errors.Join(err, fmt.Errorf("restore removed skill %q: %w", name, restoreErr))
		}
		return err
	}
	_ = root.RemoveAll(backupRel)
	return nil
}

func RemoveUnmanagedNativeSkill(skillsRoot, name string) error {
	if _, err := ValidateNativeSkillName(name); err != nil {
		return err
	}
	root, skillsDir, _, err := openManagedSkillTransactionRoot(skillsRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	destinationRel := filepath.Join(skillsDir, name)
	info, err := root.Lstat(destinationRel)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("installed skill %q is not a real directory", name)
	}
	backupRel, err := uniqueStageName(root)
	if err != nil {
		return err
	}
	if err := root.Rename(destinationRel, backupRel); err != nil {
		return fmt.Errorf("stage removal of skill %q: %w", name, err)
	}
	if err := root.RemoveAll(backupRel); err != nil {
		if restoreErr := root.Rename(backupRel, destinationRel); restoreErr != nil {
			return errors.Join(err, fmt.Errorf("restore unmanaged skill %q: %w", name, restoreErr))
		}
		return err
	}
	return nil
}

func writeManagedSourcesRoot(root *os.Root, skillsDir string, metadata ManagedSources, beforeMetadata func() error) error {
	if beforeMetadata != nil {
		if err := beforeMetadata(); err != nil {
			return err
		}
	}
	data, err := marshalManagedSources(metadata)
	if err != nil {
		return err
	}
	if err := statepkg.WriteFileAtomicRoot(root, filepath.Join(skillsDir, ManagedSourcesFile), data, 0o600); err != nil {
		return fmt.Errorf("write managed skill metadata: %w", err)
	}
	return nil
}

func marshalManagedSources(value ManagedSources) ([]byte, error) {
	if err := ValidateManagedSources(value); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func openOrCreateManagedSkillTransactionRoot(skillsRoot string) (*os.Root, string, string, error) {
	abs, err := filepath.Abs(skillsRoot)
	if err != nil {
		return nil, "", "", err
	}
	parentPath := filepath.Dir(abs)
	if err := os.MkdirAll(parentPath, 0o700); err != nil {
		return nil, "", "", err
	}
	root, err := openStableDirectoryRoot(parentPath)
	if err != nil {
		return nil, "", "", err
	}
	skillsDir := filepath.Base(abs)
	info, err := root.Lstat(skillsDir)
	if errors.Is(err, os.ErrNotExist) {
		if err := root.Mkdir(skillsDir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			_ = root.Close()
			return nil, "", "", err
		}
		info, err = root.Lstat(skillsDir)
	}
	if err != nil {
		_ = root.Close()
		return nil, "", "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		_ = root.Close()
		return nil, "", "", fmt.Errorf("skills root is not a stable directory: %s", abs)
	}
	return root, skillsDir, parentPath, nil
}

func openManagedSkillTransactionRoot(skillsRoot string) (*os.Root, string, string, error) {
	abs, err := filepath.Abs(skillsRoot)
	if err != nil {
		return nil, "", "", err
	}
	parentPath := filepath.Dir(abs)
	root, err := openStableDirectoryRoot(parentPath)
	if err != nil {
		return nil, "", "", err
	}
	skillsDir := filepath.Base(abs)
	info, err := root.Lstat(skillsDir)
	if err != nil {
		_ = root.Close()
		return nil, "", "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		_ = root.Close()
		return nil, "", "", fmt.Errorf("skills root is not a stable directory: %s", abs)
	}
	return root, skillsDir, parentPath, nil
}

func openStableDirectoryRoot(path string) (*os.Root, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("managed skill transaction root is not a stable directory: %s", abs)
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !opened.IsDir() || !os.SameFile(info, opened) {
		_ = root.Close()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("managed skill transaction root changed while opening")
	}
	return root, nil
}

func uniqueStageName(root *os.Root) (string, error) {
	for range 100 {
		id, err := idgen.New("skill", 8)
		if err != nil {
			return "", err
		}
		name := managedStageNamePrefix + id
		if _, err := root.Lstat(name); errors.Is(err, os.ErrNotExist) {
			return name, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", errors.New("create skill staging directory: too many collisions")
}

func copyCandidateIntoStage(destination *os.Root, stageRel string, candidate RepositorySkillCandidate) error {
	info, err := os.Lstat(candidate.Root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("candidate skill root must be a real directory")
	}
	source, err := os.OpenRoot(candidate.Root)
	if err != nil {
		return err
	}
	defer source.Close()
	opened, err := source.Stat(".")
	if err != nil || !opened.IsDir() || !os.SameFile(info, opened) {
		if err != nil {
			return err
		}
		return errors.New("candidate skill root changed while opening")
	}

	files := append([]string(nil), candidate.Files...)
	sort.Strings(files)
	if len(files) > maxImportedSkillFiles {
		return fmt.Errorf("candidate skill exceeds import safety limit of %d files", maxImportedSkillFiles)
	}
	var totalCopied int64
	for _, relative := range files {
		clean, err := ValidateSupportingPath(relative)
		if relative == "SKILL.md" {
			clean, err = relative, nil
		}
		if err != nil || clean != relative {
			return fmt.Errorf("candidate skill file path is invalid: %q", relative)
		}
		rel := filepath.FromSlash(relative)
		sourceInfo, err := source.Lstat(rel)
		if err != nil {
			return err
		}
		if !sourceInfo.Mode().IsRegular() || sourceInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("candidate skill file is not regular: %s", relative)
		}
		input, err := source.Open(rel)
		if err != nil {
			return err
		}
		maxBytes := int64(maxImportedSkillFileBytes)
		data, readErr := io.ReadAll(io.LimitReader(input, maxBytes+1))
		closeErr := input.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if int64(len(data)) > maxBytes {
			return fmt.Errorf("candidate skill file exceeds staging size limit: %s", relative)
		}
		totalCopied += int64(len(data))
		if totalCopied > maxImportedSkillTotalBytes {
			return fmt.Errorf("candidate skill exceeds staging safety limit of %d bytes", maxImportedSkillTotalBytes)
		}
		destinationRel := filepath.Join(stageRel, rel)
		parent := filepath.Dir(destinationRel)
		if parent != stageRel {
			if err := destination.MkdirAll(parent, 0o700); err != nil {
				return err
			}
		}
		perm := os.FileMode(0o600)
		if sourceInfo.Mode().Perm()&0o111 != 0 {
			perm = 0o700
		}
		output, err := destination.OpenFile(destinationRel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if err != nil {
			return err
		}
		if _, err := output.Write(data); err != nil {
			_ = output.Close()
			return err
		}
		if err := output.Sync(); err != nil {
			_ = output.Close()
			return err
		}
		if err := output.Close(); err != nil {
			return err
		}
	}
	return nil
}

func ManagedNames(value ManagedSources) []string {
	result := make([]string, 0, len(value.Skills))
	for name := range value.Skills {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func IsManaged(value ManagedSources, name string) bool {
	_, ok := value.Skills[strings.TrimSpace(name)]
	return ok
}
