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
		tmpl, err = expandJSONTemplates(tmpl, input)
		if err != nil {
			return nil, err
		}
	}
	if strings.Contains(tmpl, "$input.") {
		if input == nil {
			return nil, fmt.Errorf("no input result available to resolve json template")
		}
		resolved, err := resolveJSONTemplate(tmpl, input)
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

// resolveJSONTemplate resolves `$input.<path>` references in a raw JSON
// template. Each reference is replaced by the JSON encoding of the
// referenced value, except when the reference sits inside a JSON string
// literal (`"$input.name"`): then the plain string form is substituted
// so the template stays valid JSON. Bare references keep native types
// (numbers, booleans, objects, arrays).
func resolveJSONTemplate(tmpl string, input *common.ResultData) (string, error) {
	matches := resultRefPattern.FindAllStringIndex(tmpl, -1)
	if len(matches) == 0 {
		return tmpl, nil
	}
	var out strings.Builder
	pos := 0
	for _, m := range matches {
		ref := tmpl[m[0]:m[1]]
		val, err := resolveResultPath(input.Payload, ref)
		if err != nil {
			return "", err
		}
		out.WriteString(tmpl[pos:m[0]])
		if isQuotedAt(tmpl, m[0], m[1]) {
			var s string
			if str, ok := val.(string); ok {
				s = str
			} else if raw, merr := json.Marshal(val); merr == nil && isJSONScalar(raw) {
				// Numbers/bools/null render in JSON form
				// (23423532, not 2.3423532e+07) inside the
				// surrounding quotes.
				out.Write(raw)
				pos = m[1]
				continue
			} else {
				s = common.StringifyArgValue(val)
			}
			// Reuse the JSON string escaper, minus the surrounding
			// quotes, so values with quotes or newlines stay valid.
			raw, err := json.Marshal(s)
			if err != nil {
				return "", err
			}
			out.Write(raw[1 : len(raw)-1])
		} else {
			raw, err := json.Marshal(val)
			if err != nil {
				return "", err
			}
			out.Write(raw)
		}
		pos = m[1]
	}
	out.WriteString(tmpl[pos:])
	return out.String(), nil
}

// isJSONScalar reports whether raw JSON is a number, boolean, or null
// (values that embed cleanly inside a surrounding string literal).
func isJSONScalar(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	switch raw[0] {
	case '{', '[', '"':
		return false
	default:
		return true
	}
}

// isQuotedAt reports whether the match at [start,end) sits inside a JSON
// string literal, detected by quotes immediately surrounding it
// (allowing whitespace between the quote and the reference).
func isQuotedAt(tmpl string, start, end int) bool {
	before := start - 1
	for before >= 0 && (tmpl[before] == ' ' || tmpl[before] == '\t' || tmpl[before] == '\n' || tmpl[before] == '\r') {
		before--
	}
	after := end
	for after < len(tmpl) && (tmpl[after] == ' ' || tmpl[after] == '\t' || tmpl[after] == '\n' || tmpl[after] == '\r') {
		after++
	}
	return before >= 0 && tmpl[before] == '"' && after < len(tmpl) && tmpl[after] == '"'
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
	// `{{ ... }}` templates expand first; a value that is exactly one
	// filter-free template keeps the referenced native type.
	if strings.Contains(raw, "{{") {
		if inner, ok := singleTemplate(raw); ok {
			return evalTemplate(inner, input)
		}
		expanded, err := expandTemplates(raw, input)
		if err != nil {
			return "", err
		}
		raw = expanded
	}
	if !strings.Contains(raw, "$input.") {
		return raw, nil
	}
	if input == nil {
		return "", fmt.Errorf("no input result available to resolve %q", raw)
	}
	trimmed := strings.TrimSpace(raw)
	if m := resultRefPattern.FindString(trimmed); m == trimmed {
		return resolveResultPath(input.Payload, m)
	}
	var resolveErr error
	out := resultRefPattern.ReplaceAllStringFunc(raw, func(m string) string {
		if resolveErr != nil {
			return m
		}
		val, err := resolveResultPath(input.Payload, m)
		if err != nil {
			resolveErr = err
			return m
		}
		return templateString(val)
	})
	if resolveErr != nil {
		return "", resolveErr
	}
	return out, nil
}

// resolveResultPath resolves `$input.<path>` against the upstream
// payload. Supports dot-separated map keys / struct fields (json tag
// aware) and optional [index] suffixes.
func resolveResultPath(root any, fullPath string) (any, error) {
	const prefix = "$input."
	rest := strings.TrimPrefix(fullPath, prefix)
	if rest == fullPath {
		return nil, fmt.Errorf("unsupported path %q: must start with $input", fullPath)
	}
	if rest == "" {
		return nil, fmt.Errorf("unsupported path %q: empty key", fullPath)
	}
	cur := root
	for _, seg := range strings.Split(rest, ".") {
		if seg == "" {
			return nil, fmt.Errorf("unsupported path %q: empty segment", fullPath)
		}
		field, indices, err := parsePathSegment(seg, fullPath)
		if err != nil {
			return nil, err
		}
		if field != "" {
			cur, err = lookupResultField(cur, field, fullPath)
			if err != nil {
				return nil, err
			}
		} else if len(indices) == 0 {
			return nil, fmt.Errorf("unsupported path %q: empty segment", fullPath)
		}
		for _, idx := range indices {
			cur, err = lookupResultIndex(cur, idx, fullPath)
			if err != nil {
				return nil, err
			}
		}
	}
	return cur, nil
}

func parsePathSegment(seg, fullPath string) (string, []int, error) {
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

func derefResultValue(v reflect.Value) reflect.Value {
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return v
		}
		v = v.Elem()
	}
	return v
}

func lookupResultField(cur any, field, fullPath string) (any, error) {
	if cur == nil {
		return nil, fmt.Errorf("unsupported path %q: key %q not found (nil)", fullPath, field)
	}
	v := derefResultValue(reflect.ValueOf(cur))
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

func lookupResultIndex(cur any, idx int, fullPath string) (any, error) {
	if cur == nil {
		return nil, fmt.Errorf("unsupported path %q: index %d out of range (nil)", fullPath, idx)
	}
	v := derefResultValue(reflect.ValueOf(cur))
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
