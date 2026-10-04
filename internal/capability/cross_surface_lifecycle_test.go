package capability

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type lifecycleEvidence struct {
	domain  string
	file    string
	markers []string
}

func TestCrossSurfaceLifecycleRegressionEvidenceMatrix(t *testing.T) {
	root := capabilityRepositoryRoot(t)
	evidence := []lifecycleEvidence{
		{
			domain: "native instruction authoring and source precedence",
			file:   "internal/application/instruction_authoring_test.go",
			markers: []string{
				"TestInstructionAuthoringWorkspaceCreateUpdateDryRunAndResolverVisibility",
				"TestInstructionAuthoringGlobalRequiresOperatorAndPreservesManagedPolicy",
			},
		},
		{
			domain: "agent authoring scope rejection before mutation",
			file:   "internal/tools/instruction_authoring_external_test.go",
			markers: []string{
				"TestInstructionAuthoringToolsUseCanonicalWorkspaceOwnerAndExposeBuiltinGuidance",
				"TestInstructionAuthoringToolsRemainWorkspaceBoundAndFailClosed",
			},
		},
		{
			domain:  "dynamic instruction provider precedence",
			file:    "internal/instructionsource/contract_test.go",
			markers: []string{"TestSourceClassPrecedenceAndDynamicProviderOrder"},
		},
		{
			domain:  "rule provider precedence",
			file:    "internal/rules/resolver_test.go",
			markers: []string{"TestDiscoverWithUserForWorkspaceIgnoresLegacyProviderPolicyAndKeepsSourcePrecedence"},
		},
		{
			domain:  "skill provider precedence",
			file:    "internal/skills/resolver_test.go",
			markers: []string{"TestDiscoverWithUserForWorkspaceLoadsOnlyNativeGlobalSkills"},
		},
		{
			domain: "workspace identity reconnect",
			file:   "internal/workspace/reconnect_test.go",
			markers: []string{
				"TestRegisterReconnectsMissingRootPreservingIdentityReferencesAndState",
				"TestRegisterReconnectConflictsDoNotMutateRegistry",
			},
		},
		{
			domain: "duplicate workspace relocation canonical result",
			file:   "internal/application/workspace_relocate_test.go",
			markers: []string{
				"TestWorkspaceRelocateConflictReadModelAndExplicitResolution",
				"TestWorkspaceRelocateDispatcherUsesCanonicalRequest",
			},
		},
		{
			domain:  "workspace relocation admin projection",
			file:    "internal/interface/admin/api_test.go",
			markers: []string{"TestWorkspaceAPIRelocateDuplicateConflictAndResolution"},
		},
		{
			domain:  "workspace relocation tui projection",
			file:    "internal/interface/tui/page/workspace_test.go",
			markers: []string{"TestWorkspaceRelocateEditorUsesFinalEnterSubmit"},
		},
		{
			domain: "workspace relocation telegram projection",
			file:   "internal/telegram/workspace_approval_test.go",
			markers: []string{
				"TestRelocationConflictNeverChoosesRemoteResolution",
				"TestWorkspaceActionInputsRemainTypedCanonicalInputs",
			},
		},
		{
			domain:  "workspace relocation cli projection",
			file:    "internal/cli/workspace_relocate_test.go",
			markers: []string{"TestWorkspaceRelocateCommandExposesResolveFlag"},
		},
		{
			domain: "canonical settings mutation and read-after-write",
			file:   "internal/application/settings_test.go",
			markers: []string{
				"TestSettingServiceStaticMutationMatchesCanonicalConfigAuthority",
				"TestSettingServiceApplyIsAtomicAndReloadsRunningRuntimeOnce",
			},
		},
		{
			domain: "settings cli facade parity",
			file:   "internal/cli/scoped_settings_test.go",
			markers: []string{
				"TestScopedAndUniversalStaticSettingParity",
				"TestTunnelAdminGenericScopedAndFlagParity",
			},
		},
		{
			domain:  "settings admin projection",
			file:    "internal/interface/admin/api_test.go",
			markers: []string{"TestConfigAPIMutationUsesCanonicalSettingTransaction"},
		},
		{
			domain:  "settings telegram projection",
			file:    "internal/telegram/settings_integrations_test.go",
			markers: []string{"TestSettingDetailUsesCanonicalCapabilityMetadata"},
		},
		{
			domain:  "concurrent approval resolution",
			file:    "internal/approval/parity_test.go",
			markers: []string{"TestTelegramTUIBrowserResolutionRaceHasOneTerminalTruth"},
		},
		{
			domain: "background completion with live observers",
			file:   "internal/backgrounddelivery/broker_test.go",
			markers: []string{
				"TestBrokerRealtimeObservationDoesNotConsumeOrSuppressDelivery",
				"TestUIExecutionFeedAttachDetachCannotConsumeModelDelivery",
			},
		},
		{
			domain: "agent completion hooks remain independent",
			file:   "internal/history/completion/hooks_test.go",
			markers: []string{
				"TestCompletionHooksRunOnlyAfterDurableAcceptanceAndCannotRollbackTruth",
				"TestCompletionHookInvocationIsIndependentValuePerHook",
			},
		},
		{
			domain:  "codegraph completion hook",
			file:    "internal/tools/codegraph_test.go",
			markers: []string{"TestCodeGraphSystemSymlinkDrivesExploreAndCompletionHook"},
		},
		{
			domain:  "completion notifications cannot change completion truth",
			file:    "internal/app/app_test.go",
			markers: []string{"TestAcceptedCompletionNotificationFailureDoesNotChangeCompletionTruth"},
		},
		{
			domain: "single tunnel lifecycle source truth",
			file:   "internal/tunnel/tunnel_test.go",
			markers: []string{
				"TestTunnelLifecycleObserverReportsConnectingReadyAndStopped",
				"TestTunnelBackendShutdownReconnects",
				"TestReconcileConvergesOneSingleTunnelClient",
			},
		},
		{
			domain:  "single tunnel cross-interface activity projection",
			file:    "internal/app/app_test.go",
			markers: []string{"TestTunnelLifecyclePublishesActivityFromSourceObserver"},
		},
		{
			domain:  "telegram runtime reconnect",
			file:    "internal/telegram/runtime_test.go",
			markers: []string{"ReconnectCount"},
		},
		{
			domain:  "telegram mini app realtime reconnect",
			file:    "frontend/src/mini-app/app.test.tsx",
			markers: []string{"suspends the realtime socket while Telegram marks the Mini App inactive and reconnects on activation"},
		},
		{
			domain:  "browser admin realtime reconnect",
			file:    "frontend/src/pages/activity.test.tsx",
			markers: []string{"reconnects the activity stream without reloading the page"},
		},
		{
			domain: "tui realtime reconnect and pinned detail",
			file:   "internal/interface/tui/page/logs_test.go",
			markers: []string{
				"TestLogsPageStreamOpenAndReconnectBranches",
				"TestToolCallReconnectSnapshotUpdatesPinnedDetailInPlace",
				"TestExecutionReconnectSnapshotSchedulesCanonicalPinnedRefresh",
			},
		},
		{
			domain: "runtime shutdown with active subscriptions",
			file:   "internal/app/app_test.go",
			markers: []string{
				"TestStopShutsDownCompletionHookBus",
				"TestStopCancelsPendingApprovalsBeforeRuntimeTeardown",
				"TestStopDetachesApprovalNotifications",
			},
		},
		{
			domain: "shared stream shutdown and consumer isolation",
			file:   "internal/sequence/stream_test.go",
			markers: []string{
				"TestSubscriptionCloseDisposesOnlyThatConsumer",
				"TestStreamCloseDisposesSubscribersAndRejectsNewLiveAttachment",
				"TestSubscribeSnapshotBarrierHasNoGapOrDuplicateDuringConcurrentPublish",
			},
		},
		{
			domain: "tui local race prerequisites",
			file:   "internal/interface/tui/page/logs_test.go",
			markers: []string{
				"TestLogsBrowserMouseTargetAndSelectionSurviveLiveRebuild",
				"TestLogsTimelineWindowsStartNewestExpandAndKeepLogicalRecordsWhole",
				"TestRuntimeDetailStaysPinnedAcrossUnrelatedLiveReplayAndClear",
				"TestLogsCloseFencesQueuedFeedAndDetailMessages",
			},
		},
		{
			domain:  "universal operation and surface gate",
			file:    "internal/capability/universal_gate_test.go",
			markers: []string{"TestUniversalOperationAndSurfaceGate"},
		},
		{
			domain: "universal mutation ownership gate",
			file:   "internal/capability/mutation_ownership_test.go",
			markers: []string{
				"TestEveryMutationHasOneCanonicalOwner",
				"TestHumanAdaptersCannotOwnCanonicalBusinessMutations",
			},
		},
	}

	for _, item := range evidence {
		t.Run(item.domain, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(item.file)))
			if err != nil {
				t.Fatalf("read lifecycle evidence file %s: %v", item.file, err)
			}
			for _, marker := range item.markers {
				if !strings.Contains(string(body), marker) {
					t.Errorf("cross-surface lifecycle evidence %q lost marker %q in %s", item.domain, marker, item.file)
				}
			}
		})
	}
}
