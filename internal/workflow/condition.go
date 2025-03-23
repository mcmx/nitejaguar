package workflow

import (
	"fmt"
	"reflect"
)


type Condition struct {
	// Can be a standalone boolean or a value to compare
	LeftOperand any

	// Can be empty for standalone boolean expressions
	Operator string

	// Can be nil for standalone boolean expressions
	RightOperand any
}

// NewComparison creates a comparison-based condition
func NewComparison(left any, operator string, right any) *Condition {
	return &Condition{
		LeftOperand:  left,
		Operator:     operator,
		RightOperand: right,
	}
}

// NewBooleanCondition creates a simple boolean condition
func NewBooleanCondition(boolExpr any) *Condition {
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
func (cd *ConditionDictionary) AddEntry(id string, condition *Condition, next_nodes []string) {
	cd.Entries[id] = ConditionEntry{
		Condition: condition,
		Nexts:     next_nodes,
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
func (cd *ConditionDictionary) GetNextsIfTrue(id string) ([]string, error) {
	result, err := cd.EvaluateCondition(id)
	if err != nil {
		return nil, err
	}

	if !result {
		return nil, nil // Condition is false
	}

	return cd.Entries[id].Nexts, nil
}