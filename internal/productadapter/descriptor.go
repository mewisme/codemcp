package productadapter

import (
	"fmt"
	"sort"
	"strings"

	"go.mewis.me/codemcp/internal/capability"
)

type State string

const (
	StateLive State = "live"
	StateGap  State = "gap"
)

type EntryKind string

const (
	EntryCommand     EntryKind = "command"
	EntryRoute       EntryKind = "route"
	EntryAction      EntryKind = "action"
	EntryForm        EntryKind = "form"
	EntryAPI         EntryKind = "api"
	EntryCallback    EntryKind = "callback"
	EntryInput       EntryKind = "input"
	EntryMiniAppRead EntryKind = "mini-app-read"
	EntryStream      EntryKind = "stream"
	EntryDispatch    EntryKind = "dispatch"
)

type SecretPolicy string

const (
	SecretPolicyNone           SecretPolicy = ""
	SecretPolicyProtectedInput SecretPolicy = "protected-input"
	SecretPolicyMaskedRead     SecretPolicy = "masked-read"
	SecretPolicyOneTimeOutput  SecretPolicy = "one-time-output"
)

type EntryPoint struct {
	Kind  EntryKind `json:"kind"`
	Value string    `json:"value"`
}

type Descriptor struct {
	Surface              capability.Surface          `json:"surface"`
	Operation            capability.ID               `json:"operation"`
	State                State                       `json:"state"`
	Discovery            []EntryPoint                `json:"discovery,omitempty"`
	Dispatch             []EntryPoint                `json:"dispatch,omitempty"`
	Parent               capability.ID               `json:"parent,omitempty"`
	CanonicalOwner       string                      `json:"canonical_owner"`
	Confirmation         capability.ConfirmationMode `json:"confirmation"`
	ConfirmationConsumed bool                        `json:"confirmation_consumed,omitempty"`
	SecretPolicy         SecretPolicy                `json:"secret_policy,omitempty"`
	SecretRecovery       string                      `json:"secret_recovery,omitempty"`
	Gap                  string                      `json:"gap,omitempty"`
}

func Live(surface capability.Surface, operation capability.ID, discovery, dispatch []EntryPoint) Descriptor {
	spec, _ := capability.Lookup(operation)
	owner, _ := capability.CanonicalOwnerFor(operation)
	secretPolicy, secretRecovery := SecretContractFor(operation)
	return Descriptor{
		Surface:        surface,
		Operation:      operation,
		State:          StateLive,
		Discovery:      append([]EntryPoint(nil), discovery...),
		Dispatch:       append([]EntryPoint(nil), dispatch...),
		CanonicalOwner: owner,
		Confirmation:   spec.Confirmation.Mode,
		SecretPolicy:   secretPolicy,
		SecretRecovery: secretRecovery,
	}
}

func Gap(surface capability.Surface, operation capability.ID, reason string) Descriptor {
	owner, _ := capability.CanonicalOwnerFor(operation)
	spec, _ := capability.Lookup(operation)
	secretPolicy, secretRecovery := SecretContractFor(operation)
	return Descriptor{
		Surface:        surface,
		Operation:      operation,
		State:          StateGap,
		CanonicalOwner: owner,
		Confirmation:   spec.Confirmation.Mode,
		SecretPolicy:   secretPolicy,
		SecretRecovery: secretRecovery,
		Gap:            strings.TrimSpace(reason),
	}
}

func Complete(surface capability.Surface, live []Descriptor) []Descriptor {
	byOperation := make(map[capability.ID]Descriptor, len(live))
	for _, descriptor := range live {
		byOperation[descriptor.Operation] = descriptor
	}
	out := make([]Descriptor, 0)
	for _, operation := range capability.RequiredOperations(surface) {
		spec, ok := capability.Lookup(operation)
		if !ok || (spec.Audience != capability.AudienceOperator && spec.Audience != capability.AudienceReviewer) {
			continue
		}
		if descriptor, ok := byOperation[operation]; ok {
			if descriptor.Parent != "" {
				if parent, parentLive := byOperation[descriptor.Parent]; !parentLive || parent.State != StateLive {
					out = append(out, Gap(surface, operation, "parent list/domain adapter is not production-reachable"))
					continue
				}
			}
			out = append(out, descriptor)
			continue
		}
		out = append(out, Gap(surface, operation, "required product adapter is not yet production-reachable"))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Operation < out[j].Operation })
	return out
}

func Validate(descriptors []Descriptor) error {
	seen := map[string]bool{}
	for _, descriptor := range descriptors {
		key := string(descriptor.Surface) + ":" + string(descriptor.Operation)
		if seen[key] {
			return fmt.Errorf("duplicate product adapter descriptor %s", key)
		}
		seen[key] = true
		if err := ValidateDescriptor(descriptor); err != nil {
			return err
		}
	}
	return nil
}

func ValidateDescriptor(descriptor Descriptor) error {
	spec, ok := capability.Lookup(descriptor.Operation)
	if !ok {
		return fmt.Errorf("%s/%s references unknown operation", descriptor.Surface, descriptor.Operation)
	}
	contract, ok := spec.Surface(descriptor.Surface)
	if !ok || contract.State != capability.SurfaceRequired {
		return fmt.Errorf("%s/%s is not a required product-surface operation", descriptor.Surface, descriptor.Operation)
	}
	owner, ok := capability.CanonicalOwnerFor(descriptor.Operation)
	if !ok || strings.TrimSpace(owner) == "" || descriptor.CanonicalOwner != owner {
		return fmt.Errorf("%s/%s canonical owner=%q want=%q", descriptor.Surface, descriptor.Operation, descriptor.CanonicalOwner, owner)
	}
	if descriptor.Confirmation != spec.Confirmation.Mode {
		return fmt.Errorf("%s/%s confirmation=%q want=%q", descriptor.Surface, descriptor.Operation, descriptor.Confirmation, spec.Confirmation.Mode)
	}
	secretPolicy, secretRecovery := SecretContractFor(descriptor.Operation)
	if descriptor.SecretPolicy != secretPolicy || descriptor.SecretRecovery != secretRecovery {
		return fmt.Errorf("%s/%s secret contract=%q/%q want=%q/%q", descriptor.Surface, descriptor.Operation, descriptor.SecretPolicy, descriptor.SecretRecovery, secretPolicy, secretRecovery)
	}
	switch descriptor.State {
	case StateGap:
		if strings.TrimSpace(descriptor.Gap) == "" {
			return fmt.Errorf("%s/%s gap has no reason", descriptor.Surface, descriptor.Operation)
		}
		if len(descriptor.Discovery) != 0 || len(descriptor.Dispatch) != 0 {
			return fmt.Errorf("%s/%s gap advertises live entry points", descriptor.Surface, descriptor.Operation)
		}
	case StateLive:
		if strings.TrimSpace(descriptor.Gap) != "" {
			return fmt.Errorf("%s/%s live descriptor carries gap reason", descriptor.Surface, descriptor.Operation)
		}
		if len(descriptor.Discovery) == 0 || len(descriptor.Dispatch) == 0 {
			return fmt.Errorf("%s/%s live descriptor lacks discovery or dispatch evidence", descriptor.Surface, descriptor.Operation)
		}
		if !hasDiscoverableEntry(descriptor.Discovery) {
			return fmt.Errorf("%s/%s discovery is only hidden/generic dispatch evidence", descriptor.Surface, descriptor.Operation)
		}
		for _, entry := range append(append([]EntryPoint(nil), descriptor.Discovery...), descriptor.Dispatch...) {
			if strings.TrimSpace(entry.Value) == "" || !validEntryKind(entry.Kind) {
				return fmt.Errorf("%s/%s has invalid adapter entry %#v", descriptor.Surface, descriptor.Operation, entry)
			}
		}
		if spec.Effects.Destructive && spec.Confirmation.Mode != capability.ConfirmationNone && !descriptor.ConfirmationConsumed {
			return fmt.Errorf("%s/%s destructive adapter does not prove confirmation consumption", descriptor.Surface, descriptor.Operation)
		}
	default:
		return fmt.Errorf("%s/%s has invalid descriptor state %q", descriptor.Surface, descriptor.Operation, descriptor.State)
	}
	if descriptor.Parent != "" {
		parent, ok := capability.Lookup(descriptor.Parent)
		if !ok {
			return fmt.Errorf("%s/%s parent %s is unknown", descriptor.Surface, descriptor.Operation, descriptor.Parent)
		}
		parentContract, ok := parent.Surface(descriptor.Surface)
		if !ok || parentContract.State != capability.SurfaceRequired {
			return fmt.Errorf("%s/%s parent %s is not required on the same surface", descriptor.Surface, descriptor.Operation, descriptor.Parent)
		}
	}
	return nil
}

func hasDiscoverableEntry(entries []EntryPoint) bool {
	for _, entry := range entries {
		switch entry.Kind {
		case EntryCommand, EntryRoute, EntryAction, EntryForm, EntryCallback, EntryInput, EntryMiniAppRead:
			return true
		}
	}
	return false
}

func validEntryKind(kind EntryKind) bool {
	switch kind {
	case EntryCommand, EntryRoute, EntryAction, EntryForm, EntryAPI, EntryCallback, EntryInput, EntryMiniAppRead, EntryStream, EntryDispatch:
		return true
	default:
		return false
	}
}

func SecretContractFor(operation capability.ID) (SecretPolicy, string) {
	switch operation {
	case capability.AuthMCPRotate, capability.AuthAdminRotate:
		return SecretPolicyOneTimeOutput, "rotate the credential again if the one-time value is lost"
	case capability.ConfigSet, capability.ConfigPatch,
		capability.LLMProviderCredentialSet,
		capability.TunnelAdminKeySet,
		capability.TelegramSetup,
		capability.TunnelConfigure:
		return SecretPolicyProtectedInput, "replace through protected input; later reads expose configured or masked state only"
	default:
		return SecretPolicyNone, ""
	}
}

func DefaultParent(operation capability.ID) capability.ID {
	value := string(operation)
	for _, suffix := range []string{".show", ".get", ".view"} {
		if !strings.HasSuffix(value, suffix) {
			continue
		}
		candidate := capability.ID(strings.TrimSuffix(value, suffix) + ".list")
		if _, ok := capability.Lookup(candidate); ok {
			return candidate
		}
	}
	return ""
}

func CountGaps(descriptors []Descriptor) int {
	count := 0
	for _, descriptor := range descriptors {
		if descriptor.State == StateGap {
			count++
		}
	}
	return count
}
