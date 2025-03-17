package workflow

import (
	"fmt"
	"testing"
)

// ejemplo:
func TestCondition(t *testing.T) {
	// Example usage
	dict := NewConditionDictionary()

	// Add some entries
	dict.AddEntry("condition1", NewBooleanCondition(true), []string{"apple", "banana", "cherry"})
	dict.AddEntry("condition2", NewComparison(10, ">", 5), []string{"dog", "cat", "bird"})
	dict.AddEntry("condition3", NewBooleanCondition(false), []string{"red", "green", "blue"})

	// Evaluate and use entries
	strings1, _ := dict.GetStringsIfTrue("condition1")
	fmt.Println("Strings for condition1:", strings1) // Should print the strings

	strings2, _ := dict.GetStringsIfTrue("condition2")
	fmt.Println("Strings for condition2:", strings2) // Should print the strings

	strings3, _ := dict.GetStringsIfTrue("condition3")
	fmt.Println("Strings for condition3:", strings3) // Should print nil (condition is false)
}
