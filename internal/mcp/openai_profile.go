package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"go.mewis.me/codemcp/internal/tools"
)

const (
	OpenAIProfileID ProfileID = "openai"

	openAIInstructionHeading  = "For project work, select a concrete ws_* workspace and call project_context with memory enabled before substantial work; CodeMCP approval remains authoritative."
	openAILocaleMetaKey       = "openai/locale"
	openAILegacyLocaleMetaKey = "webplus/i18n"
	openAIUserAgentMetaKey    = "openai/userAgent"
	openAIUserLocationMetaKey = "openai/userLocation"
	openAISubjectMetaKey      = "openai/subject"
	openAISessionMetaKey      = "openai/session"
	openAIOrganizationMetaKey = "openai/organization"
	maxOpenAIHintRunes        = 512
)

type openAIProfile struct{ baseProfile }

type requestMetadataProjection struct {
	Client      tools.ClientHints
	Correlation tools.RequestCorrelationHints
}

type requestMetadataProfile interface {
	ProjectRequestMetadata(map[string]any) requestMetadataProjection
}

func OpenAIProfile() Profile { return openAIProfile{baseProfile: baseProfile{}} }

func ResolveProfile(value string) (Profile, error) {
	switch ProfileID(strings.ToLower(strings.TrimSpace(value))) {
	case "", BaseProfileID:
		return BaseProfile(), nil
	case OpenAIProfileID:
		return OpenAIProfile(), nil
	default:
		return nil, fmt.Errorf("unknown MCP profile %q", strings.TrimSpace(value))
	}
}

func (openAIProfile) ID() ProfileID { return OpenAIProfileID }

func (profile openAIProfile) ToolRepresentation(tool ToolDescriptor) ToolRepresentation {
	representation := profile.baseProfile.ToolRepresentation(tool)
	title := strings.TrimSpace(representation.Title)
	if title == "" {
		title = strings.TrimSpace(tool.Name)
	}
	representation.Meta = map[string]any{
		"openai/toolInvocation/invoking": openAIInvocationStatus("Running", title),
		"openai/toolInvocation/invoked":  openAIInvocationStatus("Finished", title),
	}
	return representation
}

func (openAIProfile) InstructionPresentation() InstructionPresentation {
	return InstructionPresentation{Heading: openAIInstructionHeading}
}

func (openAIProfile) ClientCertificateAuthentication() bool { return true }

func (openAIProfile) ProjectRequestMetadata(meta map[string]any) requestMetadataProjection {
	locale := openAIHintString(meta[openAILocaleMetaKey])
	if locale == "" {
		locale = openAIHintString(meta[openAILegacyLocaleMetaKey])
	}
	projection := requestMetadataProjection{
		Client: tools.ClientHints{
			Locale:    locale,
			UserAgent: openAIHintString(meta[openAIUserAgentMetaKey]),
		},
		Correlation: tools.RequestCorrelationHints{
			SubjectID:      openAIHintString(meta[openAISubjectMetaKey]),
			SessionID:      openAIHintString(meta[openAISessionMetaKey]),
			OrganizationID: openAIHintString(meta[openAIOrganizationMetaKey]),
		},
	}
	if location, ok := meta[openAIUserLocationMetaKey].(map[string]any); ok {
		projection.Client.Location = tools.ClientLocationHint{
			City:      openAIHintString(location["city"]),
			Region:    openAIHintString(location["region"]),
			Country:   openAIHintString(location["country"]),
			Timezone:  openAIHintString(location["timezone"]),
			Longitude: openAICoordinate(location["longitude"], -180, 180),
			Latitude:  openAICoordinate(location["latitude"], -90, 90),
		}
	}
	return projection
}

func withProfileRequestMetadata(ctx context.Context, profile Profile, meta map[string]any) context.Context {
	provider, ok := profile.(requestMetadataProfile)
	if !ok {
		return ctx
	}
	projection := provider.ProjectRequestMetadata(meta)
	ctx = tools.WithClientHints(ctx, projection.Client)
	return tools.WithRequestCorrelationHints(ctx, projection.Correlation)
}

func openAIInvocationStatus(action, title string) string {
	value := strings.TrimSpace(strings.TrimSpace(action) + " " + strings.TrimSpace(title))
	runes := []rune(value)
	if len(runes) <= 64 {
		return value
	}
	return string(runes[:63]) + "…"
}

func openAIHintString(value any) string {
	text, _ := value.(string)
	runes := []rune(strings.TrimSpace(text))
	if len(runes) > maxOpenAIHintRunes {
		runes = runes[:maxOpenAIHintRunes]
	}
	return string(runes)
}

func openAICoordinate(value any, minimum, maximum float64) *float64 {
	var number float64
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return nil
		}
		number = parsed
	case float64:
		number = typed
	case float32:
		number = float64(typed)
	case int:
		number = float64(typed)
	case int64:
		number = float64(typed)
	default:
		return nil
	}
	if math.IsNaN(number) || math.IsInf(number, 0) || number < minimum || number > maximum {
		return nil
	}
	return &number
}
