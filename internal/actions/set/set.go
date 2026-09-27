// Package set implements the `set` workflow action: it builds a new
// data payload from fixed values and upstream references.
//
// Two modes (arg `mode`, default `manual`):
//   - manual: assign individual fields from the `fields` JSON object
//     and/or `field.<name>` args. String values may embed
//     `$input.<path>` references resolved against the upstream result
//     payload. A value that is exactly one reference keeps its native
//     type (number, boolean, object, ...); embedded references are
//     interpolated as strings.
//   - json: parse the `json` template string (after resolving any
//     embedded `$input.<path>` references and `{{ ... }}` templates)
//     as a JSON object. Unquoted references keep native types, quoted
//     ones become strings.
//
// Value templates (`{{ ... }}`): every value also accepts
// `{{ $input.<path> }}` placeholders with an optional `|` filter
// chain (`upper`, `lower`, `trim`, `trimPrefix`, `trimSuffix`,
// `replace`, `default`), e.g. `"{{ $input.file }}.bkp"` or
// `"{{ $input.name | upper }}"`. A value that is exactly one
// filter-free template keeps the referenced native type; filtered or
// embedded templates produce strings. Templates expand before bare
// `$input.<path>` references, so both can mix in one value.
//
// Common args:
//   - keep_only_set (default false): true outputs only the assigned
//     fields; false merges the upstream payload underneath (assigned
//     fields win, nested objects merge recursively).
//   - dot_notation (default true): a field name containing dots
//     (`address.city`) builds nested objects instead of a literal key.
//   - ignore_type_errors (default false): skip assignments that fail
//     to resolve instead of emitting an error result.
//
// `$result.`/`$args.` references are rejected: the node's own result
// does not exist yet at resolution time.
package set

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/mcmx/nitejaguar/common"
)

type setAction struct {
	data   common.ActionArgs
	events chan common.ResultData
}

// New creates the action; ActionType is forced to "action".
func New(events chan common.ResultData, data common.ActionArgs) (common.Action, error) {
	s := &setAction{events: events, data: data}
	s.data.ActionType = "action"
	return s, nil
}

func (s *setAction) Stop() error { return nil }

func (s *setAction) GetArgs() common.ActionArgs { return s.data }

func (s *setAction) send(executionID string, payload map[string]any) {
	s.events <- common.ResultData{
		ExecutionID: executionID,
		ActionID:    s.data.Id,
		ActionType:  s.data.ActionType,
		ActionName:  s.data.ActionName,
		Payload:     payload,
	}
}

// Execute resolves the assigned fields and emits the built payload.
func (s *setAction) Execute(executionID string, inputs []any) {
	rawArgs, err := rawArgsMap(s.data.Args)
	if err != nil {
		s.send(executionID, errorPayload("", err.Error()))
		return
	}
	input := findInputResult(inputs)

	mode, err := normalizeMode(stringifyControl(rawArgs, "mode"))
	if err != nil {
		s.send(executionID, errorPayload("", err.Error()))
		return
	}

	keepOnly, err := parseBoolArg(rawArgs, "keep_only_set", "keep_only", "keepOnlySet", "keep")
	if err != nil {
		s.send(executionID, errorPayload(mode, err.Error()))
		return
	}
	if inc := strings.ToLower(strings.TrimSpace(stringifyControl(rawArgs, "include"))); inc != "" {
		switch inc {
		case "all", "all_input", "all-input", "allinput":
			keepOnly = false
		case "none", "nothing", "set_only", "set-only", "only_set":
			keepOnly = true
		default:
			s.send(executionID, errorPayload(mode, fmt.Sprintf("unknown include %q: use \"all\" or \"none\"", inc)))
			return
		}
	}
	dotNotation := true
	if hasControl(rawArgs, "dot_notation", "support_dot_notation", "dotNotation", "dot-notation") {
		dotNotation, err = parseBoolArg(rawArgs, "dot_notation", "support_dot_notation", "dotNotation", "dot-notation")
		if err != nil {
			s.send(executionID, errorPayload(mode, err.Error()))
			return
		}
	}
	ignoreErrors, err := parseBoolArg(rawArgs, "ignore_type_errors", "ignore_errors", "ignoreTypeErrors")
	if err != nil {
		s.send(executionID, errorPayload(mode, err.Error()))
		return
	}

	var assigned map[string]any
	switch mode {
	case "manual":
		assigned, err = collectManualFields(rawArgs, input, ignoreErrors)
	case "json":
		assigned, err = collectJSONFields(rawArgs, input, ignoreErrors)
	}
	if err != nil {
		s.send(executionID, errorPayload(mode, err.Error()))
		return
	}
	if len(assigned) == 0 {
		s.send(executionID, errorPayload(mode, "no fields to set: provide a \"fields\" object or \"field.<name>\" args (manual), or a \"json\" object template (json mode)"))
		return
	}

	expanded := make(map[string]any, len(assigned))
	for name, val := range assigned {
		if err := setField(expanded, name, val, dotNotation); err != nil {
			if ignoreErrors {
				continue
			}
			s.send(executionID, errorPayload(mode, err.Error()))
			return
		}
	}
	if len(expanded) == 0 {
		s.send(executionID, errorPayload(mode, "no fields to set: every assignment failed"))
		return
	}

	var base any
	if !keepOnly && input != nil {
		base = input.Payload
	}
	merged := common.MergePayloads(base, expanded)
	payload, ok := asObject(merged)
	if !ok {
		payload = expanded
	}
	payload["type"] = "success"
	payload["mode"] = mode
	payload["keep_only_set"] = keepOnly
	s.send(executionID, payload)
}

func errorPayload(mode, msg string) map[string]any {
	return map[string]any{"type": "error", "mode": mode, "result": msg}
}

func normalizeMode(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "manual", "manual_mapping", "manual-mapping", "manualmapping", "fields", "field":
		return "manual", nil
	case "json", "json_output", "json-output", "jsonoutput", "output":
		return "json", nil
	default:
		return "", fmt.Errorf("unknown mode %q: use \"manual\" or \"json\"", raw)
	}
}

// collectManualFields merges the `fields` object with `field.<name>` args.
// `field.` entries win on conflicts.
func collectManualFields(rawArgs map[string]any, input *common.ResultData, ignoreErrors bool) (map[string]any, error) {
	out := make(map[string]any)
	if v, ok := firstPresent(rawArgs, "fields", "values", "assignments"); ok {
		fields, err := coerceFieldsObject(v)
		if err != nil {
			return nil, err
		}
		for name, val := range fields {
			resolved, err := resolveValue(val, input)
			if err != nil {
				if ignoreErrors {
					continue
				}
				return nil, fmt.Errorf("field %q: %w", name, err)
			}
			out[name] = resolved
		}
	}
	for key, val := range rawArgs {
		name, ok := strings.CutPrefix(key, "field.")
		if !ok || name == "" {
			continue
		}
		resolved, err := resolveValue(val, input)
		if err != nil {
			if ignoreErrors {
				continue
			}
			return nil, fmt.Errorf("field %q: %w", name, err)
		}
		out[name] = resolved
	}
	return out, nil
}

// collectJSONFields resolves `$input.` references in the raw template,
// then parses the result as a JSON object.
func collectJSONFields(rawArgs map[string]any, input *common.ResultData, ignoreErrors bool) (map[string]any, error) {
	v, ok := firstPresent(rawArgs, "json", "json_output", "json-output", "output")
	if !ok {
		return nil, fmt.Errorf("missing \"json\" template object for json mode")
	}
	tmpl, ok := v.(string)
	if !ok {
		if s, ok := stringifyScalar(v); ok {
			tmpl = s
		} else {
			raw, err := json.Marshal(v)
			if err != nil {
				return nil, fmt.Errorf("invalid \"json\" template: %v", err)
			}
			tmpl = string(raw)
		}
	}
	if strings.Contains(tmpl, "$result.") || strings.Contains(tmpl, "$args.") {
		return nil, fmt.Errorf("unsupported reference in json template: only $input. resolves against upstream payload in action args")
	}
	if strings.Contains(tmpl, "{{") {
		var err error
		tmpl, err = common.ExpandJSONTemplates(tmpl, common.InputLookup(input))
		if err != nil {
			return nil, err
		}
	}
	if strings.Contains(tmpl, "$input.") {
		resolved, err := common.ExpandJSONRefs(tmpl, common.InputLookup(input))
		if err != nil {
			return nil, err
		}
		tmpl = resolved
	}
	var parsed any
	if err := json.Unmarshal([]byte(tmpl), &parsed); err != nil {
		if ignoreErrors {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("invalid json template: %v", err)
	}
	obj, ok := asObject(parsed)
	if !ok {
		return nil, fmt.Errorf("invalid json template: top level must be an object")
	}
	out := make(map[string]any, len(obj))
	for name, val := range obj {
		if s, ok := val.(string); ok && strings.Contains(s, "$input.") {
			resolved, err := resolveValue(s, input)
			if err != nil {
				if ignoreErrors {
					continue
				}
				return nil, fmt.Errorf("field %q: %w", name, err)
			}
			out[name] = resolved
			continue
		}
		out[name] = val
	}
	return out, nil
}

// setField assigns a value, expanding dot notation into nested objects.
func setField(target map[string]any, name string, value any, dotNotation bool) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("field name must not be empty")
	}
	if !dotNotation || !strings.Contains(name, ".") {
		target[name] = value
		return nil
	}
	parts := strings.Split(name, ".")
	cur := target
	for _, part := range parts[:len(parts)-1] {
		if part == "" {
			return fmt.Errorf("invalid field name %q: empty segment", name)
		}
		next, ok := cur[part]
		if !ok {
			child := make(map[string]any)
			cur[part] = child
			cur = child
			continue
		}
		child, ok := asObject(next)
		if !ok {
			return fmt.Errorf("invalid field name %q: %q already holds a non-object value", name, part)
		}
		cur = child
	}
	last := parts[len(parts)-1]
	if last == "" {
		return fmt.Errorf("invalid field name %q: empty segment", name)
	}
	cur[last] = value
	return nil
}

// resolveValue resolves `$input.<path>` references inside a value,
// preserving native types for non-string leaves. A string that is
// exactly one reference returns the referenced value as-is; strings
// with embedded references interpolate via stringification.
func resolveValue(v any, input *common.ResultData) (any, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case string:
		return resolveStringTemplate(t, input)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			r, err := resolveValue(e, input)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			r, err := resolveValue(e, input)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	default:
		rv := reflect.ValueOf(v)
		for rv.Kind() == reflect.Interface || rv.Kind() == reflect.Pointer {
			if rv.IsNil() {
				return nil, nil
			}
			rv = rv.Elem()
		}
		switch rv.Kind() {
		case reflect.Map:
			raw, err := json.Marshal(v)
			if err != nil {
				return nil, err
			}
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				return nil, err
			}
			return resolveValue(m, input)
		case reflect.Slice, reflect.Array:
			raw, err := json.Marshal(v)
			if err != nil {
				return nil, err
			}
			var s []any
			if err := json.Unmarshal(raw, &s); err != nil {
				return nil, err
			}
			return resolveValue(s, input)
		case reflect.String:
			return resolveStringTemplate(rv.String(), input)
		default:
			return v, nil
		}
	}
}

var resultRefPattern = regexp.MustCompile(`\$input\.[A-Za-z0-9_.\[\]]+`)

func resolveStringTemplate(raw string, input *common.ResultData) (any, error) {
	if raw == "" {
		return "", nil
	}
	if strings.Contains(raw, "$result.") || strings.Contains(raw, "$args.") {
		return "", fmt.Errorf("unsupported reference in %q: only $input. resolves against upstream payload in action args", raw)
	}
	if strings.Contains(raw, "{{") && !common.HasTemplate(raw) {
		return "", fmt.Errorf("invalid template in %q: unclosed \"{{\"", raw)
	}
	segs := common.SplitTemplates(raw)
	// A value that is exactly one filter-free template keeps the
	// referenced native type.
	if len(segs) == 3 && segs[0] == "" && segs[2] == "" {
		return common.EvalTemplate(segs[1], common.InputLookup(input))
	}
	// Split-out regions keep bare `$input.` refs and templates from
	// consuming each other when adjacent.
	var sb strings.Builder
	for i, seg := range segs {
		if i%2 == 1 {
			v, err := common.EvalTemplate(seg, common.InputLookup(input))
			if err != nil {
				return "", err
			}
			sb.WriteString(common.TemplateString(v))
			continue
		}
		if !strings.Contains(seg, "$input.") {
			sb.WriteString(seg)
			continue
		}
		if input == nil {
			return "", fmt.Errorf("no input result available to resolve %q", raw)
		}
		if len(segs) == 1 {
			if trimmed := strings.TrimSpace(seg); resultRefPattern.FindString(trimmed) == trimmed {
				return common.ResolveRefPath(input.Payload, trimmed)
			}
		}
		var resolveErr error
		out := resultRefPattern.ReplaceAllStringFunc(seg, func(m string) string {
			if resolveErr != nil {
				return m
			}
			val, err := common.ResolveRefPath(input.Payload, m)
			if err != nil {
				resolveErr = err
				return m
			}
			return common.TemplateString(val)
		})
		if resolveErr != nil {
			return "", resolveErr
		}
		sb.WriteString(out)
	}
	return sb.String(), nil
}

// rawArgsMap normalizes action args while preserving value types.
// Accepts map[string]any, map[string]string, and any other map with
// string keys. A nil map means "no args".
func rawArgsMap(args any) (map[string]any, error) {
	if args == nil {
		return map[string]any{}, nil
	}
	if m, ok := args.(map[string]any); ok {
		out := make(map[string]any, len(m))
		for k, v := range m {
			out[k] = v
		}
		return out, nil
	}
	if m, ok := args.(map[string]string); ok {
		out := make(map[string]any, len(m))
		for k, v := range m {
			out[k] = v
		}
		return out, nil
	}
	v := reflect.ValueOf(args)
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return map[string]any{}, nil
		}
		v = v.Elem()
	}
	if v.Kind() == reflect.Map {
		if v.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("invalid arguments type %T: map key must be string", args)
		}
		out := make(map[string]any, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out[iter.Key().String()] = iter.Value().Interface()
		}
		return out, nil
	}
	return nil, fmt.Errorf("invalid arguments type %T: must be a map", args)
}

func firstPresent(args map[string]any, names ...string) (any, bool) {
	for _, n := range names {
		if v, ok := args[n]; ok && v != nil && strings.TrimSpace(common.StringifyArgValue(v)) != "" {
			return v, true
		}
	}
	return nil, false
}

func hasControl(args map[string]any, names ...string) bool {
	for _, n := range names {
		if _, ok := args[n]; ok {
			return true
		}
	}
	return false
}

func stringifyControl(args map[string]any, name string) string {
	v, ok := args[name]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return common.StringifyArgValue(v)
}

func stringifyScalar(v any) (string, bool) {
	if v == nil {
		return "", false
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Interface || rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return "", false
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return fmt.Sprint(rv.Interface()), true
	default:
		return "", false
	}
}

func parseBoolArg(args map[string]any, names ...string) (bool, error) {
	var raw string
	var found bool
	used := ""
	for _, n := range names {
		if v, ok := args[n]; ok && v != nil {
			s := strings.TrimSpace(common.StringifyArgValue(v))
			if s == "" {
				continue
			}
			raw = s
			used = n
			found = true
			break
		}
	}
	if !found {
		return false, nil
	}
	b, err := strconv.ParseBool(strings.ToLower(raw))
	if err != nil {
		return false, fmt.Errorf("invalid boolean %q for %q: use true/false", raw, used)
	}
	return b, nil
}

// coerceFieldsObject accepts the `fields` value either as an object or
// as a JSON-encoded object string.
func coerceFieldsObject(v any) (map[string]any, error) {
	if m, ok := v.(map[string]any); ok {
		return m, nil
	}
	if s, ok := v.(string); ok {
		var m map[string]any
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			return nil, fmt.Errorf("invalid \"fields\" object: %v", err)
		}
		return m, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("invalid \"fields\" object: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("invalid \"fields\" object: must be an object")
	}
	return m, nil
}

// asObject normalizes maps and structs into map[string]any via a JSON
// round-trip. It reports false for scalars, slices, and nulls.
func asObject(v any) (map[string]any, bool) {
	if v == nil {
		return nil, false
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	if len(raw) == 0 || raw[0] != '{' {
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, false
	}
	return m, true
}

// findInputResult returns the first ResultData found in inputs, if any.
func findInputResult(inputs []any) *common.ResultData {
	for _, in := range inputs {
		switch v := in.(type) {
		case common.ResultData:
			c := v
			return &c
		case *common.ResultData:
			if v != nil {
				return v
			}
		}
	}
	return nil
}
