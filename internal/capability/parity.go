package capability

import "sort"

type SurfaceMapping struct {
	Surface     Surface      `json:"surface"`
	State       SurfaceState `json:"state"`
	Reason      string       `json:"reason,omitempty"`
	EntryPoints []string     `json:"entry_points,omitempty"`
}

type ParityRow struct {
	Operation     ID                 `json:"operation"`
	Kind          Kind               `json:"kind"`
	Authorization AuthorizationClass `json:"authorization"`
	Risk          MutationRisk       `json:"risk"`
	Confirmation  ConfirmationPolicy `json:"confirmation"`
	Effects       SemanticEffects    `json:"effects"`
	Surfaces      []SurfaceMapping   `json:"surfaces"`
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

func ParityMatrix() []ParityRow {
	snapshot := Inventory()
	rows := make([]ParityRow, 0, len(snapshot.Operations))
	for _, operation := range snapshot.Operations {
		row := ParityRow{
			Operation: operation.ID, Kind: operation.Kind, Authorization: operation.Authorization,
			Risk: operation.Risk, Confirmation: operation.Confirmation, Effects: operation.Effects,
		}
		for _, contract := range operation.Surfaces {
			row.Surfaces = append(row.Surfaces, SurfaceMapping{
				Surface: contract.Surface, State: contract.State, Reason: contract.Reason,
				EntryPoints: inventoryEntryPoints(operation, contract.Surface),
			})
		}
		rows = append(rows, row)
	}
	return rows
}

func inventoryEntryPoints(operation OperationInventory, surface Surface) []string {
	switch surface {
	case SurfaceCLI:
		if operation.CLI.CanonicalPath == "" {
			return nil
		}
		return append([]string{operation.CLI.CanonicalPath}, operation.CLI.Aliases...)
	case SurfaceTUI:
		if operation.CLI.CanonicalPath == "" || operation.Audience == AudienceAgent || operation.Audience == AudienceProtocol {
			return nil
		}
		return []string{operation.CLI.CanonicalPath}
	case SurfaceBrowser:
		if !browserRequiredIDs[operation.ID] {
			return nil
		}
		return adminEntryPoints(operation.Admin)
	case SurfaceAdminAPI:
		return adminEntryPoints(operation.Admin)
	case SurfaceMCP:
		return append([]string(nil), operation.MCPTools...)
	default:
		return nil
	}
}

func adminEntryPoints(bindings []AdminBinding) []string {
	out := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		binding = normalizeAdminBinding(binding)
		out = append(out, binding.Method+" "+binding.Path)
	}
	return out
}
