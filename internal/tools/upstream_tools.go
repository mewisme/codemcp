package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go.mewis.me/codemcp/internal/upstream"
)

type UpstreamServersResult struct {
	Servers []upstream.Status `json:"servers"`
	Count   int               `json:"count"`
}

type UpstreamToolInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	ProxiedAs   []string `json:"proxied_as"`
}

type UpstreamToolsResult struct {
	ServerID     string             `json:"server_id"`
	Tools        []UpstreamToolInfo `json:"tools"`
	ProxiedTools []string           `json:"proxied_tools"`
	Count        int                `json:"count"`
}

type UpstreamCallResult struct {
	ServerID string             `json:"server_id"`
	Tool     string             `json:"tool"`
	Output   any                `json:"output,omitempty"`
	Content  []upstream.Content `json:"content,omitempty"`
	Error    string             `json:"error,omitempty"`
}

func RegisterUpstreamTools(registry *Registry, manager *upstream.Manager) {
	manager.SetToolsChangedHandler(func(ctx context.Context, serverID string) error {
		server, ok := manager.Get(serverID)
		if !ok || !server.Enabled {
			return nil
		}
		values, err := manager.Tools(ctx, serverID, false)
		if err != nil {
			return err
		}
		return refreshServerProxy(registry, manager, server, values)
	})
	registerAnnotated := func(name, title, description, input, output string, annotations map[string]any, handler Handler) {
		registry.MustRegister(name, Schema{
			Name: name, Title: title, Description: description,
			InputSchema: json.RawMessage(input), OutputSchema: json.RawMessage(output), Annotations: annotations,
		}, handler)
	}
	register := func(name, title, description, input, output string, risk Risk, handler Handler) {
		registerAnnotated(name, title, description, input, output, ToolAnnotations(risk), handler)
	}

	registerAnnotated("upstream_servers", "Upstream Servers", "List configured Upstream servers with health status.", `{"type":"object","properties":{"refresh":{"type":"boolean","default":false}},"additionalProperties":false}`, `{"type":"object","properties":{"servers":{"type":"array","items":{"type":"object","additionalProperties":true}},"count":{"type":"integer"}},"required":["servers","count"],"additionalProperties":false}`, ToolAnnotationsOpenWorld(RiskRead), func(ctx context.Context, args map[string]any) (Result, error) {
		refresh, err := optionalBool(args, "refresh", false)
		if err != nil {
			return Result{}, err
		}
		if refresh {
			if err := manager.Reload(ctx); err != nil {
				return Result{}, err
			}
		}
		statuses := manager.ListStatuses(ctx, refresh)
		if err := RefreshUpstreamProxies(ctx, registry, manager, false); err != nil {
			// Health listing remains useful even if one proxy cannot be registered.
		}
		return JSONResult(UpstreamServersResult{Servers: statuses, Count: len(statuses)}), nil
	})

	registerAnnotated("upstream_tools", "Upstream Tools", "List tools exposed by one configured Upstream server and their proxied names.", `{"type":"object","properties":{"server_id":{"type":"string"}},"required":["server_id"],"additionalProperties":false}`, `{"type":"object","properties":{"server_id":{"type":"string"},"tools":{"type":"array","items":{"type":"object","additionalProperties":true}},"proxied_tools":{"type":"array","items":{"type":"string"}},"count":{"type":"integer"}},"required":["server_id","tools","proxied_tools","count"],"additionalProperties":false}`, ToolAnnotationsOpenWorld(RiskRead), func(ctx context.Context, args map[string]any) (Result, error) {
		serverID, err := requiredString(args, "server_id")
		if err != nil {
			return Result{}, err
		}
		server, ok := manager.Get(serverID)
		if !ok {
			return Result{}, fmt.Errorf("unknown server_id: %s", serverID)
		}
		values, err := manager.Tools(ctx, serverID, false)
		if err != nil {
			return Result{}, err
		}
		if err := refreshServerProxy(registry, manager, server, values); err != nil {
			return Result{}, err
		}
		proxied := manager.ProxiedToolNames(server, values)
		info := make([]UpstreamToolInfo, 0, len(values))
		for _, tool := range values {
			names := []string{}
			proxy := upstream.ProxyName(server.ToolPrefix, tool.Name)
			for _, exposed := range proxied {
				if exposed == proxy {
					names = append(names, exposed)
				}
			}
			info = append(info, UpstreamToolInfo{Name: tool.Name, Description: tool.Description, ProxiedAs: names})
		}
		return JSONResult(UpstreamToolsResult{ServerID: serverID, Tools: info, ProxiedTools: proxied, Count: len(values)}), nil
	})

	register("upstream_call", "Upstream Call", "Invoke a tool on a configured Upstream server. Upstream tool semantics are external and are not workspace-enforced by CodeMCP.", `{"type":"object","properties":{"server_id":{"type":"string"},"tool":{"type":"string"},"arguments":{"type":"object","additionalProperties":true,"default":{}}},"required":["server_id","tool"],"additionalProperties":false}`, `{"type":"object","additionalProperties":true}`, RiskCommand, func(ctx context.Context, args map[string]any) (Result, error) {
		serverID, err := requiredString(args, "server_id")
		if err != nil {
			return Result{}, err
		}
		tool, err := requiredString(args, "tool")
		if err != nil {
			return Result{}, err
		}
		callArgs, err := optionalObject(args, "arguments")
		if err != nil {
			return Result{}, err
		}
		value, err := callUpstream(ctx, manager, serverID, tool, callArgs)
		if err != nil {
			return Result{}, err
		}
		if value.ResultType == "input_required" {
			return forwardUpstreamResult(value), nil
		}
		result := normalizeUpstreamCall(serverID, tool, value)
		if value.IsError {
			result.IsError = true
		}
		return result, nil
	})
}

func RefreshUpstreamProxies(ctx context.Context, registry *Registry, manager *upstream.Manager, force bool) error {
	var failures []string
	replacements := map[string]map[string]Entry{}
	for _, server := range manager.List() {
		if !server.Enabled || server.Expose == "none" || server.Expose == "meta_only" {
			continue
		}
		tools, err := manager.Tools(ctx, server.ID, force)
		if err != nil {
			failures = append(failures, server.ID+": "+err.Error())
			continue
		}
		entries, err := buildServerProxyEntries(manager, server, tools)
		if err != nil {
			failures = append(failures, server.ID+": "+err.Error())
			continue
		}
		replacements["upstream:"+server.ID] = entries
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return registry.ReplaceOwnedPrefix("upstream:", replacements)
}

func refreshServerProxy(registry *Registry, manager *upstream.Manager, server upstream.Server, values []upstream.Tool) error {
	entries, err := buildServerProxyEntries(manager, server, values)
	if err != nil {
		return err
	}
	return registry.ReplaceOwned("upstream:"+server.ID, entries)
}

func buildServerProxyEntries(manager *upstream.Manager, server upstream.Server, values []upstream.Tool) (map[string]Entry, error) {
	allowed := map[string]bool{}
	for _, name := range manager.ProxiedToolNames(server, values) {
		allowed[name] = true
	}
	entries := map[string]Entry{}
	for _, tool := range values {
		proxyName := upstream.ProxyName(server.ToolPrefix, tool.Name)
		if !allowed[proxyName] {
			continue
		}
		inputSchema := tool.InputSchema
		if inputSchema == nil {
			inputSchema = map[string]any{"type": "object", "additionalProperties": true}
		}
		inputRaw, err := json.Marshal(inputSchema)
		if err != nil {
			return nil, fmt.Errorf("encode input schema for %s: %w", proxyName, err)
		}
		var outputRaw json.RawMessage
		if tool.OutputSchema != nil {
			outputRaw, err = json.Marshal(tool.OutputSchema)
			if err != nil {
				return nil, fmt.Errorf("encode output schema for %s: %w", proxyName, err)
			}
		}
		description := strings.TrimSpace(tool.Description)
		if description == "" {
			description = "Upstream tool " + server.ID + ":" + tool.Name
		}
		description += " [External upstream: native workspace path policy cannot be generically enforced.]"
		schema := Schema{
			Name: proxyName, Title: tool.Title, Description: description,
			InputSchema: inputRaw, OutputSchema: outputRaw,
			Annotations: tool.Annotations,
		}
		serverID := server.ID
		toolName := tool.Name
		entries[proxyName] = Entry{Schema: schema, Handler: func(ctx context.Context, args map[string]any) (Result, error) {
			value, err := callUpstream(ctx, manager, serverID, toolName, args)
			if err != nil {
				return Result{}, err
			}
			return forwardUpstreamResult(value), nil
		}}
	}
	return entries, nil
}

func normalizeUpstreamCall(serverID, tool string, value upstream.CallResult) Result {
	payload := UpstreamCallResult{ServerID: serverID, Tool: tool, Content: value.Content}
	if value.IsError {
		payload.Error = upstreamText(value)
	} else if value.StructuredContent != nil {
		payload.Output = value.StructuredContent
	} else {
		payload.Output = upstreamText(value)
	}
	return JSONResult(payload)
}

func callUpstream(ctx context.Context, manager *upstream.Manager, serverID, tool string, args map[string]any) (upstream.CallResult, error) {
	round := InputRoundFromContext(ctx)
	return manager.CallWithInput(ctx, serverID, tool, args, round.RequestState, round.InputResponses)
}

func forwardUpstreamResult(value upstream.CallResult) Result {
	if value.ResultType == "input_required" {
		return Result{
			ResultType: value.ResultType, RequestState: value.RequestState, InputRequests: value.InputRequests,
		}
	}
	content := make([]Content, 0, len(value.Content))
	for _, item := range value.Content {
		text := item.Text
		if text == "" {
			data, _ := json.Marshal(item.Raw)
			text = string(data)
		}
		content = append(content, Content{Type: item.Type, Text: text, Raw: item.Raw})
	}
	if len(content) == 0 {
		content = []Content{{Type: "text", Text: upstreamText(value)}}
	}
	return Result{
		Content: content, StructuredContent: value.StructuredContent, IsError: value.IsError, Meta: value.Meta, ResultType: value.ResultType,
	}
}

func upstreamText(value upstream.CallResult) string {
	parts := make([]string, 0, len(value.Content))
	for _, item := range value.Content {
		if item.Text != "" {
			parts = append(parts, item.Text)
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n")
	}
	if value.StructuredContent != nil {
		data, _ := json.Marshal(value.StructuredContent)
		return string(data)
	}
	if value.ResultType == "input_required" {
		data, _ := json.Marshal(map[string]any{
			"resultType": value.ResultType, "requestState": value.RequestState, "inputRequests": value.InputRequests,
		})
		return string(data)
	}
	return ""
}

func optionalObject(args map[string]any, key string) (map[string]any, error) {
	value, ok := args[key]
	if !ok || value == nil {
		return map[string]any{}, nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", key)
	}
	return object, nil
}
