package workflow

import (
	"fmt"
	"reflect"

	"github.com/google/uuid"
	"github.com/mcmx/nitejaguar/common"
)

type Workflow struct {
	Id          string                   `json:"id"`
	Name        string                   `json:"name"`
	TriggerList map[string]common.Action `json:"triggers"`
}

type WorkflowManager struct {
	Workflows map[string]Workflow
}

type Condition struct {
	// Can be a standalone boolean or a value to compare
	LeftOperand interface{}

	// Can be empty for standalone boolean expressions
	Operator string

	// Can be nil for standalone boolean expressions
	RightOperand interface{}
}

// NewComparison creates a comparison-based condition
func NewComparison(left interface{}, operator string, right interface{}) *Condition {
	return &Condition{
		LeftOperand:  left,
		Operator:     operator,
		RightOperand: right,
	}
}

// NewBooleanCondition creates a simple boolean condition
func NewBooleanCondition(boolExpr interface{}) *Condition {
	return &Condition{
		LeftOperand: boolExpr,
	}
}

func (c *Condition) Evaluate() (bool, error) {
	// Handle the case of a standalone boolean expression
	if c.Operator == "" && c.RightOperand == nil {
		// Try to convert LeftOperand to boolean
		boolValue, ok := c.LeftOperand.(bool)
		if !ok {
			// If it's not a direct boolean, try to evaluate it as an expression
			// (This would depend on your implementation)
			return false, fmt.Errorf("left operand is not a boolean: %v", c.LeftOperand)
		}
		return boolValue, nil
	}

	// Handle comparison operators as before
	switch c.Operator {
	case "==":
		return reflect.DeepEqual(c.LeftOperand, c.RightOperand), nil
	case "!=":
		return !reflect.DeepEqual(c.LeftOperand, c.RightOperand), nil
	case ">":
		return compareValues(c.LeftOperand, c.RightOperand, ">")
	case ">=":
		return compareValues(c.LeftOperand, c.RightOperand, ">=")
	case "<":
		return compareValues(c.LeftOperand, c.RightOperand, "<")
	case "<=":
		return compareValues(c.LeftOperand, c.RightOperand, "<=")
	default:
		return false, fmt.Errorf("unsupported operator: %s", c.Operator)
	}
}

// Helper function for comparing numerical values (unchanged)
func compareValues(left, right interface{}, op string) (bool, error) {
	// Implementation as before
	leftFloat, leftOk := toFloat64(left)
	rightFloat, rightOk := toFloat64(right)

	if !leftOk || !rightOk {
		return false, fmt.Errorf("cannot compare non-numeric values with %s", op)
	}

	switch op {
	case ">":
		return leftFloat > rightFloat, nil
	case ">=":
		return leftFloat >= rightFloat, nil
	case "<":
		return leftFloat < rightFloat, nil
	case "<=":
		return leftFloat <= rightFloat, nil
	default:
		return false, fmt.Errorf("invalid comparison operator: %s", op)
	}
}

// Helper function to convert interface{} to float64
func toFloat64(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case int:
		return float64(val), true
	case int8:
		return float64(val), true
	case int16:
		return float64(val), true
	case int32:
		return float64(val), true
	case int64:
		return float64(val), true
	case uint:
		return float64(val), true
	case uint8:
		return float64(val), true
	case uint16:
		return float64(val), true
	case uint32:
		return float64(val), true
	case uint64:
		return float64(val), true
	case float32:
		return float64(val), true
	case float64:
		return val, true
	default:
		return 0, false
	}
}

// type Node
type Node struct {
	Id          string              `json:"id"`
	Description string              `json:"description"`
	Type        string              `json:"type"`       // trigger or action
	Action      string              `json:"action"`     // the type could be infered from this, it's to make it faster
	Conditions  ConditionDictionary `json:"conditions"` // next Node's id... TODO I'm not happy with this I need a list with conditions or no condition at all
}

// ConditionEntry associates a condition with a list of strings
type ConditionEntry struct {
	Condition *Condition `json:"condition"`
	Nexts     []string   `json:"nexts"`
}

// ConditionDictionary maps condition IDs to condition entries
type ConditionDictionary struct {
	Entries map[string]ConditionEntry `json:"entries"`
}

// NewConditionDictionary creates a new condition dictionary
func NewConditionDictionary() *ConditionDictionary {
	return &ConditionDictionary{
		Entries: make(map[string]ConditionEntry),
	}
}

// AddEntry adds or updates an entry in the dictionary
func (cd *ConditionDictionary) AddEntry(id string, condition *Condition, strings []string) {
	cd.Entries[id] = ConditionEntry{
		Condition: condition,
		Strings:   strings,
	}
}

// GetEntry retrieves an entry by ID
func (cd *ConditionDictionary) GetEntry(id string) (ConditionEntry, bool) {
	entry, exists := cd.Entries[id]
	return entry, exists
}

// RemoveEntry removes an entry by ID
func (cd *ConditionDictionary) RemoveEntry(id string) {
	delete(cd.Entries, id)
}

// EvaluateCondition evaluates the condition for a specific entry
func (cd *ConditionDictionary) EvaluateCondition(id string) (bool, error) {
	entry, exists := cd.Entries[id]
	if !exists {
		return false, fmt.Errorf("condition ID not found: %s", id)
	}

	return entry.Condition.Evaluate()
}

// GetStringsIfTrue returns the string list if the condition evaluates to true
func (cd *ConditionDictionary) GetStringsIfTrue(id string) ([]string, error) {
	result, err := cd.EvaluateCondition(id)
	if err != nil {
		return nil, err
	}

	if !result {
		return nil, nil // Condition is false
	}

	return cd.Entries[id].Strings, nil
}

func (w *WorkflowManager) AddWorkflow(data Workflow) {
	if data.Id == "" {
		data.Id = uuid.New().String()
	}
	w.Workflows[data.Id] = data
}

// ejemplo:
func Main_test() {
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
