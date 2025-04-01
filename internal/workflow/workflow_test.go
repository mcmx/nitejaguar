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
		Conditions:  newConditionDictionary(),
	}
	n.Conditions.addEntry("c1", newBooleanCondition(true), []string{"n2", "n3", "n4"})
	n.Conditions.addEntry("condition2", newComparison(10, ">=", 5), []string{"node4", "node5", "node6"})
	test_nodes := []string{"n2", "n3", "n4", "node4", "node5", "node6"}
	slices.Sort(test_nodes)
	next_nodes := n.GetAllNextNodes()
	slices.Sort(next_nodes)
	if len(next_nodes) != 6 || !slices.Equal(next_nodes, test_nodes) {
		t.Errorf("Expected next nodes to be [n2 n3 n4 node4 node5 node6], got %v", next_nodes)
	}
}

// aId, _ := typeid.WithPrefix("trigger")

// crear un peque workflow
// w := workflow.Workflow{
// 	Name:  "First Workflow",
// 	Nodes: make(map[string]workflow.Node),
// }

// n := workflow.Node{
// 	Id:          aId.String(),
// 	Name:        "CLI Trigger: filechangeTrigger",
// 	Description: "CLI Trigger: filechangeTrigger",
// 	ActionType:  "trigger",
// 	ActionName:  "filechangeTrigger",
// 	Conditions:  workflow.NewConditionDictionary(),
// 	Arguments:   make(map[string]string),
// }
// n.Arguments["argument1"] = "/tmp"
// n.Conditions.AddEntry("c1", NewBooleanCondition(true), []string{"n2", "n3", "n4"})
// n.Conditions.AddEntry("condition2", NewComparison(10, ">=", 5), []string{"node4", "node5", "node6"})
// w.Nodes[n.Id] = n
