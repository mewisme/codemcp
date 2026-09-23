package instructioncontext

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"go.mewis.me/codemcp/internal/instructionpolicy"
)

const (
	DefaultMemoryMaxBytes  = 100_000
	DefaultSectionMaxBytes = 25_000
	DefaultSectionMaxLines = 200
)

type MemoryLoadOptions struct {
	WorkspaceRoots     []string
	HomeDir            string
	SourcePolicy       instructionpolicy.Config
	DisableUser        bool
	MaxTotalBytes      int
	MaxBytesPerSection int
	MaxLinesPerSection int
	ImportMaxDepth     int
	Now                func() time.Time
}

type memoryCandidate struct {
	Relative string
	Kind     SectionKind
	Source   string
}

var primaryProjectMemoryCandidates = []memoryCandidate{
	{Relative: "AGENTS.md", Kind: SectionProject, Source: "agents"},
	{Relative: filepath.Join(".agents", "AGENTS.md"), Kind: SectionProject, Source: "agents"},
}

var fallbackProjectMemoryCandidates = []memoryCandidate{
	{Relative: "CLAUDE.md", Kind: SectionProject, Source: "claude"},
	{Relative: filepath.Join(".claude", "CLAUDE.md"), Kind: SectionProject, Source: "claude"},
	{Relative: filepath.Join(".claudes", "CLAUDE.md"), Kind: SectionProject, Source: "claudes"},
	{Relative: filepath.Join(".cursor", "AGENTS.md"), Kind: SectionProject, Source: "cursor"},
	{Relative: filepath.Join(".codex", "AGENTS.md"), Kind: SectionProject, Source: "codex"},
	{Relative: "CLAUDE.local.md", Kind: SectionProject, Source: "claude"},
}

var primaryUserMemoryCandidates = []memoryCandidate{
	{Relative: filepath.Join(".agents", "AGENTS.md"), Kind: SectionUser, Source: "agents"},
}

var fallbackUserMemoryCandidates = []memoryCandidate{
	{Relative: filepath.Join(".claude", "CLAUDE.md"), Kind: SectionUser, Source: "claude"},
	{Relative: filepath.Join(".claudes", "CLAUDE.md"), Kind: SectionUser, Source: "claudes"},
	{Relative: filepath.Join(".cursor", "AGENTS.md"), Kind: SectionUser, Source: "cursor"},
	{Relative: filepath.Join(".codex", "AGENTS.md"), Kind: SectionUser, Source: "codex"},
}

func LoadProjectMemory(root string, opts MemoryLoadOptions) (ProjectMemoryBundle, error) {
	root, err := canonicalEnvironmentPath(root)
	if err != nil {
		return ProjectMemoryBundle{}, err
	}
	workspaceRoots := opts.WorkspaceRoots
	if len(workspaceRoots) == 0 {
		workspaceRoots = []string{root}
	} else {
		workspaceRoots = cleanPaths(workspaceRoots)
	}
	maxTotal := opts.MaxTotalBytes
	if maxTotal <= 0 {
		maxTotal = DefaultMemoryMaxBytes
	}
	maxBytes := opts.MaxBytesPerSection
	if maxBytes <= 0 {
		maxBytes = DefaultSectionMaxBytes
	}
	maxLines := opts.MaxLinesPerSection
	if maxLines <= 0 {
		maxLines = DefaultSectionMaxLines
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	home := strings.TrimSpace(opts.HomeDir)
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	expander := newImportExpander(workspaceRoots, home, opts.ImportMaxDepth, maxBytes, maxLines)
	capacity := len(primaryProjectMemoryCandidates) + len(fallbackProjectMemoryCandidates)
	if !opts.DisableUser {
		capacity += len(primaryUserMemoryCandidates) + len(fallbackUserMemoryCandidates)
	}
	sections := make([]Section, 0, capacity)
	totalBytes := 0
	budgetTruncated := false
	seenContent := map[string]bool{}
	rollbackImports := func(start int) {
		for _, imported := range expander.imports[start:] {
			delete(expander.importSeen, imported.Path)
		}
		expander.imports = expander.imports[:start]
	}
	appendCandidate := func(base string, candidate memoryCandidate) {
		importsStart := len(expander.imports)
		section, contentID, ok := loadMemorySection(filepath.Join(base, candidate.Relative), candidate, maxBytes, maxLines, expander)
		if !ok || seenContent[contentID] {
			rollbackImports(importsStart)
			return
		}
		remaining := maxTotal - totalBytes
		if remaining <= 0 {
			rollbackImports(importsStart)
			budgetTruncated = true
			return
		}
		if section.LoadedBytes > remaining {
			limited, _ := limitInstructionText([]byte(section.Content), remaining, maxLines)
			section.Content = strings.TrimSpace(limited)
			section.LoadedBytes = len([]byte(section.Content))
			section.Truncated = true
			budgetTruncated = true
			if section.Content == "" {
				rollbackImports(importsStart)
				return
			}
		}
		keptImports := expander.imports[:importsStart]
		for _, imported := range expander.imports[importsStart:] {
			if strings.Contains(section.Content, "<!-- @import "+imported.Path+" -->") {
				keptImports = append(keptImports, imported)
				continue
			}
			delete(expander.importSeen, imported.Path)
		}
		expander.imports = keptImports
		seenContent[contentID] = true
		sections = append(sections, section)
		totalBytes += section.LoadedBytes
	}
	appendAll := func(base string, candidates []memoryCandidate, userLevel bool) {
		for _, candidate := range candidates {
			if userLevel && !opts.SourcePolicy.Enabled(candidate.Source, instructionpolicy.ResourceContext) {
				continue
			}
			appendCandidate(base, candidate)
		}
	}
	appendAll(root, primaryProjectMemoryCandidates, false)
	if !opts.DisableUser {
		appendAll(home, primaryUserMemoryCandidates, true)
	}
	appendAll(root, fallbackProjectMemoryCandidates, false)
	if !opts.DisableUser {
		appendAll(home, fallbackUserMemoryCandidates, true)
	}
	return ProjectMemoryBundle{
		Root: root, WorkspaceRoots: workspaceRoots, Sections: sections, Imports: expander.imports,
		TotalBytes: totalBytes, BudgetBytes: maxTotal, BudgetTruncated: budgetTruncated, LoadedAt: now().UTC(),
	}, nil
}

func loadMemorySection(path string, candidate memoryCandidate, maxBytes, maxLines int, expander *importExpander) (Section, string, bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return Section{}, "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Section{}, "", false
	}
	expanded := expander.expand(string(data), path)
	content, truncated := limitInstructionText([]byte(expanded), maxBytes, maxLines)
	content = strings.TrimSpace(content)
	if content == "" {
		return Section{}, "", false
	}
	loadedBytes := len([]byte(content))
	normalized := strings.TrimSpace(strings.ReplaceAll(stripHTMLComments(expanded), "\r\n", "\n"))
	sum := sha256.Sum256([]byte(normalized))
	return Section{
		Path: path, Kind: candidate.Kind, Source: candidate.Source, Content: content,
		Truncated: truncated, OriginalBytes: len(data), LoadedBytes: loadedBytes,
	}, hex.EncodeToString(sum[:]), true
}

func limitInstructionText(data []byte, maxBytes, maxLines int) (string, bool) {
	original := string(data)
	lines := strings.Split(strings.ReplaceAll(original, "\r\n", "\n"), "\n")
	truncated := false
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		truncated = true
	}
	limited := []byte(strings.Join(lines, "\n"))
	if len(limited) > maxBytes {
		limited = limited[:maxBytes]
		for len(limited) > 0 && !utf8.Valid(limited) {
			limited = limited[:len(limited)-1]
		}
		truncated = true
	}
	return string(limited), truncated
}

func cleanPaths(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		canonical, err := canonicalEnvironmentPath(value)
		if err != nil || seen[canonical] {
			continue
		}
		seen[canonical] = true
		result = append(result, canonical)
	}
	return result
}
