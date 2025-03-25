package workflow

import (
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
	next_nodes := n.GetNextNodes()
	if len(next_nodes) != 6 || next_nodes[0] != "n2" || next_nodes[1] != "n3" || next_nodes[4] != "node5" {
		t.Errorf("Expected next nodes to be [n2 n3 n4 node4 node5 node6], got %v", next_nodes)
	}
}
