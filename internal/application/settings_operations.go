package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"go.mewis.me/codemcp/internal/capability"
)

type ConfigListInput struct {
	Prefix string
	Query  string
}

type ConfigGetInput struct {
	Key    string
	Reveal bool
	Why    bool
}

type ConfigSetInput struct {
	Action        string
	Key           string
	Value         string
	Changes       []SettingChange
	SecretSource  string
	ExpectedValue string
	CheckExpected bool
}

type ConfigExportDocument struct {
	FileName string
	Data     []byte
}

func BindSettingOperations(dispatcher *Dispatcher, service *SettingService) error {
	if dispatcher == nil {
		return errors.New("operation dispatcher is nil")
	}
	if service == nil {
		service = NewSettingService()
	}
	bindings := []struct {
		id      capability.ID
		handler OperationHandler
	}{
		{capability.ConfigExport, func(ctx context.Context, _ any) (any, error) {
			file, err := os.CreateTemp("", "codemcp-config-*.json")
			if err != nil {
				return nil, err
			}
			path := file.Name()
			if err := file.Close(); err != nil {
				_ = os.Remove(path)
				return nil, err
			}
			defer os.Remove(path)
			if _, err := ExportConfigContext(ctx, path, true); err != nil {
				return nil, err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			return ConfigExportDocument{FileName: filepath.Base(path), Data: data}, nil
		}},
		{capability.ConfigList, typedOperation[ConfigListInput](capability.ConfigList, func(ctx context.Context, input ConfigListInput) (any, error) {
			items, err := service.List(ctx, input.Prefix)
			if err != nil {
				return nil, err
			}
			query := strings.ToLower(strings.TrimSpace(input.Query))
			if query == "" {
				return items, nil
			}
			filtered := make([]SettingResult, 0, len(items))
			for _, item := range items {
				haystack := strings.ToLower(strings.Join([]string{item.Spec.Key, item.Spec.Label, item.Spec.Description, item.Spec.Domain, item.Spec.ApplicationOwner}, "\n"))
				if strings.Contains(haystack, query) {
					filtered = append(filtered, item)
				}
			}
			return filtered, nil
		})},
		{capability.ConfigGet, typedOperation[ConfigGetInput](capability.ConfigGet, func(ctx context.Context, input ConfigGetInput) (any, error) {
			if input.Reveal {
				return service.Reveal(ctx, input.Key)
			}
			if input.Why {
				return service.Why(ctx, input.Key)
			}
			return service.Present(ctx, input.Key)
		})},
		{capability.ConfigSet, typedOperation[ConfigSetInput](capability.ConfigSet, func(ctx context.Context, input ConfigSetInput) (any, error) {
			if input.CheckExpected {
				current, err := service.Present(ctx, input.Key)
				if err != nil {
					return nil, err
				}
				if current.Value != input.ExpectedValue {
					return nil, staleOperationError(capability.ConfigSet, errors.New("setting changed since this view was rendered"))
				}
			}
			switch strings.ToLower(strings.TrimSpace(input.Action)) {
			case "", "set":
				return service.SetWithOptions(ctx, input.Key, input.Value, SettingSetOptions{SecretSource: input.SecretSource})
			case "unset":
				return service.Unset(ctx, input.Key)
			case "apply":
				return service.Apply(ctx, input.Changes)
			case "rotate":
				return service.Rotate(ctx, input.Key)
			case "verify":
				return service.Verify(ctx, input.Key)
			default:
				return nil, operationError(capability.ConfigSet, ErrorInvalidArgument, errors.New("unsupported canonical setting action"))
			}
		})},
	}
	for _, binding := range bindings {
		if err := dispatcher.Register(binding.id, binding.handler); err != nil {
			return err
		}
	}
	return nil
}
