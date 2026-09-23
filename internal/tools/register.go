package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/checkpoint"
	shellruntime "go.mewis.me/codemcp/internal/shell"
	"go.mewis.me/codemcp/internal/version"
	"go.mewis.me/codemcp/internal/workspace"
)

type ReadFilesResult struct {
	Files []ReadFile `json:"files"`
	Count int        `json:"count"`
}

type VersionResult struct {
	Version              string `json:"version"`
	Commit               string `json:"commit"`
	BuildTime            string `json:"build_time"`
	ServerStartedAt      string `json:"server_started_at"`
	ServerUptime         string `json:"server_uptime"`
	ServerUptimeSeconds  int64  `json:"server_uptime_seconds"`
	MachineUptime        string `json:"machine_uptime"`
	MachineUptimeSeconds int64  `json:"machine_uptime_seconds"`
}

var processStartedAt = time.Now().UTC()
var machineUptime = readMachineUptime

const (
	maxReadFiles            = 32
	maxReadFilesBytes int64 = maxTextReadBytes
)

func RegisterCore(registry *Registry, workspaces *workspace.Manager, checkpoints *checkpoint.Store, shells ...*shellruntime.Manager) {
	registerCore(registry, workspaces, checkpoints, nil, shells...)
}

func registerCore(registry *Registry, workspaces *workspace.Manager, checkpoints *checkpoint.Store, environment ProjectContextEnvironment, shells ...*shellruntime.Manager) {
	shell := shellruntime.NewManager(workspaces, shellruntime.DefaultStateRoot())
	if len(shells) > 0 && shells[0] != nil {
		shell = shells[0]
	}
	registerCoreWithManagers(registry, workspaces, checkpoints, environment, shell, shellruntime.NewProcessManager(workspaces, shell))
}

func registerCoreWithManagers(registry *Registry, workspaces *workspace.Manager, checkpoints *checkpoint.Store, environment ProjectContextEnvironment, shell *shellruntime.Manager, processes *shellruntime.ProcessManager) {
	registry.MustRegister("get_version", coreSchema("get_version", "Get the running CodeMCP server version, build metadata, server uptime, and machine uptime.", `{"type":"object","properties":{},"additionalProperties":false}`, `{"type":"object","properties":{"version":{"type":"string"},"commit":{"type":"string"},"build_time":{"type":"string"},"server_started_at":{"type":"string"},"server_uptime":{"type":"string"},"server_uptime_seconds":{"type":"integer","minimum":0},"machine_uptime":{"type":"string"},"machine_uptime_seconds":{"type":"integer","minimum":0}},"required":["version","commit","build_time","server_started_at","server_uptime","server_uptime_seconds","machine_uptime","machine_uptime_seconds"],"additionalProperties":false}`, RiskRead), func(context.Context, map[string]any) (Result, error) {
		now := time.Now().UTC()
		serverUptime := now.Sub(processStartedAt)
		if serverUptime < 0 {
			serverUptime = 0
		}
		machineUptime, err := machineUptime()
		if err != nil {
			return Result{}, fmt.Errorf("machine uptime: %w", err)
		}
		if machineUptime < 0 {
			machineUptime = 0
		}
		return JSONResult(VersionResult{Version: version.Version, Commit: version.Commit, BuildTime: version.Date, ServerStartedAt: processStartedAt.Format(time.RFC3339), ServerUptime: serverUptime.Truncate(time.Second).String(), ServerUptimeSeconds: int64(serverUptime / time.Second), MachineUptime: machineUptime.Truncate(time.Second).String(), MachineUptimeSeconds: int64(machineUptime / time.Second)}), nil
	})
	RegisterFilesystemTools(registry, workspaces, checkpoints)
	RegisterShellTools(registry, workspaces, shell, processes)
	RegisterGitTools(registry, workspaces)
	RegisterContextTools(registry, workspaces, checkpoints, environment)
	RegisterRewindTools(registry, workspaces, checkpoints)
	RegisterAdvancedTools(registry, workspaces)
	registry.MustRegister("read_files", coreSchema("read_files", "Read multiple text files with rooted workspace access and a bounded combined payload.", `{"type":"object","properties":{"workspace_id":{"type":"string"},"paths":{"type":"array","items":{"type":"string"},"minItems":1,"maxItems":32}},"required":["workspace_id","paths"],"additionalProperties":false}`, `{"type":"object","properties":{"files":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}},"count":{"type":"integer"}},"required":["files","count"],"additionalProperties":false}`, RiskRead), handleReadFiles(workspaces))
}

func coreSchema(name, description, input, output string, risk Risk) Schema {
	return Schema{Name: name, Description: description, InputSchema: json.RawMessage(input), OutputSchema: json.RawMessage(output), Annotations: ToolAnnotations(risk)}
}

func handleReadFiles(workspaces *workspace.Manager) Handler {
	return func(_ context.Context, args map[string]any) (Result, error) {
		item, cwd, err := workspaceContext(workspaces, args)
		if err != nil {
			return Result{}, err
		}
		paths, err := requiredStrings(args, "paths")
		if err != nil {
			return Result{}, err
		}
		if len(paths) > maxReadFiles {
			return Result{}, fmt.Errorf("paths must contain at most %d files", maxReadFiles)
		}
		files := make([]ReadFile, 0, len(paths))
		var totalBytes int64
		for _, value := range paths {
			file, err := workspaces.ResolvePath(item.ID, cwd, value, true)
			if err != nil {
				return Result{}, fmt.Errorf("path %q: %w", value, err)
			}
			rooted, err := openRootedPath(workspaces, item.ID, file)
			if err != nil {
				return Result{}, fmt.Errorf("path %q: %w", value, err)
			}
			data, readErr := readRootedRegularFileLimited(rooted, maxTextReadBytes, "multi-file text read")
			_ = rooted.Close()
			if readErr != nil {
				return Result{}, fmt.Errorf("path %q: %w", value, readErr)
			}
			totalBytes += int64(len(data))
			if totalBytes > maxReadFilesBytes {
				return Result{}, fmt.Errorf("combined multi-file text read exceeds %s limit", byteLimitLabel(maxReadFilesBytes))
			}
			files = append(files, ReadFile{Path: file, Content: string(data)})
		}
		return JSONResult(ReadFilesResult{Files: files, Count: len(files)}), nil
	}
}

func requiredString(args map[string]any, key string) (string, error) {
	value, ok := args[key].(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}

func requiredStrings(args map[string]any, key string) ([]string, error) {
	switch values := args[key].(type) {
	case []string:
		if len(values) == 0 {
			return nil, fmt.Errorf("%s must not be empty", key)
		}
		for i, value := range values {
			if strings.TrimSpace(value) == "" {
				return nil, fmt.Errorf("%s[%d] must not be empty", key, i)
			}
		}
		return values, nil
	case []any:
		if len(values) == 0 {
			return nil, fmt.Errorf("%s must not be empty", key)
		}
		result := make([]string, len(values))
		for i, value := range values {
			text, ok := value.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return nil, fmt.Errorf("%s[%d] must be a non-empty string", key, i)
			}
			result[i] = text
		}
		return result, nil
	default:
		return nil, fmt.Errorf("%s must be an array of strings", key)
	}
}
