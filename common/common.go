package common

import (
	"encoding/json"
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
