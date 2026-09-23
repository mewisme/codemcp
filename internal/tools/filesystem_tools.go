package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"go.mewis.me/codemcp/internal/checkpoint"
	patcher "go.mewis.me/codemcp/internal/patch"
	"go.mewis.me/codemcp/internal/workspace"
)

func RegisterFilesystemTools(registry *Registry, workspaces *workspace.Manager, checkpoints *checkpoint.Store) {
	register := func(name, title, description, input, output string, risk Risk, handler Handler) {
		registry.MustRegister(name, Schema{Name: name, Title: title, Description: description, InputSchema: json.RawMessage(input), OutputSchema: json.RawMessage(output), Annotations: ToolAnnotations(risk)}, handler)
	}

	register("read_text_file", "Read Text File", "Read a file before editing. Full reads are capped at 4 MiB; use offset+limit, head, or tail for bounded reads of larger files.", `{"type":"object","properties":{"workspace_id":{"type":"string"},"path":{"type":"string"},"offset":{"type":"integer","minimum":1,"maximum":1000000000},"limit":{"type":"integer","minimum":1,"maximum":100000},"head":{"type":"integer","minimum":0,"maximum":100000},"tail":{"type":"integer","minimum":0,"maximum":100000}},"required":["workspace_id","path"],"additionalProperties":false}`, `{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"},"offset":{"type":"integer"},"limit":{"type":"integer"},"lines":{"type":"integer"},"head":{"type":"integer"},"tail":{"type":"integer"}},"required":["path","content"],"additionalProperties":false}`, RiskRead, handleReadTextFile(workspaces))
	register("read_file_base64", "Read File Base64", "Read any workspace file as base64. Use offset/length for large files. Max chunk 8 MiB.", `{"type":"object","properties":{"workspace_id":{"type":"string"},"path":{"type":"string"},"offset":{"type":"integer","minimum":0,"default":0},"length":{"type":"integer","minimum":1,"maximum":8388608,"default":1048576}},"required":["workspace_id","path"],"additionalProperties":false}`, `{"type":"object","properties":{"path":{"type":"string"},"size":{"type":"integer"},"offset":{"type":"integer"},"bytes_read":{"type":"integer"},"next_offset":{"type":["integer","null"]},"done":{"type":"boolean"},"encoding":{"type":"string"},"content":{"type":"string"}},"required":["path","size","offset","bytes_read","next_offset","done","encoding","content"],"additionalProperties":false}`, RiskRead, handleReadFileBase64(workspaces))
	register("write_file", "Write File", "Save text to a workspace file and capture a rewind checkpoint first.", `{"type":"object","properties":{"workspace_id":{"type":"string"},"path":{"type":"string"},"content":{"type":"string"}},"required":["workspace_id","path","content"],"additionalProperties":false}`, mutationOutputSchema(`"bytes":{"type":"integer"}`), RiskEdit, handleWriteFile(workspaces, checkpoints))
	register("write_file_base64", "Write File Base64", "Create or overwrite a binary workspace file from base64 content.", `{"type":"object","properties":{"workspace_id":{"type":"string"},"path":{"type":"string"},"content":{"type":"string"}},"required":["workspace_id","path","content"],"additionalProperties":false}`, mutationOutputSchema(`"bytes":{"type":"integer"}`), RiskEdit, handleWriteFileBase64(workspaces, checkpoints))
	register("edit_file", "Edit File", "Apply exact text replacement to a file. Returns a diff and supports dry_run.", editInputSchema(false), editOutputSchema(false), RiskEdit, handleEditFile(workspaces, checkpoints))
	register("multi_edit", "Multi Edit", "Apply multiple exact replacements to one text file atomically.", `{"type":"object","properties":{"workspace_id":{"type":"string"},"path":{"type":"string"},"edits":{"type":"array","items":{"type":"object","properties":{"old_text":{"type":"string"},"new_text":{"type":"string"},"replace_all":{"type":"boolean","default":false}},"required":["old_text","new_text"],"additionalProperties":false}},"dry_run":{"type":"boolean","default":false}},"required":["workspace_id","path","edits"],"additionalProperties":false}`, `{"type":"object","properties":{"path":{"type":"string"},"diff":{"type":"string"},"edits":{"type":"integer"},"dry_run":{"type":"boolean"},"checkpoint_id":{"type":["string","null"]}},"required":["path","diff","edits","dry_run","checkpoint_id"],"additionalProperties":false}`, RiskEdit, handleMultiEdit(workspaces, checkpoints))
	register("replace_regex", "Replace Regex", "Apply a regex replacement to a text file. Supports g/i/m/s flags and dry_run.", `{"type":"object","properties":{"workspace_id":{"type":"string"},"path":{"type":"string"},"pattern":{"type":"string"},"replacement":{"type":"string"},"flags":{"type":"string","default":"g"},"dry_run":{"type":"boolean","default":false}},"required":["workspace_id","path","pattern","replacement"],"additionalProperties":false}`, editOutputSchema(false), RiskEdit, handleReplaceRegex(workspaces, checkpoints))
	register("apply_patch", "Apply Patch", "Preferred code editing tool. Supports Codex @@ hunks, standard unified diff, and *** Begin Patch multi-file format.", `{"type":"object","properties":{"workspace_id":{"type":"string"},"path":{"type":"string"},"patch":{"type":"string"},"dry_run":{"type":"boolean","default":false}},"required":["workspace_id","patch"],"additionalProperties":false}`, `{"type":"object","properties":{"path":{"type":"string"},"diff":{"type":"string"},"files":{"type":"array","items":{"type":"object","additionalProperties":true}},"dry_run":{"type":"boolean"},"multi_file":{"type":"boolean"},"checkpoint_id":{"type":["string","null"]}},"required":["dry_run","checkpoint_id"],"additionalProperties":false}`, RiskEdit, handleApplyPatch(workspaces, checkpoints))
	register("list_directory", "List Directory", "List files and directories with optional ignore globs.", `{"type":"object","properties":{"workspace_id":{"type":"string"},"path":{"type":"string"},"ignore":{"type":"array","items":{"type":"string"}}},"required":["workspace_id","path"],"additionalProperties":false}`, `{"type":"object","properties":{"path":{"type":"string"},"entries":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},"type":{"type":"string"}},"required":["name","type"],"additionalProperties":false}},"count":{"type":"integer"}},"required":["path","entries","count"],"additionalProperties":false}`, RiskRead, handleListDirectory(workspaces))
	register("glob", "Glob", "Find files by name pattern under a workspace directory.", `{"type":"object","properties":{"workspace_id":{"type":"string"},"pattern":{"type":"string"},"path":{"type":"string"},"max_results":{"type":"integer","minimum":1,"maximum":500,"default":100}},"required":["workspace_id","pattern"],"additionalProperties":false}`, `{"type":"object","properties":{"path":{"type":"string"},"pattern":{"type":"string"},"matches":{"type":"array","items":{"type":"string"}},"count":{"type":"integer"}},"required":["path","pattern","matches","count"],"additionalProperties":false}`, RiskRead, handleGlob(workspaces))
	register("grep", "Grep", "Search a workspace file or directory by regex. Modes: content, files_with_matches, count.", grepInputSchema(), `{"type":"object","properties":{"path":{"type":"string"},"pattern":{"type":"string"},"output_mode":{"type":"string"},"output":{"type":"string"}},"required":["path","pattern","output_mode","output"],"additionalProperties":false}`, RiskRead, handleGrep(workspaces))
	register("delete_file", "Delete File", "Delete a workspace file after capturing a rewind checkpoint.", basePathInputSchema(), `{"type":"object","properties":{"path":{"type":"string"},"checkpoint_id":{"type":["string","null"]}},"required":["path","checkpoint_id"],"additionalProperties":false}`, RiskDestructive, handleDeleteFile(workspaces, checkpoints))
	register("create_directory", "Create Directory", "Create a workspace directory and parents if needed.", basePathInputSchema(), `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`, RiskEdit, handleCreateDirectory(workspaces))
	register("delete_directory", "Remove Local Folder", "Recursively remove a workspace folder after capturing a rewind checkpoint.", basePathInputSchema(), `{"type":"object","properties":{"path":{"type":"string"},"checkpoint_id":{"type":["string","null"]},"run_command_fallback":{"type":"string"}},"required":["path","checkpoint_id","run_command_fallback"],"additionalProperties":false}`, RiskDestructive, handleDeleteDirectory(workspaces, checkpoints))
	register("copy_file", "Copy File", "Copy a workspace file to a new workspace location.", sourceDestinationInputSchema(), `{"type":"object","properties":{"source":{"type":"string"},"destination":{"type":"string"},"checkpoint_id":{"type":["string","null"]}},"required":["source","destination","checkpoint_id"],"additionalProperties":false}`, RiskEdit, handleCopyFile(workspaces, checkpoints))
	register("move_file", "Move File", "Move or rename a file or directory. Source and destination must remain inside the registered workspace.", sourceDestinationInputSchema(), `{"type":"object","properties":{"source":{"type":"string"},"destination":{"type":"string"},"checkpoint_id":{"type":["string","null"]}},"required":["source","destination","checkpoint_id"],"additionalProperties":false}`, RiskEdit, handleMoveFile(workspaces, checkpoints))
	register("directory_tree", "Directory Tree", "Get recursive workspace directory structure as JSON.", `{"type":"object","properties":{"workspace_id":{"type":"string"},"path":{"type":"string"},"max_depth":{"type":"integer","minimum":0,"maximum":128,"default":4}},"required":["workspace_id","path"],"additionalProperties":false}`, `{"type":"object","properties":{"path":{"type":"string"},"tree":{"type":"object","additionalProperties":true},"max_depth":{"type":"integer"}},"required":["path","tree","max_depth"],"additionalProperties":false}`, RiskRead, handleDirectoryTree(workspaces))
	register("list_allowed_directories", "List Allowed Directories", "Show the registered workspace access scope for this tool workflow.", `{"type":"object","properties":{"workspace_id":{"type":"string"}},"required":["workspace_id"],"additionalProperties":false}`, `{"type":"object","properties":{"full_machine_access":{"type":"boolean"},"permission":{"type":"string"},"default_cwd":{"type":"string"},"machine_roots":{"type":"array","items":{"type":"string"}},"workspace_id":{"type":"string"},"workspace_root":{"type":"string"}},"required":["full_machine_access","permission","default_cwd","machine_roots","workspace_id","workspace_root"],"additionalProperties":false}`, RiskRead, handleListAllowedDirectories(workspaces))
}

func handleReadTextFile(workspaces *workspace.Manager) Handler {
	return func(_ context.Context, args map[string]any) (Result, error) {
		item, _, file, err := workspacePath(workspaces, args, "path", true)
		if err != nil {
			return Result{}, err
		}
		offset, err := optionalIntPointer(args, "offset", 1, maxTextLineOffset)
		if err != nil {
			return Result{}, err
		}
		limit, err := optionalIntPointer(args, "limit", 1, maxTextSelectionLines)
		if err != nil {
			return Result{}, err
		}
		head, err := optionalIntPointer(args, "head", 0, maxTextSelectionLines)
		if err != nil {
			return Result{}, err
		}
		tail, err := optionalIntPointer(args, "tail", 0, maxTextSelectionLines)
		if err != nil {
			return Result{}, err
		}
		if limit != nil && offset == nil {
			return Result{}, errors.New("limit requires offset")
		}
		selectors := 0
		for _, value := range []*int{offset, head, tail} {
			if value != nil {
				selectors++
			}
		}
		if selectors > 1 {
			return Result{}, errors.New("offset, head, and tail are mutually exclusive")
		}
		rooted, err := openRootedPath(workspaces, item.ID, file)
		if err != nil {
			return Result{}, err
		}
		defer rooted.Close()
		value, err := readRootedTextFile(rooted, offset, limit, head, tail)
		if err != nil {
			return Result{}, err
		}
		value.Path = file
		return JSONResult(value), nil
	}
}

func handleReadFileBase64(workspaces *workspace.Manager) Handler {
	return func(_ context.Context, args map[string]any) (Result, error) {
		item, _, file, err := workspacePath(workspaces, args, "path", true)
		if err != nil {
			return Result{}, err
		}
		offset, err := optionalInt64(args, "offset", 0, 0, 1<<62)
		if err != nil {
			return Result{}, err
		}
		length, err := optionalInt(args, "length", 1024*1024, 1, maxBinaryChunk)
		if err != nil {
			return Result{}, err
		}
		rooted, err := openRootedPath(workspaces, item.ID, file)
		if err != nil {
			return Result{}, err
		}
		defer rooted.Close()
		value, err := readRootedBase64Chunk(rooted, offset, length)
		if err != nil {
			return Result{}, err
		}
		return JSONResult(value), nil
	}
}

func handleWriteFile(workspaces *workspace.Manager, checkpoints *checkpoint.Store) Handler {
	return func(_ context.Context, args map[string]any) (Result, error) {
		item, _, file, err := workspacePath(workspaces, args, "path", false)
		if err != nil {
			return Result{}, err
		}
		content, err := stringArgAllowEmpty(args, "content")
		if err != nil {
			return Result{}, err
		}
		if err := validateMutationPayload(len(content)); err != nil {
			return Result{}, err
		}
		checkpointID, err := checkpointBefore(checkpoints, workspaces, item, "write_file", []string{file}, false)
		if err != nil {
			return Result{}, err
		}
		rooted, err := openRootedPath(workspaces, item.ID, file)
		if err != nil {
			return Result{}, err
		}
		defer rooted.Close()
		if err := rooted.WriteFile([]byte(content), 0644); err != nil {
			return Result{}, err
		}
		return JSONResult(WriteFileResult{Path: file, Bytes: len([]byte(content)), CheckpointID: checkpointPointer(checkpointID)}), nil
	}
}

func handleWriteFileBase64(workspaces *workspace.Manager, checkpoints *checkpoint.Store) Handler {
	return func(_ context.Context, args map[string]any) (Result, error) {
		item, _, file, err := workspacePath(workspaces, args, "path", false)
		if err != nil {
			return Result{}, err
		}
		content, err := stringArgAllowEmpty(args, "content")
		if err != nil {
			return Result{}, err
		}
		data, err := decodeBase64(content)
		if err != nil {
			return Result{}, err
		}
		checkpointID, err := checkpointBefore(checkpoints, workspaces, item, "write_file_base64", []string{file}, false)
		if err != nil {
			return Result{}, err
		}
		rooted, err := openRootedPath(workspaces, item.ID, file)
		if err != nil {
			return Result{}, err
		}
		defer rooted.Close()
		if err := rooted.WriteFile(data, 0644); err != nil {
			return Result{}, err
		}
		return JSONResult(WriteFileResult{Path: file, Bytes: len(data), CheckpointID: checkpointPointer(checkpointID)}), nil
	}
}

func handleEditFile(workspaces *workspace.Manager, checkpoints *checkpoint.Store) Handler {
	return func(_ context.Context, args map[string]any) (Result, error) {
		item, _, file, err := workspacePath(workspaces, args, "path", true)
		if err != nil {
			return Result{}, err
		}
		oldText, err := stringArg(args, "old_text")
		if err != nil {
			return Result{}, err
		}
		newText, err := stringArgAllowEmpty(args, "new_text")
		if err != nil {
			return Result{}, err
		}
		replaceAll, err := optionalBool(args, "replace_all", false)
		if err != nil {
			return Result{}, err
		}
		dryRun, err := optionalBool(args, "dry_run", false)
		if err != nil {
			return Result{}, err
		}
		rooted, err := openRootedPath(workspaces, item.ID, file)
		if err != nil {
			return Result{}, err
		}
		defer rooted.Close()
		original, err := readRootedRegularFileLimited(rooted, maxMutationFileBytes, "text edit")
		if err != nil {
			return Result{}, err
		}
		next, err := replaceExact(string(original), oldText, newText, replaceAll)
		if err != nil {
			return Result{}, err
		}
		if err := validateMutationPayload(len(next)); err != nil {
			return Result{}, err
		}
		diff := patcher.BuildSimpleDiff(string(original), next)
		checkpointID, err := checkpointBefore(checkpoints, workspaces, item, "edit_file", []string{file}, dryRun)
		if err != nil {
			return Result{}, err
		}
		if !dryRun {
			if err := rooted.WriteFile([]byte(next), 0644); err != nil {
				return Result{}, err
			}
		}
		return JSONResult(EditFileResult{Path: file, Diff: diff, DryRun: dryRun, CheckpointID: checkpointPointer(checkpointID)}), nil
	}
}

func handleMultiEdit(workspaces *workspace.Manager, checkpoints *checkpoint.Store) Handler {
	return func(_ context.Context, args map[string]any) (Result, error) {
		item, _, file, err := workspacePath(workspaces, args, "path", true)
		if err != nil {
			return Result{}, err
		}
		edits, err := editSpecs(args["edits"])
		if err != nil {
			return Result{}, err
		}
		dryRun, err := optionalBool(args, "dry_run", false)
		if err != nil {
			return Result{}, err
		}
		rooted, err := openRootedPath(workspaces, item.ID, file)
		if err != nil {
			return Result{}, err
		}
		defer rooted.Close()
		original, err := readRootedRegularFileLimited(rooted, maxMutationFileBytes, "multi edit")
		if err != nil {
			return Result{}, err
		}
		next := string(original)
		for _, edit := range edits {
			next, err = replaceExact(next, edit.OldText, edit.NewText, edit.ReplaceAll)
			if err != nil {
				preview := edit.OldText
				if len(preview) > 120 {
					preview = preview[:120]
				}
				return Result{}, fmt.Errorf("old_text not found: %s", preview)
			}
			if err := validateMutationPayload(len(next)); err != nil {
				return Result{}, err
			}
		}
		diff := patcher.BuildSimpleDiff(string(original), next)
		checkpointID, err := checkpointBefore(checkpoints, workspaces, item, "multi_edit", []string{file}, dryRun)
		if err != nil {
			return Result{}, err
		}
		if !dryRun {
			if err := rooted.WriteFile([]byte(next), 0644); err != nil {
				return Result{}, err
			}
		}
		return JSONResult(MultiEditResult{Path: file, Diff: diff, Edits: len(edits), DryRun: dryRun, CheckpointID: checkpointPointer(checkpointID)}), nil
	}
}

func handleReplaceRegex(workspaces *workspace.Manager, checkpoints *checkpoint.Store) Handler {
	return func(_ context.Context, args map[string]any) (Result, error) {
		item, _, file, err := workspacePath(workspaces, args, "path", true)
		if err != nil {
			return Result{}, err
		}
		pattern, err := stringArg(args, "pattern")
		if err != nil {
			return Result{}, err
		}
		replacement, err := stringArgAllowEmpty(args, "replacement")
		if err != nil {
			return Result{}, err
		}
		flags, err := optionalStringDefault(args, "flags", "g")
		if err != nil {
			return Result{}, err
		}
		dryRun, err := optionalBool(args, "dry_run", false)
		if err != nil {
			return Result{}, err
		}
		rooted, err := openRootedPath(workspaces, item.ID, file)
		if err != nil {
			return Result{}, err
		}
		defer rooted.Close()
		original, err := readRootedRegularFileLimited(rooted, maxMutationFileBytes, "regex edit")
		if err != nil {
			return Result{}, err
		}
		next, err := replaceRegex(string(original), pattern, replacement, flags)
		if err != nil {
			return Result{}, err
		}
		if err := validateMutationPayload(len(next)); err != nil {
			return Result{}, err
		}
		diff := patcher.BuildSimpleDiff(string(original), next)
		checkpointID, err := checkpointBefore(checkpoints, workspaces, item, "replace_regex", []string{file}, dryRun)
		if err != nil {
			return Result{}, err
		}
		if !dryRun {
			if err := rooted.WriteFile([]byte(next), 0644); err != nil {
				return Result{}, err
			}
		}
		return JSONResult(EditFileResult{Path: file, Diff: diff, DryRun: dryRun, CheckpointID: checkpointPointer(checkpointID)}), nil
	}
}

func handleApplyPatch(workspaces *workspace.Manager, checkpoints *checkpoint.Store) Handler {
	return func(_ context.Context, args map[string]any) (Result, error) {
		item, cwd, err := workspaceContext(workspaces, args)
		if err != nil {
			return Result{}, err
		}
		patchText, err := stringArg(args, "patch")
		if err != nil {
			return Result{}, err
		}
		if err := validateMutationPayload(len(patchText)); err != nil {
			return Result{}, err
		}
		dryRun, err := optionalBool(args, "dry_run", false)
		if err != nil {
			return Result{}, err
		}
		pathValue, err := optionalString(args, "path")
		if err != nil {
			return Result{}, err
		}

		if patcher.IsMultiFilePatch(patchText) {
			baseDir := cwd
			if strings.TrimSpace(pathValue) != "" {
				resolved, err := workspaces.ResolvePath(item.ID, cwd, pathValue, true)
				if err != nil {
					return Result{}, err
				}
				rooted, err := openRootedPath(workspaces, item.ID, resolved)
				if err != nil {
					return Result{}, err
				}
				info, err := rooted.Stat()
				_ = rooted.Close()
				if err != nil {
					return Result{}, err
				}
				if info.IsDir() {
					baseDir = resolved
				} else {
					baseDir = filepath.Dir(resolved)
				}
			}
			ops, err := patcher.ParseMultiFilePatch(patchText, baseDir)
			if err != nil {
				return Result{}, err
			}
			resolvedOps := make([]patcher.MultiFileOp, len(ops))
			paths := make([]string, 0, len(ops))
			for index, op := range ops {
				mustExist := op.Operation != "create"
				resolved, err := workspaces.ResolvePath(item.ID, cwd, op.Path, mustExist)
				if err != nil {
					return Result{}, fmt.Errorf("patch path %q: %w", op.Path, err)
				}
				op.Path = resolved
				resolvedOps[index] = op
				paths = append(paths, resolved)
			}
			checkpointID, err := checkpointBefore(checkpoints, workspaces, item, "apply_patch", paths, dryRun)
			if err != nil {
				return Result{}, err
			}
			results := make([]patcher.MultiPatchResult, 0, len(resolvedOps))
			for _, op := range resolvedOps {
				result := func() patcher.MultiPatchResult {
					result := patcher.MultiPatchResult{Path: op.Path, Operation: op.Operation}
					rooted, err := openRootedPath(workspaces, item.ID, op.Path)
					if err != nil {
						result.Error = err.Error()
						return result
					}
					defer rooted.Close()
					switch op.Operation {
					case "delete":
						if !dryRun {
							if err := rooted.Remove(); err != nil {
								result.Error = err.Error()
								return result
							}
						}
						result.OK = true
						result.Diff = "[deleted]"
					case "create":
						if err := validateMutationPayload(len(op.Content)); err != nil {
							result.Error = err.Error()
							return result
						}
						if !dryRun {
							if err := rooted.WriteFile([]byte(op.Content), 0644); err != nil {
								result.Error = err.Error()
								return result
							}
						}
						result.OK = true
						result.Diff = patcher.BuildSimpleDiff("", op.Content)
					case "update":
						original, err := readRootedRegularFileLimited(rooted, maxMutationFileBytes, "patch update")
						if err != nil {
							result.Error = err.Error()
							return result
						}
						next, err := patcher.ApplyUnifiedPatchToText(string(original), op.Patch)
						if err != nil {
							result.Error = err.Error()
							return result
						}
						if err := validateMutationPayload(len(next)); err != nil {
							result.Error = err.Error()
							return result
						}
						result.Diff = patcher.BuildSimpleDiff(string(original), next)
						if !dryRun {
							if err := rooted.WriteFile([]byte(next), 0644); err != nil {
								result.Error = err.Error()
								return result
							}
						}
						result.OK = true
					default:
						result.Error = "unknown patch operation"
					}
					return result
				}()
				results = append(results, result)
			}
			files := make([]map[string]any, len(results))
			for i, result := range results {
				files[i] = map[string]any{"path": result.Path, "operation": result.Operation, "ok": result.OK}
				if result.Diff != "" {
					files[i]["diff"] = result.Diff
				}
				if result.Error != "" {
					files[i]["error"] = result.Error
				}
			}
			value := ApplyPatchResult{Files: files, DryRun: dryRun, MultiFile: true, CheckpointID: checkpointPointer(checkpointID)}
			result := JSONResult(value)
			for _, file := range results {
				if !file.OK {
					result.IsError = true
					break
				}
			}
			return result, nil
		}

		if strings.TrimSpace(pathValue) == "" {
			return Result{}, errors.New("path is required for single-file patches")
		}
		file, err := workspaces.ResolvePath(item.ID, cwd, pathValue, true)
		if err != nil {
			return Result{}, err
		}
		rooted, err := openRootedPath(workspaces, item.ID, file)
		if err != nil {
			return Result{}, err
		}
		defer rooted.Close()
		original, err := readRootedRegularFileLimited(rooted, maxMutationFileBytes, "patch update")
		if err != nil {
			return Result{}, err
		}
		next, err := patcher.ApplyUnifiedPatchToText(string(original), patchText)
		if err != nil {
			return Result{}, err
		}
		if err := validateMutationPayload(len(next)); err != nil {
			return Result{}, err
		}
		diff := patcher.BuildSimpleDiff(string(original), next)
		checkpointID, err := checkpointBefore(checkpoints, workspaces, item, "apply_patch", []string{file}, dryRun)
		if err != nil {
			return Result{}, err
		}
		if !dryRun {
			if err := rooted.WriteFile([]byte(next), 0644); err != nil {
				return Result{}, err
			}
		}
		return JSONResult(ApplyPatchResult{Path: file, Diff: diff, DryRun: dryRun, CheckpointID: checkpointPointer(checkpointID)}), nil
	}
}

func handleListDirectory(workspaces *workspace.Manager) Handler {
	return func(_ context.Context, args map[string]any) (Result, error) {
		item, _, dir, err := workspacePath(workspaces, args, "path", true)
		if err != nil {
			return Result{}, err
		}
		rooted, err := openRootedDirectory(workspaces, item.ID, dir)
		if err != nil {
			return Result{}, err
		}
		defer rooted.Close()
		entries, err := rooted.ReadDir()
		if err != nil {
			return Result{}, err
		}
		ignore, err := optionalStrings(args, "ignore")
		if err != nil {
			return Result{}, err
		}
		matchers := make([]*regexp.Regexp, 0, len(ignore))
		for _, pattern := range ignore {
			matcher, err := ignoreMatcher(pattern)
			if err != nil {
				return Result{}, err
			}
			matchers = append(matchers, matcher)
		}
		items := make([]DirectoryItem, 0, len(entries))
		for _, entry := range entries {
			ignored := false
			for _, matcher := range matchers {
				if matcher.MatchString(entry.Name()) {
					ignored = true
					break
				}
			}
			if !ignored {
				items = append(items, DirectoryItem{Name: entry.Name(), Type: pathType(entry)})
			}
		}
		return JSONResult(ListDirectoryResult{Path: dir, Entries: items, Count: len(items)}), nil
	}
}

func handleGlob(workspaces *workspace.Manager) Handler {
	return func(_ context.Context, args map[string]any) (Result, error) {
		item, cwd, err := workspaceContext(workspaces, args)
		if err != nil {
			return Result{}, err
		}
		pattern, err := stringArg(args, "pattern")
		if err != nil {
			return Result{}, err
		}
		pathValue, err := optionalString(args, "path")
		if err != nil {
			return Result{}, err
		}
		searchRoot := cwd
		if strings.TrimSpace(pathValue) != "" {
			searchRoot, err = workspaces.ResolvePath(item.ID, cwd, pathValue, true)
			if err != nil {
				return Result{}, err
			}
		}
		maxResults, err := optionalInt(args, "max_results", 100, 1, 500)
		if err != nil {
			return Result{}, err
		}
		rooted, err := openRootedDirectory(workspaces, item.ID, searchRoot)
		if err != nil {
			return Result{}, err
		}
		defer rooted.Close()
		matches, err := globFiles(rooted, pattern, maxResults)
		if err != nil {
			return Result{}, err
		}
		paths := make([]string, len(matches))
		for i, match := range matches {
			paths[i] = match.Path
		}
		return JSONResult(GlobResult{Path: searchRoot, Pattern: pattern, Matches: paths, Count: len(paths)}), nil
	}
}

func handleGrep(workspaces *workspace.Manager) Handler {
	return func(_ context.Context, args map[string]any) (Result, error) {
		item, cwd, err := workspaceContext(workspaces, args)
		if err != nil {
			return Result{}, err
		}
		pattern, err := stringArg(args, "pattern")
		if err != nil {
			return Result{}, err
		}
		pathValue, err := optionalString(args, "path")
		if err != nil {
			return Result{}, err
		}
		searchRoot := cwd
		if strings.TrimSpace(pathValue) != "" {
			searchRoot, err = workspaces.ResolvePath(item.ID, cwd, pathValue, true)
			if err != nil {
				return Result{}, err
			}
		}
		resultPath := searchRoot
		exactFile := ""
		rootedPath, err := openRootedPath(workspaces, item.ID, searchRoot)
		if err != nil {
			return Result{}, err
		}
		info, err := rootedPath.Stat()
		_ = rootedPath.Close()
		if err != nil {
			return Result{}, err
		}
		if !info.IsDir() {
			if !info.Mode().IsRegular() {
				return Result{}, errors.New("path is not a regular file or directory")
			}
			exactFile = filepath.Base(searchRoot)
			searchRoot = filepath.Dir(searchRoot)
		}
		glob, err := optionalStringDefault(args, "glob", "*")
		if err != nil {
			return Result{}, err
		}
		outputMode, err := optionalStringDefault(args, "output_mode", "content")
		if err != nil {
			return Result{}, err
		}
		if outputMode != "content" && outputMode != "files_with_matches" && outputMode != "count" {
			return Result{}, errors.New("output_mode must be content, files_with_matches, or count")
		}
		caseInsensitive, err := optionalBool(args, "case_insensitive", false)
		if err != nil {
			return Result{}, err
		}
		multiline, err := optionalBool(args, "multiline", false)
		if err != nil {
			return Result{}, err
		}
		headLimit, err := optionalInt(args, "head_limit", 200, 1, 1000)
		if err != nil {
			return Result{}, err
		}
		contextBefore, err := optionalInt(args, "context_before", 0, 0, 20)
		if err != nil {
			return Result{}, err
		}
		contextAfter, err := optionalInt(args, "context_after", 0, 0, 20)
		if err != nil {
			return Result{}, err
		}
		contextAround, err := optionalInt(args, "context_around", 0, 0, 20)
		if err != nil {
			return Result{}, err
		}
		rooted, err := openRootedDirectory(workspaces, item.ID, searchRoot)
		if err != nil {
			return Result{}, err
		}
		defer rooted.Close()
		output, err := grepSearch(rooted, GrepOptions{
			Pattern: pattern, Path: searchRoot, Glob: glob, OutputMode: outputMode, CaseInsensitive: caseInsensitive,
			Multiline: multiline, HeadLimit: headLimit, ContextBefore: contextBefore, ContextAfter: contextAfter, ContextAround: contextAround, ExactFile: exactFile,
		})
		if err != nil {
			return Result{}, err
		}
		return JSONResult(GrepResult{Path: resultPath, Pattern: pattern, OutputMode: outputMode, Output: output}), nil
	}
}

func handleDeleteFile(workspaces *workspace.Manager, checkpoints *checkpoint.Store) Handler {
	return func(_ context.Context, args map[string]any) (Result, error) {
		item, _, file, err := workspacePath(workspaces, args, "path", true)
		if err != nil {
			return Result{}, err
		}
		rooted, err := openRootedPath(workspaces, item.ID, file)
		if err != nil {
			return Result{}, err
		}
		defer rooted.Close()
		info, err := rooted.Stat()
		if err != nil {
			return Result{}, err
		}
		if !info.Mode().IsRegular() {
			return Result{}, errors.New("path is not a file")
		}
		checkpointID, err := checkpointBefore(checkpoints, workspaces, item, "delete_file", []string{file}, false)
		if err != nil {
			return Result{}, err
		}
		if err := rooted.Remove(); err != nil {
			return Result{}, err
		}
		return JSONResult(DeleteResult{Path: file, CheckpointID: checkpointPointer(checkpointID)}), nil
	}
}

func handleCreateDirectory(workspaces *workspace.Manager) Handler {
	return func(_ context.Context, args map[string]any) (Result, error) {
		item, _, dir, err := workspacePath(workspaces, args, "path", false)
		if err != nil {
			return Result{}, err
		}
		rooted, err := openRootedPath(workspaces, item.ID, dir)
		if err != nil {
			return Result{}, err
		}
		defer rooted.Close()
		if err := rooted.MkdirAll(0755); err != nil {
			return Result{}, err
		}
		return JSONResult(CreateDirectoryResult{Path: dir}), nil
	}
}

func handleDeleteDirectory(workspaces *workspace.Manager, checkpoints *checkpoint.Store) Handler {
	return func(_ context.Context, args map[string]any) (Result, error) {
		item, _, dir, err := workspacePath(workspaces, args, "path", true)
		if err != nil {
			return Result{}, err
		}
		rooted, err := openRootedPath(workspaces, item.ID, dir)
		if err != nil {
			return Result{}, err
		}
		defer rooted.Close()
		info, err := rooted.Stat()
		if err != nil {
			return Result{}, err
		}
		if !info.IsDir() {
			return Result{}, errors.New("path is not a directory")
		}
		if err := rejectEffectiveRootMutation(workspaces, item.ID, dir, "delete"); err != nil {
			return Result{}, err
		}
		checkpointID, err := checkpointBefore(checkpoints, workspaces, item, "delete_directory", []string{dir}, false)
		if err != nil {
			return Result{}, err
		}
		if err := rooted.RemoveAll(); err != nil {
			return Result{}, err
		}
		return JSONResult(map[string]any{"path": dir, "checkpoint_id": checkpointPointer(checkpointID), "run_command_fallback": deleteDirectoryFallback(dir)}), nil
	}
}

func handleCopyFile(workspaces *workspace.Manager, checkpoints *checkpoint.Store) Handler {
	return func(_ context.Context, args map[string]any) (Result, error) {
		item, cwd, err := workspaceContext(workspaces, args)
		if err != nil {
			return Result{}, err
		}
		sourceValue, err := stringArg(args, "source")
		if err != nil {
			return Result{}, err
		}
		destinationValue, err := stringArg(args, "destination")
		if err != nil {
			return Result{}, err
		}
		source, err := workspaces.ResolvePath(item.ID, cwd, sourceValue, true)
		if err != nil {
			return Result{}, err
		}
		destination, err := workspaces.ResolvePath(item.ID, cwd, destinationValue, false)
		if err != nil {
			return Result{}, err
		}
		sourceRooted, err := openRootedPath(workspaces, item.ID, source)
		if err != nil {
			return Result{}, err
		}
		defer sourceRooted.Close()
		destinationRooted, err := openRootedPath(workspaces, item.ID, destination)
		if err != nil {
			return Result{}, err
		}
		defer destinationRooted.Close()
		info, err := sourceRooted.Stat()
		if err != nil {
			return Result{}, err
		}
		if !info.Mode().IsRegular() {
			return Result{}, errors.New("source is not a file")
		}
		checkpointID, err := checkpointBefore(checkpoints, workspaces, item, "copy_file", []string{destination}, false)
		if err != nil {
			return Result{}, err
		}
		if err := copyRootedFileContents(sourceRooted, destinationRooted); err != nil {
			return Result{}, err
		}
		return JSONResult(CopyMoveResult{Source: source, Destination: destination, CheckpointID: checkpointPointer(checkpointID)}), nil
	}
}

func handleMoveFile(workspaces *workspace.Manager, checkpoints *checkpoint.Store) Handler {
	return func(_ context.Context, args map[string]any) (Result, error) {
		item, cwd, err := workspaceContext(workspaces, args)
		if err != nil {
			return Result{}, err
		}
		sourceValue, err := stringArg(args, "source")
		if err != nil {
			return Result{}, err
		}
		destinationValue, err := stringArg(args, "destination")
		if err != nil {
			return Result{}, err
		}
		source, err := workspaces.ResolvePath(item.ID, cwd, sourceValue, true)
		if err != nil {
			return Result{}, err
		}
		destination, err := workspaces.ResolvePath(item.ID, cwd, destinationValue, false)
		if err != nil {
			return Result{}, err
		}
		if err := rejectEffectiveRootMutation(workspaces, item.ID, source, "move"); err != nil {
			return Result{}, err
		}
		checkpointID, err := checkpointBefore(checkpoints, workspaces, item, "move_file", []string{source, destination}, false)
		if err != nil {
			return Result{}, err
		}
		sourceRooted, err := openRootedPath(workspaces, item.ID, source)
		if err != nil {
			return Result{}, err
		}
		defer sourceRooted.Close()
		destinationRooted, err := openRootedPath(workspaces, item.ID, destination)
		if err != nil {
			return Result{}, err
		}
		defer destinationRooted.Close()
		parent := filepath.Dir(destinationRooted.relative)
		if sourceRooted.root.Name() == destinationRooted.root.Name() {
			if parent != "." {
				if err := sourceRooted.root.MkdirAll(parent, 0755); err != nil {
					return Result{}, err
				}
			}
			if err := sourceRooted.root.Rename(sourceRooted.relative, destinationRooted.relative); err != nil {
				return Result{}, err
			}
			return JSONResult(CopyMoveResult{Source: source, Destination: destination, CheckpointID: checkpointPointer(checkpointID)}), nil
		}
		info, err := sourceRooted.Stat()
		if err != nil {
			return Result{}, err
		}
		if !info.Mode().IsRegular() {
			return Result{}, errors.New("moving directories across workspace access roots is not supported safely; copy contents explicitly, then delete the source")
		}
		if err := copyRootedFileContents(sourceRooted, destinationRooted); err != nil {
			return Result{}, err
		}
		if err := sourceRooted.Remove(); err != nil {
			_ = destinationRooted.Remove()
			return Result{}, err
		}
		return JSONResult(CopyMoveResult{Source: source, Destination: destination, CheckpointID: checkpointPointer(checkpointID)}), nil
	}
}

func handleDirectoryTree(workspaces *workspace.Manager) Handler {
	return func(_ context.Context, args map[string]any) (Result, error) {
		item, _, root, err := workspacePath(workspaces, args, "path", true)
		if err != nil {
			return Result{}, err
		}
		maxDepth, err := optionalInt(args, "max_depth", 4, 0, 128)
		if err != nil {
			return Result{}, err
		}
		rooted, err := openRootedDirectory(workspaces, item.ID, root)
		if err != nil {
			return Result{}, err
		}
		defer rooted.Close()
		tree, err := buildTree(rooted, 0, maxDepth)
		if err != nil {
			return Result{}, err
		}
		return JSONResult(DirectoryTreeResult{Path: root, Tree: tree, MaxDepth: maxDepth}), nil
	}
}

func handleListAllowedDirectories(workspaces *workspace.Manager) Handler {
	return func(_ context.Context, args map[string]any) (Result, error) {
		item, cwd, err := workspaceContext(workspaces, args)
		if err != nil {
			return Result{}, err
		}
		roots, err := workspaces.EffectiveRoots(item.ID)
		if err != nil {
			return Result{}, err
		}
		return JSONResult(AllowedDirectoriesResult{
			FullMachineAccess: false,
			Permission:        "workspace-bound with explicit allowed directories",
			DefaultCWD:        cwd,
			MachineRoots:      roots,
			WorkspaceID:       item.ID,
			WorkspaceRoot:     item.Path,
		}), nil
	}
}

func mutationOutputSchema(extra string) string {
	if extra != "" {
		extra += ","
	}
	return `{"type":"object","properties":{"path":{"type":"string"},` + extra + `"checkpoint_id":{"type":["string","null"]}},"required":["path","checkpoint_id"],"additionalProperties":false}`
}

func editInputSchema(regex bool) string {
	_ = regex
	return `{"type":"object","properties":{"workspace_id":{"type":"string"},"path":{"type":"string"},"old_text":{"type":"string"},"new_text":{"type":"string"},"replace_all":{"type":"boolean","default":false},"dry_run":{"type":"boolean","default":false}},"required":["workspace_id","path","old_text","new_text"],"additionalProperties":false}`
}

func editOutputSchema(_ bool) string {
	return `{"type":"object","properties":{"path":{"type":"string"},"diff":{"type":"string"},"dry_run":{"type":"boolean"},"checkpoint_id":{"type":["string","null"]}},"required":["path","diff","dry_run","checkpoint_id"],"additionalProperties":false}`
}

func basePathInputSchema() string {
	return `{"type":"object","properties":{"workspace_id":{"type":"string"},"path":{"type":"string"}},"required":["workspace_id","path"],"additionalProperties":false}`
}

func sourceDestinationInputSchema() string {
	return `{"type":"object","properties":{"workspace_id":{"type":"string"},"source":{"type":"string"},"destination":{"type":"string"}},"required":["workspace_id","source","destination"],"additionalProperties":false}`
}

func grepInputSchema() string {
	return `{"type":"object","properties":{"workspace_id":{"type":"string"},"pattern":{"type":"string"},"path":{"type":"string"},"glob":{"type":"string","default":"*"},"output_mode":{"type":"string","enum":["content","files_with_matches","count"],"default":"content"},"case_insensitive":{"type":"boolean","default":false},"multiline":{"type":"boolean","default":false},"head_limit":{"type":"integer","minimum":1,"maximum":1000,"default":200},"context_before":{"type":"integer","minimum":0,"maximum":20,"default":0},"context_after":{"type":"integer","minimum":0,"maximum":20,"default":0},"context_around":{"type":"integer","minimum":0,"maximum":20,"default":0}},"required":["workspace_id","pattern"],"additionalProperties":false}`
}

func stringArg(args map[string]any, key string) (string, error) {
	value, err := stringArgAllowEmpty(args, key)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}

func stringArgAllowEmpty(args map[string]any, key string) (string, error) {
	value, ok := args[key].(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return value, nil
}

func optionalBool(args map[string]any, key string, fallback bool) (bool, error) {
	value, exists := args[key]
	if !exists {
		return fallback, nil
	}
	result, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return result, nil
}

func optionalInt(args map[string]any, key string, fallback, min, max int) (int, error) {
	value, exists := args[key]
	if !exists {
		return fallback, nil
	}
	number, err := intValue(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	if number < min || number > max {
		return 0, fmt.Errorf("%s must be between %d and %d", key, min, max)
	}
	return number, nil
}

func optionalIntPointer(args map[string]any, key string, min, max int) (*int, error) {
	value, exists := args[key]
	if !exists {
		return nil, nil
	}
	number, err := intValue(value)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	if number < min || number > max {
		return nil, fmt.Errorf("%s must be between %d and %d", key, min, max)
	}
	return &number, nil
}

func optionalInt64(args map[string]any, key string, fallback, min, max int64) (int64, error) {
	value, exists := args[key]
	if !exists {
		return fallback, nil
	}
	var number int64
	switch typed := value.(type) {
	case int:
		number = int64(typed)
	case int64:
		number = typed
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer", key)
		}
		number = parsed
	case float64:
		if typed != float64(int64(typed)) {
			return 0, fmt.Errorf("%s must be an integer", key)
		}
		number = int64(typed)
	default:
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	if number < min || number > max {
		return 0, fmt.Errorf("%s must be between %d and %d", key, min, max)
	}
	return number, nil
}

func intValue(value any) (int, error) {
	switch typed := value.(type) {
	case int:
		return typed, nil
	case int64:
		if int64(int(typed)) != typed {
			return 0, errors.New("must be an integer")
		}
		return int(typed), nil
	case json.Number:
		number, err := typed.Int64()
		if err != nil || int64(int(number)) != number {
			return 0, errors.New("must be an integer")
		}
		return int(number), nil
	case float64:
		if typed != float64(int(typed)) {
			return 0, errors.New("must be an integer")
		}
		return int(typed), nil
	default:
		return 0, errors.New("must be an integer")
	}
}

func optionalStringDefault(args map[string]any, key, fallback string) (string, error) {
	value, exists := args[key]
	if !exists {
		return fallback, nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return text, nil
}

func optionalStrings(args map[string]any, key string) ([]string, error) {
	value, exists := args[key]
	if !exists {
		return nil, nil
	}
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...), nil
	case []any:
		result := make([]string, len(typed))
		for i, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s[%d] must be a string", key, i)
			}
			result[i] = text
		}
		return result, nil
	default:
		return nil, fmt.Errorf("%s must be an array of strings", key)
	}
}

func checkpointBefore(checkpoints *checkpoint.Store, workspaces *workspace.Manager, item workspace.Workspace, tool string, paths []string, dryRun bool) (string, error) {
	roots, err := workspaces.EffectiveRoots(item.ID)
	if err != nil {
		return "", err
	}
	return checkpoints.BeforeAllowed(item.ID, item.Path, roots, tool, paths, dryRun)
}

func rejectEffectiveRootMutation(workspaces *workspace.Manager, workspaceID, path, action string) error {
	roots, err := workspaces.EffectiveRoots(workspaceID)
	if err != nil {
		return err
	}
	path = filepath.Clean(path)
	for _, root := range roots {
		if filepath.Clean(root) == path {
			return fmt.Errorf("cannot %s workspace or allowed root: %s", action, path)
		}
	}
	return nil
}

func editSpecs(value any) ([]EditSpec, error) {
	values, ok := value.([]any)
	if !ok {
		if typed, ok := value.([]map[string]any); ok {
			values = make([]any, len(typed))
			for i := range typed {
				values[i] = typed[i]
			}
		} else {
			return nil, errors.New("edits must be an array")
		}
	}
	result := make([]EditSpec, len(values))
	for i, value := range values {
		item, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("edits[%d] must be an object", i)
		}
		oldText, err := stringArg(item, "old_text")
		if err != nil {
			return nil, fmt.Errorf("edits[%d].old_text: %w", i, err)
		}
		newText, err := stringArgAllowEmpty(item, "new_text")
		if err != nil {
			return nil, fmt.Errorf("edits[%d].new_text: %w", i, err)
		}
		replaceAll, err := optionalBool(item, "replace_all", false)
		if err != nil {
			return nil, fmt.Errorf("edits[%d].replace_all: %w", i, err)
		}
		result[i] = EditSpec{OldText: oldText, NewText: newText, ReplaceAll: replaceAll}
	}
	return result, nil
}
