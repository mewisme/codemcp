package quickopen

import "testing"

func TestResourcesConvertToSearchActions(t *testing.T) {
	actions, index := Actions([]Resource{{ID: "ws_abc", Title: "ws_abc", Kind: "Workspace", Description: "/tmp/project", Path: []string{"workspace", "ws_abc"}}})
	if len(actions) != 1 || actions[0].Category != "Workspace" || len(index) != 1 {
		t.Fatalf("actions=%#v index=%#v", actions, index)
	}
	for id, resource := range index {
		if actions[0].ID != id || resource.ID != "ws_abc" {
			t.Fatalf("id=%q action=%#v resource=%#v", id, actions[0], resource)
		}
	}
}
