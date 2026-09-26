package mcp

import (
	"context"
	"testing"

	"go.mewis.me/codemcp/internal/sequence"
)

func TestResourceListChangeOverflowSignalsSingleResyncBoundary(t *testing.T) {
	registry := NewFeatureRegistry()
	registry.resourceListChanges = sequence.New[ResourceListChange](2, 1, func(change *ResourceListChange, value uint64) {
		change.Sequence = value
	})
	subscription, snapshot := registry.SubscribeResourceListChanges(0)
	defer registry.UnsubscribeResourceListChanges(subscription)
	if snapshot.LatestSequence != 0 {
		t.Fatalf("initial resource list snapshot=%#v", snapshot)
	}

	register := func(id string) {
		t.Helper()
		uri, err := GlobalResourceURI("overflow/" + id)
		if err != nil {
			t.Fatal(err)
		}
		if err := registry.Register(FeatureRegistration{
			ID: id, Family: FeatureResources,
			Resources: []ResourceDescriptor{{
				URI: uri, Name: id, MIMEType: "text/plain",
				Policy: ResourcePolicy{Cache: ResourceCachePolicy{Scope: ResourceCacheScopePrivate}},
			}},
			ReadResource: func(context.Context, ResourceReadRequest) (ResourceContent, error) {
				return TextResourceContent("ok"), nil
			},
		}); err != nil {
			t.Fatal(err)
		}
	}

	register("one")
	register("two")
	register("three")
	overflow := <-subscription.Overflow
	if overflow.DroppedSequence != 2 {
		t.Fatalf("overflow=%#v", overflow)
	}
	select {
	case extra := <-subscription.Overflow:
		t.Fatalf("overflow repeated before resync acknowledgement=%#v", extra)
	default:
	}

	// A level-triggered resources/list_changed notification is the resync
	// boundary: after the client lists again, acknowledging overflow re-enables
	// bounded delivery without replaying every dropped mutation.
	registry.AcknowledgeResourceListOverflow(subscription)
	<-subscription.Events
	register("four")
	if event := <-subscription.Events; event.Sequence != 4 {
		t.Fatalf("post-resync resource list event=%#v", event)
	}
}

func TestResourceSubscriptionAuthorizationUsesDescriptorAndWorkspaceFence(t *testing.T) {
	runtime, first, second := newCompletionWorkspaceRuntime(t)
	executor := NewFeatureExecutor(FeatureRegistryForRuntime(runtime), runtime, first.ID, "test")
	allowed, _ := WorkspaceResourceURI(first.ID, resourcePathProjectContext)
	if _, err := executor.AuthorizeResourceSubscription(context.Background(), allowed); err != nil {
		t.Fatalf("authorized resource subscription: %v", err)
	}
	denied, _ := WorkspaceResourceURI(second.ID, resourcePathProjectContext)
	if _, err := executor.AuthorizeResourceSubscription(context.Background(), denied); err == nil {
		t.Fatal("workspace subscription escaped executor binding")
	}
	if _, err := executor.AuthorizeResourceSubscription(context.Background(), "cm://global/status"); err == nil {
		t.Fatal("resource without canonical change owner accepted a subscription")
	}
}
