package workflow

import "testing"

func TestNodeAssignedToDefaults(t *testing.T) {
	wf := Workflow{
		Id:                "workflow_test",
		DefaultClientTags: []string{"gpu"},
		Nodes: map[string]Node{
			"n1": {Id: "n1"},
			"n2": {Id: "n2", ClientTags: []string{"cpu"}},
			"n3": {Id: "n3", Client: "client_specific"},
		},
	}
	// n1 inherits workflow default: gpu only.
	if !wf.NodeAssignedTo(wf.Nodes["n1"], "any-id", []string{"gpu"}) {
		t.Fatal("n1 should be assigned to gpu via default")
	}
	if wf.NodeAssignedTo(wf.Nodes["n1"], "any-id", []string{"cpu"}) {
		t.Fatal("n1 should not be assigned to cpu via gpu default")
	}
	// n2 overrides default with cpu tags.
	if !wf.NodeAssignedTo(wf.Nodes["n2"], "any-id", []string{"cpu"}) {
		t.Fatal("n2 override should assign cpu")
	}
	if wf.NodeAssignedTo(wf.Nodes["n2"], "any-id", []string{"gpu"}) {
		t.Fatal("n2 override should not assign gpu")
	}
	// n3 overrides with explicit client id.
	if !wf.NodeAssignedTo(wf.Nodes["n3"], "client_specific", nil) {
		t.Fatal("n3 explicit client should match")
	}
	if wf.NodeAssignedTo(wf.Nodes["n3"], "other", []string{"gpu"}) {
		t.Fatal("n3 explicit client should not match others")
	}
	// No defaults: broadcast.
	plain := Workflow{Id: "w2", Nodes: map[string]Node{"a": {Id: "a"}}}
	if !plain.NodeAssignedTo(plain.Nodes["a"], "anyone", nil) {
		t.Fatal("node without targeting should broadcast")
	}
}
