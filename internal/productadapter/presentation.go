package productadapter

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"go.mewis.me/codemcp/internal/capability"
)

//go:generate go run ../../scripts/generate/product-presentation -out ../../frontend/src/lib/operation-presentation.generated.ts

type ActionCategory string

const (
	ActionCategoryRead   ActionCategory = "read"
	ActionCategoryCreate ActionCategory = "create"
	ActionCategoryChange ActionCategory = "change"
	ActionCategoryDelete ActionCategory = "delete"
	ActionCategoryRun    ActionCategory = "run"
	ActionCategoryReview ActionCategory = "review"
)

type DangerLevel string

const (
	DangerNone        DangerLevel = "none"
	DangerCaution     DangerLevel = "caution"
	DangerDestructive DangerLevel = "destructive"
)

type InputShape string

const (
	InputNone            InputShape = "none"
	InputResource        InputShape = "resource"
	InputForm            InputShape = "form"
	InputSetting         InputShape = "setting"
	InputProtectedSecret InputShape = "protected-secret"
	InputDecision        InputShape = "decision"
)

type LifecycleState string

const (
	LifecycleIdle             LifecycleState = "idle"
	LifecycleConfirming       LifecycleState = "confirming"
	LifecycleWorking          LifecycleState = "working"
	LifecycleSuccess          LifecycleState = "success"
	LifecyclePartial          LifecycleState = "partial"
	LifecycleRetryableFailure LifecycleState = "retryable-failure"
	LifecycleTerminalFailure  LifecycleState = "terminal-failure"
	LifecycleUnavailable      LifecycleState = "unavailable"
)

type LifecyclePresentation struct {
	State     LifecycleState `json:"state"`
	Label     string         `json:"label"`
	Retryable bool           `json:"retryable"`
	Terminal  bool           `json:"terminal"`
}

type NavigationIntent string

const (
	NavigationBack    NavigationIntent = "back"
	NavigationHome    NavigationIntent = "home"
	NavigationRefresh NavigationIntent = "refresh"
	NavigationRetry   NavigationIntent = "retry"
	NavigationCancel  NavigationIntent = "cancel"
	NavigationClose   NavigationIntent = "close"
)

type OperationPresentation struct {
	Operation         capability.ID               `json:"operation"`
	Title             string                      `json:"title"`
	Subject           string                      `json:"subject"`
	Category          ActionCategory              `json:"category"`
	Danger            DangerLevel                 `json:"danger"`
	Confirmation      capability.ConfirmationMode `json:"confirmation"`
	Input             InputShape                  `json:"input"`
	SecretPolicy      SecretPolicy                `json:"secret_policy,omitempty"`
	SecretRecovery    string                      `json:"secret_recovery,omitempty"`
	UnavailableReason string                      `json:"unavailable_reason,omitempty"`
}

func PresentationFor(operation capability.ID, unavailableReason string) (OperationPresentation, bool) {
	spec, ok := capability.Lookup(operation)
	if !ok {
		return OperationPresentation{}, false
	}
	secretPolicy, secretRecovery := SecretContractFor(operation)
	title, subject := operationTitleSubject(spec)
	return OperationPresentation{
		Operation:         operation,
		Title:             title,
		Subject:           subject,
		Category:          actionCategory(spec),
		Danger:            dangerLevel(spec),
		Confirmation:      spec.Confirmation.Mode,
		Input:             inputShape(spec, secretPolicy),
		SecretPolicy:      secretPolicy,
		SecretRecovery:    secretRecovery,
		UnavailableReason: strings.TrimSpace(unavailableReason),
	}, true
}

func Lifecycle(state LifecycleState) (LifecyclePresentation, bool) {
	values := map[LifecycleState]LifecyclePresentation{
		LifecycleIdle:             {State: LifecycleIdle, Label: "Ready"},
		LifecycleConfirming:       {State: LifecycleConfirming, Label: "Confirmation required"},
		LifecycleWorking:          {State: LifecycleWorking, Label: "Working"},
		LifecycleSuccess:          {State: LifecycleSuccess, Label: "Completed", Terminal: true},
		LifecyclePartial:          {State: LifecyclePartial, Label: "Degraded", Terminal: true},
		LifecycleRetryableFailure: {State: LifecycleRetryableFailure, Label: "Failed — retry available", Retryable: true},
		LifecycleTerminalFailure:  {State: LifecycleTerminalFailure, Label: "Failed", Terminal: true},
		LifecycleUnavailable:      {State: LifecycleUnavailable, Label: "Unavailable", Terminal: true},
	}
	value, ok := values[state]
	return value, ok
}

func LifecycleStates() []LifecyclePresentation {
	states := []LifecycleState{
		LifecycleIdle,
		LifecycleConfirming,
		LifecycleWorking,
		LifecycleSuccess,
		LifecyclePartial,
		LifecycleRetryableFailure,
		LifecycleTerminalFailure,
		LifecycleUnavailable,
	}
	result := make([]LifecyclePresentation, 0, len(states))
	for _, state := range states {
		value, _ := Lifecycle(state)
		result = append(result, value)
	}
	return result
}

func NavigationLabel(intent NavigationIntent) string {
	switch intent {
	case NavigationBack:
		return "Back"
	case NavigationHome:
		return "Home"
	case NavigationRefresh:
		return "Refresh"
	case NavigationRetry:
		return "Retry"
	case NavigationCancel:
		return "Cancel"
	case NavigationClose:
		return "Close"
	default:
		return ""
	}
}

func NavigationIntents() []NavigationIntent {
	return []NavigationIntent{NavigationBack, NavigationHome, NavigationRefresh, NavigationRetry, NavigationCancel, NavigationClose}
}

func CanonicalTimestamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func AllOperationPresentation() []OperationPresentation {
	result := make([]OperationPresentation, 0)
	for _, spec := range capability.All() {
		if spec.Audience != capability.AudienceOperator && spec.Audience != capability.AudienceReviewer {
			continue
		}
		value, _ := PresentationFor(spec.ID, "")
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Operation < result[j].Operation })
	return result
}

type GeneratedPresentationContract struct {
	Operations      map[string]OperationPresentation `json:"operations"`
	Lifecycle       map[string]LifecyclePresentation `json:"lifecycle"`
	Navigation      map[string]string                `json:"navigation"`
	TimestampFormat string                           `json:"timestamp_format"`
}

func GeneratedContract() GeneratedPresentationContract {
	operations := map[string]OperationPresentation{}
	for _, value := range AllOperationPresentation() {
		operations[string(value.Operation)] = value
	}
	lifecycle := map[string]LifecyclePresentation{}
	for _, value := range LifecycleStates() {
		lifecycle[string(value.State)] = value
	}
	navigation := map[string]string{}
	for _, intent := range NavigationIntents() {
		navigation[string(intent)] = NavigationLabel(intent)
	}
	return GeneratedPresentationContract{
		Operations:      operations,
		Lifecycle:       lifecycle,
		Navigation:      navigation,
		TimestampFormat: "RFC3339Nano UTC",
	}
}

func GeneratedTypeScript() ([]byte, error) {
	data, err := json.MarshalIndent(GeneratedContract(), "", "  ")
	if err != nil {
		return nil, err
	}
	return []byte("// Code generated by productpresentationgen; DO NOT EDIT.\nexport const canonicalPresentationContract = " + string(data) + " as const\n"), nil
}

func actionCategory(spec capability.Spec) ActionCategory {
	if spec.Audience == capability.AudienceReviewer || spec.Confirmation.Mode == capability.ConfirmationReview {
		return ActionCategoryReview
	}
	if spec.Effects.Destructive || spec.Risk == capability.RiskDestructive {
		return ActionCategoryDelete
	}
	switch spec.Kind {
	case capability.KindQuery, capability.KindStream:
		return ActionCategoryRead
	case capability.KindRuntime:
		return ActionCategoryRun
	}
	action := lastOperationWord(spec)
	switch action {
	case "add", "create", "init", "install", "register", "setup":
		return ActionCategoryCreate
	default:
		return ActionCategoryChange
	}
}

func dangerLevel(spec capability.Spec) DangerLevel {
	if spec.Effects.Destructive || spec.Risk == capability.RiskDestructive {
		return DangerDestructive
	}
	if spec.Risk == capability.RiskSensitive || spec.Kind == capability.KindRuntime || spec.Confirmation.Mode == capability.ConfirmationReview {
		return DangerCaution
	}
	return DangerNone
}

func inputShape(spec capability.Spec, secretPolicy SecretPolicy) InputShape {
	if secretPolicy == SecretPolicyProtectedInput {
		return InputProtectedSecret
	}
	if spec.Confirmation.Mode == capability.ConfirmationReview || spec.Audience == capability.AudienceReviewer {
		return InputDecision
	}
	switch spec.ID {
	case capability.ConfigSet, capability.ConfigPatch:
		return InputSetting
	}
	if spec.Kind == capability.KindQuery || spec.Kind == capability.KindStream {
		if strings.HasSuffix(string(spec.ID), ".get") || strings.HasSuffix(string(spec.ID), ".show") || strings.HasSuffix(string(spec.ID), ".view") {
			return InputResource
		}
		return InputNone
	}
	return InputForm
}

func operationTitleSubject(spec capability.Spec) (string, string) {
	words := strings.Fields(capability.NormalizePath(spec.CLI.CanonicalPath))
	if len(words) == 0 {
		words = strings.Split(string(spec.ID), ".")
	}
	if len(words) == 0 {
		return "Operation", "Operation"
	}
	action := words[len(words)-1]
	subjectWords := words[:len(words)-1]
	if len(subjectWords) == 0 {
		subjectWords = words
	}
	if len(subjectWords) == 2 && strings.EqualFold(subjectWords[0], "auth") {
		subjectWords = []string{subjectWords[1], subjectWords[0]}
	}
	subject := titleWords(subjectWords)
	title := subject
	if len(words) > 1 {
		title = titleWord(action) + " " + subject
	}
	return strings.TrimSpace(title), strings.TrimSpace(subject)
}

func lastOperationWord(spec capability.Spec) string {
	value := capability.NormalizePath(spec.CLI.CanonicalPath)
	if value == "" {
		value = strings.ReplaceAll(string(spec.ID), ".", " ")
	}
	words := strings.Fields(value)
	if len(words) == 0 {
		return ""
	}
	return strings.ToLower(words[len(words)-1])
}

func titleWords(words []string) string {
	values := make([]string, 0, len(words))
	for _, word := range words {
		values = append(values, titleWord(word))
	}
	return strings.Join(values, " ")
}

func titleWord(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	switch strings.ToLower(value) {
	case "api":
		return "API"
	case "cf":
		return "CF"
	case "http":
		return "HTTP"
	case "llm":
		return "LLM"
	case "mcp":
		return "MCP"
	case "rtk":
		return "RTK"
	case "oauth":
		return "OAuth"
	}
	runes := []rune(strings.ReplaceAll(value, "_", " "))
	if len(runes) == 0 {
		return ""
	}
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func ValidatePresentation(value OperationPresentation) error {
	want, ok := PresentationFor(value.Operation, value.UnavailableReason)
	if !ok {
		return fmt.Errorf("presentation references unknown operation %q", value.Operation)
	}
	if value != want {
		return fmt.Errorf("presentation for %s diverges from canonical semantics", value.Operation)
	}
	if value.Danger == DangerDestructive && value.Category != ActionCategoryDelete && value.Category != ActionCategoryReview {
		return fmt.Errorf("destructive operation %s uses ordinary action category %q", value.Operation, value.Category)
	}
	return nil
}
