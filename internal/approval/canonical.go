package approval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"go.mewis.me/codemcp/internal/controlguard"
)

type Target struct {
	CallerID    string
	WorkspaceID string
	Source      string
	TargetTool  string
	Arguments   map[string]any
	GuardCode   controlguard.Code
}

func CanonicalTargetDigest(instanceID string, target Target) (string, json.RawMessage, error) {
	instanceID = strings.TrimSpace(instanceID)
	target.CallerID = strings.TrimSpace(target.CallerID)
	target.WorkspaceID = strings.TrimSpace(target.WorkspaceID)
	target.Source = strings.TrimSpace(target.Source)
	target.TargetTool = strings.TrimSpace(target.TargetTool)
	if instanceID == "" {
		return "", nil, errors.New("runtime instance id is required")
	}
	if target.CallerID == "" {
		return "", nil, errors.New("approval caller correlation is required")
	}
	if target.WorkspaceID == "" {
		return "", nil, errors.New("workspace id is required")
	}
	if target.TargetTool == "" {
		return "", nil, errors.New("target tool is required")
	}
	if target.GuardCode == "" {
		return "", nil, errors.New("guard code is required")
	}
	arguments, err := canonicalArguments(target.Arguments)
	if err != nil {
		return "", nil, err
	}
	envelope := struct {
		InstanceID  string            `json:"instance_id"`
		CallerID    string            `json:"caller_id"`
		WorkspaceID string            `json:"workspace_id"`
		Source      string            `json:"source"`
		TargetTool  string            `json:"target_tool"`
		Arguments   json.RawMessage   `json:"arguments"`
		GuardCode   controlguard.Code `json:"guard_code"`
	}{instanceID, target.CallerID, target.WorkspaceID, target.Source, target.TargetTool, arguments, target.GuardCode}
	data, err := json.Marshal(envelope)
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), arguments, nil
}

func canonicalArguments(arguments map[string]any) (json.RawMessage, error) {
	if arguments == nil {
		arguments = map[string]any{}
	}
	data, err := json.Marshal(arguments)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

func cloneRaw(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}
