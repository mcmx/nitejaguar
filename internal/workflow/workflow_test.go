package workflow

import (
	"slices"
	"testing"
)

func TestGetNextNodes(t *testing.T) {
	n := &Node{
		Id:          "n1",
		Name:        "n1",
		Description: "n1",
		ActionType:  "trigger",
		ActionName:  "triggerFile",
		Conditions:  NewConditionDictionary(),
	}
	n.Conditions.AddEntry("c1", NewBooleanCondition(true), []string{"n2", "n3", "n4"})
	n.Conditions.AddEntry("condition2", NewComparison(10, ">=", 5), []string{"node4", "node5", "node6"})
	test_nodes := []string{"n2", "n3", "n4", "node4", "node5", "node6"}
	slices.Sort(test_nodes)
	next_nodes := n.GetNextNodes()
	slices.Sort(next_nodes)
	if len(next_nodes) != 6 || !slices.Equal(next_nodes, test_nodes) {
		t.Errorf("Expected next nodes to be [n2 n3 n4 node4 node5 node6], got %v", next_nodes)
	}
}
