package tools

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"go.mewis.me/codemcp/internal/instructionsource"
	"go.mewis.me/codemcp/internal/workspace"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

const (
	maxBinaryChunk        = 8 * 1024 * 1024
	maxTextReadBytes      = 4 * 1024 * 1024
	maxMutationFileBytes  = 16 * 1024 * 1024
	maxSearchFileBytes    = 16 * 1024 * 1024
	maxTextSelectionLines = 100_000
	maxTextLineOffset     = 1_000_000_000
)

func rejectManagedFilesystemMutation(item workspace.Workspace, paths ...string) error {
	store := workspacestate.New(item.Path)
	plansRoot := filepath.Clean(store.PlansRoot())
	instructionRoots := []string{filepath.Clean(store.RulesRoot()), filepath.Clean(store.SkillsRoot())}
	for _, candidate := range paths {
		candidate = filepath.Clean(candidate)
		if pathsOverlap(candidate, plansRoot) {
			return fmt.Errorf("path is managed by the canonical plan authoring service: %s", candidate)
		}
		for _, instructionRoot := range instructionRoots {
			if pathsOverlap(candidate, instructionRoot) {
				return fmt.Errorf("path is managed by the canonical instruction authoring service: %s", candidate)
			}
		}
		if providerRoot, ok := readOnlyDynamicProviderRoot(item.Path, candidate); ok {
			return fmt.Errorf("path belongs to a read-only dynamic instruction provider: %s", providerRoot)
		}
	}
	return nil
}

func readOnlyDynamicProviderRoot(workspaceRoot, candidate string) (string, bool) {
	workspaceRoot = filepath.Clean(workspaceRoot)
	relative, err := filepath.Rel(workspaceRoot, filepath.Clean(candidate))
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	parts := strings.Split(relative, string(filepath.Separator))
	for index, part := range parts {
		if _, ok := instructionsource.DynamicProviderIdentity(part); !ok {
			continue
		}
		providerRoot := filepath.Join(append([]string{workspaceRoot}, parts[:index+1]...)...)
		remainder := parts[index+1:]
		if dynamicProviderResourcePath(remainder) || dynamicProviderIsDiscovered(providerRoot) {
			return providerRoot, true
		}
	}
	return "", false
}

func dynamicProviderResourcePath(relative []string) bool {
	if len(relative) == 0 {
		return false
	}
	if len(relative) == 1 && (relative[0] == "AGENTS.md" || relative[0] == "CLAUDE.md") {
		return true
	}
	return relative[0] == "rules" || relative[0] == "skills"
}

func dynamicProviderIsDiscovered(providerRoot string) bool {
	providers, err := instructionsource.DiscoverDynamicProviders(filepath.Dir(providerRoot))
	if err != nil {
		return false
	}
	providerRoot = filepath.Clean(providerRoot)
	for _, provider := range providers {
		if filepath.Clean(provider.Path) == providerRoot {
			return true
		}
	}
	return false
}

func pathsOverlap(left, right string) bool {
	return pathContains(left, right) || pathContains(right, left)
}

func pathContains(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

type ReadTextFileResult struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Offset  *int   `json:"offset,omitempty"`
	Limit   *int   `json:"limit,omitempty"`
	Lines   *int   `json:"lines,omitempty"`
	Head    *int   `json:"head,omitempty"`
	Tail    *int   `json:"tail,omitempty"`
}

type ReadFileBase64Result struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	Offset     int64  `json:"offset"`
	BytesRead  int    `json:"bytes_read"`
	NextOffset *int64 `json:"next_offset"`
	Done       bool   `json:"done"`
	Encoding   string `json:"encoding"`
	Content    string `json:"content"`
}

type WriteFileResult struct {
	Path         string  `json:"path"`
	Bytes        int     `json:"bytes"`
	CheckpointID *string `json:"checkpoint_id"`
}

type EditFileResult struct {
	Path         string  `json:"path"`
	Diff         string  `json:"diff"`
	DryRun       bool    `json:"dry_run"`
	CheckpointID *string `json:"checkpoint_id"`
}

type MultiEditResult struct {
	Path         string  `json:"path"`
	Diff         string  `json:"diff"`
	Edits        int     `json:"edits"`
	DryRun       bool    `json:"dry_run"`
	CheckpointID *string `json:"checkpoint_id"`
}

type ApplyPatchResult struct {
	Path         string           `json:"path,omitempty"`
	Diff         string           `json:"diff,omitempty"`
	Files        []map[string]any `json:"files,omitempty"`
	DryRun       bool             `json:"dry_run"`
	MultiFile    bool             `json:"multi_file,omitempty"`
	CheckpointID *string          `json:"checkpoint_id"`
}

type DirectoryItem struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type ListDirectoryResult struct {
	Path    string          `json:"path"`
	Entries []DirectoryItem `json:"entries"`
	Count   int             `json:"count"`
}

type GlobResult struct {
	Path    string   `json:"path"`
	Pattern string   `json:"pattern"`
	Matches []string `json:"matches"`
	Count   int      `json:"count"`
}

type GrepResult struct {
	Path       string `json:"path"`
	Pattern    string `json:"pattern"`
	OutputMode string `json:"output_mode"`
	Output     string `json:"output"`
}

type DeleteResult struct {
	Path         string  `json:"path"`
	CheckpointID *string `json:"checkpoint_id"`
}

type CreateDirectoryResult struct {
	Path string `json:"path"`
}

type CopyMoveResult struct {
	Source       string  `json:"source"`
	Destination  string  `json:"destination"`
	CheckpointID *string `json:"checkpoint_id"`
}

type DirectoryTreeResult struct {
	Path     string   `json:"path"`
	Tree     TreeNode `json:"tree"`
	MaxDepth int      `json:"max_depth"`
}

type AllowedDirectoriesResult struct {
	FullMachineAccess bool     `json:"full_machine_access"`
	Permission        string   `json:"permission"`
	DefaultCWD        string   `json:"default_cwd"`
	MachineRoots      []string `json:"machine_roots"`
	WorkspaceID       string   `json:"workspace_id"`
	WorkspaceRoot     string   `json:"workspace_root"`
}

type EditSpec struct {
	OldText    string
	NewText    string
	ReplaceAll bool
}

type rootedPath struct {
	root     *os.Root
	relative string
	absolute string
}

type rootedDirectory struct {
	root     *os.Root
	absolute string
}

func openRootedPath(workspaces *workspace.Manager, workspaceID, absolute string) (*rootedPath, error) {
	root, relative, err := workspaces.OpenRootForPath(workspaceID, absolute)
	if err != nil {
		return nil, err
	}
	return &rootedPath{root: root, relative: relative, absolute: absolute}, nil
}

func (p *rootedPath) Close() error {
	if p == nil || p.root == nil {
		return nil
	}
	return p.root.Close()
}

func (p *rootedPath) Open() (*os.File, error) {
	if p == nil || p.root == nil {
		return nil, errors.New("rooted path is unavailable")
	}
	return p.root.Open(p.relative)
}

func (p *rootedPath) Stat() (os.FileInfo, error) {
	if p == nil || p.root == nil {
		return nil, errors.New("rooted path is unavailable")
	}
	return p.root.Stat(p.relative)
}

func (p *rootedPath) WriteFile(data []byte, perm os.FileMode) error {
	if p == nil || p.root == nil {
		return errors.New("rooted path is unavailable")
	}
	parent := filepath.Dir(p.relative)
	if parent != "." {
		if err := p.root.MkdirAll(parent, 0755); err != nil {
			return err
		}
	}
	return p.root.WriteFile(p.relative, data, perm)
}

func (p *rootedPath) MkdirAll(perm os.FileMode) error {
	if p == nil || p.root == nil {
		return errors.New("rooted path is unavailable")
	}
	return p.root.MkdirAll(p.relative, perm)
}

func (p *rootedPath) Remove() error {
	if p == nil || p.root == nil {
		return errors.New("rooted path is unavailable")
	}
	return p.root.Remove(p.relative)
}

func (p *rootedPath) RemoveAll() error {
	if p == nil || p.root == nil {
		return errors.New("rooted path is unavailable")
	}
	return p.root.RemoveAll(p.relative)
}

func openRootedDirectory(workspaces *workspace.Manager, workspaceID, absolute string) (*rootedDirectory, error) {
	path, err := openRootedPath(workspaces, workspaceID, absolute)
	if err != nil {
		return nil, err
	}
	info, err := path.Stat()
	if err != nil {
		_ = path.Close()
		return nil, err
	}
	if !info.IsDir() {
		_ = path.Close()
		return nil, errors.New("path is not a directory")
	}
	root, err := path.root.OpenRoot(path.relative)
	_ = path.Close()
	if err != nil {
		return nil, err
	}
	return &rootedDirectory{root: root, absolute: absolute}, nil
}

func (d *rootedDirectory) Close() error {
	if d == nil || d.root == nil {
		return nil
	}
	return d.root.Close()
}

func (d *rootedDirectory) ReadDir() ([]os.DirEntry, error) {
	if d == nil || d.root == nil {
		return nil, errors.New("rooted directory is unavailable")
	}
	file, err := d.root.Open(".")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return file.ReadDir(-1)
}

func (d *rootedDirectory) OpenChild(name string) (*rootedDirectory, error) {
	if d == nil || d.root == nil {
		return nil, errors.New("rooted directory is unavailable")
	}
	root, err := d.root.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	return &rootedDirectory{root: root, absolute: filepath.Join(d.absolute, name)}, nil
}

func (d *rootedDirectory) ReadRegularFileLimited(name string, maxBytes int64, operation string) ([]byte, error) {
	if d == nil || d.root == nil {
		return nil, errors.New("rooted directory is unavailable")
	}
	file, err := d.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("path is not a regular file")
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("%s exceeds %s limit (%d bytes)", operation, byteLimitLabel(maxBytes), info.Size())
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%s exceeds %s limit", operation, byteLimitLabel(maxBytes))
	}
	return data, nil
}

func checkpointPointer(id string) *string {
	if id == "" {
		return nil
	}
	return &id
}

func readTextSlice(content string, offset, limit, head, tail *int) ReadTextFileResult {
	lines := strings.Split(content, "\n")
	result := ReadTextFileResult{}
	if offset != nil {
		start := *offset - 1
		if start < 0 {
			start = 0
		}
		if start > len(lines) {
			start = len(lines)
		}
		end := len(lines)
		if limit != nil && start+*limit < end {
			end = start + *limit
		}
		slice := lines[start:end]
		numbered := make([]string, len(slice))
		for i, line := range slice {
			numbered[i] = fmt.Sprintf("%6d|%s", start+i+1, line)
		}
		count := len(slice)
		result.Content = strings.Join(numbered, "\n")
		result.Offset = offset
		result.Limit = limit
		result.Lines = &count
		return result
	}
	switch {
	case head != nil:
		count := *head
		if count < 0 {
			count = 0
		}
		if count > len(lines) {
			count = len(lines)
		}
		result.Content = strings.Join(lines[:count], "\n")
		result.Head = head
	case tail != nil:
		count := *tail
		if count < 0 {
			count = 0
		}
		if count > len(lines) {
			count = len(lines)
		}
		result.Content = strings.Join(lines[len(lines)-count:], "\n")
		result.Tail = tail
	default:
		result.Content = content
	}
	return result
}

func readRootedTextFile(path *rootedPath, offset, limit, head, tail *int) (ReadTextFileResult, error) {
	file, err := path.Open()
	if err != nil {
		return ReadTextFileResult{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ReadTextFileResult{}, err
	}
	if !info.Mode().IsRegular() {
		return ReadTextFileResult{}, errors.New("path is not a regular file")
	}
	if offset == nil && head == nil && tail == nil {
		if info.Size() > maxTextReadBytes {
			return ReadTextFileResult{}, fmt.Errorf("text read exceeds %s limit (%d bytes); use a partial/chunked operation", byteLimitLabel(maxTextReadBytes), info.Size())
		}
		data, err := io.ReadAll(io.LimitReader(file, maxTextReadBytes+1))
		if err != nil {
			return ReadTextFileResult{}, err
		}
		if len(data) > maxTextReadBytes {
			return ReadTextFileResult{}, textOutputLimitError()
		}
		return readTextSlice(string(data), nil, nil, nil, nil), nil
	}
	reader := bufio.NewReader(file)
	if offset != nil {
		return readTextOffset(reader, offset, limit)
	}
	if head != nil {
		return readTextHead(reader, head)
	}
	return readTextTail(reader, tail)
}

func readTextOffset(reader *bufio.Reader, offset, limit *int) (ReadTextFileResult, error) {
	start := *offset
	if start < 1 {
		start = 1
	}
	want := maxTextSelectionLines
	if limit != nil {
		want = *limit
	}
	lines := make([]string, 0, min(want, 256))
	lineNumber := 1
	bytes := 0
	for len(lines) < want {
		capture := lineNumber >= start
		line, ok, err := readLogicalLine(reader, capture, maxTextReadBytes-bytes)
		if err != nil {
			return ReadTextFileResult{}, err
		}
		if !ok {
			break
		}
		if capture {
			numbered := fmt.Sprintf("%6d|%s", lineNumber, line)
			if len(lines) > 0 {
				bytes++
			}
			bytes += len(numbered)
			if bytes > maxTextReadBytes {
				return ReadTextFileResult{}, textOutputLimitError()
			}
			lines = append(lines, numbered)
		}
		lineNumber++
	}
	if limit == nil && len(lines) == maxTextSelectionLines {
		if _, ok, err := readLogicalLine(reader, false, 0); err != nil {
			return ReadTextFileResult{}, err
		} else if ok {
			return ReadTextFileResult{}, fmt.Errorf("text read exceeds %d-line selection limit; provide limit to narrow the result", maxTextSelectionLines)
		}
	}
	count := len(lines)
	return ReadTextFileResult{Content: strings.Join(lines, "\n"), Offset: offset, Limit: limit, Lines: &count}, nil
}

func readTextHead(reader *bufio.Reader, head *int) (ReadTextFileResult, error) {
	want := *head
	lines := make([]string, 0, min(want, 256))
	bytes := 0
	for len(lines) < want {
		line, ok, err := readLogicalLine(reader, true, maxTextReadBytes-bytes)
		if err != nil {
			return ReadTextFileResult{}, err
		}
		if !ok {
			break
		}
		if len(lines) > 0 {
			bytes++
		}
		bytes += len(line)
		if bytes > maxTextReadBytes {
			return ReadTextFileResult{}, textOutputLimitError()
		}
		lines = append(lines, line)
	}
	return ReadTextFileResult{Content: strings.Join(lines, "\n"), Head: head}, nil
}

func readTextTail(reader *bufio.Reader, tail *int) (ReadTextFileResult, error) {
	want := *tail
	if want == 0 {
		return ReadTextFileResult{Content: "", Tail: tail}, nil
	}
	ring := make([]string, 0, min(want, 256))
	bytes := 0
	truncatedByBytes := false
	for {
		line, ok, err := readLogicalLine(reader, true, maxTextReadBytes)
		if err != nil {
			return ReadTextFileResult{}, err
		}
		if !ok {
			break
		}
		lineBytes := len(line)
		if len(ring) > 0 {
			lineBytes++
		}
		ring = append(ring, line)
		bytes += lineBytes
		if len(ring) > want {
			bytes -= len(ring[0])
			if len(ring) > 1 {
				bytes--
			}
			ring = ring[1:]
		}
		for bytes > maxTextReadBytes && len(ring) > 0 {
			truncatedByBytes = true
			bytes -= len(ring[0])
			if len(ring) > 1 {
				bytes--
			}
			ring = ring[1:]
		}
	}
	if truncatedByBytes && len(ring) < want {
		return ReadTextFileResult{}, textOutputLimitError()
	}
	return ReadTextFileResult{Content: strings.Join(ring, "\n"), Tail: tail}, nil
}

func readLogicalLine(reader *bufio.Reader, capture bool, remaining int) (string, bool, error) {
	if remaining < 0 {
		return "", false, textOutputLimitError()
	}
	var builder strings.Builder
	for {
		fragment, err := reader.ReadSlice('\n')
		if capture {
			if builder.Len()+len(fragment) > remaining+1 {
				return "", false, textOutputLimitError()
			}
			builder.Write(fragment)
		}
		switch {
		case err == nil:
			line := builder.String()
			return strings.TrimSuffix(line, "\n"), true, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if len(fragment) == 0 && builder.Len() == 0 {
				return "", false, nil
			}
			return builder.String(), true, nil
		default:
			return "", false, err
		}
	}
}

func readRootedRegularFileLimited(path *rootedPath, maxBytes int64, operation string) ([]byte, error) {
	file, err := path.Open()
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("path is not a regular file")
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("%s exceeds %s limit (%d bytes); use a partial/chunked operation", operation, byteLimitLabel(maxBytes), info.Size())
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%s exceeds %s limit", operation, byteLimitLabel(maxBytes))
	}
	return data, nil
}

func validateMutationPayload(size int) error {
	if int64(size) > maxMutationFileBytes {
		return fmt.Errorf("mutation payload exceeds %s limit", byteLimitLabel(maxMutationFileBytes))
	}
	return nil
}

func textOutputLimitError() error {
	return fmt.Errorf("text read exceeds %s output limit; reduce offset/limit/head/tail or use read_file_base64", byteLimitLabel(maxTextReadBytes))
}

func byteLimitLabel(bytes int64) string {
	if bytes%(1024*1024) == 0 {
		return fmt.Sprintf("%d MiB", bytes/(1024*1024))
	}
	return fmt.Sprintf("%d bytes", bytes)
}

func readRootedBase64Chunk(path *rootedPath, offset int64, length int) (ReadFileBase64Result, error) {
	file, err := path.Open()
	if err != nil {
		return ReadFileBase64Result{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ReadFileBase64Result{}, err
	}
	if !info.Mode().IsRegular() {
		return ReadFileBase64Result{}, errors.New("path is not a regular file")
	}
	if offset < 0 {
		return ReadFileBase64Result{}, errors.New("offset must be non-negative")
	}
	if offset > info.Size() {
		offset = info.Size()
	}
	if length <= 0 {
		return ReadFileBase64Result{}, errors.New("length must be positive")
	}
	if length > maxBinaryChunk {
		length = maxBinaryChunk
	}
	remaining := info.Size() - offset
	if int64(length) > remaining {
		length = int(remaining)
	}
	buffer := make([]byte, length)
	bytesRead, err := file.ReadAt(buffer, offset)
	if err != nil && !errors.Is(err, io.EOF) && bytesRead == 0 && length > 0 {
		return ReadFileBase64Result{}, err
	}
	next := offset + int64(bytesRead)
	var nextOffset *int64
	if next < info.Size() {
		value := next
		nextOffset = &value
	}
	return ReadFileBase64Result{Path: path.absolute, Size: info.Size(), Offset: offset, BytesRead: bytesRead, NextOffset: nextOffset, Done: next >= info.Size(), Encoding: "base64", Content: base64.StdEncoding.EncodeToString(buffer[:bytesRead])}, nil
}

func decodeBase64(value string) ([]byte, error) {
	if int64(base64.StdEncoding.DecodedLen(len(value))) > maxMutationFileBytes {
		return nil, fmt.Errorf("decoded mutation payload exceeds %s limit", byteLimitLabel(maxMutationFileBytes))
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("invalid base64 content: %w", err)
	}
	return data, nil
}

func replaceExact(content, oldText, newText string, replaceAll bool) (string, error) {
	if !strings.Contains(content, oldText) {
		return "", errors.New("old_text not found in file. Ensure exact match")
	}
	if replaceAll {
		return strings.ReplaceAll(content, oldText, newText), nil
	}
	return strings.Replace(content, oldText, newText, 1), nil
}

func replaceRegex(content, pattern, replacement, flags string) (string, error) {
	global := strings.Contains(flags, "g")
	inline := ""
	for _, flag := range flags {
		switch flag {
		case 'g', 'u':
		case 'i', 'm', 's':
			if !strings.ContainsRune(inline, flag) {
				inline += string(flag)
			}
		default:
			return "", fmt.Errorf("unsupported regex flag: %c", flag)
		}
	}
	if inline != "" {
		pattern = "(?" + inline + ")" + pattern
	}
	regex, err := regexp.Compile(pattern)
	if err != nil {
		return "", err
	}
	template := strings.ReplaceAll(replacement, "$&", "${0}")
	template = regexp.MustCompile(`\$<([A-Za-z_][A-Za-z0-9_]*)>`).ReplaceAllString(template, "${$1}")
	if global {
		next := regex.ReplaceAllString(content, template)
		if next == content {
			return "", errors.New("regex made no changes")
		}
		return next, nil
	}
	index := regex.FindStringSubmatchIndex(content)
	if index == nil {
		return "", errors.New("regex made no changes")
	}
	var expanded []byte
	expanded = regex.ExpandString(expanded, template, content, index)
	next := content[:index[0]] + string(expanded) + content[index[1]:]
	if next == content {
		return "", errors.New("regex made no changes")
	}
	return next, nil
}

func pathType(entry os.DirEntry) string {
	if entry.IsDir() {
		return "directory"
	}
	return "file"
}

func copyRootedFileContents(source, destination *rootedPath) error {
	if source == nil || destination == nil {
		return errors.New("rooted copy path is unavailable")
	}
	if filepath.Clean(source.absolute) == filepath.Clean(destination.absolute) {
		return nil
	}
	input, err := source.Open()
	if err != nil {
		return err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("source is not a file")
	}
	parent := filepath.Dir(destination.relative)
	if parent != "." {
		if err := destination.root.MkdirAll(parent, 0755); err != nil {
			return err
		}
	}
	output, err := destination.root.OpenFile(destination.relative, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
