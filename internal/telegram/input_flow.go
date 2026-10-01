package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type inputFlowFieldKind string

const (
	inputFlowText      inputFlowFieldKind = "text"
	inputFlowMultiline inputFlowFieldKind = "multiline"
	inputFlowSecret    inputFlowFieldKind = "secret"
	inputFlowEnum      inputFlowFieldKind = "enum"
	inputFlowBool      inputFlowFieldKind = "bool"
	inputFlowJSON      inputFlowFieldKind = "json"
)

type inputFlowOption struct {
	Label       string
	Value       string
	Description string
}

type inputFlowField struct {
	Key          string
	Label        string
	Description  string
	Kind         inputFlowFieldKind
	Required     bool
	Placeholder  string
	Example      string
	Accepted     string
	Options      []inputFlowOption
	Default      string
	DefaultLabel string
	HasDefault   bool
	CanClear     bool
	Secret       bool
	When         func(inputFlowData) bool
	Validate     func(string) error
}

type inputFlowDescriptor struct {
	Title       string
	Description string
	SubmitLabel string
	Fields      []inputFlowField
	Build       func(inputFlowData) (any, error)
}

type inputFlowState struct {
	FieldIndex int
	Values     map[string]string
	Error      string
	Awaiting   bool
	MessageID  int64
}

type inputFlowData struct {
	values map[string]string
	set    map[string]bool
}

func (data inputFlowData) Value(key string) string {
	return data.values[key]
}

func (data inputFlowData) Explicit(key string) (string, bool) {
	return data.values[key], data.set[key]
}

func (ui *Interface) inputFlowDescriptor(ctx context.Context, state ActionState) (inputFlowDescriptor, bool, error) {
	if descriptor, ok, err := ui.llmInputFlow(ctx, state); ok || err != nil {
		return descriptor, ok, err
	}
	if descriptor, ok, err := ui.networkInputFlow(ctx, state); ok || err != nil {
		return descriptor, ok, err
	}
	if descriptor, ok, err := ui.settingsInputFlow(ctx, state); ok || err != nil {
		return descriptor, ok, err
	}
	if descriptor, ok, err := ui.systemInputFlow(ctx, state); ok || err != nil {
		return descriptor, ok, err
	}
	if descriptor, ok := workspaceInputFlow(state); ok {
		return descriptor, true, nil
	}
	return inputFlowDescriptor{}, false, nil
}

func (ui *Interface) beginInputFlow(ctx context.Context, owner ViewOwner, messageID int64, state ActionState) (bool, error) {
	if ui == nil || ui.runtime == nil || messageID <= 0 {
		return false, errors.New("telegram input workflow is unavailable")
	}
	descriptor, ok, err := ui.inputFlowDescriptor(ctx, state)
	if err != nil || !ok {
		return false, err
	}
	state.InputFlow = newInputFlowState(descriptor)
	screen, err := ui.inputFlowScreenWithDescriptor(owner, state, descriptor)
	if err != nil {
		return true, err
	}
	return true, ui.runtime.EditScreen(ctx, owner.ChatID, messageID, screen)
}

func (ui *Interface) inputFlowScreen(ctx context.Context, owner ViewOwner, state ActionState) (Screen, error) {
	descriptor, ok, err := ui.inputFlowDescriptor(ctx, state)
	if err != nil {
		return Screen{}, err
	}
	if !ok {
		return Screen{}, errors.New("telegram input flow is unsupported")
	}
	return ui.inputFlowScreenWithDescriptor(owner, state, descriptor)
}

func (ui *Interface) inputFlowScreenWithDescriptor(owner ViewOwner, state ActionState, descriptor inputFlowDescriptor) (Screen, error) {
	flow := cloneInputFlowState(state.InputFlow)
	if flow == nil {
		flow = newInputFlowState(descriptor)
	}
	flow.FieldIndex = normalizeInputFlowIndex(descriptor, flow, flow.FieldIndex)
	state.InputFlow = flow
	if flow.FieldIndex >= len(descriptor.Fields) {
		return ui.inputFlowReviewScreen(owner, state, descriptor)
	}
	field := descriptor.Fields[flow.FieldIndex]
	data := inputFlowDataFor(descriptor, flow)
	position, total := inputFlowProgress(descriptor, flow, flow.FieldIndex)
	blocks := []RichBlock{
		{Kind: RichHeading, Title: descriptor.Title, Text: descriptor.Description},
		{Kind: RichSection, Title: fmt.Sprintf("Step %d of %d", position, total), Text: field.Label},
		{Kind: RichSection, Title: field.Label, Text: field.Description},
	}
	rows := [][]string{{"Input", stateLabel(field.Required, "Required", "Optional")}}
	if current, hasCurrent := inputFlowCurrentValue(field, data); hasCurrent {
		label := field.DefaultLabel
		if label == "" {
			label = "Current"
		}
		if field.Secret {
			current = stateLabel(strings.TrimSpace(current) != "", "Configured", "Not configured")
		} else {
			current = inputFlowDisplayValue(field, current)
		}
		rows = append(rows, []string{label, compactPresentationValue(current)})
	}
	if field.Accepted != "" {
		rows = append(rows, []string{"Accepted", field.Accepted})
	}
	blocks = append(blocks, FieldsBlock("", rows...))
	if len(field.Options) > 0 {
		items := make([]string, 0, len(field.Options))
		for _, option := range field.Options {
			item := option.Label
			if option.Description != "" {
				item += " — " + option.Description
			}
			items = append(items, item)
		}
		blocks = append(blocks, RichBlock{Kind: RichList, Title: "Accepted values", Items: items})
	} else if field.Example != "" {
		blocks = append(blocks, RichBlock{Kind: RichCode, Title: "Example", Text: field.Example})
	}
	if strings.TrimSpace(flow.Error) != "" {
		blocks = append(blocks, NoticeBlock(ToneFailure, "Invalid value", flow.Error))
	}

	primary := []Button{}
	secondary := []Button{}
	switch field.Kind {
	case inputFlowEnum, inputFlowBool:
		for _, option := range field.Options {
			next := state
			next.InputFlow = cloneInputFlowState(flow)
			next.InputFlow.Values[field.Key] = option.Value
			next.InputFlow.Error = ""
			next.InputFlow.FieldIndex = nextInputFlowIndex(descriptor, next.InputFlow, flow.FieldIndex, 1)
			button, err := ui.stateButton(owner, option.Label, CallbackOpen, next)
			if err != nil {
				return Screen{}, err
			}
			button.Role = ButtonRolePrimary
			primary = append(primary, button)
		}
	default:
		entry := state
		entry.InputFlow = cloneInputFlowState(flow)
		entry.InputFlow.Awaiting = true
		entry.InputFlow.Error = ""
		label := "Enter value"
		if field.HasDefault {
			label = "Edit value"
		}
		button, err := ui.stateButton(owner, label, CallbackOpen, entry)
		if err != nil {
			return Screen{}, err
		}
		button.Role = ButtonRolePrimary
		primary = append(primary, button)
	}

	if field.HasDefault || !field.Required {
		next := state
		next.InputFlow = cloneInputFlowState(flow)
		next.InputFlow.Error = ""
		next.InputFlow.FieldIndex = nextInputFlowIndex(descriptor, next.InputFlow, flow.FieldIndex, 1)
		label := "Skip"
		if field.HasDefault {
			label = "Keep current"
		}
		button, err := ui.stateButton(owner, label, CallbackOpen, next)
		if err != nil {
			return Screen{}, err
		}
		secondary = append(secondary, button)
	}
	if field.CanClear {
		if current, hasCurrent := inputFlowCurrentValue(field, data); hasCurrent && strings.TrimSpace(current) != "" {
			next := state
			next.InputFlow = cloneInputFlowState(flow)
			next.InputFlow.Values[field.Key] = ""
			next.InputFlow.Error = ""
			next.InputFlow.FieldIndex = nextInputFlowIndex(descriptor, next.InputFlow, flow.FieldIndex, 1)
			button, err := ui.stateButton(owner, "Clear", CallbackOpen, next)
			if err != nil {
				return Screen{}, err
			}
			button.Role = ButtonRoleDestructive
			secondary = append(secondary, button)
		}
	}

	navigation := []Button{}
	if previous := nextInputFlowIndex(descriptor, flow, flow.FieldIndex, -1); previous >= 0 {
		backState := state
		backState.InputFlow = cloneInputFlowState(flow)
		backState.InputFlow.FieldIndex = previous
		backState.InputFlow.Error = ""
		back, err := ui.stateButton(owner, previousNavigationLabel("Back"), CallbackBack, backState)
		if err != nil {
			return Screen{}, err
		}
		back.Role = ButtonRoleNavigation
		navigation = append(navigation, back)
	}
	cancel, err := ui.stateButton(owner, "Cancel", CallbackCancel, operationBackState(state))
	if err != nil {
		return Screen{}, err
	}
	cancel.Role = ButtonRoleNavigation
	navigation = append(navigation, cancel)
	return withRouteBreadcrumb(Screen{
		Rich: BuildRichPresentation(blocks...),
		Keyboard: BoundedActionGroups(ActionGroups{
			Primary: primary, Secondary: secondary, Navigation: navigation,
		}),
	}, state), nil
}

func (ui *Interface) inputFlowReviewScreen(owner ViewOwner, state ActionState, descriptor inputFlowDescriptor) (Screen, error) {
	flow := cloneInputFlowState(state.InputFlow)
	data := inputFlowDataFor(descriptor, flow)
	rows := make([][]string, 0, len(descriptor.Fields))
	for _, field := range descriptor.Fields {
		if field.When != nil && !field.When(data) {
			continue
		}
		value, explicit := data.Explicit(field.Key)
		if !explicit {
			value, _ = inputFlowCurrentValue(field, data)
		}
		if field.Secret {
			switch {
			case explicit && value == "":
				value = "Clear"
			case explicit:
				value = "Provided"
			case field.HasDefault && field.Default != "":
				value = "Unchanged"
			default:
				value = "Not set"
			}
		} else if strings.TrimSpace(value) == "" {
			value = "Not set"
		} else {
			value = compactPresentationValue(inputFlowDisplayValue(field, value))
		}
		rows = append(rows, []string{field.Label, value})
	}
	blocks := []RichBlock{
		{Kind: RichHeading, Title: descriptor.Title, Text: "Review before applying"},
		FieldsBlock("Review", rows...),
	}
	input, buildErr := descriptor.Build(data)
	primary := []Button{}
	if buildErr != nil {
		blocks = append(blocks, NoticeBlock(ToneFailure, "Cannot apply", buildErr.Error()))
	} else {
		apply := state
		apply.Input = input
		apply.InputKind = ""
		apply.InputFlow = nil
		label := strings.TrimSpace(descriptor.SubmitLabel)
		if label == "" {
			label = "Apply"
		}
		button, err := ui.stateButton(owner, label, CallbackOpen, apply)
		if err != nil {
			return Screen{}, err
		}
		button.Role = ButtonRolePositive
		primary = append(primary, button)
	}
	navigation := []Button{}
	if previous := nextInputFlowIndex(descriptor, flow, len(descriptor.Fields), -1); previous >= 0 {
		backState := state
		backState.InputFlow = cloneInputFlowState(flow)
		backState.InputFlow.FieldIndex = previous
		backState.InputFlow.Error = ""
		back, err := ui.stateButton(owner, previousNavigationLabel("Back"), CallbackBack, backState)
		if err != nil {
			return Screen{}, err
		}
		back.Role = ButtonRoleNavigation
		navigation = append(navigation, back)
	}
	cancel, err := ui.stateButton(owner, "Cancel", CallbackCancel, operationBackState(state))
	if err != nil {
		return Screen{}, err
	}
	cancel.Role = ButtonRoleNavigation
	navigation = append(navigation, cancel)
	return withRouteBreadcrumb(Screen{
		Rich:     BuildRichPresentation(blocks...),
		Keyboard: BoundedActionGroups(ActionGroups{Primary: primary, Navigation: navigation}),
	}, state), nil
}

func (ui *Interface) beginInputFlowReply(ctx context.Context, owner ViewOwner, flowMessageID int64, state ActionState) error {
	if ui == nil || ui.runtime == nil || ui.inputs == nil || flowMessageID <= 0 || state.InputFlow == nil {
		return errors.New("telegram input workflow is unavailable")
	}
	descriptor, ok, err := ui.inputFlowDescriptor(ctx, state)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("telegram input flow is unsupported")
	}
	flow := cloneInputFlowState(state.InputFlow)
	flow.FieldIndex = normalizeInputFlowIndex(descriptor, flow, flow.FieldIndex)
	if flow.FieldIndex < 0 || flow.FieldIndex >= len(descriptor.Fields) {
		return errors.New("telegram input field is unavailable")
	}
	field := descriptor.Fields[flow.FieldIndex]
	if field.Kind == inputFlowEnum || field.Kind == inputFlowBool {
		return errors.New("telegram input field does not accept text")
	}
	flow.Awaiting = false
	flow.MessageID = flowMessageID
	state.InputFlow = flow
	prompt := Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: field.Label},
		RichBlock{Kind: RichSection, Title: "Reply with the value", Text: field.Description},
	)}
	if field.Example != "" {
		prompt.Rich.Blocks = append(prompt.Rich.Blocks, RichBlock{Kind: RichCode, Title: "Example", Text: field.Example})
	}
	placeholder := field.Placeholder
	if placeholder == "" {
		placeholder = field.Label
	}
	promptID, err := ui.runtime.SendRichMessage(ctx, owner.ChatID, prompt, RichMessageOptions{
		ForceReplyPlaceholder: placeholder,
		ProtectContent:        field.Secret,
	})
	if err != nil {
		return err
	}
	if err := ui.inputs.PutAction(owner, promptID, field.Secret, state); err != nil {
		_ = ui.runtime.DeleteMessage(ctx, owner.ChatID, promptID)
		return err
	}
	waiting := withRouteBreadcrumb(Screen{Rich: BuildRichPresentation(
		RichBlock{Kind: RichHeading, Title: descriptor.Title},
		RichBlock{Kind: RichSection, Title: field.Label, Text: field.Description},
		StateBlock(TonePending, "Waiting for reply", "Reply to the input message sent below."),
	)}, state)
	return ui.runtime.EditScreen(ctx, owner.ChatID, flowMessageID, waiting)
}

func (ui *Interface) handleInputFlowReply(ctx context.Context, owner ViewOwner, pending PendingInput, message Message, value PendingInputValue) error {
	if pending.Action == nil || pending.Action.InputFlow == nil {
		return errors.New("telegram input flow state is unavailable")
	}
	state := *pending.Action
	descriptor, ok, err := ui.inputFlowDescriptor(ctx, state)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("telegram input flow is unsupported")
	}
	flow := cloneInputFlowState(state.InputFlow)
	flow.FieldIndex = normalizeInputFlowIndex(descriptor, flow, flow.FieldIndex)
	if flow.FieldIndex < 0 || flow.FieldIndex >= len(descriptor.Fields) {
		return errors.New("telegram input field is unavailable")
	}
	field := descriptor.Fields[flow.FieldIndex]
	text := strings.TrimSpace(value.Text)
	validationErr := validateInputFlowField(field, text)
	flow.Awaiting = false
	flow.Error = ""
	if validationErr != nil {
		flow.Error = validationErr.Error()
	} else {
		if flow.Values == nil {
			flow.Values = map[string]string{}
		}
		flow.Values[field.Key] = text
		flow.FieldIndex = nextInputFlowIndex(descriptor, flow, flow.FieldIndex, 1)
	}
	flowMessageID := flow.MessageID
	flow.MessageID = 0
	state.InputFlow = flow
	_ = ui.runtime.DeleteMessage(ctx, owner.ChatID, pending.PromptMessageID)
	if field.Secret && message.MessageID > 0 {
		_ = ui.runtime.DeleteMessage(ctx, owner.ChatID, message.MessageID)
	}
	if flowMessageID <= 0 {
		return errors.New("telegram input flow message is unavailable")
	}
	screen, screenErr := ui.inputFlowScreenWithDescriptor(owner, state, descriptor)
	if screenErr != nil {
		return screenErr
	}
	return ui.replaceInputFlowRoot(ctx, owner, flowMessageID, screen)
}

func (ui *Interface) failInputFlowReply(ctx context.Context, owner ViewOwner, pending PendingInput, inputErr error) error {
	if pending.Action == nil || pending.Action.InputFlow == nil {
		return errors.New("telegram input flow state is unavailable")
	}
	state := *pending.Action
	descriptor, ok, err := ui.inputFlowDescriptor(ctx, state)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("telegram input flow is unsupported")
	}
	flow := cloneInputFlowState(state.InputFlow)
	flow.Awaiting = false
	flow.Error = compactPresentationValue(inputErr.Error())
	flowMessageID := flow.MessageID
	flow.MessageID = 0
	state.InputFlow = flow
	_ = ui.runtime.DeleteMessage(ctx, owner.ChatID, pending.PromptMessageID)
	if flowMessageID <= 0 {
		return errors.New("telegram input flow message is unavailable")
	}
	screen, err := ui.inputFlowScreenWithDescriptor(owner, state, descriptor)
	if err != nil {
		return err
	}
	return ui.replaceInputFlowRoot(ctx, owner, flowMessageID, screen)
}

func (ui *Interface) replaceInputFlowRoot(ctx context.Context, owner ViewOwner, oldMessageID int64, screen Screen) error {
	if ui == nil || ui.runtime == nil || oldMessageID <= 0 {
		return errors.New("telegram input flow message is unavailable")
	}
	// Send first so a transient transport failure leaves the existing flow visible.
	if err := ui.runtime.SendScreen(ctx, owner.ChatID, screen); err != nil {
		return err
	}
	_ = ui.runtime.DeleteMessage(ctx, owner.ChatID, oldMessageID)
	return nil
}

func newInputFlowState(descriptor inputFlowDescriptor) *inputFlowState {
	flow := &inputFlowState{Values: map[string]string{}}
	flow.FieldIndex = nextInputFlowIndex(descriptor, flow, -1, 1)
	return flow
}

func cloneInputFlowState(value *inputFlowState) *inputFlowState {
	if value == nil {
		return nil
	}
	clone := *value
	clone.Values = make(map[string]string, len(value.Values))
	for key, item := range value.Values {
		clone.Values[key] = item
	}
	return &clone
}

func inputFlowDataFor(descriptor inputFlowDescriptor, flow *inputFlowState) inputFlowData {
	data := inputFlowData{values: map[string]string{}, set: map[string]bool{}}
	for _, field := range descriptor.Fields {
		if field.HasDefault {
			data.values[field.Key] = field.Default
		}
	}
	if flow != nil {
		for key, value := range flow.Values {
			data.values[key] = value
			data.set[key] = true
		}
	}
	return data
}

func inputFlowCurrentValue(field inputFlowField, data inputFlowData) (string, bool) {
	value, ok := data.values[field.Key]
	if ok {
		return value, true
	}
	return "", false
}

func inputFlowDisplayValue(field inputFlowField, value string) string {
	for _, option := range field.Options {
		if option.Value == value {
			return option.Label
		}
	}
	return value
}

func normalizeInputFlowIndex(descriptor inputFlowDescriptor, flow *inputFlowState, index int) int {
	if index >= len(descriptor.Fields) {
		return len(descriptor.Fields)
	}
	if index < 0 {
		return nextInputFlowIndex(descriptor, flow, -1, 1)
	}
	data := inputFlowDataFor(descriptor, flow)
	if descriptor.Fields[index].When == nil || descriptor.Fields[index].When(data) {
		return index
	}
	return nextInputFlowIndex(descriptor, flow, index, 1)
}

func nextInputFlowIndex(descriptor inputFlowDescriptor, flow *inputFlowState, from, direction int) int {
	if direction == 0 {
		direction = 1
	}
	data := inputFlowDataFor(descriptor, flow)
	for index := from + direction; index >= 0 && index < len(descriptor.Fields); index += direction {
		field := descriptor.Fields[index]
		if field.When == nil || field.When(data) {
			return index
		}
	}
	if direction > 0 {
		return len(descriptor.Fields)
	}
	return -1
}

func inputFlowProgress(descriptor inputFlowDescriptor, flow *inputFlowState, current int) (int, int) {
	data := inputFlowDataFor(descriptor, flow)
	total, position := 0, 0
	for index, field := range descriptor.Fields {
		if field.When != nil && !field.When(data) {
			continue
		}
		total++
		if index <= current {
			position++
		}
	}
	if total == 0 {
		total = 1
	}
	if position == 0 {
		position = 1
	}
	return position, total
}

func validateInputFlowField(field inputFlowField, value string) error {
	value = strings.TrimSpace(value)
	if field.Required && value == "" {
		return fmt.Errorf("%s is required", field.Label)
	}
	if value == "" {
		return nil
	}
	if field.Validate != nil {
		return field.Validate(value)
	}
	return nil
}

func inputFlowList(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '\n' || r == ',' })
	result := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		result = append(result, part)
	}
	return result
}

func inputFlowLines(value string) []string {
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" {
			result = append(result, line)
		}
	}
	return result
}

func inputFlowListText(values []string) string {
	return strings.Join(values, "\n")
}

func inputFlowBoolValue(value string) (bool, error) {
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return false, fmt.Errorf("expected true or false")
	}
	return parsed, nil
}

func inputFlowIntValue(value string, min, max int) (int, error) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, errors.New("expected an integer")
	}
	if min != 0 && parsed < min {
		return 0, fmt.Errorf("must be at least %d", min)
	}
	if max != 0 && parsed > max {
		return 0, fmt.Errorf("must be at most %d", max)
	}
	return parsed, nil
}

func inputFlowJSONValidator(target func() any) func(string) error {
	return func(value string) error {
		if strings.TrimSpace(value) == "" {
			return nil
		}
		if err := json.Unmarshal([]byte(value), target()); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		return nil
	}
}

func inputFlowBoolOptions() []inputFlowOption {
	return []inputFlowOption{
		{Label: "Enabled", Value: "true"},
		{Label: "Disabled", Value: "false"},
	}
}
