package capability

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestRequiredHumanOperationsAreReachableOnEveryProductSurface(t *testing.T) {
	report := ProductParityReportSnapshot()
	for _, row := range report.Operations {
		for _, mapping := range row.Surfaces {
			if mapping.State != SurfaceRequired {
				continue
			}
			if !mapping.Reachable || mapping.Gap != "" || len(mapping.EntryPoints) == 0 {
				t.Errorf("%s/%s is required but not production-reachable: %#v", row.Operation, mapping.Surface, mapping)
			}
		}
	}
}

func TestRepresentativeStrictCrossSurfaceSemanticMatrix(t *testing.T) {
	cases := []struct {
		domain string
		id     ID
	}{
		{"read", StatusOverview},
		{"create", PromptCreate},
		{"update", PromptUpdate},
		{"delete", PromptDelete},
		{"generated secret rotation", AuthAdminRotate},
		{"approval decision", RequestApprove},
		{"runtime grant revoke", RequestGrantRevoke},
		{"process action", ProcessClear},
		{"integration action", IntegrationCodeGraphWorkspaceSync},
		{"auth action", AuthAdminEnable},
		{"runtime action", RuntimeRestart},
		{"workspace register", WorkspaceRegister},
		{"workspace relocate", WorkspaceRelocate},
		{"workspace access", WorkspaceAccessAdd},
		{"upstream oauth", UpstreamAuthLogin},
		{"execution observability", ExecutionList},
		{"notification state", NotificationStatus},
		{"completion state", CompletionList},
		{"llm provider read", LLMProviderList},
		{"llm provider create", LLMProviderAdd},
		{"llm provider configure", LLMProviderConfigure},
		{"llm provider delete", LLMProviderRemove},
		{"llm provider select", LLMProviderSelect},
		{"llm model query", LLMProviderModels},
		{"llm probe", LLMProviderProbe},
		{"llm credential set", LLMProviderCredentialSet},
		{"llm credential clear", LLMProviderCredentialClear},
		{"approval explain status", RequestExplainStatus},
		{"approval explain view", RequestExplanationView},
		{"approval explain trigger", RequestExplain},
	}
	report := ProductParityReportSnapshot()
	for _, test := range cases {
		t.Run(test.domain, func(t *testing.T) {
			spec, ok := Lookup(test.id)
			if !ok {
				t.Fatalf("operation %s missing", test.id)
			}
			if spec.Audience != AudienceOperator && spec.Audience != AudienceReviewer {
				t.Fatalf("operation %s audience=%s is outside product parity", test.id, spec.Audience)
			}
			owner, ok := CanonicalOwnerFor(test.id)
			if !ok || owner == "" {
				t.Fatalf("operation %s has no canonical owner", test.id)
			}
			row, ok := parityRowFor(report, test.id)
			if !ok {
				t.Fatalf("operation %s missing from product parity report", test.id)
			}
			if row.CanonicalOwner != owner || row.Authorization != spec.Authorization || row.Risk != spec.Risk ||
				row.Confirmation != spec.Confirmation || row.Effects != spec.Effects {
				t.Fatalf("operation %s semantic metadata drift: row=%#v spec=%#v owner=%q", test.id, row, spec, owner)
			}
			for _, surface := range ProductSurfaces {
				mapping, ok := parityMapping(report, test.id, surface)
				if !ok || mapping.State != SurfaceRequired || !mapping.Reachable || mapping.Gap != "" || len(mapping.EntryPoints) == 0 {
					t.Fatalf("operation %s/%s is not a live required adapter: %#v ok=%t", test.id, surface, mapping, ok)
				}
			}
		})
	}
}

func TestProductSurfaceExemptionVocabularyIsExactAndBounded(t *testing.T) {
	gotClasses := []SurfaceExemptionClass{
		SurfaceExemptionProtocolOnly,
		SurfaceExemptionSurfaceBootstrap,
		SurfaceExemptionHostLocalPrimitive,
		SurfaceExemptionRemovedArchitecture,
	}
	gotGuards := []SurfaceExemptionGuard{
		SurfaceGuardProtocolAudience,
		SurfaceGuardBootstrapOperation,
		SurfaceGuardHostLocalOperation,
		SurfaceGuardRemovedArchitecture,
	}
	sort.Slice(gotClasses, func(i, j int) bool { return gotClasses[i] < gotClasses[j] })
	sort.Slice(gotGuards, func(i, j int) bool { return gotGuards[i] < gotGuards[j] })

	wantClasses := []SurfaceExemptionClass{"host-local-primitive", "protocol-only", "removed-architecture", "surface-bootstrap"}
	wantGuards := []SurfaceExemptionGuard{"bootstrap-operation", "host-local-operation", "protocol-audience", "removed-architecture"}
	for i := range wantClasses {
		if gotClasses[i] != wantClasses[i] {
			t.Fatalf("surface exemption vocabulary drifted: got=%v want=%v", gotClasses, wantClasses)
		}
	}
	for i := range wantGuards {
		if gotGuards[i] != wantGuards[i] {
			t.Fatalf("surface exemption guard vocabulary drifted: got=%v want=%v", gotGuards, wantGuards)
		}
	}
}

func TestStrictCrossSurfaceBehavioralEvidenceRemainsExecutable(t *testing.T) {
	type evidence struct {
		domain  string
		file    string
		markers []string
	}
	evidenceSet := []evidence{
		{
			domain: "canonical read-after-write convergence",
			file:   "internal/application/settings_test.go",
			markers: []string{
				"TestSettingServiceStaticMutationMatchesCanonicalConfigAuthority",
				"TestTunnelAdminGenericBatchAndDomainFacadeConverge",
			},
		},
		{
			domain: "typed canonical failures",
			file:   "internal/application/operation_test.go",
			markers: []string{
				"TestDispatcherReturnsCanonicalMetadataAndTypedErrors",
				"TestWorkspaceServiceExposesRuntimeDiagnosticsAndConflicts",
			},
		},
		{
			domain:  "provider failure classification",
			file:    "internal/application/llm_operations_test.go",
			markers: []string{"TestLLMDispatcherOwnsCustomProviderMutationAndTypedErrors"},
		},
		{
			domain: "destructive and semantic approval cannot be bypassed",
			file:   "internal/tools/approval_runtime_test.go",
			markers: []string{
				"TestHostConfirmationMetadataCannotBypassCodeMCPApproval",
				"TestDestructiveShellApprovalIsExactOneShotAndWorkspaceBound",
			},
		},
		{
			domain:  "telegram destructive confirmation",
			file:    "internal/telegram/navigation_test.go",
			markers: []string{"TestCanonicalRequiredConfirmationBlocksDispatchUntilConfirmed"},
		},
		{
			domain:  "protected secret input convergence",
			file:    "internal/cli/protected_input_test.go",
			markers: []string{"TestProtectedSecretSourcesConvergeAndNeverRenderRawValue"},
		},
		{
			domain: "generated and configured secret state",
			file:   "internal/application/settings_operations_test.go",
			markers: []string{
				"TestSettingSemanticStateSeparatesConfiguredGeneratedAndDerivedValues",
			},
		},
		{
			domain: "workspace identity and relocation convergence",
			file:   "internal/application/workspace_relocate_test.go",
			markers: []string{
				"TestWorkspaceRelocateConflictReadModelAndExplicitResolution",
				"TestWorkspaceRelocateDispatcherUsesCanonicalRequest",
			},
		},
		{
			domain:  "workspace access and registration adapter convergence",
			file:    "internal/cli/workspace_runtime_test.go",
			markers: []string{"TestWorkspaceRegisterSynchronizesRunningRuntime", "TestWorkspaceContainerMutationIsImmediatelyVisibleToRunningMCPRuntime"},
		},
		{
			domain:  "upstream oauth safe projection",
			file:    "internal/telegram/semantic_parity_test.go",
			markers: []string{"TestUpstreamOAuthIsDiscoverableAndAuthorizationResultIsTokenFree"},
		},
		{
			domain:  "upstream oauth browser projection",
			file:    "internal/interface/admin/oauth_test.go",
			markers: []string{"TestUpstreamOAuthAdminFlowDoesNotExposeTokens"},
		},
		{
			domain:  "codegraph workspace lifecycle",
			file:    "internal/application/codegraph_test.go",
			markers: []string{"TestCodeGraphWorkspaceLifecyclePersistsSharedReadModel", "TestCodeGraphWorkspaceRejectsUnregisteredAndEscapingTargets"},
		},
		{
			domain: "approval and grant terminal truth",
			file:   "internal/approval/parity_test.go",
			markers: []string{
				"TestTelegramTUIBrowserResolutionRaceHasOneTerminalTruth",
				"TestApprovalReviewerSurfaceCountDoesNotChangeTruth",
			},
		},
		{
			domain:  "runtime grant lifecycle",
			file:    "internal/approval/manager_test.go",
			markers: []string{"TestRuntimeSessionGrantCrossesMCPSessionUntilTTL", "TestRuntimeSessionGrantRevoke"},
		},
		{
			domain:  "process terminal truth",
			file:    "internal/runtime/shell/process_cleanup_test.go",
			markers: []string{"TestProcessManagerPublishesCommittedNaturalTerminalTruth", "TestProcessManagerPublishesOneTerminalEventWhenStopRacesExit"},
		},
		{
			domain:  "execution stream canonical projection",
			file:    "internal/interface/admin/executions_test.go",
			markers: []string{"TestWorkspaceExecutionSSEStartsWithSnapshotAndStreamsUntilCompletion"},
		},
		{
			domain:  "completion truth and notification isolation",
			file:    "internal/app/app_test.go",
			markers: []string{"TestAcceptedCompletionNotificationFailureDoesNotChangeCompletionTruth"},
		},
		{
			domain:  "completion telegram projection",
			file:    "internal/telegram/completions_test.go",
			markers: []string{"TestCompletionListAndDetailUseCanonicalResolvers"},
		},
		{
			domain: "llm canonical provider lifecycle",
			file:   "internal/application/llm_operations_test.go",
			markers: []string{
				"TestCanonicalLLMOperationsBindStableResultsAndProtectedCredentials",
				"TestLLMDispatcherOwnsCustomProviderMutationAndTypedErrors",
			},
		},
		{
			domain:  "llm browser convergence",
			file:    "internal/interface/admin/llm_test.go",
			markers: []string{"TestLLMAdminRoutesConvergeWithCanonicalStateAndProtectCredentials"},
		},
		{
			domain:  "llm telegram convergence",
			file:    "internal/telegram/llm_test.go",
			markers: []string{"TestTelegramLLMNavigationUsesCanonicalStatusAndProviderList", "TestTelegramLLMProviderActionsProtectCoreIdentityAndSecretState"},
		},
		{
			domain: "approval explanation retry and authority",
			file:   "internal/application/approval_explain_test.go",
			markers: []string{
				"TestApprovalExplainFailureRequiresExplicitRetryAndReviewRemainsUsable",
				"TestApprovalExplanationStateIsNotPartOfApprovalRequestAuthority",
			},
		},
	}

	root := capabilityRepositoryRoot(t)
	for _, item := range evidenceSet {
		t.Run(item.domain, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(item.file)))
			if err != nil {
				t.Fatalf("read evidence file %s: %v", item.file, err)
			}
			for _, marker := range item.markers {
				if !strings.Contains(string(body), "func "+marker+"(") {
					t.Errorf("behavioral evidence %q lost %s in %s", item.domain, marker, item.file)
				}
			}
		})
	}
}

func TestReferenceWorkflowClassificationHasCurrentExecutableEvidence(t *testing.T) {
	root := capabilityRepositoryRoot(t)
	retained := []struct {
		domain string
		file   string
		marker string
	}{
		{"telegram administration", "internal/telegram/semantic_parity_test.go", "TestUpstreamOAuthIsDiscoverableAndAuthorizationResultIsTokenFree"},
		{"configuration import and canonical mutation", "internal/cli/config_test.go", "TestConfigSetSecretValueDoesNotLeakIntoPresentationOrDiagnostics"},
		{"workspace administration", "internal/application/workspace_relocate_test.go", "TestWorkspaceRelocateDispatcherUsesCanonicalRequest"},
		{"integration lifecycle", "internal/application/codegraph_test.go", "TestCodeGraphWorkspaceLifecyclePersistsSharedReadModel"},
	}
	for _, item := range retained {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(item.file)))
		if err != nil {
			t.Fatalf("read retained workflow evidence %s: %v", item.file, err)
		}
		if !strings.Contains(string(body), "func "+item.marker+"(") {
			t.Errorf("retained workflow %q lost executable evidence %s", item.domain, item.marker)
		}
	}

	rejected := []struct {
		domain string
		path   string
	}{
		{"multi-tunnel collection authority", "internal/application/tunnel_collection.go"},
		{"tunnel profile authority", "internal/application/tunnel_profiles.go"},
		{"tunnel admin profile authority", "internal/tunnel/admin_profile.go"},
		{"managed bash runtime fallback", "internal/runtime/bash"},
	}
	for _, item := range rejected {
		path := filepath.Join(root, filepath.FromSlash(item.path))
		if _, err := os.Stat(path); err == nil {
			t.Errorf("rejected reference behavior %q reappeared at %s", item.domain, item.path)
		} else if !os.IsNotExist(err) {
			t.Fatalf("inspect rejected reference behavior %s: %v", item.path, err)
		}
	}
}

func parityRowFor(report ProductParityReport, id ID) (ParityRow, bool) {
	for _, row := range report.Operations {
		if row.Operation == id {
			return row, true
		}
	}
	return ParityRow{}, false
}
