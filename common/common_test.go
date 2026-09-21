package common

import (
	"reflect"
	"testing"
)

func TestMergePayloadsResultWins(t *testing.T) {
	input := map[string]any{"Name": "Sergio", "LastName": "Who"}
	result := map[string]any{"Name": "Dr", "Phone": "555-768790"}
	merged, ok := MergePayloads(input, result).(map[string]any)
	if !ok {
		t.Fatalf("expected map payload, got %T", MergePayloads(input, result))
	}
	want := map[string]any{"Name": "Dr", "LastName": "Who", "Phone": "555-768790"}
	if !reflect.DeepEqual(merged, want) {
		t.Fatalf("merged = %v, want %v", merged, want)
	}
}

func TestMergePayloadsDeep(t *testing.T) {
	input := map[string]any{"a": map[string]any{"x": 1, "y": 2}, "keep": true}
	result := map[string]any{"a": map[string]any{"y": 3, "z": 4}}
	merged, ok := MergePayloads(input, result).(map[string]any)
	if !ok {
		t.Fatalf("expected map payload, got %T", MergePayloads(input, result))
	}
	want := map[string]any{"a": map[string]any{"x": float64(1), "y": float64(3), "z": float64(4)}, "keep": true}
	if !reflect.DeepEqual(merged, want) {
		t.Fatalf("merged = %v, want %v", merged, want)
	}
}

func TestMergePayloadsNonObjects(t *testing.T) {
	// Non-object input: result passes through unchanged.
	res := map[string]any{"a": 1}
	if got := MergePayloads("nope", res); !reflect.DeepEqual(got, res) {
		t.Fatalf("expected result unchanged, got %v", got)
	}
	// Non-object result: result passes through unchanged.
	if got := MergePayloads(map[string]any{"a": 1}, []any{1}); !reflect.DeepEqual(got, []any{1}) {
		t.Fatalf("expected result unchanged, got %v", got)
	}
	// Nil input yields result; nil result yields input.
	if got := MergePayloads(nil, res); !reflect.DeepEqual(got, res) {
		t.Fatalf("expected result, got %v", got)
	}
	in := map[string]any{"a": 1}
	if got := MergePayloads(in, nil); !reflect.DeepEqual(got, in) {
		t.Fatalf("expected input, got %v", got)
	}
}

func TestMergePayloadsDoesNotMutate(t *testing.T) {
	input := map[string]any{"a": 1}
	result := map[string]any{"b": 2}
	MergePayloads(input, result)
	if !reflect.DeepEqual(input, map[string]any{"a": 1}) || !reflect.DeepEqual(result, map[string]any{"b": 2}) {
		t.Fatalf("inputs mutated: %v %v", input, result)
	}
}
