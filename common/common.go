package common

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

// ActionArgs holds the arguments for an action
type ActionArgs struct {
	Id         string
	Name       string
	ActionType string
	ActionName string
	Args       any
}

// Action interface for actions
type Action interface {
	Execute(executionId string, inputs []any)
	Stop() error
	GetArgs() ActionArgs
}

// Generic ResultData struct for various actions
type ResultData struct {
	ResultID    string    `json:"result_id"`
	WorkflowID  string    `json:"workflow_id"`
	ExecutionID string    `json:"execution_id"`
	ActionID    string    `json:"action_id"`
	ActionType  string    `json:"action_type"`
	ActionName  string    `json:"action_name"`
	ExecutorID  string    `json:"executor_id"`
	TenantID    string    `json:"tenant_id"`
	CreatedAt   time.Time `json:"created_at"`
	Payload     any       `json:"payload"` // Generic payload for additional data
	// ConditionResults records the per-entry outcome of the node's
	// conditions evaluation (entry ID -> matched). It is computed by
	// the workflow engine when routing (GetNextNodes / IngestResult)
	// and persisted with the result so the decision is visible in
	// result JSON, the DB, and the /results page.
	ConditionResults map[string]bool `json:"condition_results,omitempty"`
	// Nexts is the routing decision derived from ConditionResults: the
	// downstream node IDs whose entry condition evaluated to true.
	Nexts []string `json:"nexts,omitempty"`
	// ConditionError captures the first condition evaluation error, if
	// any. Routing continues with the remaining entries; the error is
	// kept here (and logged) instead of failing the execution.
	ConditionError string `json:"condition_error,omitempty"`
}

// MergePayloads deep-merges an upstream $input payload into an action's
// $result payload. The result wins on conflicting keys; nested objects are
// merged recursively. Non-object payloads (or a nil input) cannot be merged
// and yield the result unchanged. A nil result yields the input.
func MergePayloads(input, result any) any {
	if input == nil {
		return result
	}
	if result == nil {
		return input
	}
	inMap, ok := asStringMap(input)
	if !ok {
		return result
	}
	resMap, ok := asStringMap(result)
	if !ok {
		return result
	}
	return deepMergeMaps(inMap, resMap)
}

// deepMergeMaps returns base overlaid with override: override scalars and
// slices win, nested maps merge recursively. Neither argument is mutated.
func deepMergeMaps(base, override map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		if bv, ok := out[k]; ok {
			bm, bok := asStringMap(bv)
			vm, vok := asStringMap(v)
			if bok && vok {
				out[k] = deepMergeMaps(bm, vm)
				continue
			}
		}
		out[k] = v
	}
	return out
}

// asStringMap normalizes maps (any value type) and structs into a
// map[string]any via a JSON round-trip. It reports false for scalars,
// slices, and other non-object payloads.
func asStringMap(v any) (map[string]any, bool) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	// Reject JSON nulls and non-objects up front.
	if len(raw) == 0 || raw[0] != '{' {
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, false
	}
	return m, true
}

// ExpandPath expands a leading `~` to the user home directory and sanitizes the path.
// `~` is only allowed as the first character (`~` or `~/...`).
// Returns an error if the path contains null bytes or invalid `~` placement.
func ExpandPath(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	if strings.Contains(p, "\x00") {
		return "", errors.New("path contains null bytes")
	}
	if strings.Contains(p, "~") {
		if !strings.HasPrefix(p, "~") {
			return "", errors.New("tilde (~) is only allowed as the first character")
		}
		if p != "~" && !strings.HasPrefix(p, "~/") {
			return "", errors.New("invalid tilde path format; use '~' or '~/path'")
		}
	}

	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return p, nil
		}
		if p == "~" {
			return home, nil
		}
		return filepath.Join(home, strings.TrimPrefix(p, "~/")), nil
	}

	return filepath.Clean(p), nil
}

// ArgsToStringMap normalizes action args without panicking.
// Accepts map[string]string, map[string]any, and any other map with
// string keys via reflection. Values are stringified.
func ArgsToStringMap(args any) (map[string]string, error) {
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
			out[k] = StringifyArgValue(v)
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
			out[k] = StringifyArgValue(iter.Value().Interface())
		}
		return out, nil
	}
	return nil, fmt.Errorf("invalid arguments type %T: must be a map", args)
}

func StringifyArgValue(v any) string {
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
