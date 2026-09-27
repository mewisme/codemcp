package capability

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type correctiveEvidence struct {
	domain string
	file   string
	tests  []string
}

func TestCorrectiveRegressionEvidenceMatrix(t *testing.T) {
	root := capabilityRepositoryRoot(t)
	evidence := []correctiveEvidence{
		{
			domain: "source-run control-plane isolation",
			file:   "internal/workspace/shell_policy_test.go",
			tests: []string{
				"TestShellPolicyCodeMCPSourceRunParity",
				"TestShellPolicySourceRunApprovalIsExactAndCanonical",
				"TestControlPlaneEntryPointsReconcileWithUnifiedClassifier",
			},
		},
		{
			domain: "workspace runtime reconciliation",
			file:   "internal/application/operation_test.go",
			tests: []string{
				"TestWorkspaceServiceMutationReconcilesExactlyOnceAndUsesCanonicalTrace",
				"TestWorkspaceServiceExposesRuntimeDiagnosticsAndConflicts",
			},
		},
		{
			domain: "workspace adapter runtime synchronization",
			file:   "internal/cli/workspace_runtime_test.go",
			tests: []string{
				"TestWorkspaceRegisterSynchronizesRunningRuntime",
				"TestWorkspaceContainerMutationIsImmediatelyVisibleToRunningMCPRuntime",
				"TestWorkspaceContainerMutationReportsRuntimeSynchronizationFailure",
			},
		},
		{
			domain: "MCP config approval binding",
			file:   "internal/application/mcp_config_test.go",
			tests: []string{
				"TestMCPConfigSetApprovalBindingIsPrivateCanonicalAndStateBound",
				"TestMCPConfigSetTraceContainsKeysButNeverUserValues",
			},
		},
		{
			domain: "MCP config atomic apply",
			file:   "internal/tools/config_set_apply_external_test.go",
			tests: []string{
				"TestConfigSetApprovedStaticBatchAppliesOnceAndReloadsOnce",
				"TestConfigSetInvalidBatchIsRejectedBeforeApproval",
				"TestConfigSetReloadFailureRollsBackWithSafeError",
			},
		},
		{
			domain: "MCP config approval replay safety",
			file:   "internal/tools/config_set_approval_external_test.go",
			tests: []string{
				"TestConfigSetApprovalExactRetryIsPrivateAndOneShot",
				"TestConfigSetApprovalRejectsChangedOrderAndStaleConfig",
				"TestConfigSetApprovalRejectsSecretsMissingScopeDenialAndUnavailableReviewer",
			},
		},
		{
			domain: "product telemetry ownership",
			file:   "internal/telemetry/product_ownership_contract_test.go",
			tests: []string{
				"TestProductTelemetryOwnershipAndEventCatalogContract",
				"TestProductTelemetryLifecycleCannotOwnFunctionalOutcomes",
			},
		},
		{
			domain: "product telemetry privacy and transport",
			file:   "internal/telemetry/product_contract_test.go",
			tests: []string{
				"TestProductTelemetryBackendAndPrivacyContract",
				"TestProductTelemetryConfigIdentityAndTransportContract",
			},
		},
		{
			domain: "canonical integration identity",
			file:   "internal/integrations/integrations_test.go",
			tests:  []string{"TestCanonicalIntegrationIdentityAndOwner"},
		},
		{
			domain: "RTK effective command fidelity",
			file:   "internal/integrations/rtk/rtk_test.go",
			tests:  []string{"TestRewriteUsesResolvedRTKAndPreservesRequestedCommand"},
		},
		{
			domain: "CodeGraph runtime reload fidelity",
			file:   "internal/tools/codegraph_test.go",
			tests: []string{
				"TestCodeGraphExploreToolRegistrationIsStableAcrossIntegrationReload",
				"TestCodeGraphProjectContextGuidanceTracksRuntimeReload",
				"TestCodeGraphIntegrationReloadCatchesUpFailedCompletion",
			},
		},
		{
			domain: "SystemOne semantic and risk wire contract",
			file:   "internal/integrations/typesafe/systemone_test.go",
			tests: []string{
				"TestSystemOneClientMapsNeutralSemanticRoundTrip",
				"TestSystemOneClientClassifiesCanonicalRiskWithChoiceContract",
				"TestSystemOneManagerOwnsRetriesForBothCapabilities",
			},
		},
		{
			domain: "TypeSafe dual-capability production runtime",
			file:   "internal/app/typesafe_runtime_test.go",
			tests: []string{
				"TestTypeSafeRuntimeWiresMemorySearchAndApprovalRiskThroughOneSystemOneClient",
				"TestTypeSafeRuntimeCredentialRotationReplacesBothCapabilitiesTogether",
				"TestTypeSafeProductionFailuresPreserveSemanticFallbackAndRequireRiskReview",
				"TestTypeSafeRuntimeRejectsPartialProductionRegistration",
			},
		},
		{
			domain: "shell provider resolution",
			file:   "internal/runtime/shell/provider_test.go",
			tests: []string{
				"TestWindowsAutomaticResolutionOrder",
				"TestWindowsUnavailableShellReturnsTypedActionableErrorWithoutManagedFallback",
				"TestForegroundAndBackgroundShareResolvedProvider",
			},
		},
		{
			domain: "checkpoint retention recovery",
			file:   "internal/checkpoint/archive_test.go",
			tests:  []string{"TestInterruptedRetentionTransitionKeepsActiveOrArchivedCheckpointRecoverable"},
		},
		{
			domain: "checkpoint concurrent health",
			file:   "internal/checkpoint/health_test.go",
			tests:  []string{"TestCheckpointConcurrentDiagnosticsAndRetentionKeepIndexesConsistent"},
		},
		{
			domain: "single persistent tunnel runtime",
			file:   "internal/application/tunnel_test.go",
			tests: []string{
				"TestTunnelRuntimeConfigureBatchReloadsOnce",
				"TestManagedCreateFailureDoesNotChangeRuntimeConfig",
			},
		},
		{
			domain: "aggregate doctor inventory",
			file:   "internal/application/doctor_coverage_test.go",
			tests: []string{
				"TestDefaultDoctorProvidersSatisfyCanonicalInventory",
				"TestDoctorDegradedFixtures",
				"TestShellDoctorCoversPlatformProviderKinds",
			},
		},
		{
			domain: "CLI capability parity",
			file:   "internal/cli/capability_parity_test.go",
			tests: []string{
				"TestPublicCommandsHaveCanonicalCapabilities",
				"TestRunnableCLICommandsCarryCanonicalOperationAnnotations",
				"TestRestoredCanonicalCLIReachabilityMatrix",
			},
		},
		{
			domain: "Admin API operation parity",
			file:   "internal/interface/admin/operation_contract_test.go",
			tests:  []string{"TestPublicAdminOperationsHaveCanonicalIDs"},
		},
		{
			domain: "Browser Admin adapter parity",
			file:   "internal/capability/adapter_enforcement_test.go",
			tests:  []string{"TestBrowserRequiredOperationsHaveFrontendAdapters"},
		},
		{
			domain: "TUI capability parity",
			file:   "internal/interface/tui/capability_parity_test.go",
			tests: []string{
				"TestEveryPublicCapabilityHasTUIRepresentation",
				"TestExecutableTUIActionsCarryCanonicalOperationIDs",
				"TestCapabilityActionsHaveReachableContexts",
			},
		},
		{
			domain: "TUI Browser stable row identity",
			file:   "internal/interface/tui/component/browser_test.go",
			tests: []string{
				"TestBrowserMouseTargetKeepsStableRowIdentityAcrossRebuild",
				"TestBrowserMouseTargetForRemovedRowIsNoOp",
				"TestBrowserMouseRowTargetsMatchRenderedRows",
			},
		},
		{
			domain: "TUI Logs retention and lazy window",
			file:   "internal/interface/tui/page/logs_test.go",
			tests: []string{
				"TestLogsBrowserAndTimelineRetentionSemanticsAreIndependent",
				"TestLogsTimelineWindowsStartNewestExpandAndKeepLogicalRecordsWhole",
				"TestLogsClearViewUsesSessionWatermarksWithoutDestroyingRetainedHistory",
			},
		},
		{
			domain: "TUI Logs mouse and pinned detail stability",
			file:   "internal/interface/tui/page/logs_test.go",
			tests: []string{
				"TestLogsBrowserMouseRoutesThroughExecutionAndToolCallPages",
				"TestLogsDetailMouseWheelRoutesThroughPageUpdate",
				"TestRuntimeDetailStaysPinnedAcrossUnrelatedLiveReplayAndClear",
				"TestToolCallReconnectSnapshotUpdatesPinnedDetailInPlace",
				"TestExecutionReconnectSnapshotSchedulesCanonicalPinnedRefresh",
				"TestLogsBrowserMouseTargetAndSelectionSurviveLiveRebuild",
			},
		},
		{
			domain: "completed Telegram rollout",
			file:   "internal/capability/telegram_rollout_test.go",
			tests: []string{
				"TestTelegramRolloutCompletedContractsRemainLive",
				"TestTelegramRolloutFutureOwnershipIsExplicit",
			},
		},
		{
			domain: "universal operation and surface gate",
			file:   "internal/capability/universal_gate_test.go",
			tests: []string{
				"TestUniversalOperationAndSurfaceGate",
				"TestUniversalInventoryDoesNotReintroduceRejectedTunnelModels",
			},
		},
		{
			domain: "universal mutation ownership gate",
			file:   "internal/capability/mutation_ownership_test.go",
			tests: []string{
				"TestEveryMutationHasOneCanonicalOwner",
				"TestHumanAdaptersCannotOwnCanonicalBusinessMutations",
			},
		},
	}

	for _, item := range evidence {
		t.Run(item.domain, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(item.file)))
			if err != nil {
				t.Fatalf("read evidence file %s: %v", item.file, err)
			}
			for _, testName := range item.tests {
				marker := "func " + testName + "("
				if !strings.Contains(string(body), marker) {
					t.Errorf("behavioral regression evidence %q lost %s in %s", item.domain, testName, item.file)
				}
			}
		})
	}
}

func TestReferenceOnlyArchitecturesRemainAbsent(t *testing.T) {
	root := capabilityRepositoryRoot(t)
	removed := []string{
		"internal/application/tunnel_collection.go",
		"internal/application/tunnel_collection_management.go",
		"internal/application/tunnel_profiles.go",
		"internal/tunnel/collection.go",
		"internal/tunnel/admin_profile.go",
		"internal/tunnel/admin_profile_secure.go",
		"internal/runtime/bash",
	}
	for _, relative := range removed {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if _, err := os.Stat(path); err == nil {
			t.Errorf("superseded architecture reappeared at %s", relative)
		} else if !os.IsNotExist(err) {
			t.Fatalf("inspect %s: %v", relative, err)
		}
	}

	provider, err := os.ReadFile(filepath.Join(root, "internal", "runtime", "shell", "provider.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"inspectManaged", "managed Bash", "runtime/bash"} {
		if strings.Contains(string(provider), forbidden) {
			t.Errorf("shell provider reintroduced managed fallback marker %q", forbidden)
		}
	}
}
