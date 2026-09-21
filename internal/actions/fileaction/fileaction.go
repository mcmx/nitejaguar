package fileaction

// Package fileaction implements a workflow action that creates, deletes, or renames files.
// When a matching event occurs, it emits a result to the workflow engine.
//
// Dynamic args (issue #28):
//   - args values support literal strings, `$input.<path>` references
//     (resolved against the upstream ResultData payload threaded through
//     WorkflowManager.Run inputs, i.e. the results of dependencies).
//     `$json.<path>` is an n8n-style alias for the same upstream document.
//     `{{...}}` placeholders are also supported.
//   - `{{date}}` defaults to local YYYYMMDD (Go layout "20060102").
//     `{{date:<layout>}}` (e.g. `{{date:2006-01-02}}`) uses the given Go layout.
//   - `{{file}}`, `{{base}}`, `{{ext}}`, `{{stem}}` are derived from the
//     resolved source file.
//   - `~` leading paths are expanded to the user home dir.
//   - collision policy: if the destination already exists, do NOT overwrite;
//     emit a Type:"error" result and leave the source untouched.

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mcmx/nitejaguar/common"
)

type fileaction struct {
	data   common.ActionArgs
	events chan common.ResultData
}

type payload struct {
	Type    string `json:"type"`               // Event type
	File    string `json:"file"`               // File name
	NewFile string `json:"new_file,omitempty"` // New file name
	Result  any    `json:"result"`             // Generic payload for event-specific data
}

func (f *fileaction) Execute(executionId string, inputs []any) {
	fmt.Println("Executing File Action with id:", f.data.Id)
	rawArgs, err := argsToStringMap(f.data.Args)
	if err != nil {
		fmt.Println("[fileaction] Invalid arguments:", err)
		f.sendResult(executionId, payload{Type: "error", Result: err.Error()})
		return
	}
	trigger := findTriggerResult(inputs)

	// Resolve source file first so {{file}}/{{base}}/{{ext}}/{{stem}}
	// in new_file can derive from it.
	src := ""
	if v, ok := rawArgs["file"]; ok {
		src, err = resolveArgValue(v, trigger, "")
		if err != nil {
			fmt.Println("[fileaction] Cannot resolve file arg:", err)
			f.sendResult(executionId, payload{Type: "error", Result: err.Error()})
			return
		}
		src = expandPath(src)
	}

	dst := ""
	if v, ok := rawArgs["new_file"]; ok {
		dst, err = resolveArgValue(v, trigger, src)
		if err != nil {
			fmt.Println("[fileaction] Cannot resolve new_file arg:", err)
			f.sendResult(executionId, payload{Type: "error", File: src, Result: err.Error()})
			return
		}
		dst = expandPath(dst)
	}

	// Merge resolved values back for the switch.
	args := map[string]string{"action": rawArgs["action"], "file": src}
	if dst != "" {
		args["new_file"] = dst
	}
	// Preserve any other raw args (resolved best-effort).
	for k, v := range rawArgs {
		if _, known := args[k]; !known {
			rv, rerr := resolveArgValue(v, trigger, src)
			if rerr != nil {
				rv = v
			} else {
				rv = expandPath(rv)
			}
			args[k] = rv
		}
	}

	switch args["action"] {
	case "create":
		if args["file"] == "" {
			msg := "missing file argument for create"
			fmt.Println("[fileaction]", msg)
			f.sendResult(executionId, payload{Type: "error", Result: msg})
			return
		}
		if _, statErr := os.Stat(args["file"]); statErr == nil {
			msg := fmt.Sprintf("destination already exists: %s", args["file"])
			fmt.Println("[fileaction]", msg)
			f.sendResult(executionId, payload{Type: "error", File: args["file"], Result: msg})
			return
		}
		if err := os.MkdirAll(filepath.Dir(args["file"]), 0o755); err != nil {
			fmt.Println("Error creating parent dirs with id:", f.data.Id, err)
			f.sendResult(executionId, payload{Type: "error", File: args["file"], Result: err.Error()})
			return
		}
		if _, err := os.Create(args["file"]); err != nil {
			fmt.Println("Error creating file with id:", f.data.Id, err)
			f.sendResult(executionId, payload{Type: "error", File: args["file"], Result: err.Error()})
			return
		}
		f.sendResult(executionId, payload{Type: "success", File: args["file"], Result: "File created successfully"})
	case "remove":
		if args["file"] == "" {
			msg := "missing file argument for remove"
			fmt.Println("[fileaction]", msg)
			f.sendResult(executionId, payload{Type: "error", Result: msg})
			return
		}
		if err := os.Remove(args["file"]); err != nil {
			fmt.Println("Error removing file with id:", f.data.Id, err)
			f.sendResult(executionId, payload{Type: "error", File: args["file"], Result: err.Error()})
			return
		}
		f.sendResult(executionId, payload{Type: "success", File: args["file"], Result: "File removed successfully"})
	case "rename":
		if args["file"] == "" || args["new_file"] == "" {
			msg := "missing file or new_file argument for rename"
			fmt.Println("[fileaction]", msg)
			f.sendResult(executionId, payload{Type: "error", File: args["file"], NewFile: args["new_file"], Result: msg})
			return
		}
		if _, statErr := os.Stat(args["new_file"]); statErr == nil {
			msg := fmt.Sprintf("destination already exists: %s", args["new_file"])
			fmt.Println("[fileaction]", msg)
			f.sendResult(executionId, payload{Type: "error", File: args["file"], NewFile: args["new_file"], Result: msg})
			return
		}
		if err := os.MkdirAll(filepath.Dir(args["new_file"]), 0o755); err != nil {
			fmt.Println("Error creating parent dirs with id:", f.data.Id, err)
			f.sendResult(executionId, payload{Type: "error", File: args["file"], NewFile: args["new_file"], Result: err.Error()})
			return
		}
		if err := os.Rename(args["file"], args["new_file"]); err != nil {
			fmt.Println("Error renaming file with id:", f.data.Id, err)
			f.sendResult(executionId, payload{Type: "error", File: args["file"], NewFile: args["new_file"], Result: err.Error()})
			return
		}
		f.sendResult(executionId, payload{Type: "success", File: args["file"], NewFile: args["new_file"], Result: "File renamed successfully"})
	default:
		fmt.Println("Unknown action with id:", f.data.Id)
		f.sendResult(executionId, payload{Type: "error", Result: fmt.Sprintf("unknown action %q", args["action"])})
	}
}

func (f *fileaction) Stop() error {
	fmt.Println("Stopping File Action with id:", f.data.Id)
	return nil
}

func (f *fileaction) GetArgs() common.ActionArgs {
	return f.data
}

func New(events chan common.ResultData, data common.ActionArgs) (common.Action, error) {
	s := &fileaction{events: events, data: data}
	s.data.ActionType = "action"
	fmt.Println("Initializing File Action with id:", s.data.Id)
	return s, nil
}

func (t *fileaction) sendResult(executionId string, payload payload) {
	t.events <- common.ResultData{
		ExecutionID: executionId,
		ActionID:    t.data.Id,
		ActionType:  t.data.ActionType,
		ActionName:  t.data.ActionName,
		Payload:     payload,
	}
}

// argsToStringMap normalizes action args without panicking.
// Accepts map[string]string, map[string]any, and any other map with
// string keys via reflection. Values are stringified (strings kept as-is,
// others via fmt.Sprint). Non-map args return an explicit error.
func argsToStringMap(args any) (map[string]string, error) {
	if args == nil {
		return nil, fmt.Errorf("missing arguments")
	}
	if m, ok := args.(map[string]string); ok {
		out := make(map[string]string, len(m))
		for k, v := range m {
			out[k] = v
		}
		return out, nil
	}
	if m, ok := args.(map[string]any); ok {
		out := make(map[string]string, len(m))
		for k, v := range m {
			out[k] = stringifyArgValue(v)
		}
		return out, nil
	}
	v := reflect.ValueOf(args)
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil, fmt.Errorf("missing arguments")
		}
		v = v.Elem()
	}
	if v.Kind() == reflect.Map {
		if v.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("invalid arguments type %T: map key must be string", args)
		}
		out := make(map[string]string, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			k := iter.Key().String()
			out[k] = stringifyArgValue(iter.Value().Interface())
		}
		return out, nil
	}
	return nil, fmt.Errorf("invalid arguments type %T: must be a map", args)
}

func stringifyArgValue(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Interface || rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return ""
		}
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.String {
		return rv.String()
	}
	return fmt.Sprint(v)
}

// expandPath expands a leading `~` to the user home directory.
// `~` -> home, `~/rest` -> home/rest. Other values unchanged.
func expandPath(p string) string {
	if p == "" {
		return p
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return p
		}
		if p == "~" {
			return home
		}
		return filepath.Join(home, strings.TrimPrefix(p, "~/"))
	}
	return p
}

// findTriggerResult returns the first ResultData found in inputs, if any.
func findTriggerResult(inputs []any) *common.ResultData {
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

var resultRefPattern = regexp.MustCompile(`\$(?:input|json)\.[A-Za-z0-9_.\[\]]+`)
var legacyRefPattern = regexp.MustCompile(`\$\.[A-Za-z0-9_.\[\]]+`)
var placeholderPattern = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)

// resolveArgValue resolves one arg value: inline `$input.<path>` references
// (plus n8n-style `$json.<path>` alias) plus `{{...}}` placeholders.
// Literals pass through. Both prefixes resolve against the upstream
// ResultData payload. $result./$args. and legacy $. prefixes are rejected.
func resolveArgValue(raw string, trigger *common.ResultData, sourceFile string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if legacyRefPattern.MatchString(raw) {
		return "", fmt.Errorf("unsupported reference in %q: legacy $. prefix was removed, use $input. or $json", raw)
	}
	if strings.Contains(raw, "$result.") || strings.Contains(raw, "$args.") {
		return "", fmt.Errorf("unsupported reference in %q: only $input. and $json. resolve against upstream payload in action args", raw)
	}
	out := raw
	hasRef := strings.Contains(out, "$input.") || strings.Contains(out, "$json.")
	if trigger != nil && hasRef {
		var resolveErr error
		out = resultRefPattern.ReplaceAllStringFunc(out, func(m string) string {
			if resolveErr != nil {
				return m
			}
			val, err := resolveResultPath(trigger.Payload, m)
			if err != nil {
				resolveErr = err
				return m
			}
			return stringifyArgValue(val)
		})
		if resolveErr != nil {
			return "", resolveErr
		}
	} else if trigger == nil && hasRef {
		return "", fmt.Errorf("no trigger result available to resolve %q", raw)
	}
	if strings.Contains(out, "{{") {
		var tmplErr error
		out = placeholderPattern.ReplaceAllStringFunc(out, func(m string) string {
			if tmplErr != nil {
				return m
			}
			inner := strings.TrimSpace(m[2 : len(m)-2])
			repl, err := expandPlaceholder(inner, sourceFile)
			if err != nil {
				tmplErr = err
				return m
			}
			return repl
		})
		if tmplErr != nil {
			return "", tmplErr
		}
	}
	return out, nil
}

// expandPlaceholder expands one `{{...}}` inner expression.
func expandPlaceholder(inner string, sourceFile string) (string, error) {
	expr := strings.TrimSpace(inner)
	// Allow `{{.file}}` style: strip one leading dot.
	expr = strings.TrimPrefix(expr, ".")
	if expr == "" {
		return "", fmt.Errorf("empty placeholder")
	}
	lower := strings.ToLower(expr)
	if lower == "date" {
		return time.Now().Local().Format("20060102"), nil
	}
	if strings.HasPrefix(lower, "date:") || strings.HasPrefix(lower, "date ") {
		sep := 5
		layout := strings.TrimSpace(expr[sep:])
		layout = strings.Trim(layout, `"'`)
		if layout == "" {
			return "", fmt.Errorf("empty date layout in placeholder {{%s}}", inner)
		}
		if layout == "YYYYMMDD" {
			layout = "20060102"
		}
		return time.Now().Local().Format(layout), nil
	}
	switch lower {
	case "file":
		return sourceFile, nil
	case "base":
		if sourceFile == "" {
			return "", fmt.Errorf("no source file available for {{base}}")
		}
		return filepath.Base(sourceFile), nil
	case "ext":
		if sourceFile == "" {
			return "", fmt.Errorf("no source file available for {{ext}}")
		}
		return filepath.Ext(sourceFile), nil
	case "stem":
		if sourceFile == "" {
			return "", fmt.Errorf("no source file available for {{stem}}")
		}
		base := filepath.Base(sourceFile)
		return strings.TrimSuffix(base, filepath.Ext(base)), nil
	}
	return "", fmt.Errorf("unknown placeholder {{%s}}", inner)
}

// resolveResultPath resolves `$input.<path>` (or n8n-style `$json.<path>`)
// against the upstream trigger payload. Supports dot-separated map keys /
// struct fields (json tag aware) and optional [index] suffixes.
func resolveResultPath(root any, fullPath string) (any, error) {
	var rest string
	switch {
	case strings.HasPrefix(fullPath, "$input."):
		rest = strings.TrimPrefix(fullPath, "$input.")
	case strings.HasPrefix(fullPath, "$json."):
		rest = strings.TrimPrefix(fullPath, "$json.")
	default:
		return nil, fmt.Errorf("unsupported path %q: must start with $input. or $json", fullPath)
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
