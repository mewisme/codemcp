package capability

import "strings"

type ID string

type Kind string

const (
	KindQuery    Kind = "query"
	KindMutation Kind = "mutation"
	KindRuntime  Kind = "runtime"
	KindStream   Kind = "stream"
	KindProtocol Kind = "protocol"
)

type Audience string

const (
	AudienceOperator Audience = "operator"
	AudienceReviewer Audience = "reviewer"
	AudienceAgent    Audience = "agent"
	AudienceProtocol Audience = "protocol"
)

type AuthorizationClass string

const (
	AuthorizationOperator AuthorizationClass = "operator"
	AuthorizationReviewer AuthorizationClass = "reviewer"
	AuthorizationAgent    AuthorizationClass = "agent"
	AuthorizationProtocol AuthorizationClass = "protocol"
)

type MutationRisk string

const (
	RiskNone        MutationRisk = "none"
	RiskState       MutationRisk = "state"
	RiskSensitive   MutationRisk = "sensitive"
	RiskDestructive MutationRisk = "destructive"
)

type ConfirmationMode string

const (
	ConfirmationNone        ConfirmationMode = "none"
	ConfirmationRecommended ConfirmationMode = "recommended"
	ConfirmationRequired    ConfirmationMode = "required"
	ConfirmationReview      ConfirmationMode = "review-decision"
)

type ConfirmationPolicy struct {
	Mode            ConfirmationMode `json:"mode"`
	ControlApproval bool             `json:"control_approval"`
}

type SemanticEffects struct {
	Key         string `json:"key"`
	ReadOnly    bool   `json:"read_only"`
	Destructive bool   `json:"destructive"`
	Idempotent  bool   `json:"idempotent"`
	OpenWorld   bool   `json:"open_world"`
}

type Surface string

const (
	SurfaceCLI      Surface = "cli"
	SurfaceTUI      Surface = "tui"
	SurfaceBrowser  Surface = "browser"
	SurfaceAdminAPI Surface = "admin-api"
	SurfaceMCP      Surface = "mcp"
	SurfaceTelegram Surface = "telegram"
)

// ProductSurfaces is the strict operator/reviewer parity boundary. Admin API is
// Browser transport and MCP is an agent/protocol projection, not a fifth or
// sixth product interface.
var ProductSurfaces = []Surface{SurfaceCLI, SurfaceTUI, SurfaceBrowser, SurfaceTelegram}

// AllSurfaces is kept as the canonical product-surface iteration set.
var AllSurfaces = ProductSurfaces

var TransportSurfaces = []Surface{SurfaceAdminAPI, SurfaceMCP}

type SurfaceState string

const (
	SurfaceRequired SurfaceState = "required"
	SurfacePlanned  SurfaceState = "planned"
	SurfaceExempt   SurfaceState = "exempt"
)

type SurfaceExemptionClass string

const (
	SurfaceExemptionProtocolOnly        SurfaceExemptionClass = "protocol-only"
	SurfaceExemptionSurfaceBootstrap    SurfaceExemptionClass = "surface-bootstrap"
	SurfaceExemptionHostLocalPrimitive  SurfaceExemptionClass = "host-local-primitive"
	SurfaceExemptionRemovedArchitecture SurfaceExemptionClass = "removed-architecture"
)

type SurfaceExemptionGuard string

const (
	SurfaceGuardProtocolAudience    SurfaceExemptionGuard = "protocol-audience"
	SurfaceGuardBootstrapOperation  SurfaceExemptionGuard = "bootstrap-operation"
	SurfaceGuardHostLocalOperation  SurfaceExemptionGuard = "host-local-operation"
	SurfaceGuardRemovedArchitecture SurfaceExemptionGuard = "removed-architecture"
)

type SurfaceContract struct {
	Surface         Surface               `json:"surface"`
	State           SurfaceState          `json:"state"`
	Exemption       SurfaceExemptionClass `json:"exemption,omitempty"`
	Reason          string                `json:"reason,omitempty"`
	ExemptionOwner  string                `json:"exemption_owner,omitempty"`
	Guard           SurfaceExemptionGuard `json:"guard,omitempty"`
	SafeAlternative string                `json:"safe_alternative,omitempty"`
}

type CLIBinding struct {
	CanonicalPath string   `json:"canonical_path,omitempty"`
	Aliases       []string `json:"aliases,omitempty"`
}

type AdminBinding struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

type Spec struct {
	ID              ID
	Kind            Kind
	Audience        Audience
	Authorization   AuthorizationClass
	Risk            MutationRisk
	Confirmation    ConfirmationPolicy
	Effects         SemanticEffects
	CLI             CLIBinding
	Admin           []AdminBinding
	MCPTools        []string
	PlannedMCPTools []string
	Surfaces        []SurfaceContract
}

func (spec Spec) Surface(surface Surface) (SurfaceContract, bool) {
	for _, contract := range spec.Surfaces {
		if contract.Surface == surface {
			return contract, true
		}
	}
	return SurfaceContract{}, false
}

func (spec Spec) HasCLI() bool {
	return NormalizePath(spec.CLI.CanonicalPath) != ""
}

func (spec Spec) CLIPaths() []string {
	if !spec.HasCLI() {
		return nil
	}
	paths := make([]string, 0, 1+len(spec.CLI.Aliases))
	paths = append(paths, spec.CLI.CanonicalPath)
	paths = append(paths, spec.CLI.Aliases...)
	return paths
}

func normalizeAdminBinding(binding AdminBinding) AdminBinding {
	binding.Method = strings.ToUpper(strings.TrimSpace(binding.Method))
	binding.Path = "/" + strings.Trim(strings.TrimSpace(binding.Path), "/")
	return binding
}
