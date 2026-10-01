package capability

import (
	"encoding/json"
	"sort"
	"strings"
)

const ProductParityReportVersion = 1

type SurfaceMapping struct {
	Surface         Surface               `json:"surface"`
	State           SurfaceState          `json:"state"`
	Exemption       SurfaceExemptionClass `json:"exemption,omitempty"`
	Reason          string                `json:"reason,omitempty"`
	ExemptionOwner  string                `json:"exemption_owner,omitempty"`
	Guard           SurfaceExemptionGuard `json:"guard,omitempty"`
	SafeAlternative string                `json:"safe_alternative,omitempty"`
	EntryPoints     []string              `json:"entry_points,omitempty"`
	Reachable       bool                  `json:"reachable"`
	Gap             string                `json:"gap,omitempty"`
}

type ParityRow struct {
	Operation      ID                 `json:"operation"`
	Kind           Kind               `json:"kind"`
	Audience       Audience           `json:"audience"`
	CanonicalOwner string             `json:"canonical_owner"`
	Authorization  AuthorizationClass `json:"authorization"`
	Risk           MutationRisk       `json:"risk"`
	Confirmation   ConfirmationPolicy `json:"confirmation"`
	Effects        SemanticEffects    `json:"effects"`
	Surfaces       []SurfaceMapping   `json:"surfaces"`
}

type ProductParityReport struct {
	Version    int         `json:"version"`
	Surfaces   []Surface   `json:"surfaces"`
	Operations []ParityRow `json:"operations"`
}

func RequiredOperations(surface Surface) []ID {
	var ids []ID
	for _, spec := range specs {
		if contract, ok := spec.Surface(surface); ok && contract.State == SurfaceRequired {
			ids = append(ids, spec.ID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// ParityMatrix returns canonical product-surface mappings for every operation.
// ProductParityReportSnapshot filters this inventory to operator/reviewer
// operations, which are the strict product parity contract.
func ParityMatrix() []ParityRow {
	snapshot := Inventory()
	rows := make([]ParityRow, 0, len(snapshot.Operations))
	for _, operation := range snapshot.Operations {
		owner, _ := CanonicalOwnerFor(operation.ID)
		row := ParityRow{
			Operation: operation.ID, Kind: operation.Kind, Audience: operation.Audience, CanonicalOwner: owner,
			Authorization: operation.Authorization, Risk: operation.Risk,
			Confirmation: operation.Confirmation, Effects: operation.Effects,
		}
		for _, contract := range operation.Surfaces {
			entries, reachable := productAdapterEvidence(operation, contract.Surface)
			mapping := SurfaceMapping{
				Surface: contract.Surface, State: contract.State, Exemption: contract.Exemption, Reason: contract.Reason,
				ExemptionOwner: contract.ExemptionOwner, Guard: contract.Guard, SafeAlternative: contract.SafeAlternative,
				EntryPoints: entries, Reachable: reachable,
			}
			if contract.State == SurfaceRequired && !reachable {
				mapping.Gap = "required product adapter is not yet production-reachable"
			}
			row.Surfaces = append(row.Surfaces, mapping)
		}
		rows = append(rows, row)
	}
	return rows
}

func ProductParityReportSnapshot() ProductParityReport {
	rows := ParityMatrix()
	operations := make([]ParityRow, 0, len(rows))
	for _, row := range rows {
		if row.Audience != AudienceOperator && row.Audience != AudienceReviewer {
			continue
		}
		operations = append(operations, row)
	}
	return ProductParityReport{
		Version:    ProductParityReportVersion,
		Surfaces:   append([]Surface(nil), ProductSurfaces...),
		Operations: operations,
	}
}

func ProductParityReportJSON() ([]byte, error) {
	return json.Marshal(ProductParityReportSnapshot())
}

func productAdapterEvidence(operation OperationInventory, surface Surface) ([]string, bool) {
	switch surface {
	case SurfaceCLI:
		if operation.CLI.CanonicalPath == "" {
			return nil, false
		}
		entries := append([]string{operation.CLI.CanonicalPath}, operation.CLI.Aliases...)
		return entries, true
	case SurfaceTUI:
		if entries := tuiInventoryEntryPoints[operation.ID]; len(entries) > 0 {
			return append([]string(nil), entries...), true
		}
		if operation.CLI.CanonicalPath == "" ||
			operation.Audience == AudienceAgent || operation.Audience == AudienceProtocol {
			return nil, false
		}
		return []string{"command-backed action " + operation.CLI.CanonicalPath}, true
	case SurfaceBrowser:
		if !browserFrontendOperationIDs[operation.ID] {
			return nil, false
		}
		return []string{"frontend operation " + string(operation.ID)}, true
	case SurfaceTelegram:
		return telegramProductAdapterEvidence(operation.ID)
	default:
		return nil, false
	}
}

var tuiInventoryEntryPoints = map[ID][]string{
	RequestExplanationView: {"tui requests explanation"},
}

func telegramProductAdapterEvidence(id ID) ([]string, bool) {
	for _, item := range TelegramRolloutInventory() {
		if item.Operation != id || item.State != TelegramRolloutLive {
			continue
		}
		out := make([]string, 0, len(item.EntryPoints))
		for _, entry := range item.EntryPoints {
			out = append(out, string(entry.Kind)+" "+entry.Value)
		}
		// The rollout inventory owns completion state; concrete discoverability and
		// dispatch are independently enforced by Telegram product-adapter tests.
		return out, TelegramAdapterOperationLive(id) && len(out) > 0
	}
	return nil, false
}

func parityMapping(report ProductParityReport, id ID, surface Surface) (SurfaceMapping, bool) {
	for _, row := range report.Operations {
		if row.Operation != id {
			continue
		}
		for _, mapping := range row.Surfaces {
			if mapping.Surface == surface {
				return mapping, true
			}
		}
	}
	return SurfaceMapping{}, false
}

func reportHasUnclassifiedSurface(report ProductParityReport) bool {
	for _, row := range report.Operations {
		if strings.TrimSpace(row.CanonicalOwner) == "" || len(row.Surfaces) != len(ProductSurfaces) {
			return true
		}
		for _, mapping := range row.Surfaces {
			switch mapping.State {
			case SurfaceRequired:
				if mapping.Exemption != "" || mapping.Reason != "" || mapping.ExemptionOwner != "" || mapping.Guard != "" {
					return true
				}
			case SurfaceExempt:
				if !validSurfaceExemption(mapping.Exemption) || !validSurfaceReason(mapping.Reason) ||
					strings.TrimSpace(mapping.ExemptionOwner) == "" || !validSurfaceExemptionGuard(mapping.Guard) {
					return true
				}
			default:
				return true
			}
		}
	}
	return false
}
