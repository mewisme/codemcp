package controlguard

import (
	"errors"
	"strings"
)

type Code string

const (
	CodeControlPlaneMutation Code = "control_plane_mutation"
	CodeDestructiveMutation  Code = "destructive_mutation"
	CodeHostMutation         Code = "host_mutation"
	CodeExternalAccess       Code = "external_access"
	CodeExternalMutation     Code = "external_mutation"
	CodeShellExecution       Code = "shell_execution"
	CodeProtectedState       Code = "protected_state_access"
	CodeContextTamper        Code = "tool_context_tamper"
	CodeSemanticRisk         Code = "semantic_risk"
)

type Invocation struct {
	Program string   `json:"program,omitempty"`
	Args    []string `json:"args,omitempty"`
	Command string   `json:"command,omitempty"`
}

type Error struct {
	Code       Code        `json:"code"`
	Message    string      `json:"message"`
	Approvable bool        `json:"approvable"`
	Invocation *Invocation `json:"invocation,omitempty"`
}

func New(code Code, message string, approvable bool, invocation *Invocation) *Error {
	return &Error{Code: code, Message: strings.TrimSpace(message), Approvable: approvable, Invocation: cloneInvocation(invocation)}
}

func (e *Error) Error() string {
	if e == nil {
		return "control guard denied"
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Code != "" {
		return "control guard denied: " + string(e.Code)
	}
	return "control guard denied"
}

func As(err error) (*Error, bool) {
	var value *Error
	if !errors.As(err, &value) || value == nil {
		return nil, false
	}
	return value, true
}

func IsCode(err error, code Code) bool {
	value, ok := As(err)
	return ok && value.Code == code
}

func cloneInvocation(value *Invocation) *Invocation {
	if value == nil {
		return nil
	}
	copy := *value
	copy.Args = append([]string(nil), value.Args...)
	return &copy
}
