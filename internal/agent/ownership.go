package agent

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const MaxControllerIDBytes = 256

type ControllerKind string

const (
	ControllerMCP      ControllerKind = "mcp"
	ControllerOperator ControllerKind = "operator"
)

type Controller struct {
	Kind ControllerKind
	ID   string
}

func NewMCPController(id string) (Controller, error) {
	controller := Controller{Kind: ControllerMCP, ID: strings.TrimSpace(id)}
	if err := controller.Validate(); err != nil {
		return Controller{}, err
	}
	return controller, nil
}

func OperatorController() Controller {
	return Controller{Kind: ControllerOperator, ID: "local"}
}

func (controller Controller) Validate() error {
	if controller.Kind != ControllerMCP && controller.Kind != ControllerOperator {
		return fmt.Errorf("invalid managed agent controller kind %q", controller.Kind)
	}
	if controller.ID != strings.TrimSpace(controller.ID) || controller.ID == "" {
		return errors.New("managed agent controller id is required")
	}
	if !utf8.ValidString(controller.ID) || len(controller.ID) > MaxControllerIDBytes {
		return fmt.Errorf("managed agent controller id exceeds %d-byte limit or is invalid UTF-8", MaxControllerIDBytes)
	}
	if controller.Kind == ControllerOperator && controller.ID != "local" {
		return errors.New("operator controller id must be local")
	}
	return nil
}

func (controller Controller) CanControl(owner Controller) bool {
	if controller.Validate() != nil || owner.Validate() != nil {
		return false
	}
	if controller.Kind == ControllerOperator {
		return true
	}
	return controller == owner
}
