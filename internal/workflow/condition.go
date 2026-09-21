package workflow

import (
	"fmt"
	"path"
	"reflect"
	"regexp"
	"strconv"
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

// Reference syntax:
//   - $result.<path> addresses the node's own ResultData payload (what the
//     node just produced).
//   - $args.<path> addresses the node's static arguments (Node.Arguments).
//   - $input.<path> (upstream dependency payloads) is NOT available in
//     conditions: routing runs on the node's own result, so use $result.
//     Referencing $input here returns an explicit error.
//   - An omitted or empty condition defaults to true: an entry that lists
//     nexts without a condition is an unconditional route.
func (c *condition) evaluate(actionArgs common.ActionArgs, inputs []any, result common.ResultData) (bool, error) {
	// An omitted (`"condition"` absent, nil) or empty (`{}`) condition is
	// an unconditional route. This also keeps GetNextNodes safe against
	// entries without a condition object.
	if c == nil || (c.LeftOperand == nil && c.Operator == "" && c.RightOperand == nil) {
		return true, nil
	}
	// we will need to test if the operands must be resolved, I'll follow the format
	// jsonpath format to resolve the values.

	// we'll make a copy of the operands
	leftOperand := c.LeftOperand
	rightOperand := c.RightOperand

	var err error
	leftOperand, err = resolveOperand(leftOperand, actionArgs, result)
	if err != nil {
		return false, err
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
	rightOperand, err = resolveOperand(rightOperand, actionArgs, result)
	if err != nil {
		return false, err
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
	case "=~":
		return matchRegex(leftOperand, rightOperand)
	case "glob":
		return matchGlob(leftOperand, rightOperand)
	default:
		return false, fmt.Errorf("unsupported operator: %s", c.Operator)
	}
}

// resolveOperand resolves $result. against the node's own payload and
// $args. against its static arguments. $input. (upstream) is rejected with
// an explicit error.
func resolveOperand(operand any, actionArgs common.ActionArgs, result common.ResultData) (any, error) {
	s, ok := operand.(string)
	if !ok {
		return operand, nil
	}
	switch {
	case strings.HasPrefix(s, "$result."):
		return resolveResult(result, s)
	case strings.HasPrefix(s, "$args."):
		return resolveArgs(actionArgs, s)
	case strings.HasPrefix(s, "$input."):
		return nil, fmt.Errorf("unsupported path %q: $input (upstream) is not available in conditions, use $result for own output", s)
	}
	return operand, nil
}

func resolveArgs(input common.ActionArgs, path string) (any, error) {
	const prefix = "$args."
	rest := strings.TrimPrefix(path, prefix)
	if rest == "" {
		return nil, fmt.Errorf("unsupported path %q: empty key after %q", path, prefix)
	}
	return lookupPath(input.Args, rest, path)
}

func resolveResult(result common.ResultData, path string) (any, error) {
	const prefix = "$result."
	rest := strings.TrimPrefix(path, prefix)
	if rest == path {
		return nil, fmt.Errorf("unsupported path %q: must start with $result", path)
	}
	if rest == "" {
		return nil, fmt.Errorf("unsupported path %q: empty key", path)
	}
	return lookupPath(result.Payload, rest, path)
}

// lookupPath walks dot-separated segments (with optional [index] suffixes)
// starting at root. Missing keys, type mismatches, and out-of-range indices
// return explicit errors instead of panicking. A leaf that resolves to nil
// (JSON null) returns (nil, nil).
func lookupPath(root any, rest, fullPath string) (any, error) {
	cur := root
	for _, seg := range strings.Split(rest, ".") {
		if seg == "" {
			return nil, fmt.Errorf("unsupported path %q: empty segment", fullPath)
		}
		field, indices, err := parseSegment(seg, fullPath)
		if err != nil {
			return nil, err
		}
		if field != "" {
			cur, err = lookupField(cur, field, fullPath)
			if err != nil {
				return nil, err
			}
		} else if len(indices) == 0 {
			return nil, fmt.Errorf("unsupported path %q: empty segment", fullPath)
		}
		for _, idx := range indices {
			cur, err = lookupIndex(cur, idx, fullPath)
			if err != nil {
				return nil, err
			}
		}
	}
	return cur, nil
}

// parseSegment splits e.g. "items[0][2]" into field "items" and indices [0 2].
// A segment like "[0]" yields an empty field with indices [0].
func parseSegment(seg, fullPath string) (string, []int, error) {
	open := strings.IndexByte(seg, '[')
	if open == -1 {
		return seg, nil, nil
	}
	field := seg[:open]
	rest := seg[open:]
	var indices []int
	for len(rest) > 0 {
		if !strings.HasPrefix(rest, "[") {
			return "", nil, fmt.Errorf("unsupported path %q: malformed segment %q", fullPath, seg)
		}
		closeIdx := strings.IndexByte(rest, ']')
		if closeIdx == -1 {
			return "", nil, fmt.Errorf("unsupported path %q: malformed segment %q", fullPath, seg)
		}
		n, err := strconv.Atoi(rest[1:closeIdx])
		if err != nil {
			return "", nil, fmt.Errorf("unsupported path %q: invalid index in segment %q", fullPath, seg)
		}
		indices = append(indices, n)
		rest = rest[closeIdx+1:]
	}
	return field, indices, nil
}

func derefValue(v reflect.Value) reflect.Value {
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return v
		}
		v = v.Elem()
	}
	return v
}

func lookupField(cur any, field, fullPath string) (any, error) {
	if cur == nil {
		return nil, fmt.Errorf("unsupported path %q: key %q not found (nil)", fullPath, field)
	}
	v := derefValue(reflect.ValueOf(cur))
	switch v.Kind() {
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("unsupported path %q: cannot resolve key %q on non-string map", fullPath, field)
		}
		key := reflect.ValueOf(field)
		if key.Type() != v.Type().Key() {
			if !key.CanConvert(v.Type().Key()) {
				return nil, fmt.Errorf("unsupported path %q: cannot resolve key %q", fullPath, field)
			}
			key = key.Convert(v.Type().Key())
		}
		mv := v.MapIndex(key)
		if !mv.IsValid() {
			return nil, fmt.Errorf("unsupported path %q: key %q not found", fullPath, field)
		}
		return mv.Interface(), nil
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			tag := strings.Split(f.Tag.Get("json"), ",")[0]
			if tag == field || f.Name == field {
				return v.Field(i).Interface(), nil
			}
		}
		return nil, fmt.Errorf("unsupported path %q: key %q not found", fullPath, field)
	default:
		return nil, fmt.Errorf("unsupported path %q: cannot resolve key %q on %s", fullPath, field, v.Kind())
	}
}

func lookupIndex(cur any, idx int, fullPath string) (any, error) {
	if cur == nil {
		return nil, fmt.Errorf("unsupported path %q: index %d out of range (nil)", fullPath, idx)
	}
	v := derefValue(reflect.ValueOf(cur))
	switch v.Kind() {
	case reflect.Slice, reflect.Array:
		if idx < 0 || idx >= v.Len() {
			return nil, fmt.Errorf("unsupported path %q: index %d out of range (len %d)", fullPath, idx, v.Len())
		}
		return v.Index(idx).Interface(), nil
	default:
		return nil, fmt.Errorf("unsupported path %q: cannot index %d on %s", fullPath, idx, v.Kind())
	}
}

// matchRegex reports whether the string value s matches the regular
// expression pattern. Both operands must already be resolved to strings:
// left is the value, right is the regex pattern (Go RE2 syntax, as in
// regexp.Compile). The pattern is unanchored, so use ^...$ to require a
// full match. An invalid pattern or non-string operand returns an explicit
// error, never a panic.
func matchRegex(left, right any) (bool, error) {
	leftStr, ok := left.(string)
	if !ok {
		return false, fmt.Errorf("operator \"=~\" requires string operands: left operand is %T (%v)", left, left)
	}
	rightStr, ok := right.(string)
	if !ok {
		return false, fmt.Errorf("operator \"=~\" requires string operands: right operand is %T (%v)", right, right)
	}
	re, err := regexp.Compile(rightStr)
	if err != nil {
		return false, fmt.Errorf("invalid regex pattern %q: %w", rightStr, err)
	}
	return re.MatchString(leftStr), nil
}

// matchGlob reports whether the string value s matches the glob pattern.
// Both operands must already be resolved to strings: left is the value
// (e.g. a file path or name), right is the glob pattern (e.g.
// "statement-*.pdf"). Matching uses path.Match semantics: '*' matches any
// sequence of non-separator characters, '?' matches any single
// non-separator character, '[...]' denotes character classes/ranges, and
// '\\' escapes. The pattern must match the entire value, and '/' is the
// separator ('*' does not cross it). As trigger payloads are full paths
// (e.g. "/home/u/Downloads/statement-x.pdf"), a non-matching full path
// falls back to matching path.Base(value), so "statement-*.pdf" matches.
// An invalid pattern or non-string operand returns an explicit error,
// never a panic.
func matchGlob(left, right any) (bool, error) {
	leftStr, ok := left.(string)
	if !ok {
		return false, fmt.Errorf("operator \"glob\" requires string operands: left operand is %T (%v)", left, left)
	}
	rightStr, ok := right.(string)
	if !ok {
		return false, fmt.Errorf("operator \"glob\" requires string operands: right operand is %T (%v)", right, right)
	}
	matched, err := path.Match(rightStr, leftStr)
	if err != nil {
		return false, fmt.Errorf("invalid glob pattern %q: %w", rightStr, err)
	}
	if matched {
		return true, nil
	}
	if strings.Contains(leftStr, "/") {
		baseMatched, err := path.Match(rightStr, path.Base(leftStr))
		if err != nil {
			return false, fmt.Errorf("invalid glob pattern %q: %w", rightStr, err)
		}
		return baseMatched, nil
	}
	return false, nil
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

	return entry.Condition.evaluate(common.ActionArgs{}, []any{}, common.ResultData{})
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
