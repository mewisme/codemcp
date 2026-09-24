package instructionsource

import (
	"path/filepath"
	"strings"
)

type Class uint8

const (
	ClassWorkspaceNative Class = iota
	ClassGlobalNative
	ClassProjectRoot
	ClassDynamicProvider
	ClassBuiltin
)

const (
	NativeSource         = ".cm"
	PreferredAgentSource = ".agents"
)

type ProviderCandidate struct {
	Name       string
	Immediate  bool
	Directory  bool
	Symlink    bool
	HasContext bool
	HasRules   bool
	HasSkills  bool
}

type Source struct {
	Class    Class
	Provider string
	Path     string
}

func DynamicProviderIdentity(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if !validHiddenBasename(name) || name == NativeSource {
		return "", false
	}
	return name, true
}

func EligibleDynamicProvider(candidate ProviderCandidate) bool {
	if !candidate.Immediate || !candidate.Directory || candidate.Symlink || (!candidate.HasContext && !candidate.HasRules && !candidate.HasSkills) {
		return false
	}
	_, ok := DynamicProviderIdentity(candidate.Name)
	return ok
}

func Compare(left, right Source) int {
	if left.Class != right.Class {
		if left.Class < right.Class {
			return -1
		}
		return 1
	}
	if left.Class == ClassDynamicProvider {
		lp, rp := dynamicPriority(left.Provider), dynamicPriority(right.Provider)
		if lp != rp {
			if lp < rp {
				return -1
			}
			return 1
		}
		if v := strings.Compare(left.Provider, right.Provider); v != 0 {
			return v
		}
	}
	return strings.Compare(filepath.Clean(left.Path), filepath.Clean(right.Path))
}

func validHiddenBasename(name string) bool {
	if len(name) < 2 || name[0] != '.' || name == "." || name == ".." {
		return false
	}
	return filepath.Base(name) == name && !strings.ContainsAny(name, "/\\")
}

func dynamicPriority(source string) int {
	if source == PreferredAgentSource {
		return 0
	}
	return 1
}
