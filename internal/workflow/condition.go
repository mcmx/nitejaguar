package workflow

import (
	"fmt"
	"reflect"
)

type condition struct {
	// Can be a standalone boolean or a value to compare
	LeftOperand any `json:"leftOperand"`

	// Can be empty for standalone boolean expressions
	Operator string `json:"operator"`

	// Can be nil for standalone boolean expressions
	RightOperand any `json:"rightOperand"`
}

// NewComparison creates a comparison-based condition
func newComparison(left any, operator string, right any) *condition {
	return &condition{
		LeftOperand:  left,
		Operator:     operator,
		RightOperand: right,
	}
}

// NewBooleanCondition creates a simple boolean condition
func newBooleanCondition(boolExpr any) *condition {
	return &condition{
		LeftOperand: boolExpr,
	}
}

func (c *condition) evaluate() (bool, error) {
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
func compareValues(left, right any, op string) (bool, error) {
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

// Helper function to convert any to float64
func toFloat64(v any) (float64, bool) {
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

// ConditionEntry associates a condition with a list of strings
type conditionEntry struct {
	Condition *condition `json:"condition"`
	Nexts     []string   `json:"nexts"`
}

// ConditionDictionary maps condition IDs to condition entries
type conditionDictionary struct {
	Entries map[string]conditionEntry `json:"entries"`
}

// NewConditionDictionary creates a new condition dictionary
func newConditionDictionary() *conditionDictionary {
	return &conditionDictionary{
		Entries: make(map[string]conditionEntry),
	}
}

// AddEntry adds or updates an entry in the dictionary
func (cd *conditionDictionary) addEntry(id string, condition *condition, next_nodes []string) {
	cd.Entries[id] = conditionEntry{
		Condition: condition,
		Nexts:     next_nodes,
	}
}

// GetEntry retrieves an entry by ID
func (cd *conditionDictionary) getEntry(id string) (conditionEntry, bool) {
	entry, exists := cd.Entries[id]
	return entry, exists
}

// RemoveEntry removes an entry by ID
func (cd *conditionDictionary) removeEntry(id string) {
	delete(cd.Entries, id)
}

// EvaluateCondition evaluates the condition for a specific entry
func (cd *conditionDictionary) evaluateCondition(id string) (bool, error) {
	entry, exists := cd.Entries[id]
	if !exists {
		return false, fmt.Errorf("condition ID not found: %s", id)
	}

	return entry.Condition.evaluate()
}

// GetStringsIfTrue returns the string list if the condition evaluates to true
func (cd *conditionDictionary) getNextsIfTrue(id string) ([]string, error) {
	result, err := cd.evaluateCondition(id)
	if err != nil {
		return nil, err
	}

	if !result {
		return nil, nil // Condition is false
	}

	return cd.Entries[id].Nexts, nil
}
