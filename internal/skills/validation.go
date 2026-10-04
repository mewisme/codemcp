package skills

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	MaxNativeSkillDescriptionChars   = 1024
	MaxNativeSkillCompatibilityChars = 500
	maxImportedSkillFiles            = 4096
	maxImportedSkillFileBytes        = 16 * 1024 * 1024
	maxImportedSkillTotalBytes       = 64 * 1024 * 1024
)

var nativeSkillNamePattern = regexp.MustCompile("^[a-z0-9][a-z0-9-]{0,63}$")

type Manifest struct {
	Frontmatter  map[string]any
	Name         string
	Description  string
	Instructions string
}

type ValidatedNativeSkill struct {
	Skill       Skill          `json:"skill"`
	Root        string         `json:"root"`
	Files       []string       `json:"files"`
	Frontmatter map[string]any `json:"frontmatter"`
}

func ParseManifest(data []byte) (Manifest, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return Manifest{}, errors.New("skill frontmatter is missing")
	}
	rest := strings.TrimPrefix(text, "---\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return Manifest{}, errors.New("skill frontmatter is unclosed")
	}
	closingEnd := end + len("\n---")
	if closingEnd < len(rest) && rest[closingEnd] != '\n' {
		return Manifest{}, errors.New("skill frontmatter is unclosed")
	}
	frontmatter, err := parseManifestFrontmatter(rest[:end])
	if err != nil {
		return Manifest{}, fmt.Errorf("parse skill frontmatter: %w", err)
	}
	name, _ := frontmatter["name"].(string)
	description, _ := frontmatter["description"].(string)
	if strings.TrimSpace(name) == "" || strings.TrimSpace(description) == "" {
		return Manifest{}, errors.New("skill frontmatter requires name and description")
	}
	if err := validateOptionalManifestFields(frontmatter); err != nil {
		return Manifest{}, err
	}
	instructions := ""
	if closingEnd < len(rest) {
		instructions = rest[closingEnd+1:]
	}
	return Manifest{
		Frontmatter: frontmatter,
		Name:        name, Description: description, Instructions: instructions,
	}, nil
}

func parseManifestFrontmatter(raw string) (map[string]any, error) {
	frontmatter := map[string]any{}
	strictErr := yaml.Unmarshal([]byte(raw), &frontmatter)
	if strictErr == nil {
		return frontmatter, nil
	}
	repaired, ok := repairLooseDescriptionFrontmatter(raw)
	if !ok {
		return nil, strictErr
	}
	frontmatter = map[string]any{}
	if err := yaml.Unmarshal([]byte(repaired), &frontmatter); err != nil {
		return nil, strictErr
	}
	return frontmatter, nil
}

func repairLooseDescriptionFrontmatter(raw string) (string, bool) {
	lines := strings.Split(raw, "\n")
	match := -1
	for index, line := range lines {
		const prefix = "description:"
		if !strings.HasPrefix(line, prefix) || len(line) <= len(prefix) || line[len(prefix)] != ' ' && line[len(prefix)] != '\t' {
			continue
		}
		if match >= 0 {
			return "", false
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		if value == "" || !strings.Contains(value, ": ") {
			return "", false
		}
		switch value[0] {
		case '\'', '"', '|', '>', '[', '{', '*', '&', '!':
			return "", false
		}
		quoted, err := json.Marshal(value)
		if err != nil {
			return "", false
		}
		lines[index] = "description: " + string(quoted)
		match = index
	}
	if match < 0 {
		return "", false
	}
	return strings.Join(lines, "\n"), true
}

func validateOptionalManifestFields(frontmatter map[string]any) error {
	for _, key := range []string{"license", "allowed-tools"} {
		if value, ok := frontmatter[key]; ok {
			if _, valid := value.(string); !valid {
				return fmt.Errorf("skill frontmatter %q must be a string", key)
			}
		}
	}
	if value, ok := frontmatter["compatibility"]; ok {
		text, valid := value.(string)
		if !valid {
			return errors.New("skill frontmatter compatibility must be a string")
		}
		if utf8.RuneCountInString(text) > MaxNativeSkillCompatibilityChars {
			return fmt.Errorf("skill compatibility exceeds %d characters", MaxNativeSkillCompatibilityChars)
		}
	}
	if value, ok := frontmatter["metadata"]; ok {
		if _, valid := value.(map[string]any); !valid {
			return errors.New("skill frontmatter metadata must be a mapping")
		}
	}
	return nil
}

func ParseFrontmatter(data []byte) (map[string]any, error) {
	manifest, err := ParseManifest(data)
	if err != nil {
		return nil, err
	}
	return manifest.Frontmatter, nil
}

func ValidateName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || url.PathEscape(name) != name || strings.ContainsAny(name, "/\\?#%") {
		return errors.New("skill name is not safe")
	}
	return nil
}

func ValidateNativeSkillName(name string) (string, error) {
	if name != strings.TrimSpace(name) || !nativeSkillNamePattern.MatchString(name) || strings.HasSuffix(name, "-") || strings.Contains(name, "--") {
		return "", errors.New("skill name must be 1-64 lowercase letters, digits, or hyphens, cannot start or end with a hyphen, and cannot contain consecutive hyphens")
	}
	return name, nil
}

func NormalizeNativeSkillDescription(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\r\n", "\n"))
	if value == "" {
		return "", errors.New("skill description must be non-empty")
	}
	if utf8.RuneCountInString(value) > MaxNativeSkillDescriptionChars {
		return "", fmt.Errorf("skill description exceeds %d characters", MaxNativeSkillDescriptionChars)
	}
	return value, nil
}

func NormalizeNativeSkillInstructions(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\r\n", "\n"))
	if value == "" {
		return "", errors.New("skill instructions are required")
	}
	return value, nil
}

func ValidateSupportingPath(value string) (string, error) {
	if value != strings.TrimSpace(value) || value == "" || strings.ContainsAny(value, "\x00\\") {
		return "", fmt.Errorf("invalid skill supporting path %q", value)
	}
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || isPortableVolumePath(value) {
		return "", fmt.Errorf("skill supporting path must be relative: %q", value)
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return "", fmt.Errorf("skill supporting path escapes skill root: %q", value)
	}
	return clean, nil
}

func isPortableVolumePath(value string) bool {
	return len(value) >= 2 && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) && value[1] == ':'
}

func ValidateNativeSkillRoot(root string) (ValidatedNativeSkill, error) {
	return validateNativeSkillRoot(root, false)
}

func validateNativeSkillRoot(root string, enforceImportSafety bool) (ValidatedNativeSkill, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return ValidatedNativeSkill{}, err
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return ValidatedNativeSkill{}, err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return ValidatedNativeSkill{}, errors.New("skill root must be a regular directory")
	}
	manifestPath := filepath.Join(root, "SKILL.md")
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ValidatedNativeSkill{}, errors.New("skill manifest is missing")
		}
		return ValidatedNativeSkill{}, err
	}
	if manifestInfo.Mode()&os.ModeSymlink != 0 || !manifestInfo.Mode().IsRegular() {
		return ValidatedNativeSkill{}, errors.New("skill manifest must be a regular non-symlink file")
	}
	if enforceImportSafety && manifestInfo.Size() > maxImportedSkillFileBytes {
		return ValidatedNativeSkill{}, fmt.Errorf("skill manifest exceeds import safety limit of %d bytes", maxImportedSkillFileBytes)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return ValidatedNativeSkill{}, err
	}
	manifest, err := ParseManifest(data)
	if err != nil {
		return ValidatedNativeSkill{}, err
	}
	name, err := ValidateNativeSkillName(manifest.Name)
	if err != nil {
		return ValidatedNativeSkill{}, err
	}
	description, err := NormalizeNativeSkillDescription(manifest.Description)
	if err != nil {
		return ValidatedNativeSkill{}, err
	}
	files := make([]string, 0)
	totalBytes := int64(0)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("skill content escaped skill root")
		}
		if relative == "." {
			return nil
		}
		relativeSlash := filepath.ToSlash(relative)
		if entry.Name() == ".git" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("skill contains symlink: %s", relativeSlash)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("skill contains non-regular file: %s", relativeSlash)
		}
		if enforceImportSafety {
			if info.Size() > maxImportedSkillFileBytes {
				return fmt.Errorf("skill file %q exceeds import safety limit of %d bytes", relativeSlash, maxImportedSkillFileBytes)
			}
			totalBytes += info.Size()
			if totalBytes > maxImportedSkillTotalBytes {
				return fmt.Errorf("skill tree exceeds import safety limit of %d bytes", maxImportedSkillTotalBytes)
			}
		}
		if relativeSlash != "SKILL.md" {
			if strings.EqualFold(relativeSlash, "SKILL.md") {
				return fmt.Errorf("duplicate or reserved skill file path %q", relativeSlash)
			}
			clean, err := ValidateSupportingPath(relativeSlash)
			if err != nil {
				return err
			}
			if clean != relativeSlash {
				return fmt.Errorf("skill supporting path is not canonical: %q", relativeSlash)
			}
		}
		files = append(files, relativeSlash)
		if enforceImportSafety && len(files) > maxImportedSkillFiles {
			return fmt.Errorf("skill tree exceeds import safety limit of %d files", maxImportedSkillFiles)
		}
		return nil
	})
	if err != nil {
		return ValidatedNativeSkill{}, err
	}
	sort.Strings(files)
	return ValidatedNativeSkill{
		Skill: Skill{Name: name, Description: description, Path: manifestPath},
		Root:  root, Files: files, Frontmatter: manifest.Frontmatter,
	}, nil
}

func ValidateNativeSkillDirectory(root string) (ValidatedNativeSkill, error) {
	validated, err := ValidateNativeSkillRoot(root)
	if err != nil {
		return ValidatedNativeSkill{}, err
	}
	base := filepath.Base(filepath.Clean(root))
	if base != validated.Skill.Name {
		return ValidatedNativeSkill{}, fmt.Errorf("skill directory name %q must match skill name %q", base, validated.Skill.Name)
	}
	return validated, nil
}

func validateManagedNativeSkillDirectory(root string) (ValidatedNativeSkill, error) {
	validated, err := validateNativeSkillRoot(root, true)
	if err != nil {
		return ValidatedNativeSkill{}, err
	}
	base := filepath.Base(filepath.Clean(root))
	if base != validated.Skill.Name {
		return ValidatedNativeSkill{}, fmt.Errorf("skill directory name %q must match skill name %q", base, validated.Skill.Name)
	}
	return validated, nil
}

func ValidateNativeSkillManifestDirectory(root string) (ValidatedNativeSkill, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return ValidatedNativeSkill{}, err
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return ValidatedNativeSkill{}, err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return ValidatedNativeSkill{}, errors.New("skill root must be a regular directory")
	}
	manifestPath := filepath.Join(root, "SKILL.md")
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ValidatedNativeSkill{}, errors.New("skill manifest is missing")
		}
		return ValidatedNativeSkill{}, err
	}
	if manifestInfo.Mode()&os.ModeSymlink != 0 || !manifestInfo.Mode().IsRegular() {
		return ValidatedNativeSkill{}, errors.New("skill manifest must be a regular non-symlink file")
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return ValidatedNativeSkill{}, err
	}
	manifest, err := ParseManifest(data)
	if err != nil {
		return ValidatedNativeSkill{}, err
	}
	name, err := ValidateNativeSkillName(manifest.Name)
	if err != nil {
		return ValidatedNativeSkill{}, err
	}
	description, err := NormalizeNativeSkillDescription(manifest.Description)
	if err != nil {
		return ValidatedNativeSkill{}, err
	}
	if filepath.Base(filepath.Clean(root)) != name {
		return ValidatedNativeSkill{}, fmt.Errorf("skill directory name %q must match skill name %q", filepath.Base(filepath.Clean(root)), name)
	}
	return ValidatedNativeSkill{
		Skill: Skill{Name: name, Description: description, Path: manifestPath},
		Root:  root, Frontmatter: manifest.Frontmatter,
	}, nil
}

func HashNativeSkillRoot(root string) (string, error) {
	validated, err := ValidateNativeSkillDirectory(root)
	if err != nil {
		return "", err
	}
	return HashValidatedNativeSkill(validated)
}

func HashValidatedNativeSkill(validated ValidatedNativeSkill) (string, error) {
	hash := sha256.New()
	files := append([]string(nil), validated.Files...)
	sort.Strings(files)
	for _, relative := range files {
		clean := relative
		if relative != "SKILL.md" {
			var err error
			clean, err = ValidateSupportingPath(relative)
			if err != nil || clean != relative {
				return "", fmt.Errorf("skill hash path is invalid: %q", relative)
			}
		}
		path := filepath.Join(validated.Root, filepath.FromSlash(clean))
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("skill hash file is not regular: %s", relative)
		}
		if err := binary.Write(hash, binary.LittleEndian, uint32(len(relative))); err != nil {
			return "", err
		}
		_, _ = io.WriteString(hash, relative)
		executable := byte(0)
		if info.Mode().Perm()&0o111 != 0 {
			executable = 1
		}
		_, _ = hash.Write([]byte{executable})
		if err := binary.Write(hash, binary.LittleEndian, uint64(info.Size())); err != nil {
			return "", err
		}
		file, err := os.Open(path)
		if err != nil {
			return "", err
		}
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func ValidateSkillContentHash(value string) error {
	if len(value) != sha256.Size*2 {
		return errors.New("skill content hash must be a 64 character lowercase hexadecimal sha256")
	}
	for _, r := range value {
		if r >= '0' && r <= '9' || r >= 'a' && r <= 'f' {
			continue
		}
		return errors.New("skill content hash must be lowercase hexadecimal")
	}
	return nil
}
