package integrations

import (
	"encoding/json"
	"fmt"
	"strings"
)

type ID string

const (
	PonytailID ID = "ponytail"
	CavemanID  ID = "caveman"
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

type Config struct {
	Ponytail Ponytail `json:"ponytail"`
	Caveman  Caveman  `json:"caveman"`
}

func Default() Config {
	return Config{Ponytail: Ponytail{Active: true, Mode: "full"}, Caveman: Caveman{Active: true, Mode: "full"}}
}

func IdentityFor(id ID) (Identity, bool) {
	switch id {
	case PonytailID:
		return Identity{ID: PonytailID, Name: "Ponytail"}, true
	case CavemanID:
		return Identity{ID: CavemanID, Name: "Caveman"}, true
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
