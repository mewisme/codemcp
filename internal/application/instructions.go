package application

import (
	"os"

	"go.mewis.me/codemcp/internal/idgen"
	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/instructionpolicy"
)

type InstructionSettings struct {
	Version         int                                       `json:"version"`
	Context         string                                    `json:"context"`
	Rules           []instructionpolicy.GlobalRule            `json:"rules"`
	SourcePolicy    map[string]instructionpolicy.SourcePolicy `json:"source_policy"`
	DetectedSources []instructioncontext.SourceSnapshot       `json:"detected_sources"`
}

type InstructionSettingsPatch struct {
	Context      *string                                   `json:"context,omitempty"`
	Rules        *[]instructionpolicy.GlobalRule           `json:"rules,omitempty"`
	SourcePolicy map[string]instructionpolicy.SourcePolicy `json:"source_policy,omitempty"`
}

type InstructionSettingsService struct {
	Store       *instructionpolicy.Store
	UserHomeDir func() (string, error)
}

func NewInstructionSettingsService(store *instructionpolicy.Store) *InstructionSettingsService {
	if store == nil {
		store = instructionpolicy.DefaultStore()
	}
	return &InstructionSettingsService{Store: store, UserHomeDir: os.UserHomeDir}
}

func LoadInstructionSettings() (InstructionSettings, error) {
	return NewInstructionSettingsService(nil).Load()
}

func SaveInstructionSettings(patch InstructionSettingsPatch) (InstructionSettings, error) {
	return NewInstructionSettingsService(nil).Save(patch)
}

func (service *InstructionSettingsService) Load() (InstructionSettings, error) {
	value, err := service.store().Load()
	if err != nil {
		return InstructionSettings{}, err
	}
	return service.view(value)
}

func (service *InstructionSettingsService) Save(patch InstructionSettingsPatch) (InstructionSettings, error) {
	store := service.store()
	value, err := store.Load()
	if err != nil {
		return InstructionSettings{}, err
	}
	if patch.Context != nil {
		value.Context = *patch.Context
	}
	if patch.Rules != nil {
		value.Rules = append([]instructionpolicy.GlobalRule(nil), (*patch.Rules)...)
	}
	if value.Sources == nil {
		value.Sources = map[string]instructionpolicy.SourcePolicy{}
	}
	for provider, source := range patch.SourcePolicy {
		value.Sources[instructionpolicy.ProviderID(provider)] = source
	}
	if err := store.Save(value); err != nil {
		return InstructionSettings{}, err
	}
	return service.view(value)
}

func (service *InstructionSettingsService) view(value instructionpolicy.Config) (InstructionSettings, error) {
	homeDir := os.UserHomeDir
	if service != nil && service.UserHomeDir != nil {
		homeDir = service.UserHomeDir
	}
	home, err := homeDir()
	if err != nil {
		return InstructionSettings{}, err
	}
	sources, err := instructioncontext.DiscoverUserSources(home, value)
	if err != nil {
		return InstructionSettings{}, err
	}
	return InstructionSettings{
		Version: value.Version, Context: value.Context, Rules: append([]instructionpolicy.GlobalRule(nil), value.Rules...),
		SourcePolicy: cloneInstructionSourcePolicy(value.Sources), DetectedSources: sources,
	}, nil
}

func (service *InstructionSettingsService) store() *instructionpolicy.Store {
	if service != nil && service.Store != nil {
		return service.Store
	}
	return instructionpolicy.DefaultStore()
}

func NewInstructionRuleID() (string, error) {
	return idgen.New("rule", 16)
}

func cloneInstructionSourcePolicy(values map[string]instructionpolicy.SourcePolicy) map[string]instructionpolicy.SourcePolicy {
	result := make(map[string]instructionpolicy.SourcePolicy, len(values))
	for provider, policy := range values {
		result[provider] = policy
	}
	return result
}
