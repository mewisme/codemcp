package capability

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestTelegramRolloutInventoryStagesCompletedAndFutureCoverage(t *testing.T) {
	items := TelegramRolloutInventory()
	if len(items) == 0 {
		t.Fatal("Telegram rollout inventory is empty")
	}
	if SurfaceActive(SurfaceTelegram) {
		t.Fatal("staged Telegram rollout activated the global surface before final parity")
	}

	wantSorted := append([]TelegramRolloutItem(nil), items...)
	sort.Slice(wantSorted, func(i, j int) bool { return wantSorted[i].ID < wantSorted[j].ID })
	if !reflect.DeepEqual(items, wantSorted) {
		t.Fatal("Telegram rollout inventory is not sorted")
	}

	seenIDs := map[string]bool{}
	coveredOperations := map[ID]bool{}
	planned, exempt := 0, 0
	for _, item := range items {
		if strings.TrimSpace(item.ID) == "" || seenIDs[item.ID] {
			t.Fatalf("invalid or duplicate Telegram rollout id %q", item.ID)
		}
		seenIDs[item.ID] = true
		if !validTelegramRolloutState(item.State) || !validTelegramRolloutStage(item.Stage) || !validTelegramOwner(item.Owner) {
			t.Fatalf("Telegram rollout item has unbounded metadata: %#v", item)
		}
		if item.Operation != "" {
			if _, ok := Lookup(item.Operation); !ok {
				t.Fatalf("Telegram rollout item %s references unknown operation %s", item.ID, item.Operation)
			}
			coveredOperations[item.Operation] = true
		}
		switch item.State {
		case TelegramRolloutLive:
			if len(item.EntryPoints) == 0 || item.Reason != "" {
				t.Fatalf("live Telegram item lacks entry point or has exemption reason: %#v", item)
			}
			for _, entry := range item.EntryPoints {
				if !validTelegramEntryPointKind(entry.Kind) || strings.TrimSpace(entry.Value) == "" {
					t.Fatalf("live Telegram item has invalid entry point: %#v", item)
				}
			}
		case TelegramRolloutPlanned:
			planned++
			if len(item.EntryPoints) != 0 || item.Reason != "" {
				t.Fatalf("planned Telegram item claims live entry point or exemption: %#v", item)
			}
		case TelegramRolloutExempt:
			exempt++
			if len(item.EntryPoints) != 0 || strings.TrimSpace(item.Reason) == "" {
				t.Fatalf("exempt Telegram item lacks bounded reason: %#v", item)
			}
		}
	}
	if planned == 0 || exempt == 0 {
		t.Fatalf("Telegram rollout must preserve future and exempt work: planned=%d exempt=%d", planned, exempt)
	}

	for _, spec := range All() {
		if spec.Audience != AudienceOperator && spec.Audience != AudienceReviewer {
			continue
		}
		if !coveredOperations[spec.ID] {
			t.Fatalf("human operation %s has no staged Telegram rollout attribution", spec.ID)
		}
	}
}

func TestTelegramRolloutCompletedContractsRemainLive(t *testing.T) {
	items := map[string]TelegramRolloutItem{}
	for _, item := range TelegramRolloutInventory() {
		items[item.ID] = item
	}
	tests := []struct {
		id        string
		stage     TelegramRolloutStage
		owner     TelegramOwner
		operation ID
	}{
		{"runtime.polling", TelegramStageRuntimeAuthorization, TelegramOwnerRuntime, ""},
		{"runtime.authorization", TelegramStageRuntimeAuthorization, TelegramOwnerRuntime, ""},
		{"setup.pairing", TelegramStageSetupPairing, TelegramOwnerSetup, TelegramSetup},
		{"setup.token.read", TelegramStageSetupPairing, TelegramOwnerSetup, ConfigGet},
		{"setup.token.write", TelegramStageSetupPairing, TelegramOwnerSetup, ConfigSet},
		{"setup.logout", TelegramStageSetupPairing, TelegramOwnerSetup, ""},
		{"navigation.home", TelegramStageNavigationDispatch, TelegramOwnerNavigation, ""},
		{"navigation.status", TelegramStageNavigationDispatch, TelegramOwnerNavigation, StatusOverview},
		{"navigation.commands", TelegramStageNavigationDispatch, TelegramOwnerNavigation, ""},
		{"navigation.callback", TelegramStageNavigationDispatch, TelegramOwnerNavigation, ""},
		{"navigation.dispatch", TelegramStageNavigationDispatch, TelegramOwnerNavigation, ""},
	}
	for _, test := range tests {
		item, ok := items[test.id]
		if !ok || item.State != TelegramRolloutLive || item.Stage != test.stage || item.Owner != test.owner || item.Operation != test.operation {
			t.Fatalf("completed Telegram contract %s=%#v", test.id, item)
		}
	}
}

func TestTelegramRolloutFutureOwnershipIsExplicit(t *testing.T) {
	byOperation := map[ID][]TelegramRolloutItem{}
	for _, item := range TelegramRolloutInventory() {
		if item.Operation != "" {
			byOperation[item.Operation] = append(byOperation[item.Operation], item)
		}
	}
	assertFuture := func(id ID, state TelegramRolloutState, stage TelegramRolloutStage, owner TelegramOwner) {
		t.Helper()
		for _, item := range byOperation[id] {
			if item.ID != "operation."+string(id) {
				continue
			}
			if item.State != state || item.Stage != stage || item.Owner != owner {
				t.Fatalf("future Telegram contract %s=%#v", id, item)
			}
			return
		}
		t.Fatalf("future Telegram operation %s missing", id)
	}
	assertFuture(WorkspaceList, TelegramRolloutPlanned, TelegramStageWorkspaceApproval, TelegramOwnerWorkspace)
	assertFuture(RequestApprove, TelegramRolloutPlanned, TelegramStageWorkspaceApproval, TelegramOwnerApproval)
	assertFuture(UpstreamServerList, TelegramRolloutPlanned, TelegramStageNetworkUpstream, TelegramOwnerNetwork)
	assertFuture(ConfigList, TelegramRolloutPlanned, TelegramStageSettingsIntegration, TelegramOwnerSettings)
	assertFuture(IntegrationRTKStatus, TelegramRolloutPlanned, TelegramStageSettingsIntegration, TelegramOwnerIntegration)
	assertFuture(CompletionList, TelegramRolloutPlanned, TelegramStageCompletion, TelegramOwnerCompletion)
	assertFuture(TunnelList, TelegramRolloutExempt, TelegramStageNetworkUpstream, TelegramOwnerNetwork)
	assertFuture(ConfigPath, TelegramRolloutExempt, TelegramStageParityResilience, TelegramOwnerLocal)
}
