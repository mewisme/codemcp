package integrations

import (
	"encoding/json"
	"fmt"
	"strings"
)

type ID string

const (
	PonytailID   ID = "ponytail"
	CavemanID    ID = "caveman"
	RTKID        ID = "rtk"
	CodeGraphID  ID = "codegraph"
	TypeSafeID   ID = "typesafe"
	CFTunnelID   ID = "cf-tunnel"
	BrowserID    ID = "browser"
	ChatGPTWebID ID = "chatgpt-web"
)

type Identity struct {
	ID   ID     `json:"id"`
	Name string `json:"name"`
}

type Status struct {
	Identity  Identity `json:"identity"`
	Active    bool     `json:"active"`
	Available bool     `json:"available"`
}

type Ponytail struct {
	Active bool   `json:"active"`
	Mode   string `json:"mode"`
}

type Caveman struct {
	Active bool   `json:"active"`
	Mode   string `json:"mode"`
}

type RTK struct {
	Enabled bool   `json:"enabled"`
	Path    string `json:"path"`
}

type CodeGraph struct {
	Enabled bool   `json:"enabled"`
	Path    string `json:"path"`
}

type TypeSafe struct {
	Enabled   bool   `json:"enabled"`
	Model     string `json:"model"`
	TimeoutMS int    `json:"timeout_ms"`
}

type Browser struct {
	Enabled bool   `json:"enabled"`
	Path    string `json:"path"`
}

type ChatGPTWeb struct {
	Enabled       bool   `json:"enabled"`
	ConnectorName string `json:"connector_name"`
	MaxAgents     int    `json:"max_agents"`
}

type Config struct {
	Ponytail   Ponytail   `json:"ponytail"`
	Caveman    Caveman    `json:"caveman"`
	RTK        RTK        `json:"rtk"`
	CodeGraph  CodeGraph  `json:"codegraph"`
	TypeSafe   TypeSafe   `json:"typesafe"`
	Browser    Browser    `json:"browser"`
	ChatGPTWeb ChatGPTWeb `json:"chatgpt_web"`
}

func Default() Config {
	return Config{
		Ponytail:   Ponytail{Active: true, Mode: "full"},
		Caveman:    Caveman{Active: true, Mode: "full"},
		RTK:        RTK{Enabled: true},
		CodeGraph:  CodeGraph{Enabled: true},
		TypeSafe:   TypeSafe{Enabled: true, Model: "jev-latest", TimeoutMS: 3000},
		Browser:    Browser{Enabled: true},
		ChatGPTWeb: ChatGPTWeb{Enabled: true, ConnectorName: "CodeMCP", MaxAgents: 5},
	}
}

func IdentityFor(id ID) (Identity, bool) {
	switch id {
	case PonytailID:
		return Identity{ID: PonytailID, Name: "Ponytail"}, true
	case CavemanID:
		return Identity{ID: CavemanID, Name: "Caveman"}, true
	case RTKID:
		return Identity{ID: RTKID, Name: "RTK"}, true
	case CodeGraphID:
		return Identity{ID: CodeGraphID, Name: "CodeGraph"}, true
	case TypeSafeID:
		return Identity{ID: TypeSafeID, Name: "TypeSafe"}, true
	case CFTunnelID:
		return Identity{ID: CFTunnelID, Name: "Cloudflare Quick Tunnel"}, true
	case BrowserID:
		return Identity{ID: BrowserID, Name: "Browser"}, true
	case ChatGPTWebID:
		return Identity{ID: ChatGPTWebID, Name: "ChatGPT Web"}, true
	default:
		return Identity{}, false
	}
}

func Owner(id ID) string {
	if _, ok := IdentityFor(id); !ok {
		return ""
	}
	return "integration:" + string(id)
}

func ParseID(value string) (ID, error) {
	id := ID(strings.ToLower(strings.TrimSpace(value)))
	if _, ok := IdentityFor(id); !ok {
		return "", fmt.Errorf("unknown integration %q", value)
	}
	return id, nil
}

func (value *Ponytail) UnmarshalJSON(data []byte) error {
	return unmarshalMode(data, &value.Active, &value.Mode)
}

func (value *Caveman) UnmarshalJSON(data []byte) error {
	return unmarshalMode(data, &value.Active, &value.Mode)
}

func unmarshalMode(data []byte, active *bool, mode *string) error {
	var value struct {
		Active *bool   `json:"active"`
		Mode   *string `json:"mode"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if value.Active != nil {
		*active = *value.Active
	}
	if value.Mode != nil {
		*mode = *value.Mode
	}
	return nil
}
