package set

import (
	"testing"

	"github.com/mcmx/nitejaguar/common"
)

func newTestAction(t *testing.T, args any) (common.Action, chan common.ResultData) {
	t.Helper()
	events := make(chan common.ResultData, 4)
	a, err := New(events, common.ActionArgs{
		Id:         "action_test",
		Name:       "test",
		ActionType: "action",
		ActionName: "set",
		Args:       args,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	return a, events
}

func readPayload(t *testing.T, ch chan common.ResultData) map[string]any {
	t.Helper()
	r := <-ch
	p, ok := r.Payload.(map[string]any)
	if !ok {
		t.Fatalf("unexpected payload type %T", r.Payload)
	}
	return p
}

func inputWith(payload map[string]any) []any {
	return []any{common.ResultData{
		ExecutionID: "exec1",
		ActionID:    "trigger_1",
		ActionType:  "trigger",
		ActionName:  "filechange",
		Payload:     payload,
	}}
}

func TestManualLiteralFields(t *testing.T) {
	a, events := newTestAction(t, map[string]string{
		"field.city": "Madrid",
		"field.kind": "capital",
	})
	a.Execute("exec1", nil)
	p := readPayload(t, events)
	if p["type"] != "success" {
		t.Fatalf("expected success, got %+v", p)
	}
	if p["city"] != "Madrid" || p["kind"] != "capital" {
		t.Fatalf("unexpected payload: %+v", p)
	}
}

func TestManualRefKeepsNativeType(t *testing.T) {
	a, events := newTestAction(t, map[string]any{
		"fields": map[string]any{
			"count":  "$input.n",
			"active": "$input.flag",
			"label":  "count is $input.n",
		},
	})
	a.Execute("exec1", inputWith(map[string]any{"n": float64(3), "flag": true}))
	p := readPayload(t, events)
	if p["type"] != "success" {
		t.Fatalf("expected success, got %+v", p)
	}
	if p["count"] != float64(3) {
		t.Fatalf("expected native number, got %v (%T)", p["count"], p["count"])
	}
	if p["active"] != true {
		t.Fatalf("expected native bool, got %v (%T)", p["active"], p["active"])
	}
	if p["label"] != "count is 3" {
		t.Fatalf("expected interpolation, got %q", p["label"])
	}
}

func TestManualFieldsJSONMergesWithFieldPrefix(t *testing.T) {
	a, events := newTestAction(t, map[string]string{
		"fields":        `{"a": "1", "b": "from-fields"}`,
		"field.b":       "from-prefix",
		"field.c":       "$input.extra",
		"keep_only_set": "true",
	})
	a.Execute("exec1", inputWith(map[string]any{"extra": "e"}))
	p := readPayload(t, events)
	if p["type"] != "success" {
		t.Fatalf("expected success, got %+v", p)
	}
	if p["a"] != "1" || p["b"] != "from-prefix" || p["c"] != "e" {
		t.Fatalf("unexpected payload: %+v", p)
	}
}

func TestDotNotationBuildsNestedObjects(t *testing.T) {
	a, events := newTestAction(t, map[string]string{
		"field.number.one": "20",
		"keep_only_set":    "true",
	})
	a.Execute("exec1", nil)
	p := readPayload(t, events)
	if p["type"] != "success" {
		t.Fatalf("expected success, got %+v", p)
	}
	num, ok := p["number"].(map[string]any)
	if !ok || num["one"] != "20" {
		t.Fatalf("expected nested object, got %+v", p)
	}
}

func TestDotNotationOffKeepsLiteralKey(t *testing.T) {
	a, events := newTestAction(t, map[string]string{
		"field.number.one": "20",
		"dot_notation":     "false",
		"keep_only_set":    "true",
	})
	a.Execute("exec1", nil)
	p := readPayload(t, events)
	if p["type"] != "success" {
		t.Fatalf("expected success, got %+v", p)
	}
	if p["number.one"] != "20" {
		t.Fatalf("expected literal key, got %+v", p)
	}
}

func TestKeepOnlySetFalseMergesInput(t *testing.T) {
	a, events := newTestAction(t, map[string]string{
		"field.b": "2",
	})
	a.Execute("exec1", inputWith(map[string]any{"a": "1", "b": "old"}))
	p := readPayload(t, events)
	if p["type"] != "success" {
		t.Fatalf("expected success, got %+v", p)
	}
	if p["a"] != "1" || p["b"] != "2" {
		t.Fatalf("expected merge with override, got %+v", p)
	}
}

func TestKeepOnlySetTrueDropsInput(t *testing.T) {
	a, events := newTestAction(t, map[string]string{
		"field.b":       "2",
		"keep_only_set": "true",
	})
	a.Execute("exec1", inputWith(map[string]any{"a": "1"}))
	p := readPayload(t, events)
	if p["type"] != "success" {
		t.Fatalf("expected success, got %+v", p)
	}
	if _, ok := p["a"]; ok {
		t.Fatalf("expected input dropped, got %+v", p)
	}
	if p["b"] != "2" {
		t.Fatalf("unexpected payload: %+v", p)
	}
}

func TestJSONModePreservesTypes(t *testing.T) {
	a, events := newTestAction(t, map[string]string{
		"mode":          "json",
		"json":          `{"newKey": "new value", "array": [$input.id, "$input.name"], "object": {"inner": "$input.id"}}`,
		"keep_only_set": "true",
	})
	a.Execute("exec1", inputWith(map[string]any{"id": float64(23423532), "name": "Jay"}))
	p := readPayload(t, events)
	if p["type"] != "success" {
		t.Fatalf("expected success, got %+v", p)
	}
	if p["newKey"] != "new value" {
		t.Fatalf("unexpected payload: %+v", p)
	}
	arr, ok := p["array"].([]any)
	if !ok || len(arr) != 2 || arr[0] != float64(23423532) || arr[1] != "Jay" {
		t.Fatalf("expected typed array, got %+v", p["array"])
	}
	obj, ok := p["object"].(map[string]any)
	if !ok || obj["inner"] != "23423532" {
		t.Fatalf("expected object, got %+v", p["object"])
	}
}

func TestJSONModeMergesInputByDefault(t *testing.T) {
	a, events := newTestAction(t, map[string]string{
		"mode": "json",
		"json": `{"extra": "x"}`,
	})
	a.Execute("exec1", inputWith(map[string]any{"id": "1"}))
	p := readPayload(t, events)
	if p["type"] != "success" {
		t.Fatalf("expected success, got %+v", p)
	}
	if p["id"] != "1" || p["extra"] != "x" {
		t.Fatalf("unexpected payload: %+v", p)
	}
}

func TestUnknownModeIsError(t *testing.T) {
	a, events := newTestAction(t, map[string]string{"mode": "sideways"})
	a.Execute("exec1", nil)
	if p := readPayload(t, events); p["type"] != "error" {
		t.Fatalf("expected error, got %+v", p)
	}
}

func TestNoFieldsIsError(t *testing.T) {
	a, events := newTestAction(t, map[string]string{"mode": "manual"})
	a.Execute("exec1", inputWith(map[string]any{"a": "1"}))
	if p := readPayload(t, events); p["type"] != "error" {
		t.Fatalf("expected error, got %+v", p)
	}
}

func TestUnresolvableRefIsError(t *testing.T) {
	a, events := newTestAction(t, map[string]string{"field.a": "$input.missing"})
	a.Execute("exec1", inputWith(map[string]any{"b": "1"}))
	if p := readPayload(t, events); p["type"] != "error" {
		t.Fatalf("expected error, got %+v", p)
	}
}

func TestIgnoreTypeErrorsSkipsBadFields(t *testing.T) {
	a, events := newTestAction(t, map[string]string{
		"field.good":         "ok",
		"field.bad":          "$input.missing",
		"keep_only_set":      "true",
		"ignore_type_errors": "true",
	})
	a.Execute("exec1", inputWith(map[string]any{"b": "1"}))
	p := readPayload(t, events)
	if p["type"] != "success" {
		t.Fatalf("expected success, got %+v", p)
	}
	if p["good"] != "ok" {
		t.Fatalf("unexpected payload: %+v", p)
	}
	if _, ok := p["bad"]; ok {
		t.Fatalf("expected bad field skipped, got %+v", p)
	}
}

func TestInvalidFieldsJSONIsError(t *testing.T) {
	a, events := newTestAction(t, map[string]string{"fields": "{oops"})
	a.Execute("exec1", nil)
	if p := readPayload(t, events); p["type"] != "error" {
		t.Fatalf("expected error, got %+v", p)
	}
}

func TestInvalidJSONTemplateIsError(t *testing.T) {
	a, events := newTestAction(t, map[string]string{"mode": "json", "json": "{oops"})
	a.Execute("exec1", nil)
	if p := readPayload(t, events); p["type"] != "error" {
		t.Fatalf("expected error, got %+v", p)
	}
}

func TestResultRefIsRejected(t *testing.T) {
	a, events := newTestAction(t, map[string]string{"field.a": "$result.x"})
	a.Execute("exec1", inputWith(map[string]any{"x": "1"}))
	if p := readPayload(t, events); p["type"] != "error" {
		t.Fatalf("expected error, got %+v", p)
	}
}

func TestMissingInputWithRefIsError(t *testing.T) {
	a, events := newTestAction(t, map[string]string{"field.a": "$input.x"})
	a.Execute("exec1", nil)
	if p := readPayload(t, events); p["type"] != "error" {
		t.Fatalf("expected error, got %+v", p)
	}
}

func TestTypeFieldStaysSuccess(t *testing.T) {
	a, events := newTestAction(t, map[string]string{
		"field.type":    "custom",
		"keep_only_set": "true",
	})
	a.Execute("exec1", nil)
	p := readPayload(t, events)
	if p["type"] != "success" {
		t.Fatalf("expected protected type=success, got %+v", p)
	}
}

func TestNestedRefAndIndexPath(t *testing.T) {
	a, events := newTestAction(t, map[string]string{
		"field.first":   "$input.users[0].name",
		"keep_only_set": "true",
	})
	a.Execute("exec1", inputWith(map[string]any{
		"users": []any{map[string]any{"name": "Ada"}},
	}))
	p := readPayload(t, events)
	if p["type"] != "success" || p["first"] != "Ada" {
		t.Fatalf("unexpected payload: %+v", p)
	}
}

func TestNativeMapArgs(t *testing.T) {
	a, events := newTestAction(t, map[string]any{
		"fields":        map[string]any{"n": 7, "ok": true, "tags": []any{"a", "b"}},
		"keep_only_set": true,
	})
	a.Execute("exec1", nil)
	p := readPayload(t, events)
	if p["type"] != "success" {
		t.Fatalf("expected success, got %+v", p)
	}
	if p["n"] != float64(7) || p["ok"] != true {
		t.Fatalf("unexpected payload: %+v", p)
	}
	tags, ok := p["tags"].([]any)
	if !ok || len(tags) != 2 {
		t.Fatalf("unexpected tags: %+v", p["tags"])
	}
}

func TestInvalidArgsTypeIsError(t *testing.T) {
	a, events := newTestAction(t, "nope")
	a.Execute("exec1", nil)
	if p := readPayload(t, events); p["type"] != "error" {
		t.Fatalf("expected error, got %+v", p)
	}
}

func TestBraceConcatWithSuffix(t *testing.T) {
	a, events := newTestAction(t, map[string]string{
		"field.backup":  "{{ $input.file }}.bkp",
		"keep_only_set": "true",
	})
	a.Execute("exec1", inputWith(map[string]any{"file": "report.pdf"}))
	p := readPayload(t, events)
	if p["type"] != "success" || p["backup"] != "report.pdf.bkp" {
		t.Fatalf("unexpected payload: %+v", p)
	}
}

func TestBraceUpperLowerTrim(t *testing.T) {
	a, events := newTestAction(t, map[string]string{
		"field.shout":   "{{ $input.name | upper }}",
		"field.quiet":   "{{ $input.name | lower }}",
		"field.neat":    "{{ $input.padded | trim }}",
		"keep_only_set": "true",
	})
	a.Execute("exec1", inputWith(map[string]any{"name": "aDa", "padded": "  hi  "}))
	p := readPayload(t, events)
	if p["type"] != "success" {
		t.Fatalf("expected success, got %+v", p)
	}
	if p["shout"] != "ADA" || p["quiet"] != "ada" || p["neat"] != "hi" {
		t.Fatalf("unexpected payload: %+v", p)
	}
}

func TestBraceWholeValueKeepsNativeType(t *testing.T) {
	a, events := newTestAction(t, map[string]string{
		"field.n":       "{{ $input.count }}",
		"keep_only_set": "true",
	})
	a.Execute("exec1", inputWith(map[string]any{"count": float64(7)}))
	p := readPayload(t, events)
	if p["type"] != "success" || p["n"] != float64(7) {
		t.Fatalf("unexpected payload: %+v", p)
	}
}

func TestBraceFiltersProduceStrings(t *testing.T) {
	a, events := newTestAction(t, map[string]string{
		"field.n":       "{{ $input.count | trim }}",
		"keep_only_set": "true",
	})
	a.Execute("exec1", inputWith(map[string]any{"count": float64(7)}))
	p := readPayload(t, events)
	if p["type"] != "success" || p["n"] != "7" {
		t.Fatalf("unexpected payload: %+v", p)
	}
}

func TestBraceReplaceAndAffixes(t *testing.T) {
	a, events := newTestAction(t, map[string]string{
		"field.a":       "{{ $input.s | replace:a:b }}",
		"field.b":       "{{ $input.p | trimPrefix:/tmp/ }}",
		"field.c":       "{{ $input.p | trimSuffix:.tmp }}",
		"keep_only_set": "true",
	})
	a.Execute("exec1", inputWith(map[string]any{"s": "aaa", "p": "/tmp/x.tmp"}))
	p := readPayload(t, events)
	if p["type"] != "success" {
		t.Fatalf("expected success, got %+v", p)
	}
	if p["a"] != "bbb" || p["b"] != "x.tmp" || p["c"] != "/tmp/x" {
		t.Fatalf("unexpected payload: %+v", p)
	}
}

func TestBraceDefaultFallback(t *testing.T) {
	a, events := newTestAction(t, map[string]string{
		"field.a":       "{{ $input.missing | default:n/a }}",
		"field.b":       "{{ $input.present | default:n/a }}",
		"field.c":       "{{ $input.missing | default:x | upper }}",
		"keep_only_set": "true",
	})
	a.Execute("exec1", inputWith(map[string]any{"present": "here"}))
	p := readPayload(t, events)
	if p["type"] != "success" {
		t.Fatalf("expected success, got %+v", p)
	}
	if p["a"] != "n/a" || p["b"] != "here" || p["c"] != "X" {
		t.Fatalf("unexpected payload: %+v", p)
	}
}

func TestBraceLiteralAndChainedFilters(t *testing.T) {
	a, events := newTestAction(t, map[string]string{
		"field.a":       `{{ "hi" | upper }}`,
		"field.b":       "{{ $input.name | trim | upper }}",
		"keep_only_set": "true",
	})
	a.Execute("exec1", inputWith(map[string]any{"name": "  ada  "}))
	p := readPayload(t, events)
	if p["type"] != "success" || p["a"] != "HI" || p["b"] != "ADA" {
		t.Fatalf("unexpected payload: %+v", p)
	}
}

func TestBraceMixesWithBareRefs(t *testing.T) {
	a, events := newTestAction(t, map[string]string{
		"field.a":       "{{ $input.first | upper }}-$input.last",
		"keep_only_set": "true",
	})
	a.Execute("exec1", inputWith(map[string]any{"first": "ada", "last": "lovelace"}))
	p := readPayload(t, events)
	if p["type"] != "success" || p["a"] != "ADA-lovelace" {
		t.Fatalf("unexpected payload: %+v", p)
	}
}

func TestBraceInJSONMode(t *testing.T) {
	a, events := newTestAction(t, map[string]string{
		"mode":          "json",
		"json":          `{"file": "{{ $input.file }}.bkp", "n": {{ $input.count }}, "u": "{{ $input.name | upper }}"}`,
		"keep_only_set": "true",
	})
	a.Execute("exec1", inputWith(map[string]any{"file": "r.pdf", "count": float64(3), "name": "ada"}))
	p := readPayload(t, events)
	if p["type"] != "success" {
		t.Fatalf("expected success, got %+v", p)
	}
	if p["file"] != "r.pdf.bkp" || p["n"] != float64(3) || p["u"] != "ADA" {
		t.Fatalf("unexpected payload: %+v", p)
	}
}

func TestBraceUnknownFilterIsError(t *testing.T) {
	a, events := newTestAction(t, map[string]string{"field.a": "{{ $input.x | frobnicate }}"})
	a.Execute("exec1", inputWith(map[string]any{"x": "1"}))
	if p := readPayload(t, events); p["type"] != "error" {
		t.Fatalf("expected error, got %+v", p)
	}
}

func TestBraceUnclosedIsError(t *testing.T) {
	a, events := newTestAction(t, map[string]string{"field.a": "{{ $input.x"})
	a.Execute("exec1", inputWith(map[string]any{"x": "1"}))
	if p := readPayload(t, events); p["type"] != "error" {
		t.Fatalf("expected error, got %+v", p)
	}
}

func TestBraceBadExpressionIsError(t *testing.T) {
	a, events := newTestAction(t, map[string]string{"field.a": "{{ nope }}"})
	a.Execute("exec1", inputWith(map[string]any{"x": "1"}))
	if p := readPayload(t, events); p["type"] != "error" {
		t.Fatalf("expected error, got %+v", p)
	}
}

func TestBraceErrorSkippedWithIgnoreFlag(t *testing.T) {
	a, events := newTestAction(t, map[string]string{
		"field.good":         `{{ "ok" | upper }}`,
		"field.bad":          "{{ $input.x | frobnicate }}",
		"keep_only_set":      "true",
		"ignore_type_errors": "true",
	})
	a.Execute("exec1", inputWith(map[string]any{"x": "1"}))
	p := readPayload(t, events)
	if p["type"] != "success" || p["good"] != "OK" {
		t.Fatalf("unexpected payload: %+v", p)
	}
	if _, ok := p["bad"]; ok {
		t.Fatalf("expected bad field skipped, got %+v", p)
	}
}

func TestBraceLargeNumberStaysReadable(t *testing.T) {
	a, events := newTestAction(t, map[string]string{
		"field.a":       "id-{{ $input.id }}",
		"keep_only_set": "true",
	})
	a.Execute("exec1", inputWith(map[string]any{"id": float64(23423532)}))
	p := readPayload(t, events)
	if p["type"] != "success" || p["a"] != "id-23423532" {
		t.Fatalf("unexpected payload: %+v", p)
	}
}
