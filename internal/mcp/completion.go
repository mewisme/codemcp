package mcp

import (
	"context"
	"errors"
	"sort"
	"strings"

	"go.mewis.me/codemcp/internal/tools"
)

const (
	CompletionCompleteMethod = "completion/complete"
	maxCompletionValues      = 100
)

type CompletionReference struct {
	Type string
	Name string
	URI  string
}

type CompletionArgument struct {
	Name  string
	Value string
}

type CompletionRequest struct {
	Ref       CompletionReference
	Argument  CompletionArgument
	Arguments map[string]string
}

type CompletionResult struct {
	Values  []string
	Total   int
	HasMore bool
}

func (e *FeatureExecutor) Complete(ctx context.Context, request CompletionRequest) (CompletionResult, error) {
	if e == nil || e.Registry == nil || e.Tools == nil {
		return CompletionResult{}, NewError(ErrInternal, "completion runtime is unavailable")
	}
	if strings.TrimSpace(request.Ref.Type) == "ref/prompt" {
		if request.Argument.Name == "workspace_id" {
			values, err := e.visibleCompletionWorkspaceIDs(ctx)
			if err != nil {
				return CompletionResult{}, err
			}
			values = filterCompletionPrefix(values, request.Argument.Value)
			if len(values) > maxCompletionValues {
				return CompletionResult{Values: values[:maxCompletionValues], Total: len(values), HasMore: true}, nil
			}
			return CompletionResult{Values: values, Total: len(values)}, nil
		}
		params := map[string]any{}
		if workspaceID, ok := request.Arguments["workspace_id"]; ok {
			params["workspace_id"] = workspaceID
		}
		workspaceID, err := authorizedPromptWorkspace(ctx, e.Tools, e.BoundWorkspace, params)
		if err != nil {
			return CompletionResult{}, err
		}
		store, err := promptStoreFor(e.Tools, workspaceID)
		if err != nil {
			return CompletionResult{}, err
		}
		var values []string
		switch request.Argument.Name {
		case "name":
			prompts, listErr := store.List()
			if listErr != nil {
				return CompletionResult{}, listErr
			}
			for _, prompt := range prompts {
				values = append(values, prompt.Definition.Name)
			}
		default:
			// Prompt argument content is free text; avoid guessing or leaking values.
		}
		values = filterCompletionPrefix(values, request.Argument.Value)
		total := len(values)
		if total > maxCompletionValues {
			return CompletionResult{Values: values[:maxCompletionValues], Total: total, HasMore: true}, nil
		}
		return CompletionResult{Values: values, Total: total}, nil
	}
	if strings.TrimSpace(request.Ref.Type) != "ref/resource" {
		return CompletionResult{Values: []string{}}, nil
	}
	name := strings.TrimSpace(request.Argument.Name)
	prefix := request.Argument.Value
	var values []string
	switch name {
	case "workspace_id", "workspace":
		ids, err := e.visibleCompletionWorkspaceIDs(ctx)
		if err != nil {
			return CompletionResult{}, err
		}
		values = filterCompletionPrefix(ids, prefix)
	case "uri", "resource", "resource_uri":
		values, _ = e.visibleCompletionResourceURIs(ctx, request.Arguments)
		values = filterCompletionPrefix(values, prefix)
	default:
		if strings.Contains(request.Ref.URI, "{workspace_id}") {
			ids, err := e.visibleCompletionWorkspaceIDs(ctx)
			if err != nil {
				return CompletionResult{}, err
			}
			values = filterCompletionPrefix(ids, prefix)
		} else {
			values = []string{}
		}
	}
	total := len(values)
	hasMore := total > maxCompletionValues
	if hasMore {
		values = append([]string(nil), values[:maxCompletionValues]...)
	} else {
		values = append([]string(nil), values...)
	}
	return CompletionResult{Values: values, Total: total, HasMore: hasMore}, nil
}

func (e *FeatureExecutor) visibleCompletionWorkspaceIDs(ctx context.Context) ([]string, error) {
	if e == nil || e.Tools == nil || e.Tools.Workspaces == nil {
		return nil, NewError(ErrInternal, "workspace manager is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if e.Source != "" {
		ctx = tools.WithCallSource(ctx, e.Source)
	}
	executorBound := strings.TrimSpace(e.BoundWorkspace)
	contextBound := strings.TrimSpace(tools.BoundWorkspace(ctx))
	if executorBound != "" {
		ctx = tools.WithBoundWorkspace(ctx, executorBound)
		contextBound = executorBound
	}
	if contextBound != "" {
		canonical, err := e.Tools.Workspaces.CanonicalID(contextBound)
		if err != nil {
			return nil, NewError(ErrInvalidParams, "Bound workspace is unavailable")
		}
		return []string{canonical}, nil
	}

	sessionID := strings.TrimSpace(tools.MCPSessionID(ctx))
	if sessionID == "" || e.Tools.SessionAccess == nil {
		return []string{}, nil
	}
	session, ok := e.Tools.SessionAccess.Lookup(sessionID)
	if !ok || len(session.Workspaces) == 0 {
		return []string{}, nil
	}
	seen := map[string]bool{}
	values := make([]string, 0, len(session.Workspaces))
	for workspaceID := range session.Workspaces {
		canonical, err := e.Tools.Workspaces.CanonicalID(workspaceID)
		if err != nil || seen[canonical] {
			continue
		}
		seen[canonical] = true
		values = append(values, canonical)
	}
	sort.Strings(values)
	return values, nil
}

func (e *FeatureExecutor) visibleCompletionResourceURIs(ctx context.Context, arguments map[string]string) ([]string, error) {
	if e == nil || e.Registry == nil {
		return nil, NewError(ErrInternal, "feature registry is unavailable")
	}
	workspaceIDs, err := e.visibleCompletionWorkspaceIDs(ctx)
	if err != nil {
		return nil, err
	}
	if requested := strings.TrimSpace(arguments["workspace_id"]); requested != "" {
		found := false
		for _, workspaceID := range workspaceIDs {
			if workspaceID == requested {
				workspaceIDs = []string{workspaceID}
				found = true
				break
			}
		}
		if !found {
			workspaceIDs = nil
		}
	}

	snapshot := e.Registry.Snapshot()
	seen := map[string]bool{}
	values := make([]string, 0, len(snapshot.Resources)+len(snapshot.ResourceTemplates)*len(workspaceIDs))
	for _, descriptor := range snapshot.Resources {
		if descriptor.URI != "" && !seen[descriptor.URI] {
			seen[descriptor.URI] = true
			values = append(values, descriptor.URI)
		}
	}
	const prefix = ResourceScheme + "://" + ResourceWorkspaceHost + "/{workspace_id}/"
	for _, descriptor := range snapshot.ResourceTemplates {
		if !strings.HasPrefix(descriptor.URITemplate, prefix) {
			continue
		}
		resourcePath := strings.TrimPrefix(descriptor.URITemplate, prefix)
		for _, workspaceID := range workspaceIDs {
			uri, uriErr := WorkspaceResourceURI(workspaceID, resourcePath)
			if uriErr != nil || seen[uri] {
				continue
			}
			seen[uri] = true
			values = append(values, uri)
		}
	}
	sort.Strings(values)
	return values, nil
}

func filterCompletionPrefix(values []string, prefix string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func completionRequestFromParams(params map[string]any) (CompletionRequest, error) {
	refRaw, ok := params["ref"].(map[string]any)
	if !ok {
		return CompletionRequest{}, NewError(ErrInvalidParams, "ref must be an object")
	}
	refType, _ := refRaw["type"].(string)
	if strings.TrimSpace(refType) == "" {
		return CompletionRequest{}, NewError(ErrInvalidParams, "ref.type is required")
	}
	ref := CompletionReference{Type: refType}
	ref.Name, _ = refRaw["name"].(string)
	ref.URI, _ = refRaw["uri"].(string)
	if ref.Type == "ref/resource" && strings.TrimSpace(ref.URI) == "" {
		return CompletionRequest{}, NewError(ErrInvalidParams, "resource ref URI is required")
	}
	if ref.Type == "ref/prompt" && strings.TrimSpace(ref.Name) == "" {
		return CompletionRequest{}, NewError(ErrInvalidParams, "prompt ref name is required")
	}
	if ref.Type != "ref/resource" && ref.Type != "ref/prompt" {
		return CompletionRequest{}, NewError(ErrInvalidParams, "unsupported completion ref type")
	}

	argumentRaw, ok := params["argument"].(map[string]any)
	if !ok {
		return CompletionRequest{}, NewError(ErrInvalidParams, "argument must be an object")
	}
	name, _ := argumentRaw["name"].(string)
	value, valueOK := argumentRaw["value"].(string)
	if strings.TrimSpace(name) == "" || !valueOK {
		return CompletionRequest{}, NewError(ErrInvalidParams, "argument name and value are required")
	}

	arguments := map[string]string{}
	if contextRaw, exists := params["context"]; exists {
		contextMap, ok := contextRaw.(map[string]any)
		if !ok {
			return CompletionRequest{}, NewError(ErrInvalidParams, "context must be an object")
		}
		if rawArgs, exists := contextMap["arguments"]; exists {
			argMap, ok := rawArgs.(map[string]any)
			if !ok {
				return CompletionRequest{}, NewError(ErrInvalidParams, "context.arguments must be an object")
			}
			for key, raw := range argMap {
				text, ok := raw.(string)
				if !ok {
					return CompletionRequest{}, NewError(ErrInvalidParams, "context.arguments values must be strings")
				}
				arguments[key] = text
			}
		}
	}
	return CompletionRequest{
		Ref: ref, Argument: CompletionArgument{Name: name, Value: value}, Arguments: arguments,
	}, nil
}

func completionResultMap(result CompletionResult) map[string]any {
	values := append([]string(nil), result.Values...)
	if values == nil {
		values = []string{}
	}
	return map[string]any{
		"completion": map[string]any{
			"values": values, "total": result.Total, "hasMore": result.HasMore,
		},
	}
}

func validateCompletionParams(params map[string]any) error {
	_, err := completionRequestFromParams(params)
	var protocol *Error
	if errors.As(err, &protocol) {
		return protocol
	}
	return err
}
