package capability

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestEveryMutationHasOneCanonicalOwner(t *testing.T) {
	mutations := map[ID]bool{}
	for _, spec := range All() {
		ownership, owned := MutationOwnershipFor(spec.ID)
		if spec.Kind != KindMutation {
			if owned {
				t.Errorf("non-mutation %s has mutation ownership %#v", spec.ID, ownership)
			}
			continue
		}
		mutations[spec.ID] = true
		if !owned {
			t.Errorf("mutation %s has no canonical validation/side-effect owner", spec.ID)
			continue
		}
		if ownership.Operation != spec.ID || ownership.ValidationOwner == "" || ownership.SideEffectOwner == "" {
			t.Errorf("mutation %s has incomplete ownership %#v", spec.ID, ownership)
		}
		if ownership.ValidationOwner != ownership.SideEffectOwner {
			t.Errorf("mutation %s splits validation owner %q from side-effect owner %q", spec.ID, ownership.ValidationOwner, ownership.SideEffectOwner)
		}
	}

	all := AllMutationOwnership()
	if len(all) != len(mutations) {
		t.Fatalf("mutation ownership entries=%d canonical mutations=%d", len(all), len(mutations))
	}
	for i, ownership := range all {
		if !mutations[ownership.Operation] {
			t.Errorf("stale mutation ownership for %s", ownership.Operation)
		}
		if i > 0 && all[i-1].Operation >= ownership.Operation {
			t.Fatalf("mutation ownership is not stable and sorted at %s", ownership.Operation)
		}
	}
}

func TestSettingLikeMutationsDeclareCanonicalApplicationOwners(t *testing.T) {
	expected := map[ID]MutationOwner{
		ConfigSet:                   MutationOwnerApplicationSettings,
		ConfigPatch:                 MutationOwnerApplicationSettings,
		AuthMCPRotate:               MutationOwnerApplicationAuth,
		AuthMCPEnable:               MutationOwnerApplicationAuth,
		AuthMCPDisable:              MutationOwnerApplicationAuth,
		AuthAdminRotate:             MutationOwnerApplicationAuth,
		AuthAdminEnable:             MutationOwnerApplicationAuth,
		AuthAdminDisable:            MutationOwnerApplicationAuth,
		TunnelConfigure:             MutationOwnerApplicationTunnel,
		TunnelAdminKeySet:           MutationOwnerApplicationTunnel,
		TunnelAdminKeyRemove:        MutationOwnerApplicationTunnel,
		TelemetryEnable:             MutationOwnerApplicationTelemetry,
		TelemetryDisable:            MutationOwnerApplicationTelemetry,
		IntegrationRTKEnable:        MutationOwnerApplicationIntegrations,
		IntegrationRTKDisable:       MutationOwnerApplicationIntegrations,
		IntegrationTypeSafeEnable:   MutationOwnerApplicationIntegrations,
		IntegrationTypeSafeDisable:  MutationOwnerApplicationIntegrations,
		IntegrationChatGPTWebLogin:  MutationOwnerApplicationIntegrations,
		IntegrationChatGPTWebLogout: MutationOwnerApplicationIntegrations,
		LLMProviderAdd:              MutationOwnerApplicationLLM,
		LLMProviderConfigure:        MutationOwnerApplicationLLM,
		LLMProviderRemove:           MutationOwnerApplicationLLM,
		LLMProviderSelect:           MutationOwnerApplicationLLM,
		LLMProviderCredentialSet:    MutationOwnerApplicationLLM,
		LLMProviderCredentialClear:  MutationOwnerApplicationLLM,
	}
	for id, want := range expected {
		ownership, ok := MutationOwnershipFor(id)
		if !ok || ownership.ValidationOwner != want || ownership.SideEffectOwner != want {
			t.Errorf("setting-like mutation %s ownership=%#v ok=%t want=%s", id, ownership, ok, want)
		}
	}
}

func TestPlanAuthoringMutationHasDedicatedCanonicalOwner(t *testing.T) {
	ownership, ok := MutationOwnershipFor(PlanCreate)
	if !ok || ownership.ValidationOwner != MutationOwnerPlanAuthoring || ownership.SideEffectOwner != MutationOwnerPlanAuthoring {
		t.Fatalf("plan authoring ownership=%#v ok=%t", ownership, ok)
	}
}

func TestHumanAdaptersCannotOwnCanonicalBusinessMutations(t *testing.T) {
	root := capabilityRepositoryRoot(t)
	adapterRoots := []string{
		"internal/cli",
		"internal/interface/admin",
		"internal/interface/tui",
		"internal/telegram",
	}
	forbidden := []struct {
		pattern *regexp.Regexp
		reason  string
	}{
		{regexp.MustCompile(`\bconfig\.Save\s*\(`), "configuration persistence belongs to application/domain owners"},
		{regexp.MustCompile(`\.Config\.Update\s*\(`), "runtime config mutation belongs to application/domain owners"},
		{regexp.MustCompile(`\b(?:Save|Remove|Sync)TunnelMetadata\s*\(`), "tunnel metadata mutation belongs to the tunnel application owner"},
		{regexp.MustCompile(`\.(?:Reconfigure|ReconfigureSeeded|SyncManagementConfig)\s*\(`), "runtime reconciliation belongs to canonical application owners"},
		{regexp.MustCompile(`\bapi\.Tools\.Processes\.ClearFinished\s*\(`), "process mutation belongs to the application process service"},
		{regexp.MustCompile(`(?:workspaceManagerForCommand\([^)]*\)|\bmanager)\.(?:Register|Unregister|Relocate|Purge|CreateContainer|RenameContainer|DeleteContainer|AddAllowDir|RemoveAllowDir|AddWorkspacesToContainer|RemoveWorkspacesFromContainer|AddWorkspaceToContainers|RemoveWorkspaceFromContainers)\s*\(`), "workspace mutation belongs to application.WorkspaceService"},
		{regexp.MustCompile(`(?:\.service\.Manager\(\)|\bapi\.Upstream|\bpage\.manager)\.(?:Add|Remove|CreateBatch|SetEnabled)\s*\(`), "upstream mutation belongs to application.UpstreamService"},
	}

	for _, relativeRoot := range adapterRoots {
		path := filepath.Join(root, filepath.FromSlash(relativeRoot))
		err := filepath.WalkDir(path, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, check := range forbidden {
				if match := check.pattern.Find(body); match != nil {
					t.Errorf("%s owns business mutation via %q: %s", filepath.ToSlash(path), string(match), check.reason)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestUniversalMutationOwnershipGuardsRemainPresent(t *testing.T) {
	root := capabilityRepositoryRoot(t)
	guards := map[string][]string{
		"internal/cli/cli_adapter_parity_test.go": {
			"TestCLICommandsDoNotDuplicateCanonicalSecurityMetadata",
			"TestCLIAdaptersDoNotBypassCanonicalMutationOwners",
		},
		"internal/cli/command_presentation_test.go": {
			"TestAliasesInheritCanonicalPresentationContract",
		},
		"internal/cli/scoped_settings_test.go": {
			"TestScopedAndUniversalStaticSettingParity",
			"TestTunnelAdminGenericScopedAndFlagParity",
			"TestScopedMutationReloadsRunningRuntimeExactlyOnce",
			"TestScopedConfigFacadesDoNotBypassCanonicalSettingAuthority",
		},
		"internal/cli/typesafe_settings_test.go": {
			"TestTypeSafeScopedSettingsUseCanonicalSettingService",
		},
		"internal/cli/telegram_test.go": {
			"TestTelegramTokenScopedAndGenericSettingsConverge",
		},
		"internal/interface/admin/ownership_test.go": {
			"TestAdminInterfaceDoesNotOwnBusinessMutations",
		},
		"internal/interface/admin/api_test.go": {
			"TestConfigAPIMutationUsesCanonicalSettingTransaction",
			"TestTunnelConfigureUsesCanonicalPersistence",
		},
		"internal/interface/tui/page/config_test.go": {
			"TestConfigSuccessfulEditorSaveCommitsDraftBeforeNavigation",
		},
		"internal/interface/tui/page/read_views_test.go": {
			"TestIntegrationsReadViewMutationUsesCanonicalSettingEffect",
		},
		"internal/interface/tui/page/workspace_test.go": {
			"TestWorkspaceMutationsSynchronizeRunningRuntime",
		},
		"internal/interface/tui/actions_test.go": {
			"TestActionSecurityMetadataComesFromCanonicalCapability",
		},
		"internal/telegram/navigation_test.go": {
			"TestCanonicalRequiredConfirmationBlocksDispatchUntilConfirmed",
			"TestMutationCallbackStateIsOneShot",
			"TestStaleExpectedVersionBlocksCanonicalDispatch",
		},
		"internal/telegram/runtime_test.go": {
			"TestTokenIsSecretStoreStateNotRuntimeConfig",
		},
		"internal/application/settings_test.go": {
			"TestSettingServiceStaticMutationMatchesCanonicalConfigAuthority",
			"TestSettingServiceApplyIsAtomicAndReloadsRunningRuntimeOnce",
			"TestTunnelAdminGenericBatchAndDomainFacadeConverge",
			"TestSettingServiceTunnelSecretPresentationAndTraceAreSafe",
			"TestSettingServiceLegacyAuthHashUsesExplicitLegacyMaskedPreview",
			"TestSettingServiceTunnelAdminConfiguredInputsAreOfflineAndInvalidateDerivedState",
		},
		"internal/application/tunnel_test.go": {
			"TestTunnelRuntimeConfigureBatchReloadsOnce",
		},
		"internal/application/operation_test.go": {
			"TestDispatcherMetadataCannotBeOverriddenByAdapterInput",
			"TestWorkspaceServiceMutationReconcilesExactlyOnceAndUsesCanonicalTrace",
		},
		"internal/application/architecture_test.go": {
			"TestRepresentativeWorkspaceAdaptersCannotBypassApplicationMutationOwner",
		},
		"internal/capability/inventory_test.go": {
			"TestSecurityPolicyIsOwnedByCanonicalOperationAcrossBindings",
			"TestParityMatrixCannotOverrideCanonicalSecurityPolicy",
		},
		"internal/cli/config_test.go": {
			"TestTunnelAdminConfiguredInputsDoNotBypassSecretOwnershipOrRetainVerification",
		},
	}

	paths := make([]string, 0, len(guards))
	for path := range guards {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, relative := range paths {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("mutation ownership guard %s missing: %v", relative, err)
		}
		for _, marker := range guards[relative] {
			if !strings.Contains(string(body), marker) {
				t.Errorf("mutation ownership guard %s lost marker %q", relative, marker)
			}
		}
	}
}
