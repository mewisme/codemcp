package capability

import (
	"encoding/json"
	"sort"
	"strings"
)

const InventoryVersion = 1

type SurfaceLifecycle struct {
	Surface Surface `json:"surface"`
	Active  bool    `json:"active"`
}

type InventorySnapshot struct {
	Version    int                  `json:"version"`
	Surfaces   []SurfaceLifecycle   `json:"surfaces"`
	Operations []OperationInventory `json:"operations"`
}

type OperationInventory struct {
	ID              ID                 `json:"id"`
	Kind            Kind               `json:"kind"`
	Audience        Audience           `json:"audience"`
	Authorization   AuthorizationClass `json:"authorization"`
	Risk            MutationRisk       `json:"risk"`
	Confirmation    ConfirmationPolicy `json:"confirmation"`
	Effects         SemanticEffects    `json:"effects"`
	CLI             CLIBinding         `json:"cli"`
	Admin           []AdminBinding     `json:"admin"`
	MCPTools        []string           `json:"mcp_tools"`
	PlannedMCPTools []string           `json:"planned_mcp_tools,omitempty"`
	Surfaces        []SurfaceContract  `json:"surfaces"`
}

type InventoryDiagnostics struct {
	Version        int                       `json:"version"`
	Operations     int                       `json:"operations"`
	SurfaceSummary map[Surface]SurfaceCounts `json:"surface_summary"`
}

type SurfaceCounts struct {
	Active   bool `json:"active"`
	Required int  `json:"required"`
	Planned  int  `json:"planned"`
	Exempt   int  `json:"exempt"`
}

var surfaceLifecycles = []SurfaceLifecycle{
	{Surface: SurfaceCLI, Active: true},
	{Surface: SurfaceTUI, Active: true},
	{Surface: SurfaceBrowser, Active: true},
	{Surface: SurfaceAdminAPI, Active: true},
	{Surface: SurfaceMCP, Active: true},
	{Surface: SurfaceTelegram, Active: false},
}

func SurfaceLifecycles() []SurfaceLifecycle {
	out := append([]SurfaceLifecycle(nil), surfaceLifecycles...)
	return out
}

func SurfaceActive(surface Surface) bool {
	for _, item := range surfaceLifecycles {
		if item.Surface == surface {
			return item.Active
		}
	}
	return false
}

func Inventory() InventorySnapshot {
	operations := make([]OperationInventory, 0, len(specs))
	for _, spec := range All() {
		operations = append(operations, OperationInventory{
			ID: spec.ID, Kind: spec.Kind, Audience: spec.Audience, Authorization: spec.Authorization,
			Risk: spec.Risk, Confirmation: spec.Confirmation, Effects: spec.Effects, CLI: spec.CLI,
			Admin: append([]AdminBinding(nil), spec.Admin...), MCPTools: append([]string(nil), spec.MCPTools...),
			PlannedMCPTools: append([]string(nil), spec.PlannedMCPTools...),
			Surfaces:        append([]SurfaceContract(nil), spec.Surfaces...),
		})
	}
	sort.Slice(operations, func(i, j int) bool { return operations[i].ID < operations[j].ID })
	return InventorySnapshot{Version: InventoryVersion, Surfaces: SurfaceLifecycles(), Operations: operations}
}

func InventoryJSON() ([]byte, error) {
	return json.Marshal(Inventory())
}

func Diagnostics() InventoryDiagnostics {
	snapshot := Inventory()
	result := InventoryDiagnostics{
		Version: snapshot.Version, Operations: len(snapshot.Operations),
		SurfaceSummary: make(map[Surface]SurfaceCounts, len(snapshot.Surfaces)),
	}
	for _, lifecycle := range snapshot.Surfaces {
		result.SurfaceSummary[lifecycle.Surface] = SurfaceCounts{Active: lifecycle.Active}
	}
	for _, operation := range snapshot.Operations {
		for _, contract := range operation.Surfaces {
			counts := result.SurfaceSummary[contract.Surface]
			switch contract.State {
			case SurfaceRequired:
				counts.Required++
			case SurfacePlanned:
				counts.Planned++
			case SurfaceExempt:
				counts.Exempt++
			}
			result.SurfaceSummary[contract.Surface] = counts
		}
	}
	return result
}

type SecurityPolicy struct {
	Authorization AuthorizationClass `json:"authorization"`
	Risk          MutationRisk       `json:"risk"`
	Destructive   bool               `json:"destructive"`
	Confirmation  ConfirmationPolicy `json:"confirmation"`
}

func SecurityPolicyFor(id ID) (SecurityPolicy, bool) {
	spec, ok := Lookup(id)
	if !ok {
		return SecurityPolicy{}, false
	}
	return SecurityPolicy{
		Authorization: spec.Authorization, Risk: spec.Risk,
		Destructive: spec.Effects.Destructive, Confirmation: spec.Confirmation,
	}, true
}

type ProtocolProjection struct {
	Operation     ID                 `json:"operation"`
	Profile       string             `json:"profile"`
	Effects       SemanticEffects    `json:"effects"`
	Authorization AuthorizationClass `json:"authorization"`
	Confirmation  ConfirmationPolicy `json:"confirmation"`
	Metadata      map[string]any     `json:"metadata,omitempty"`
}

func ProjectProtocol(id ID, profile string, metadata map[string]any) (ProtocolProjection, bool) {
	spec, ok := Lookup(id)
	if !ok {
		return ProtocolProjection{}, false
	}
	copied := make(map[string]any, len(metadata))
	for key, value := range metadata {
		copied[key] = value
	}
	return ProtocolProjection{
		Operation: id, Profile: strings.TrimSpace(profile), Effects: spec.Effects,
		Authorization: spec.Authorization, Confirmation: spec.Confirmation, Metadata: copied,
	}, true
}
