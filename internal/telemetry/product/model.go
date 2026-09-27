package product

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	SchemaVersion   = 1
	MaxBatchEvents  = 100
	MaxBodyBytes    = 64 * 1024
	MaxStringLength = 128
	MaxDurationMS   = int64(30 * 24 * 60 * 60 * 1000)
)

var tokenPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

type EventName string

const (
	EventOperationCompleted            EventName = "operation.completed"
	EventRuntimeStarted                EventName = "runtime.started"
	EventRuntimeStopped                EventName = "runtime.stopped"
	EventApprovalRequested             EventName = "approval.requested"
	EventApprovalResolved              EventName = "approval.resolved"
	EventBackgroundCompleted           EventName = "background.completed"
	EventInstallCompleted              EventName = "install.completed"
	EventIntegrationBootstrapCompleted EventName = "integration.bootstrap.completed"
)

type Interface string

const (
	InterfaceCLI      Interface = "cli"
	InterfaceTUI      Interface = "tui"
	InterfaceAdmin    Interface = "admin"
	InterfaceTelegram Interface = "telegram"
	InterfaceMCP      Interface = "mcp"
	InterfaceRuntime  Interface = "runtime"
)

type ErrorCode string

const (
	ErrorCancelled    ErrorCode = "cancelled"
	ErrorInvalidInput ErrorCode = "invalid_input"
	ErrorUnauthorized ErrorCode = "unauthorized"
	ErrorNotFound     ErrorCode = "not_found"
	ErrorUnavailable  ErrorCode = "unavailable"
	ErrorTimeout      ErrorCode = "timeout"
	ErrorNetwork      ErrorCode = "network"
	ErrorConflict     ErrorCode = "conflict"
	ErrorInternal     ErrorCode = "internal"
)

type Event struct {
	Name        EventName `json:"name"`
	AnonymousID string    `json:"anonymous_id"`
	Version     string    `json:"version,omitempty"`
	OS          string    `json:"os,omitempty"`
	Arch        string    `json:"arch,omitempty"`
	Interface   Interface `json:"interface,omitempty"`
	Command     string    `json:"command,omitempty"`
	Feature     string    `json:"feature,omitempty"`
	ErrorCode   ErrorCode `json:"error_code,omitempty"`
	DurationMS  *int64    `json:"duration_ms,omitempty"`
	Success     *bool     `json:"success,omitempty"`
}

type EventFields struct {
	Interface  Interface
	Command    string
	Feature    string
	ErrorCode  ErrorCode
	DurationMS *int64
	Success    *bool
}

type ClientFields struct {
	AnonymousID string
	Version     string
	OS          string
	Arch        string
}

type Batch struct {
	Schema int     `json:"schema"`
	Events []Event `json:"events"`
}

func NewEvent(name EventName, client ClientFields, fields EventFields) (Event, error) {
	event := Event{
		Name: name, AnonymousID: strings.TrimSpace(client.AnonymousID),
		Version: strings.TrimSpace(client.Version), OS: strings.TrimSpace(client.OS), Arch: strings.TrimSpace(client.Arch),
		Interface: fields.Interface, Command: strings.TrimSpace(fields.Command), Feature: strings.TrimSpace(fields.Feature),
		ErrorCode: fields.ErrorCode, DurationMS: fields.DurationMS, Success: fields.Success,
	}
	if err := event.Validate(); err != nil {
		return Event{}, err
	}
	return event, nil
}

func (event Event) Validate() error {
	if !validEventName(event.Name) {
		return fmt.Errorf("unsupported telemetry event name %q", event.Name)
	}
	if !validUUID(event.AnonymousID) {
		return errors.New("anonymous_id must be a UUID")
	}
	for name, value := range map[string]string{
		"version": event.Version, "os": event.OS, "arch": event.Arch,
	} {
		if len(value) > MaxStringLength {
			return fmt.Errorf("%s exceeds 128 bytes", name)
		}
		if value != "" && !tokenPattern.MatchString(value) {
			return fmt.Errorf("invalid telemetry %s %q", name, value)
		}
	}
	if event.Interface != "" && !validInterface(event.Interface) {
		return fmt.Errorf("unsupported telemetry interface %q", event.Interface)
	}
	if event.Command != "" && !tokenPattern.MatchString(event.Command) {
		return fmt.Errorf("invalid telemetry command %q", event.Command)
	}
	if event.Feature != "" && !tokenPattern.MatchString(event.Feature) {
		return fmt.Errorf("invalid telemetry feature %q", event.Feature)
	}
	if event.ErrorCode != "" && !validErrorCode(event.ErrorCode) {
		return fmt.Errorf("unsupported telemetry error code %q", event.ErrorCode)
	}
	if event.DurationMS != nil && (*event.DurationMS < 0 || *event.DurationMS > MaxDurationMS) {
		return fmt.Errorf("duration_ms must be between 0 and %d", MaxDurationMS)
	}
	return nil
}

func NewBatch(events []Event) (Batch, error) {
	if len(events) == 0 || len(events) > MaxBatchEvents {
		return Batch{}, fmt.Errorf("telemetry batch must contain between 1 and %d events", MaxBatchEvents)
	}
	copied := append([]Event(nil), events...)
	for _, event := range copied {
		if err := event.Validate(); err != nil {
			return Batch{}, err
		}
	}
	batch := Batch{Schema: SchemaVersion, Events: copied}
	data, err := json.Marshal(batch)
	if err != nil {
		return Batch{}, err
	}
	if len(data) > MaxBodyBytes {
		return Batch{}, fmt.Errorf("telemetry batch exceeds %d bytes", MaxBodyBytes)
	}
	return batch, nil
}

func validEventName(value EventName) bool {
	switch value {
	case EventOperationCompleted, EventRuntimeStarted, EventRuntimeStopped, EventApprovalRequested,
		EventApprovalResolved, EventBackgroundCompleted, EventInstallCompleted, EventIntegrationBootstrapCompleted:
		return true
	default:
		return false
	}
}

func validInterface(value Interface) bool {
	switch value {
	case InterfaceCLI, InterfaceTUI, InterfaceAdmin, InterfaceTelegram, InterfaceMCP, InterfaceRuntime:
		return true
	default:
		return false
	}
}

func validErrorCode(value ErrorCode) bool {
	switch value {
	case ErrorCancelled, ErrorInvalidInput, ErrorUnauthorized, ErrorNotFound, ErrorUnavailable,
		ErrorTimeout, ErrorNetwork, ErrorConflict, ErrorInternal:
		return true
	default:
		return false
	}
}
