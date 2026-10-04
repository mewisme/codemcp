package skills

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	MaxNativeSkillDescriptionBytes  = 200
	MaxNativeSkillInstructionsBytes = 500_000
	MaxNativeSkillSupportingFiles   = 32
	MaxNativeSkillSupportingBytes   = 256_000
	MaxNativeSkillTotalBytes        = 1_000_000
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
	frontmatter := map[string]any{}
	if err := yaml.Unmarshal([]byte(rest[:end]), &frontmatter); err != nil {
		return Manifest{}, fmt.Errorf("parse skill frontmatter: %w", err)
	}
	name, _ := frontmatter["name"].(string)
	description, _ := frontmatter["description"].(string)
	if strings.TrimSpace(name) == "" || strings.TrimSpace(description) == "" {
		return Manifest{}, errors.New("skill frontmatter requires name and description")
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
	if name != strings.TrimSpace(name) || !nativeSkillNamePattern.MatchString(name) {
		return "", errors.New("skill name must be 1-64 lowercase letters, digits, or hyphens and start with a letter or digit")
	}
	return name, nil
}

func NormalizeNativeSkillDescription(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\r\n", "\n"))
	if value == "" || strings.Contains(value, "\n") {
		return "", errors.New("skill description must be one non-empty line")
	}
	if len([]byte(value)) > MaxNativeSkillDescriptionBytes {
		return "", fmt.Errorf("skill description exceeds %d bytes", MaxNativeSkillDescriptionBytes)
	}
	return value, nil
}

func NormalizeNativeSkillInstructions(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\r\n", "\n"))
	if value == "" {
		return "", errors.New("skill instructions are required")
	}
	if len([]byte(value)) > MaxNativeSkillInstructionsBytes {
		return "", fmt.Errorf("skill instructions exceed %d bytes", MaxNativeSkillInstructionsBytes)
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
	if manifestInfo.Size() > MaxNativeSkillTotalBytes {
		return ValidatedNativeSkill{}, fmt.Errorf("skill manifest exceeds %d bytes", MaxNativeSkillTotalBytes)
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
	if _, err := NormalizeNativeSkillInstructions(manifest.Instructions); err != nil {
		return ValidatedNativeSkill{}, err
	}
	files := make([]string, 0)
	supportingFiles := 0
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
		totalBytes += info.Size()
		if totalBytes > MaxNativeSkillTotalBytes {
			return fmt.Errorf("skill tree exceeds %d bytes", MaxNativeSkillTotalBytes)
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
			supportingFiles++
			if supportingFiles > MaxNativeSkillSupportingFiles {
				return fmt.Errorf("skill defines more than %d supporting files", MaxNativeSkillSupportingFiles)
			}
			if info.Size() > MaxNativeSkillSupportingBytes {
				return fmt.Errorf("skill file %q exceeds %d bytes", relativeSlash, MaxNativeSkillSupportingBytes)
			}
		}
		files = append(files, relativeSlash)
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
