package workflow

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/mcmx/nitejaguar/common"
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

// input corresponds to the action arguments input that the action received
// result corresponds to the result of the action
// either operand can be a variable name that will be resolved from the input or result
// or neither in which is can be considered a constant value and should not be resolved
func (c *condition) evaluate(input common.ActionArgs, result common.ResultData) (bool, error) {
	// we will need to test if the operands must be resolved, I'll follow the format
	// jsonpath format to resolve the values.

	// we'll make a copy of the operands
	leftOperand := c.LeftOperand
	rightOperand := c.RightOperand

	if reflect.TypeOf(leftOperand).Kind() == reflect.String {
		if leftOperand != nil && strings.HasPrefix(leftOperand.(string), "$.input.") {
			leftOperand = resolveInput(input.Args, leftOperand.(string))
		}
		if leftOperand != nil && strings.HasPrefix(leftOperand.(string), "$.result.") {
			leftOperand = resolveResult(result.Payload, leftOperand.(string))
		}
	}

	// Handle the case of a standalone boolean expression
	if c.Operator == "" && rightOperand == nil {
		// Try to convert LeftOperand to boolean
		boolValue, ok := leftOperand.(bool)
		if !ok {
			// If it's not a direct boolean, try to evaluate it as an expression
			// (This would depend on your implementation)
			return false, fmt.Errorf("left operand is not a boolean: %v", leftOperand)
		}
		return boolValue, nil
	}
	if reflect.TypeOf(rightOperand).Kind() == reflect.String {
		if rightOperand != nil && strings.HasPrefix(rightOperand.(string), "$.input.") {
			rightOperand = resolveInput(input.Args, rightOperand.(string))
		}

		if rightOperand != nil && strings.HasPrefix(rightOperand.(string), "$.result.") {
			rightOperand = resolveResult(result.Payload, rightOperand.(string))
		}
	}

	// Handle comparison operators as before
	switch c.Operator {
	case "==":
		return reflect.DeepEqual(leftOperand, rightOperand), nil
	case "!=":
		return !reflect.DeepEqual(leftOperand, rightOperand), nil
	case ">":
		return compareValues(leftOperand, rightOperand, ">")
	case ">=":
		return compareValues(leftOperand, rightOperand, ">=")
	case "<":
		return compareValues(leftOperand, rightOperand, "<")
	case "<=":
		return compareValues(leftOperand, rightOperand, "<=")
	default:
		return false, fmt.Errorf("unsupported operator: %s", c.Operator)
	}
}

func resolveInput(input map[string]string, path string) any {
	return input[path]
}

func resolveResult(result any, path string) any {
	return result
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
	// TODO: check this, does it make any sense now?

	return entry.Condition.evaluate(common.ActionArgs{}, common.ResultData{})
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
