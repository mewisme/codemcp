package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"go.mewis.me/codemcp/internal/tools"
)

const (
	ResourceScheme                    = "cm"
	ResourceGlobalHost                = "global"
	ResourceWorkspaceHost             = "workspace"
	ResourceCacheScopePublic          = "public"
	ResourceCacheScopePrivate         = "private"
	ResourcesListMethod               = "resources/list"
	ResourcesReadMethod               = "resources/read"
	ResourceTemplatesListMethod       = "resources/templates/list"
	defaultResourceMaxBytes     int64 = 1 << 20
	maxResourceMaxBytes         int64 = 4 << 20
	resourceCacheScopeMetaKey         = "io.codemcp/internal-resource-cache-scope"
)

type ResourceCachePolicy struct {
	TTLMs int    `json:"ttl_ms"`
	Scope string `json:"scope"`
}

type ResourceSubscriptionPolicy struct {
	Allowed bool `json:"allowed"`
}

type ResourcePolicy struct {
	MaxBytes     int64                      `json:"max_bytes"`
	Cache        ResourceCachePolicy        `json:"cache"`
	Subscription ResourceSubscriptionPolicy `json:"subscription"`
}

type ParsedResourceURI struct {
	URI         string       `json:"uri"`
	Scope       FeatureScope `json:"scope"`
	WorkspaceID string       `json:"workspace_id,omitempty"`
	Path        string       `json:"path"`
}

type ResourceReadRequest struct {
	URI         string
	Scope       FeatureScope
	WorkspaceID string
	Path        string
}

type ResourceContent struct {
	MIMEType string
	Text     *string
	Blob     []byte
}

type ResourceReadResult struct {
	URI     string
	Content ResourceContent
	Policy  ResourcePolicy
}

type ResourceReadHandler func(context.Context, ResourceReadRequest) (ResourceContent, error)

func normalizeResourceRegistration(registration *FeatureRegistration) error {
	if registration == nil {
		return errors.New("feature registration is required")
	}
	hasResources := len(registration.Resources) > 0 || len(registration.ResourceTemplates) > 0
	if hasResources && registration.Family != FeatureResources {
		return errors.New("resource descriptors require the resources feature family")
	}
	if !hasResources {
		if registration.ReadResource != nil {
			return errors.New("resource reader requires at least one resource descriptor or template")
		}
		return nil
	}
	if registration.ReadResource == nil {
		return errors.New("resource reader is required")
	}
	for index, descriptor := range registration.Resources {
		normalized, err := normalizeResourceDescriptor(descriptor)
		if err != nil {
			return fmt.Errorf("resource %d: %w", index, err)
		}
		registration.Resources[index] = normalized
	}
	for index, descriptor := range registration.ResourceTemplates {
		normalized, err := normalizeResourceTemplateDescriptor(descriptor)
		if err != nil {
			return fmt.Errorf("resource template %d: %w", index, err)
		}
		registration.ResourceTemplates[index] = normalized
	}
	if registration.Capabilities.Resources == nil {
		registration.Capabilities.Resources = &ResourcesCapability{}
	}
	registration.Capabilities.Resources.ListChanged = true
	registration.Capabilities.Resources.Subscribe = false
	for _, descriptor := range registration.Resources {
		registration.Capabilities.Resources.Subscribe = registration.Capabilities.Resources.Subscribe || descriptor.Policy.Subscription.Allowed
	}
	for _, descriptor := range registration.ResourceTemplates {
		registration.Capabilities.Resources.Subscribe = registration.Capabilities.Resources.Subscribe || descriptor.Policy.Subscription.Allowed
	}
	return nil
}

func TextResourceContent(text string) ResourceContent {
	return ResourceContent{Text: &text}
}

func BlobResourceContent(blob []byte) ResourceContent {
	return ResourceContent{Blob: append([]byte(nil), blob...)}
}

func GlobalResourceURI(resourcePath string) (string, error) {
	resourcePath, err := normalizeResourcePath(resourcePath)
	if err != nil {
		return "", err
	}
	return ResourceScheme + "://" + ResourceGlobalHost + "/" + resourcePath, nil
}

func WorkspaceResourceURI(workspaceID, resourcePath string) (string, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if !validWorkspaceResourceID(workspaceID) {
		return "", errors.New("workspace_id must be a canonical ws_* id")
	}
	resourcePath, err := normalizeResourcePath(resourcePath)
	if err != nil {
		return "", err
	}
	return ResourceScheme + "://" + ResourceWorkspaceHost + "/" + workspaceID + "/" + resourcePath, nil
}

func WorkspaceResourceTemplate(resourcePath string) (string, error) {
	resourcePath, err := normalizeResourcePath(resourcePath)
	if err != nil {
		return "", err
	}
	return ResourceScheme + "://" + ResourceWorkspaceHost + "/{workspace_id}/" + resourcePath, nil
}

func ParseResourceURI(raw string) (ParsedResourceURI, error) {
	original := raw
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ParsedResourceURI{}, errors.New("resource URI is required")
	}
	if raw != original {
		return ParsedResourceURI{}, errors.New("resource URI is not canonical")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ParsedResourceURI{}, errors.New("resource URI is invalid")
	}
	if parsed.Scheme != ResourceScheme || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return ParsedResourceURI{}, errors.New("resource URI must use canonical cm:// scope without userinfo, query, fragment, or opaque data")
	}
	if parsed.RawPath != "" || strings.Contains(raw, "%") || strings.Contains(raw, "\\") {
		return ParsedResourceURI{}, errors.New("resource URI must not use escaped or backslash path segments")
	}
	switch parsed.Host {
	case ResourceGlobalHost:
		resourcePath, err := normalizeResourcePath(strings.TrimPrefix(parsed.Path, "/"))
		if err != nil {
			return ParsedResourceURI{}, err
		}
		canonical := ResourceScheme + "://" + ResourceGlobalHost + "/" + resourcePath
		if raw != canonical {
			return ParsedResourceURI{}, errors.New("resource URI is not canonical")
		}
		return ParsedResourceURI{URI: canonical, Scope: FeatureScopeGlobal, Path: resourcePath}, nil
	case ResourceWorkspaceHost:
		trimmed := strings.TrimPrefix(parsed.Path, "/")
		workspaceID, resourcePath, found := strings.Cut(trimmed, "/")
		if !found || !validWorkspaceResourceID(workspaceID) {
			return ParsedResourceURI{}, errors.New("workspace resource URI requires a canonical ws_* workspace id")
		}
		resourcePath, err := normalizeResourcePath(resourcePath)
		if err != nil {
			return ParsedResourceURI{}, err
		}
		canonical := ResourceScheme + "://" + ResourceWorkspaceHost + "/" + workspaceID + "/" + resourcePath
		if raw != canonical {
			return ParsedResourceURI{}, errors.New("resource URI is not canonical")
		}
		return ParsedResourceURI{URI: canonical, Scope: FeatureScopeWorkspace, WorkspaceID: workspaceID, Path: resourcePath}, nil
	default:
		return ParsedResourceURI{}, errors.New("resource URI host must be global or workspace")
	}
}

func normalizeResourcePath(resourcePath string) (string, error) {
	resourcePath = strings.TrimSpace(resourcePath)
	if resourcePath == "" || strings.HasPrefix(resourcePath, "/") || strings.HasSuffix(resourcePath, "/") {
		return "", errors.New("resource path must be a non-empty relative path")
	}
	if strings.ContainsAny(resourcePath, "\\?#{}%") {
		return "", errors.New("resource path contains reserved scope or escape characters")
	}
	segments := strings.Split(resourcePath, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", errors.New("resource path contains an invalid segment")
		}
		if url.PathEscape(segment) != segment {
			return "", errors.New("resource path contains non-canonical characters")
		}
	}
	return strings.Join(segments, "/"), nil
}

func validWorkspaceResourceID(value string) bool {
	return strings.HasPrefix(value, "ws_") && len(value) > len("ws_") && !strings.ContainsAny(value, "/\\?#{}%")
}

func normalizeResourcePolicy(policy ResourcePolicy, workspace bool) (ResourcePolicy, error) {
	if policy.MaxBytes == 0 {
		policy.MaxBytes = defaultResourceMaxBytes
	}
	if policy.MaxBytes < 1 || policy.MaxBytes > maxResourceMaxBytes {
		return ResourcePolicy{}, fmt.Errorf("resource max_bytes must be between 1 and %d", maxResourceMaxBytes)
	}
	if policy.Cache.TTLMs < 0 {
		return ResourcePolicy{}, errors.New("resource cache ttl_ms must not be negative")
	}
	policy.Cache.Scope = strings.TrimSpace(policy.Cache.Scope)
	if policy.Cache.Scope == "" {
		policy.Cache.Scope = ResourceCacheScopePrivate
	}
	if policy.Cache.Scope != ResourceCacheScopePublic && policy.Cache.Scope != ResourceCacheScopePrivate {
		return ResourcePolicy{}, errors.New("resource cache scope must be public or private")
	}
	if workspace && policy.Cache.Scope != ResourceCacheScopePrivate {
		return ResourcePolicy{}, errors.New("workspace resources must use private cache scope")
	}
	return policy, nil
}

func normalizeResourceDescriptor(descriptor ResourceDescriptor) (ResourceDescriptor, error) {
	parsed, err := ParseResourceURI(descriptor.URI)
	if err != nil {
		return ResourceDescriptor{}, err
	}
	if parsed.Scope != FeatureScopeGlobal {
		return ResourceDescriptor{}, errors.New("workspace resources must be registered as templates")
	}
	descriptor.URI = parsed.URI
	descriptor.Name = strings.TrimSpace(descriptor.Name)
	if descriptor.Name == "" {
		return ResourceDescriptor{}, errors.New("resource name is required")
	}
	descriptor.MIMEType, err = normalizeResourceMIME(descriptor.MIMEType)
	if err != nil {
		return ResourceDescriptor{}, err
	}
	descriptor.Policy, err = normalizeResourcePolicy(descriptor.Policy, false)
	if err != nil {
		return ResourceDescriptor{}, err
	}
	if descriptor.Size < 0 || descriptor.Size > descriptor.Policy.MaxBytes {
		return ResourceDescriptor{}, errors.New("resource size must fit within max_bytes")
	}
	return descriptor, nil
}

func normalizeResourceTemplateDescriptor(descriptor ResourceTemplateDescriptor) (ResourceTemplateDescriptor, error) {
	descriptor.URITemplate = strings.TrimSpace(descriptor.URITemplate)
	const prefix = ResourceScheme + "://" + ResourceWorkspaceHost + "/{workspace_id}/"
	if !strings.HasPrefix(descriptor.URITemplate, prefix) {
		return ResourceTemplateDescriptor{}, errors.New("resource template must use cm://workspace/{workspace_id}/ path")
	}
	resourcePath, err := normalizeResourcePath(strings.TrimPrefix(descriptor.URITemplate, prefix))
	if err != nil {
		return ResourceTemplateDescriptor{}, err
	}
	descriptor.URITemplate = prefix + resourcePath
	descriptor.Name = strings.TrimSpace(descriptor.Name)
	if descriptor.Name == "" {
		return ResourceTemplateDescriptor{}, errors.New("resource template name is required")
	}
	descriptor.MIMEType, err = normalizeResourceMIME(descriptor.MIMEType)
	if err != nil {
		return ResourceTemplateDescriptor{}, err
	}
	descriptor.Policy, err = normalizeResourcePolicy(descriptor.Policy, true)
	if err != nil {
		return ResourceTemplateDescriptor{}, err
	}
	return descriptor, nil
}

func normalizeResourceMIME(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("resource MIME type is required")
	}
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil || mediaType == "" {
		return "", errors.New("resource MIME type is invalid")
	}
	return mediaType, nil
}

func (r *FeatureRegistry) supportsResourceMethods() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, registration := range r.registrations {
		if len(registration.Resources) > 0 || len(registration.ResourceTemplates) > 0 {
			return true
		}
	}
	return false
}

func (r *FeatureRegistry) resourceRegistration(uri ParsedResourceURI) (FeatureRegistration, ResourceDescriptor, ResourceTemplateDescriptor, bool) {
	if r == nil {
		return FeatureRegistration{}, ResourceDescriptor{}, ResourceTemplateDescriptor{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, registration := range r.registrations {
		if uri.Scope == FeatureScopeGlobal {
			for _, descriptor := range registration.Resources {
				if descriptor.URI == uri.URI {
					return cloneFeatureRegistration(registration), descriptor, ResourceTemplateDescriptor{}, true
				}
			}
			continue
		}
		for _, descriptor := range registration.ResourceTemplates {
			const prefix = ResourceScheme + "://" + ResourceWorkspaceHost + "/{workspace_id}/"
			if strings.TrimPrefix(descriptor.URITemplate, prefix) == uri.Path {
				return cloneFeatureRegistration(registration), ResourceDescriptor{}, descriptor, true
			}
		}
	}
	return FeatureRegistration{}, ResourceDescriptor{}, ResourceTemplateDescriptor{}, false
}

func (e *FeatureExecutor) ListResources(ctx context.Context, cursor string) (map[string]any, error) {
	if strings.TrimSpace(cursor) != "" {
		return nil, NewError(ErrInvalidParams, "resource cursor is not valid")
	}
	if e == nil || e.Registry == nil {
		return nil, NewError(ErrInternal, "feature registry is unavailable")
	}
	snapshot := e.Registry.Snapshot()
	resources := make([]map[string]any, 0, len(snapshot.Resources))
	for _, descriptor := range snapshot.Resources {
		resources = append(resources, resourceDescriptorMap(descriptor))
	}
	return boundedResourceMethodResult(map[string]any{"resources": resources})
}

func (e *FeatureExecutor) ListResourceTemplates(ctx context.Context, cursor string) (map[string]any, error) {
	if strings.TrimSpace(cursor) != "" {
		return nil, NewError(ErrInvalidParams, "resource template cursor is not valid")
	}
	if e == nil || e.Registry == nil {
		return nil, NewError(ErrInternal, "feature registry is unavailable")
	}
	snapshot := e.Registry.Snapshot()
	templates := make([]map[string]any, 0, len(snapshot.ResourceTemplates))
	for _, descriptor := range snapshot.ResourceTemplates {
		templates = append(templates, resourceTemplateDescriptorMap(descriptor))
	}
	return boundedResourceMethodResult(map[string]any{"resourceTemplates": templates})
}

func resourceDescriptorMap(descriptor ResourceDescriptor) map[string]any {
	result := map[string]any{
		"uri": descriptor.URI, "name": descriptor.Name, "mimeType": descriptor.MIMEType,
	}
	if descriptor.Title != "" {
		result["title"] = descriptor.Title
	}
	if descriptor.Description != "" {
		result["description"] = descriptor.Description
	}
	if descriptor.Size > 0 {
		result["size"] = descriptor.Size
	}
	return result
}

func resourceTemplateDescriptorMap(descriptor ResourceTemplateDescriptor) map[string]any {
	result := map[string]any{
		"uriTemplate": descriptor.URITemplate, "name": descriptor.Name, "mimeType": descriptor.MIMEType,
	}
	if descriptor.Title != "" {
		result["title"] = descriptor.Title
	}
	if descriptor.Description != "" {
		result["description"] = descriptor.Description
	}
	return result
}

func (e *FeatureExecutor) ReadResource(ctx context.Context, rawURI string) (ResourceReadResult, error) {
	if e == nil || e.Registry == nil {
		return ResourceReadResult{}, NewError(ErrInternal, "feature registry is unavailable")
	}
	parsed, err := ParseResourceURI(rawURI)
	if err != nil {
		return ResourceReadResult{}, NewErrorData(ErrInvalidParams, "Invalid resource URI", map[string]any{"uri": strings.TrimSpace(rawURI)})
	}
	registration, exact, template, ok := e.Registry.resourceRegistration(parsed)
	if !ok {
		return ResourceReadResult{}, ResourceNotFoundError(parsed.URI)
	}
	if registration.ReadResource == nil {
		return ResourceReadResult{}, NewError(ErrInternal, "resource owner is unavailable")
	}

	if ctx == nil {
		ctx = context.Background()
	}
	if e.Source != "" {
		ctx = tools.WithCallSource(ctx, e.Source)
	}
	if e.BoundWorkspace != "" {
		ctx = tools.WithBoundWorkspace(ctx, e.BoundWorkspace)
	}
	if parsed.Scope == FeatureScopeWorkspace {
		if e.Tools == nil {
			return ResourceReadResult{}, NewError(ErrInternal, "tool runtime is unavailable")
		}
		resolution, accessErr := e.Tools.ResolveWorkspaceAccess(ctx, parsed.WorkspaceID)
		if accessErr != nil {
			return ResourceReadResult{}, NewErrorData(ErrInvalidParams, "Resource access denied", map[string]any{"uri": parsed.URI})
		}
		parsed.WorkspaceID = resolution.WorkspaceID
		canonical, uriErr := WorkspaceResourceURI(parsed.WorkspaceID, parsed.Path)
		if uriErr != nil {
			return ResourceReadResult{}, NewError(ErrInternal, "resource URI normalization failed")
		}
		parsed.URI = canonical
	}

	policy := exact.Policy
	mimeType := exact.MIMEType
	if parsed.Scope == FeatureScopeWorkspace {
		policy = template.Policy
		mimeType = template.MIMEType
	}
	content, readErr := registration.ReadResource(ctx, ResourceReadRequest(parsed))
	if readErr != nil {
		var protocol *Error
		if errors.As(readErr, &protocol) {
			return ResourceReadResult{}, protocol
		}
		return ResourceReadResult{}, NewErrorData(ErrInternal, "Resource resolution failed", map[string]any{"uri": parsed.URI})
	}
	content, err = normalizeResourceContent(parsed.URI, mimeType, policy, content)
	if err != nil {
		return ResourceReadResult{}, err
	}
	return ResourceReadResult{URI: parsed.URI, Content: content, Policy: policy}, nil
}

func normalizeResourceContent(uri, descriptorMIME string, policy ResourcePolicy, content ResourceContent) (ResourceContent, error) {
	if content.Text != nil && content.Blob != nil {
		return ResourceContent{}, NewErrorData(ErrInternal, "Resource content is ambiguous", map[string]any{"uri": uri})
	}
	if content.Text == nil && content.Blob == nil {
		empty := ""
		content.Text = &empty
	}
	content.MIMEType = strings.TrimSpace(content.MIMEType)
	if content.MIMEType == "" {
		content.MIMEType = descriptorMIME
	}
	normalizedMIME, err := normalizeResourceMIME(content.MIMEType)
	if err != nil || normalizedMIME != descriptorMIME {
		return ResourceContent{}, NewErrorData(ErrInternal, "Resource content MIME type violates descriptor", map[string]any{"uri": uri})
	}
	content.MIMEType = normalizedMIME
	size := len(content.Blob)
	if content.Text != nil {
		size = len([]byte(*content.Text))
	}
	if int64(size) > policy.MaxBytes {
		return ResourceContent{}, NewErrorData(ErrInternal, "Resource content exceeds size limit", map[string]any{
			"uri": uri, "limit": policy.MaxBytes,
		})
	}
	content.Blob = append([]byte(nil), content.Blob...)
	return content, nil
}

func (e *FeatureExecutor) ResourceToolFallback(uriArgument string) tools.Handler {
	uriArgument = strings.TrimSpace(uriArgument)
	if uriArgument == "" {
		uriArgument = "uri"
	}
	return func(ctx context.Context, args map[string]any) (tools.Result, error) {
		rawURI, ok := args[uriArgument].(string)
		if !ok || strings.TrimSpace(rawURI) == "" {
			return tools.Result{}, fmt.Errorf("%s must be a non-empty string", uriArgument)
		}
		result, err := e.ReadResource(ctx, rawURI)
		if err != nil {
			return tools.Result{}, err
		}
		return tools.JSONResult(resourceReadResultMap(result)), nil
	}
}

func resourceReadResultMap(result ResourceReadResult) map[string]any {
	content := map[string]any{"uri": result.URI, "mimeType": result.Content.MIMEType}
	if result.Content.Text != nil {
		content["text"] = *result.Content.Text
	} else {
		content["blob"] = base64.StdEncoding.EncodeToString(result.Content.Blob)
	}
	return map[string]any{
		"contents":   []any{content},
		"ttlMs":      result.Policy.Cache.TTLMs,
		"cacheScope": result.Policy.Cache.Scope,
	}
}

func boundedResourceMethodResult(result map[string]any) (map[string]any, error) {
	encoded, err := cloneFeatureMap(result)
	if err != nil {
		return nil, NewError(ErrInternal, "resource method result is not JSON serializable")
	}
	raw, err := json.Marshal(encoded)
	if err != nil {
		return nil, NewError(ErrInternal, "resource method result is not JSON serializable")
	}
	if int64(len(raw)) > maxResourceMaxBytes {
		return nil, NewError(ErrInternal, "resource method result exceeds size limit")
	}
	return encoded, nil
}

func InstallResourceProjection(server *sdkmcp.Server, executor *FeatureExecutor) error {
	return installResourceProjection(server, executor, newSDKResourceProjectionState())
}

type sdkResourceProjectionState struct {
	resources           map[string]struct{}
	templates           map[string]struct{}
	middlewareInstalled bool
}

func newSDKResourceProjectionState() *sdkResourceProjectionState {
	return &sdkResourceProjectionState{
		resources: map[string]struct{}{},
		templates: map[string]struct{}{},
	}
}

func installResourceProjection(server *sdkmcp.Server, executor *FeatureExecutor, state *sdkResourceProjectionState) error {
	if server == nil || executor == nil || executor.Registry == nil {
		return nil
	}
	if state == nil {
		state = newSDKResourceProjectionState()
	}
	snapshot := executor.Registry.Snapshot()
	handler := func(ctx context.Context, request *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
		if request == nil || request.Params == nil {
			return nil, featureJSONRPCError(NewError(ErrInvalidParams, "resource read params are required"))
		}
		if request.Session != nil {
			if sessionID := strings.TrimSpace(request.Session.ID()); sessionID != "" {
				ctx = tools.WithMCPSessionID(ctx, sessionID)
			}
		}
		result, err := executor.ReadResource(ctx, request.Params.URI)
		if err != nil {
			var protocol *Error
			if errors.As(err, &protocol) && protocol.Code == ErrInvalidParams && protocol.Message == "Resource not found" {
				return nil, sdkmcp.ResourceNotFoundError(request.Params.URI)
			}
			return nil, featureJSONRPCError(err)
		}
		content := &sdkmcp.ResourceContents{URI: result.URI, MIMEType: result.Content.MIMEType}
		if result.Content.Text != nil {
			content.Text = *result.Content.Text
		} else {
			content.Blob = append([]byte(nil), result.Content.Blob...)
		}
		return &sdkmcp.ReadResourceResult{
			Meta:      sdkmcp.Meta{resourceCacheScopeMetaKey: result.Policy.Cache.Scope},
			Cacheable: sdkmcp.Cacheable{TTLMs: result.Policy.Cache.TTLMs, CacheScope: result.Policy.Cache.Scope},
			Contents:  []*sdkmcp.ResourceContents{content},
		}, nil
	}
	for _, descriptor := range snapshot.Resources {
		if _, exists := state.resources[descriptor.URI]; exists {
			continue
		}
		state.resources[descriptor.URI] = struct{}{}
		server.AddResource(&sdkmcp.Resource{
			URI: descriptor.URI, Name: descriptor.Name, Title: descriptor.Title,
			Description: descriptor.Description, MIMEType: descriptor.MIMEType, Size: descriptor.Size,
		}, handler)
	}
	for _, descriptor := range snapshot.ResourceTemplates {
		if _, exists := state.templates[descriptor.URITemplate]; exists {
			continue
		}
		state.templates[descriptor.URITemplate] = struct{}{}
		server.AddResourceTemplate(&sdkmcp.ResourceTemplate{
			URITemplate: descriptor.URITemplate, Name: descriptor.Name, Title: descriptor.Title,
			Description: descriptor.Description, MIMEType: descriptor.MIMEType,
		}, handler)
	}
	if !state.middlewareInstalled {
		server.AddReceivingMiddleware(resourceCachePolicyMiddleware())
		state.middlewareInstalled = true
	}
	return nil
}

func resourceCachePolicyMiddleware() sdkmcp.Middleware {
	return func(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
		return func(ctx context.Context, method string, request sdkmcp.Request) (sdkmcp.Result, error) {
			result, err := next(ctx, method, request)
			if err != nil || method != ResourcesReadMethod {
				return result, err
			}
			readResult, ok := result.(*sdkmcp.ReadResourceResult)
			if !ok || readResult == nil {
				return result, err
			}
			meta := readResult.GetMeta()
			scope, _ := meta[resourceCacheScopeMetaKey].(string)
			if scope == ResourceCacheScopePublic || scope == ResourceCacheScopePrivate {
				readResult.CacheScope = scope
			}
			delete(meta, resourceCacheScopeMetaKey)
			readResult.SetMeta(meta)
			return readResult, nil
		}
	}
}

func cloneResourceTemplateDescriptors(values []ResourceTemplateDescriptor) []ResourceTemplateDescriptor {
	if len(values) == 0 {
		return nil
	}
	return append([]ResourceTemplateDescriptor(nil), values...)
}
