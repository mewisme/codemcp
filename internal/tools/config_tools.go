package tools

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/controlguard"
	mcpconfigwire "go.mewis.me/codemcp/internal/mcpconfig/wire"
)

const configCursorVersion = 1

type ConfigReadProvider interface {
	List(context.Context, string) ([]mcpconfigwire.Setting, mcpconfigwire.ErrorCode)
	Get(context.Context, string) (mcpconfigwire.Setting, mcpconfigwire.ErrorCode)
}

type ConfigSetApprovalProvider interface {
	BindSetApproval(context.Context, map[string]any) (mcpconfigwire.SetApprovalBinding, mcpconfigwire.ErrorCode)
}

type ConfigSetApplyProvider interface {
	ApplySet(context.Context, map[string]any, mcpconfigwire.SetApprovalBinding) (mcpconfigwire.MutationResult, *mcpconfigwire.MutationError)
}

type configListCursor struct {
	Version    int    `json:"v"`
	Offset     int    `json:"o"`
	PrefixHash string `json:"p"`
}

func RegisterConfigTools(registry *Registry, runtime *Runtime) {
	if registry == nil {
		return
	}
	registry.MustRegister(mcpconfigwire.ListToolName, Schema{
		Name:         mcpconfigwire.ListToolName,
		Title:        "List Configuration",
		Description:  "List the bounded, agent-safe projection of global CodeMCP settings when operator-controlled read eligibility is enabled. Never returns managed secret values.",
		InputSchema:  mcpconfigwire.ListInputSchema,
		OutputSchema: mcpconfigwire.ListOutputSchema,
		Annotations:  ToolAnnotations(RiskRead),
		Capability:   toolCapability(CapabilityDomainConfig),
	}, configListHandler(runtime))
	registry.MustRegister(mcpconfigwire.GetToolName, Schema{
		Name:         mcpconfigwire.GetToolName,
		Title:        "Get Configuration Setting",
		Description:  "Read one agent-safe global CodeMCP setting when operator-controlled read eligibility is enabled. Returns configured-state metadata instead of managed secret values.",
		InputSchema:  mcpconfigwire.GetInputSchema,
		OutputSchema: mcpconfigwire.GetOutputSchema,
		Annotations:  ToolAnnotations(RiskRead),
		Capability:   toolCapability(CapabilityDomainConfig),
	}, configGetHandler(runtime))
	registry.MustRegister(mcpconfigwire.SetToolName, Schema{
		Name:         mcpconfigwire.SetToolName,
		Title:        "Set Configuration",
		Description:  "Apply one bounded batch of agent-eligible global CodeMCP settings. Every batch requires exact one-shot local human approval and managed secret values are forbidden.",
		InputSchema:  mcpconfigwire.SetInputSchema,
		OutputSchema: mcpconfigwire.SetOutputSchema,
		Annotations:  ToolAnnotations(RiskEdit),
		Capability:   toolCapability(CapabilityDomainConfig),
	}, configSetHandler(runtime))
}

func (r *Runtime) SetConfigReadProvider(provider ConfigReadProvider) {
	if r == nil {
		return
	}
	r.configReadMu.Lock()
	r.configReads = provider
	r.configReadMu.Unlock()
}

func (r *Runtime) SetConfigSetApprovalProvider(provider ConfigSetApprovalProvider) {
	if r == nil {
		return
	}
	r.configApprovalMu.Lock()
	r.configApprovals = provider
	r.configApprovalMu.Unlock()
}

func (r *Runtime) SetConfigSetApplyProvider(provider ConfigSetApplyProvider) {
	if r == nil {
		return
	}
	r.configApplyMu.Lock()
	r.configApplies = provider
	r.configApplyMu.Unlock()
}

func (r *Runtime) configReadProvider() ConfigReadProvider {
	if r == nil {
		return nil
	}
	r.configReadMu.RLock()
	defer r.configReadMu.RUnlock()
	return r.configReads
}

func (r *Runtime) configSetApprovalProvider() ConfigSetApprovalProvider {
	if r == nil {
		return nil
	}
	r.configApprovalMu.RLock()
	defer r.configApprovalMu.RUnlock()
	return r.configApprovals
}

func (r *Runtime) configSetApplyProvider() ConfigSetApplyProvider {
	if r == nil {
		return nil
	}
	r.configApplyMu.RLock()
	defer r.configApplyMu.RUnlock()
	return r.configApplies
}

func (r *Runtime) bindConfigSetApproval(ctx context.Context, arguments map[string]any) (map[string]any, mcpconfigwire.ErrorCode) {
	provider := r.configSetApprovalProvider()
	if provider == nil {
		return nil, mcpconfigwire.ErrorAccessDenied
	}
	binding, code := provider.BindSetApproval(ctx, arguments)
	if code != "" {
		return nil, code
	}
	if strings.TrimSpace(binding.ConfigRoot) == "" || strings.TrimSpace(binding.ConfigFingerprint) == "" {
		return nil, mcpconfigwire.ErrorInvalidRequest
	}
	_, workspaceID, err := mcpconfigwire.CanonicalSetArguments(arguments)
	if err != nil {
		return nil, mcpconfigwire.ErrorInvalidRequest
	}
	bound := map[string]any{
		"changes": append([]mcpconfigwire.Change(nil), binding.Changes...),
		mcpconfigwire.SetApprovalBindingKey: map[string]any{
			"version":            mcpconfigwire.SetApprovalBindingVersion,
			"config_root":        strings.TrimSpace(binding.ConfigRoot),
			"config_fingerprint": strings.TrimSpace(binding.ConfigFingerprint),
		},
	}
	if workspaceID != "" {
		bound["workspace_id"] = workspaceID
	}
	return bound, ""
}

func RequireConfigSetApproval(ctx context.Context) error {
	grant, ok := controlguard.GrantFromContext(ctx)
	if ok && grant.Code == controlguard.CodeControlPlaneMutation && strings.TrimSpace(grant.RequestID) != "" {
		return nil
	}
	return controlguard.New(controlguard.CodeControlPlaneMutation, "CodeMCP configuration changes require local approval", true, nil)
}

func configSetHandler(runtime *Runtime) Handler {
	return func(ctx context.Context, args map[string]any) (Result, error) {
		if err := RequireConfigSetApproval(ctx); err != nil {
			return Result{}, err
		}
		binding, code := runtime.approvedConfigSetBinding(ctx)
		if code != "" {
			return configSetApprovalError(code), nil
		}
		provider := runtime.configSetApplyProvider()
		if provider == nil {
			return configSetApprovalError(mcpconfigwire.ErrorAccessDenied), nil
		}
		result, mutationErr := provider.ApplySet(ctx, args, binding)
		if mutationErr != nil {
			return configSetMutationError(*mutationErr), nil
		}
		return JSONResult(result), nil
	}
}

func (r *Runtime) approvedConfigSetBinding(ctx context.Context) (mcpconfigwire.SetApprovalBinding, mcpconfigwire.ErrorCode) {
	if r == nil || r.Approvals == nil {
		return mcpconfigwire.SetApprovalBinding{}, mcpconfigwire.ErrorAccessDenied
	}
	requestID := strings.TrimSpace(ApprovalRequestID(ctx))
	if requestID == "" {
		return mcpconfigwire.SetApprovalBinding{}, mcpconfigwire.ErrorApprovalRequired
	}
	request, ok := r.Approvals.Get(requestID)
	if !ok || request.Status != approval.StatusConsumed || request.TargetTool != mcpconfigwire.SetToolName {
		return mcpconfigwire.SetApprovalBinding{}, mcpconfigwire.ErrorApprovalRequired
	}
	binding, _, err := mcpconfigwire.ParseBoundSetArguments(request.Arguments)
	if err != nil {
		return mcpconfigwire.SetApprovalBinding{}, mcpconfigwire.ErrorApprovalRequired
	}
	return binding, ""
}

func configListHandler(runtime *Runtime) Handler {
	return func(ctx context.Context, args map[string]any) (Result, error) {
		prefix, limit, cursor, ok := parseConfigListArguments(args)
		if !ok {
			return configReadError(mcpconfigwire.ErrorInvalidRequest), nil
		}
		offset, err := decodeConfigCursor(cursor, prefix)
		if err != nil {
			return configReadError(mcpconfigwire.ErrorInvalidRequest), nil
		}
		provider := runtime.configReadProvider()
		if provider == nil {
			return configReadError(mcpconfigwire.ErrorAccessDenied), nil
		}
		settings, code := provider.List(ctx, prefix)
		if code != "" {
			return configReadError(code), nil
		}
		if offset > len(settings) {
			return configReadError(mcpconfigwire.ErrorInvalidRequest), nil
		}
		end := min(offset+limit, len(settings))
		page := make([]mcpconfigwire.Setting, end-offset)
		copy(page, settings[offset:end])
		result := mcpconfigwire.ListResult{Settings: page}
		if end < len(settings) {
			result.NextCursor = encodeConfigCursor(prefix, end)
		}
		return JSONResult(result), nil
	}
}

func configGetHandler(runtime *Runtime) Handler {
	return func(ctx context.Context, args map[string]any) (Result, error) {
		key, ok := parseConfigGetArguments(args)
		if !ok {
			return configReadError(mcpconfigwire.ErrorInvalidRequest), nil
		}
		provider := runtime.configReadProvider()
		if provider == nil {
			return configReadError(mcpconfigwire.ErrorAccessDenied), nil
		}
		setting, code := provider.Get(ctx, key)
		if code != "" {
			return configReadError(code), nil
		}
		return JSONResult(mcpconfigwire.GetResult{Setting: setting}), nil
	}
}

func parseConfigListArguments(args map[string]any) (prefix string, limit int, cursor string, ok bool) {
	if !onlyConfigArguments(args, "prefix", "limit", "cursor") {
		return "", 0, "", false
	}
	prefix, ok = optionalConfigString(args, "prefix", mcpconfigwire.MaxKeyBytes)
	if !ok {
		return "", 0, "", false
	}
	cursor, ok = optionalConfigString(args, "cursor", mcpconfigwire.MaxCursorBytes)
	if !ok {
		return "", 0, "", false
	}
	limit, ok = optionalConfigInteger(args, "limit", mcpconfigwire.DefaultListLimit)
	if !ok || limit < 1 || limit > mcpconfigwire.MaxListLimit {
		return "", 0, "", false
	}
	return prefix, limit, cursor, true
}

func parseConfigGetArguments(args map[string]any) (string, bool) {
	if !onlyConfigArguments(args, "key") {
		return "", false
	}
	raw, exists := args["key"]
	if !exists {
		return "", false
	}
	key, ok := raw.(string)
	if !ok {
		return "", false
	}
	key = strings.TrimSpace(key)
	return key, key != "" && len(key) <= mcpconfigwire.MaxKeyBytes
}

func onlyConfigArguments(args map[string]any, allowed ...string) bool {
	accepted := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		accepted[key] = struct{}{}
	}
	for key := range args {
		if _, ok := accepted[key]; !ok {
			return false
		}
	}
	return true
}

func optionalConfigString(args map[string]any, key string, maxBytes int) (string, bool) {
	raw, exists := args[key]
	if !exists {
		return "", true
	}
	value, ok := raw.(string)
	if !ok || len(value) > maxBytes {
		return "", false
	}
	return strings.TrimSpace(value), true
}

func optionalConfigInteger(args map[string]any, key string, fallback int) (int, bool) {
	raw, exists := args[key]
	if !exists {
		return fallback, true
	}
	switch value := raw.(type) {
	case int:
		return value, true
	case int8:
		return int(value), true
	case int16:
		return int(value), true
	case int32:
		return int(value), true
	case int64:
		return int(value), int64(int(value)) == value
	case uint:
		return int(value), uint(int(value)) == value
	case uint8:
		return int(value), true
	case uint16:
		return int(value), true
	case uint32:
		return int(value), uint32(int(value)) == value
	case uint64:
		return int(value), uint64(int(value)) == value
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) || value != math.Trunc(value) {
			return 0, false
		}
		converted := int(value)
		if float64(converted) != value {
			return 0, false
		}
		return converted, true
	case json.Number:
		parsed, err := strconv.ParseInt(string(value), 10, 64)
		if err != nil {
			return 0, false
		}
		return int(parsed), int64(int(parsed)) == parsed
	default:
		return 0, false
	}
}

func encodeConfigCursor(prefix string, offset int) string {
	data, _ := json.Marshal(configListCursor{Version: configCursorVersion, Offset: offset, PrefixHash: configPrefixHash(prefix)})
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeConfigCursor(raw, prefix string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	if len(raw) > mcpconfigwire.MaxCursorBytes {
		return 0, errors.New("invalid cursor")
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(data) > mcpconfigwire.MaxCursorBytes {
		return 0, errors.New("invalid cursor")
	}
	var cursor configListCursor
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil || cursor.Version != configCursorVersion || cursor.Offset < 0 || cursor.PrefixHash != configPrefixHash(prefix) {
		return 0, errors.New("invalid cursor")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return 0, errors.New("invalid cursor")
	}
	return cursor.Offset, nil
}

func configPrefixHash(prefix string) string {
	hash := sha256.Sum256([]byte(prefix))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

func configReadError(code mcpconfigwire.ErrorCode) Result {
	switch code {
	case mcpconfigwire.ErrorAccessDenied, mcpconfigwire.ErrorInvalidRequest, mcpconfigwire.ErrorUnsupportedSetting:
	default:
		code = mcpconfigwire.ErrorInvalidRequest
	}
	data, _ := json.Marshal(mcpconfigwire.PublicError{Code: code})
	return Result{
		Content:    []Content{{Type: "text", Text: string(data)}},
		IsError:    true,
		ResultType: "complete",
	}
}

func configSetApprovalError(code mcpconfigwire.ErrorCode) Result {
	switch code {
	case mcpconfigwire.ErrorAccessDenied,
		mcpconfigwire.ErrorInvalidRequest,
		mcpconfigwire.ErrorUnsupportedSetting,
		mcpconfigwire.ErrorSecretWriteForbidden:
	default:
		code = mcpconfigwire.ErrorInvalidRequest
	}
	data, _ := json.Marshal(mcpconfigwire.PublicError{Code: code})
	return Result{
		Content:    []Content{{Type: "text", Text: string(data)}},
		IsError:    true,
		ResultType: "complete",
	}
}

func configSetMutationError(value mcpconfigwire.MutationError) Result {
	data, _ := json.Marshal(value)
	return Result{
		Content:           []Content{{Type: "text", Text: string(data)}},
		StructuredContent: value,
		IsError:           true,
		ResultType:        "complete",
	}
}
