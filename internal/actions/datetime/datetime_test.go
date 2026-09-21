package datetime

import (
	"testing"
	"time"

	"github.com/mcmx/nitejaguar/common"
)

func newTestAction(t *testing.T, args any) (common.Action, chan common.ResultData) {
	t.Helper()
	events := make(chan common.ResultData, 4)
	a, err := New(events, common.ActionArgs{
		Id:         "action_test",
		Name:       "test",
		ActionType: "action",
		ActionName: "datetimeAction",
		Args:       args,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	return a, events
}

func readPayload(t *testing.T, ch chan common.ResultData) map[string]any {
	t.Helper()
	select {
	case r := <-ch:
		p, ok := r.Payload.(map[string]any)
		if !ok {
			t.Fatalf("unexpected payload type %T", r.Payload)
		}
		return p
	default:
		r := <-ch
		p, ok := r.Payload.(map[string]any)
		if !ok {
			t.Fatalf("unexpected payload type %T", r.Payload)
		}
		return p
	}
}

func withFixedNow(t *testing.T, now time.Time) {
	t.Helper()
	old := nowFunc
	nowFunc = func() time.Time { return now }
	t.Cleanup(func() { nowFunc = old })
}

func TestDefaultFormatIsRFC3339(t *testing.T) {
	fixed := time.Date(2026, 9, 21, 12, 34, 56, 0, time.UTC)
	withFixedNow(t, fixed)
	a, events := newTestAction(t, map[string]string{})
	a.Execute("exec1", nil)
	p := readPayload(t, events)
	if p["type"] != "success" {
		t.Fatalf("expected success, got %+v", p)
	}
	if p["operation"] != string(OpGetCurrentDate) {
		t.Fatalf("expected operation getCurrentDate, got %+v", p)
	}
	if p["datetime"] != fixed.In(time.Local).Format(time.RFC3339) {
		t.Fatalf("expected %q, got %q", fixed.In(time.Local).Format(time.RFC3339), p["datetime"])
	}
	if p["format"] != time.RFC3339 {
		t.Fatalf("expected format RFC3339, got %q", p["format"])
	}
}

func TestCustomGoLayoutAndTimezone(t *testing.T) {
	// 12:00 UTC == 14:00 Europe/Madrid (CEST, September).
	fixed := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	withFixedNow(t, fixed)
	a, events := newTestAction(t, map[string]string{
		"format":   "2006-01-02 15:04",
		"timezone": "Europe/Madrid",
	})
	a.Execute("exec1", nil)
	p := readPayload(t, events)
	if p["type"] != "success" {
		t.Fatalf("expected success, got %+v", p)
	}
	if p["datetime"] != "2026-09-21 14:00" {
		t.Fatalf("expected Madrid time, got %q", p["datetime"])
	}
	if p["timezone"] != "Europe/Madrid" {
		t.Fatalf("expected timezone echo, got %q", p["timezone"])
	}
}

func TestUnixFormats(t *testing.T) {
	fixed := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	withFixedNow(t, fixed)
	for _, f := range []string{"unix", "unix_ms"} {
		a, events := newTestAction(t, map[string]string{"format": f})
		a.Execute("exec1", nil)
		p := readPayload(t, events)
		if p["type"] != "success" {
			t.Fatalf("format %s: expected success, got %+v", f, p)
		}
		if _, ok := p["datetime"].(string); !ok {
			t.Fatalf("format %s: datetime not a string: %+v", f, p)
		}
	}
	a, events := newTestAction(t, map[string]string{"format": "unix"})
	a.Execute("exec1", nil)
	if p := readPayload(t, events); p["datetime"] != "1789992000" {
		t.Fatalf("expected epoch seconds, got %q", p["datetime"])
	}
}

func TestOutputFieldDuplicated(t *testing.T) {
	fixed := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	withFixedNow(t, fixed)
	a, events := newTestAction(t, map[string]string{"output_field": "now"})
	a.Execute("exec1", nil)
	p := readPayload(t, events)
	if p["type"] != "success" {
		t.Fatalf("expected success, got %+v", p)
	}
	if p["output_field"] != "now" {
		t.Fatalf("expected output_field echo, got %+v", p)
	}
	if p["now"] != p["datetime"] {
		t.Fatalf("expected duplicated field, got %+v", p)
	}
}

func TestInvalidTimezoneIsError(t *testing.T) {
	a, events := newTestAction(t, map[string]string{"timezone": "Nope/Nowhere"})
	a.Execute("exec1", nil)
	if p := readPayload(t, events); p["type"] != "error" {
		t.Fatalf("expected error, got %+v", p)
	}
}

func TestUnknownOperationIsError(t *testing.T) {
	a, events := newTestAction(t, map[string]string{"operation": "timeTravel"})
	a.Execute("exec1", nil)
	if p := readPayload(t, events); p["type"] != "error" {
		t.Fatalf("expected error, got %+v", p)
	}
}

func TestFormatResolvesFromInput(t *testing.T) {
	fixed := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	withFixedNow(t, fixed)
	a, events := newTestAction(t, map[string]string{"format": "$input.layout"})
	a.Execute("exec1", []any{common.ResultData{
		Payload: map[string]any{"layout": "2006-01-02"},
	}})
	p := readPayload(t, events)
	if p["type"] != "success" {
		t.Fatalf("expected success, got %+v", p)
	}
	if p["datetime"] != "2026-09-21" {
		t.Fatalf("expected resolved layout, got %q", p["datetime"])
	}
}

func TestMapStringAnyArgs(t *testing.T) {
	withFixedNow(t, time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC))
	a, events := newTestAction(t, map[string]any{"format": "2006", "timezone": "UTC"})
	a.Execute("exec1", nil)
	if p := readPayload(t, events); p["type"] != "success" || p["datetime"] != "2026" {
		t.Fatalf("unexpected payload: %+v", p)
	}
}

func TestInvalidArgsTypeIsError(t *testing.T) {
	a, events := newTestAction(t, "nope")
	a.Execute("exec1", nil)
	if p := readPayload(t, events); p["type"] != "error" {
		t.Fatalf("expected error, got %+v", p)
	}
}
