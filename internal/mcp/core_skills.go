package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"

	"go.mewis.me/codemcp/internal/instructionpolicy"
	"go.mewis.me/codemcp/internal/skills"
	"go.mewis.me/codemcp/internal/tools"
)

const (
	coreSkillsRegistrationID = "codemcp-core-skills"
	skillContentMaxBytes     = 500_000
	SkillsExtensionID        = "io.modelcontextprotocol/skills"
	SkillsListMethod         = "skills/list"
	SkillsGetMethod          = "skills/get"
	skillScheme              = "skill"
	skillServerID            = "codemcp"
	openAIMaxSkills          = 5
	openAIMaxSkillFiles      = 100
	openAIMaxManifestBytes   = 256 << 10
	openAIMaxSupportBytes    = 1 << 20
	openAIMaxSkillBytes      = 5 << 20
	openAIMaxArchiveBytes    = 8 << 20
)

func ensureCoreSkills(registry *FeatureRegistry, runtime *tools.Runtime) error {
	if registry == nil || runtime == nil {
		return nil
	}
	registry.coreSkillsOnce.Do(func() {
		registry.coreSkillsErr = registry.Register(coreSkillRegistration(runtime))
	})
	return registry.coreSkillsErr
}

func coreSkillRegistration(runtime *tools.Runtime) FeatureRegistration {
	descriptors := []SkillDescriptor{}
	if values, err := globalResolvedSkills(); err == nil {
		descriptors = skillDescriptors(values)
	}
	return FeatureRegistration{
		ID:     coreSkillsRegistrationID,
		Family: FeatureSkills,
		Skills: descriptors,
		Capabilities: FeatureCapabilities{
			Extensions: map[string]any{SkillsExtensionID: map[string]any{}},
		},
		Methods: []FeatureMethod{
			{Name: SkillsListMethod, Scope: FeatureScopeGlobal, Custom: true, Handler: unavailableSkillMethod},
			{Name: SkillsGetMethod, Scope: FeatureScopeGlobal, Custom: true, Handler: unavailableSkillMethod},
		},
	}
}

func unavailableSkillMethod(context.Context, FeatureRequest) (map[string]any, error) {
	return nil, errors.New("skill method requires feature executor")
}

func globalResolvedSkills() ([]skills.Skill, error) {
	home, _ := os.UserHomeDir()
	values, err := skills.DiscoverUser(home, instructionpolicy.DefaultConfig())
	if err != nil {
		return nil, err
	}
	result := make([]skills.Skill, 0, len(values))
	for _, value := range values {
		if value.Source == ".cm" || skills.IsBuiltin(value) {
			result = append(result, value)
		}
	}
	return result, nil
}

func workspaceResolvedSkills(runtime *tools.Runtime, workspaceID string) ([]skills.Skill, error) {
	if runtime == nil || runtime.Workspaces == nil {
		return nil, errors.New("skill workspace manager is unavailable")
	}
	item, err := runtime.Workspaces.Get(workspaceID)
	if err != nil {
		return nil, err
	}
	home, _ := os.UserHomeDir()
	return skills.DiscoverWithUser(item.Path, home, instructionpolicy.DefaultConfig())
}

func skillDescriptors(values []skills.Skill) []SkillDescriptor {
	out := make([]SkillDescriptor, 0, len(values))
	for _, value := range values {
		extensions := map[string]any{
			"source":   value.Source,
			"builtin":  skills.IsBuiltin(value),
			"readOnly": skills.IsBuiltin(value) || value.Source != ".cm",
		}
		out = append(out, SkillDescriptor{
			Name:        value.Name,
			Description: value.Description,
			Extensions:  extensions,
		})
	}
	return out
}

func loadCanonicalSkill(runtime *tools.Runtime, workspaceID, name string, maxBytes int) (skills.Loaded, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return skills.Loaded{}, errors.New("skill name is required")
	}
	if maxBytes <= 0 || maxBytes > skillContentMaxBytes {
		return skills.Loaded{}, errors.New("max skill bytes is out of range")
	}
	if workspaceID == "" {
		values, err := globalResolvedSkills()
		if err != nil {
			return skills.Loaded{}, err
		}
		return loadResolvedSkill(values, name, maxBytes)
	}
	values, err := workspaceResolvedSkills(runtime, workspaceID)
	if err != nil {
		return skills.Loaded{}, err
	}
	return loadResolvedSkill(values, name, maxBytes)
}

func loadResolvedSkill(values []skills.Skill, name string, maxBytes int) (skills.Loaded, error) {
	return skills.LoadFromInventory(values, name, maxBytes)
}

func skillContentIdentity(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func skillManifest(value skills.Loaded) map[string]any {
	return map[string]any{
		"name":        value.Skill.Name,
		"description": value.Skill.Description,
		"source":      value.Skill.Source,
		"builtin":     skills.IsBuiltin(value.Skill),
		"readOnly":    skills.IsBuiltin(value.Skill) || value.Skill.Source != ".cm",
		"digest":      "sha256:" + skillContentIdentity(value.Content),
		"bytes":       len([]byte(value.Content)),
		"truncated":   value.Truncated,
	}
}

func canonicalSkillInventory(ctx context.Context, runtime *tools.Runtime, workspaceID string) ([]skills.Skill, error) {
	_ = ctx
	if strings.TrimSpace(workspaceID) == "" {
		return globalResolvedSkills()
	}
	return workspaceResolvedSkills(runtime, workspaceID)
}

type skillResource struct {
	URI      string
	Relative string
	Data     []byte
	Digest   string
	MIMEType string
}

type skillBundle struct {
	Skill       skills.Skill
	URI         string
	Frontmatter map[string]any
	Resources   []skillResource
}

func (e *FeatureExecutor) ListSkills(ctx context.Context, params map[string]any) (map[string]any, error) {
	workspaceID, err := e.skillWorkspace(ctx, params)
	if err != nil {
		return nil, err
	}
	values, err := canonicalSkillInventory(ctx, e.Tools, workspaceID)
	if err != nil {
		return nil, NewError(ErrInternal, err.Error())
	}
	bundles := projectedSkillBundles(values, e.Profile)
	start, err := skillCursor(params)
	if err != nil {
		return nil, err
	}
	limit := len(bundles)
	pageSize := 50
	if e.Profile != nil && e.Profile.ID() == OpenAIProfileID {
		pageSize = openAIMaxSkills
	}
	if start < 0 || start > limit {
		return nil, NewError(ErrInvalidParams, "skill cursor is not valid")
	}
	end := start + pageSize
	if end > limit {
		end = limit
	}
	items := make([]map[string]any, 0, end-start)
	for _, bundle := range bundles[start:end] {
		items = append(items, skillCatalogEntry(bundle))
	}
	result := map[string]any{"skills": items}
	if end < limit {
		result["nextCursor"] = strconv.Itoa(end)
	}
	return result, nil
}

func projectedSkillBundles(values []skills.Skill, profile Profile) []skillBundle {
	result := make([]skillBundle, 0, len(values))
	openAI := profile != nil && profile.ID() == OpenAIProfileID
	archiveBytes := 0
	for _, value := range values {
		bundle, err := buildSkillBundle(value, profile)
		if err != nil {
			continue
		}
		if openAI {
			bundleBytes := 0
			for _, resource := range bundle.Resources {
				bundleBytes += len(resource.Data)
			}
			if archiveBytes+bundleBytes > openAIMaxArchiveBytes {
				continue
			}
			archiveBytes += bundleBytes
		}
		result = append(result, bundle)
		if openAI && len(result) == openAIMaxSkills {
			break
		}
	}
	return result
}

func (e *FeatureExecutor) GetSkill(ctx context.Context, params map[string]any) (map[string]any, error) {
	workspaceID, err := e.skillWorkspace(ctx, params)
	if err != nil {
		return nil, err
	}
	rawURI, _ := params["uri"].(string)
	name, relative, err := parseSkillURI(rawURI)
	if err != nil || relative != "SKILL.md" {
		return nil, NewError(ErrInvalidParams, "skill uri must identify SKILL.md")
	}
	values, err := canonicalSkillInventory(ctx, e.Tools, workspaceID)
	if err != nil {
		return nil, NewError(ErrInternal, err.Error())
	}
	for _, value := range values {
		if value.Name != name {
			continue
		}
		bundle, err := buildSkillBundle(value, e.Profile)
		if err != nil {
			return nil, NewError(ErrInvalidParams, err.Error())
		}
		if bundle.URI != rawURI {
			return nil, NewError(ErrInvalidParams, "skill uri is not canonical")
		}
		return skillCatalogEntry(bundle), nil
	}
	return nil, NewError(ErrInvalidParams, "unknown skill")
}

func (e *FeatureExecutor) skillWorkspace(ctx context.Context, params map[string]any) (string, error) {
	if e == nil || e.Tools == nil {
		return "", NewError(ErrInternal, "skill runtime is unavailable")
	}
	if e.BoundWorkspace != "" {
		return e.BoundWorkspace, nil
	}
	requested, _ := params["workspace_id"].(string)
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return "", nil
	}
	return "", NewError(ErrInvalidParams, "workspace skills require a workspace-bound MCP session")
}

func skillCursor(params map[string]any) (int, error) {
	if params == nil {
		return 0, nil
	}
	raw, exists := params["cursor"]
	if !exists || raw == nil || raw == "" {
		return 0, nil
	}
	value, ok := raw.(string)
	if !ok {
		return 0, NewError(ErrInvalidParams, "skill cursor must be a string")
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0, NewError(ErrInvalidParams, "skill cursor is not valid")
	}
	return parsed, nil
}

func skillCatalogEntry(bundle skillBundle) map[string]any {
	resources := make([]map[string]any, 0, len(bundle.Resources))
	for _, resource := range bundle.Resources {
		resources = append(resources, map[string]any{
			"uri": resource.URI, "digest": resource.Digest,
		})
	}
	return map[string]any{
		"uri":         bundle.URI,
		"name":        bundle.Skill.Name,
		"description": bundle.Skill.Description,
		"frontmatter": bundle.Frontmatter,
		"resources":   resources,
	}
}

func buildSkillBundle(value skills.Skill, profile Profile) (skillBundle, error) {
	if err := validateSkillURIName(value.Name); err != nil {
		return skillBundle{}, err
	}
	if skills.IsBuiltin(value) {
		loaded, err := loadResolvedSkill([]skills.Skill{value}, value.Name, skillContentMaxBytes)
		if err != nil {
			return skillBundle{}, err
		}
		data := []byte(loaded.Content)
		frontmatter, err := parseSkillFrontmatter(data)
		if err != nil {
			return skillBundle{}, err
		}
		uri := skillURI(value.Name, "SKILL.md")
		bundle := skillBundle{Skill: value, URI: uri, Frontmatter: frontmatter, Resources: []skillResource{{
			URI: uri, Relative: "SKILL.md", Data: data, Digest: digestBytes(data), MIMEType: "text/markdown",
		}}}
		return validateSkillBundleForProfile(bundle, profile)
	}
	root := filepath.Dir(value.Path)
	entries := []skillResource{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("skill contains symlink: %s", filepath.ToSlash(relative))
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("skill contains non-regular file: %s", filepath.ToSlash(relative))
		}
		relative = filepath.ToSlash(relative)
		if _, err := normalizeSkillRelative(relative); err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		mimeType := mime.TypeByExtension(filepath.Ext(relative))
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		entries = append(entries, skillResource{
			URI: skillURI(value.Name, relative), Relative: relative, Data: data,
			Digest: digestBytes(data), MIMEType: mimeType,
		})
		return nil
	})
	if err != nil {
		return skillBundle{}, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Relative < entries[j].Relative })
	manifestIndex := -1
	for i := range entries {
		if strings.EqualFold(entries[i].Relative, "SKILL.md") {
			manifestIndex = i
			break
		}
	}
	if manifestIndex < 0 {
		return skillBundle{}, errors.New("skill manifest is missing")
	}
	entries[manifestIndex].Relative = "SKILL.md"
	entries[manifestIndex].URI = skillURI(value.Name, "SKILL.md")
	frontmatter, err := parseSkillFrontmatter(entries[manifestIndex].Data)
	if err != nil {
		return skillBundle{}, err
	}
	bundle := skillBundle{
		Skill: value, URI: skillURI(value.Name, "SKILL.md"),
		Frontmatter: frontmatter, Resources: entries,
	}
	return validateSkillBundleForProfile(bundle, profile)
}

func validateSkillURIName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || url.PathEscape(name) != name || strings.ContainsAny(name, "/\\?#%") {
		return errors.New("skill name is not safe for skill URI projection")
	}
	return nil
}

func validateSkillBundleForProfile(bundle skillBundle, profile Profile) (skillBundle, error) {
	if profile == nil || profile.ID() != OpenAIProfileID {
		return bundle, nil
	}
	if !skills.IsBuiltin(bundle.Skill) && filepath.Base(filepath.Dir(bundle.Skill.Path)) != bundle.Skill.Name {
		return skillBundle{}, fmt.Errorf("skill %q directory name must match skill name for OpenAI import", bundle.Skill.Name)
	}
	if len(bundle.Resources) > openAIMaxSkillFiles {
		return skillBundle{}, fmt.Errorf("skill %q exceeds OpenAI file limit", bundle.Skill.Name)
	}
	total := 0
	for _, resource := range bundle.Resources {
		size := len(resource.Data)
		total += size
		if resource.Relative == "SKILL.md" {
			if size > openAIMaxManifestBytes {
				return skillBundle{}, fmt.Errorf("skill %q manifest exceeds OpenAI size limit", bundle.Skill.Name)
			}
		} else if size > openAIMaxSupportBytes {
			return skillBundle{}, fmt.Errorf("skill %q resource %q exceeds OpenAI size limit", bundle.Skill.Name, resource.Relative)
		}
	}
	if total > openAIMaxSkillBytes {
		return skillBundle{}, fmt.Errorf("skill %q exceeds OpenAI total size limit", bundle.Skill.Name)
	}
	return bundle, nil
}

func parseSkillFrontmatter(data []byte) (map[string]any, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return nil, errors.New("skill frontmatter is missing")
	}
	rest := strings.TrimPrefix(text, "---\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, errors.New("skill frontmatter is unclosed")
	}
	frontmatter := map[string]any{}
	if err := yaml.Unmarshal([]byte(rest[:end]), &frontmatter); err != nil {
		return nil, fmt.Errorf("parse skill frontmatter: %w", err)
	}
	name, _ := frontmatter["name"].(string)
	description, _ := frontmatter["description"].(string)
	if strings.TrimSpace(name) == "" || strings.TrimSpace(description) == "" {
		return nil, errors.New("skill frontmatter requires name and description")
	}
	return frontmatter, nil
}

func skillURI(name, relative string) string {
	return skillScheme + "://" + skillServerID + "/" + name + "/" + relative
}

func parseSkillURI(raw string) (string, string, error) {
	prefix := skillScheme + "://" + skillServerID + "/"
	if raw != strings.TrimSpace(raw) || !strings.HasPrefix(raw, prefix) || strings.ContainsAny(raw, "?#\\%") {
		return "", "", errors.New("skill uri is not canonical")
	}
	rest := strings.TrimPrefix(raw, prefix)
	name, relative, ok := strings.Cut(rest, "/")
	if !ok || strings.TrimSpace(name) == "" {
		return "", "", errors.New("skill uri is invalid")
	}
	if _, err := normalizeSkillRelative(relative); err != nil {
		return "", "", err
	}
	return name, relative, nil
}

func normalizeSkillRelative(relative string) (string, error) {
	if relative == "" || strings.HasPrefix(relative, "/") || strings.HasSuffix(relative, "/") || strings.Contains(relative, "\\") {
		return "", errors.New("skill resource path is invalid")
	}
	for _, segment := range strings.Split(relative, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", errors.New("skill resource path is invalid")
		}
	}
	return relative, nil
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (e *FeatureExecutor) readSkillResource(ctx context.Context, rawURI string) (ResourceReadResult, error) {
	name, relative, err := parseSkillURI(rawURI)
	if err != nil {
		return ResourceReadResult{}, NewError(ErrInvalidParams, err.Error())
	}
	values, err := canonicalSkillInventory(ctx, e.Tools, e.BoundWorkspace)
	if err != nil {
		return ResourceReadResult{}, NewError(ErrInternal, err.Error())
	}
	for _, value := range values {
		if value.Name != name {
			continue
		}
		bundle, err := buildSkillBundle(value, e.Profile)
		if err != nil {
			return ResourceReadResult{}, NewError(ErrInvalidParams, err.Error())
		}
		for _, resource := range bundle.Resources {
			if resource.Relative != relative {
				continue
			}
			content := ResourceContent{MIMEType: resource.MIMEType}
			if strings.HasPrefix(resource.MIMEType, "text/") || resource.Relative == "SKILL.md" {
				text := string(resource.Data)
				content.Text = &text
			} else {
				content.Blob = append([]byte(nil), resource.Data...)
			}
			return ResourceReadResult{
				URI: rawURI, Content: content,
				Policy: ResourcePolicy{MaxBytes: maxResourceMaxBytes, Cache: ResourceCachePolicy{Scope: ResourceCacheScopePrivate}},
			}, nil
		}
	}
	return ResourceReadResult{}, ResourceNotFoundError(rawURI)
}

func InstallSkillProjection(server *sdkmcp.Server, executor *FeatureExecutor) error {
	if server == nil || executor == nil {
		return nil
	}
	values, err := canonicalSkillInventory(context.Background(), executor.Tools, executor.BoundWorkspace)
	if err != nil {
		return err
	}
	for _, bundle := range projectedSkillBundles(values, executor.Profile) {
		value := bundle.Skill
		for _, resource := range bundle.Resources {
			resourceURI := resource.URI
			server.AddResource(&sdkmcp.Resource{
				URI: resourceURI, Name: value.Name + ":" + resource.Relative,
				Description: value.Description, MIMEType: resource.MIMEType, Size: int64(len(resource.Data)),
			}, func(ctx context.Context, request *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
				if request == nil || request.Params == nil {
					return nil, featureJSONRPCError(NewError(ErrInvalidParams, "resource read params are required"))
				}
				result, err := executor.ReadResource(ctx, request.Params.URI)
				if err != nil {
					return nil, featureJSONRPCError(err)
				}
				content := &sdkmcp.ResourceContents{URI: result.URI, MIMEType: result.Content.MIMEType}
				if result.Content.Text != nil {
					content.Text = *result.Content.Text
				} else {
					content.Blob = append([]byte(nil), result.Content.Blob...)
				}
				return &sdkmcp.ReadResourceResult{Contents: []*sdkmcp.ResourceContents{content}}, nil
			})
		}
	}
	return nil
}
