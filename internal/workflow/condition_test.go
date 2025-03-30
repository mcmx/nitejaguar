package workflow

import (
	"fmt"
	"testing"
)

// ejemplo:
func TestCondition(t *testing.T) {
	// Example usage
	dict := newConditionDictionary()

	// Add some entries
	dict.addEntry("condition1", newBooleanCondition(true), []string{"node1", "node2", "node3"})
	dict.addEntry("condition2", newComparison(10, ">=", 5), []string{"node4", "node5", "node6"})
	dict.addEntry("condition3", newBooleanCondition(false), []string{"red", "green", "blue"})
	dict.addEntry("condition4", newBooleanCondition(false), []string{"red", "green", "blue"})
	dict.removeEntry("condition4")
	_, exists := dict.getEntry("condition4")
	if exists {
		t.Error("condition4 should not exist")
	}
	// Evaluate and use entries
	strings1, _ := dict.getNextsIfTrue("condition1")
	if strings1 == nil {
		fmt.Println("Strings for condition1:", strings1) // Should print the strings
		t.Error("Strings for condition1 should not be nil")
	}

	strings2, _ := dict.getNextsIfTrue("condition2")
	c, e := dict.evaluateCondition("condition2")
	if e != nil {
		t.Errorf("Error: %v", e)
	}
	if !c {
		t.Error("Condition is false")
	}
	if strings2 == nil {
		fmt.Println("Strings for condition2:", strings2) // Should print the strings
		t.Error("Strings for condition2 should not be nil")
	}

	strings3, _ := dict.getNextsIfTrue("condition3")
	if strings3 != nil {
		t.Error("Strings for condition3 should be nil")
	}
}
