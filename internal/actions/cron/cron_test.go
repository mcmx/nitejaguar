package cron

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/mcmx/nitejaguar/common"

	robcron "github.com/robfig/cron/v3"
)

// newTestTrigger builds the trigger with the given args and a buffered event
// channel the test drains itself.
func newTestTrigger(t *testing.T, args any) (*cronTrigger, chan common.ResultData) {
	t.Helper()
	events := make(chan common.ResultData, 8)
	a, err := New(events, common.ActionArgs{
		Id:         "trigger_test",
		Name:       "test",
		ActionType: "trigger",
		ActionName: "cron",
		Args:       args,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	return a.(*cronTrigger), events
}

// startExecute runs Execute in the background and returns a function that
// stops the trigger and waits for the loop to exit.
func startExecute(t *testing.T, trigger *cronTrigger, executionID string) func() {
	t.Helper()
	done := make(chan struct{})
	go func() {
		trigger.Execute(executionID, nil)
		close(done)
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			if err := trigger.Stop(); err != nil {
				t.Errorf("Stop error: %v", err)
			}
		})
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Errorf("Execute did not return after Stop")
		}
	}
}

func receive(t *testing.T, events chan common.ResultData, timeout time.Duration) common.ResultData {
	t.Helper()
	select {
	case r := <-events:
		return r
	case <-time.After(timeout):
		t.Fatalf("timed out after %v waiting for a cron result", timeout)
		return common.ResultData{}
	}
}

func payloadOf(t *testing.T, r common.ResultData) map[string]any {
	t.Helper()
	p, ok := r.Payload.(map[string]any)
	if !ok {
		t.Fatalf("unexpected payload type %T", r.Payload)
	}
	return p
}

func TestNewAppliesDefaults(t *testing.T) {
	trigger, _ := newTestTrigger(t, nil)
	if trigger.scheduleDesc != defaultSpec {
		t.Errorf("schedule = %q, want %q", trigger.scheduleDesc, defaultSpec)
	}
	if trigger.format != "" {
		t.Errorf("format = %q, want empty (RFC3339 default)", trigger.format)
	}
	if trigger.tzName != "Local" || trigger.loc != time.Local {
		t.Errorf("timezone = %q/%v, want Local", trigger.tzName, trigger.loc)
	}
	if trigger.runOnStart {
		t.Errorf("run_on_start = true, want false by default")
	}
	if trigger.data.ActionType != "trigger" {
		t.Errorf("ActionType = %q, want trigger", trigger.data.ActionType)
	}
}

func TestNewRejectsInvalidDefinitions(t *testing.T) {
	cases := map[string]any{
		"bad cron expression": map[string]string{"cron": "not a schedule"},
		"too many fields":     map[string]string{"cron": "* * * * * * *"},
		"bad interval":        map[string]string{"interval": "every so often"},
		"zero interval":       map[string]string{"interval": "0s"},
		"bad timezone":        map[string]string{"timezone": "Mars/Olympus"},
		"bad run_on_start":    map[string]string{"run_on_start": "maybe"},
	}
	for name, args := range cases {
		events := make(chan common.ResultData, 1)
		if _, err := New(events, common.ActionArgs{ActionName: "cron", Args: args}); err == nil {
			t.Errorf("%s: New succeeded, want error", name)
		}
	}
}

func TestNewAcceptsIntervalOverCron(t *testing.T) {
	trigger, _ := newTestTrigger(t, map[string]string{"cron": "0 0 * * *", "interval": "90s"})
	if trigger.scheduleDesc != "90s" {
		t.Errorf("schedule = %q, want interval %q to win", trigger.scheduleDesc, "90s")
	}
}

func TestNewAcceptsDescriptorsAndSecondsField(t *testing.T) {
	for spec, want := range map[string]string{
		"@every 30s":     "@every 30s",
		"@daily":         "@daily",
		"*/15 * * * * *": "*/15 * * * * *",
	} {
		trigger, _ := newTestTrigger(t, map[string]string{"cron": spec})
		if trigger.scheduleDesc != want {
			t.Errorf("cron %q: schedule = %q, want %q", spec, trigger.scheduleDesc, want)
		}
	}
}

func TestNewTimezoneOverridesScheduleLocation(t *testing.T) {
	// A fixed zone makes the assertion independent of the host timezone.
	loc := time.FixedZone("UTC-4", -4*3600)
	args := map[string]string{"cron": "0 9 * * *"}
	schedule, desc, err := buildSchedule(args, true, loc)
	if err != nil {
		t.Fatalf("buildSchedule: %v", err)
	}
	if desc != "0 9 * * *" {
		t.Fatalf("schedule desc = %q", desc)
	}
	base := time.Date(2026, time.January, 1, 0, 0, 0, 0, loc)
	next := schedule.Next(base)
	want := time.Date(2026, time.January, 1, 9, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Errorf("next fire = %v, want %v (9am in the trigger timezone)", next, want)
	}
}

func TestNewTimezoneArgIsLoaded(t *testing.T) {
	trigger, _ := newTestTrigger(t, map[string]string{"timezone": "UTC"})
	if trigger.tzName != "UTC" || trigger.loc != time.UTC {
		t.Errorf("timezone = %q/%v, want UTC", trigger.tzName, trigger.loc)
	}
	// The explicit timezone must also drive cron field matching, not just
	// the rendered payload.
	spec, ok := trigger.schedule.(*robcron.SpecSchedule)
	if !ok {
		t.Fatalf("schedule type %T, want *cron.SpecSchedule", trigger.schedule)
	}
	if spec.Location != time.UTC {
		t.Errorf("schedule location = %v, want UTC", spec.Location)
	}
}

func TestExecuteRunOnStartEmitsNow(t *testing.T) {
	trigger, events := newTestTrigger(t, map[string]string{
		"interval":     "1h",
		"run_on_start": "true",
	})
	stop := startExecute(t, trigger, "exec_1")
	defer stop()

	before := time.Now()
	result := receive(t, events, 2*time.Second)
	after := time.Now()

	if result.ActionID != "trigger_test" {
		t.Errorf("ActionID = %q, want trigger_test", result.ActionID)
	}
	if result.ActionType != "trigger" || result.ActionName != "cron" {
		t.Errorf("type/name = %q/%q, want trigger/cron", result.ActionType, result.ActionName)
	}
	if result.ExecutionID != "exec_1" {
		t.Errorf("ExecutionID = %q, want exec_1", result.ExecutionID)
	}
	if result.CreatedAt.Before(before.Add(-time.Second)) || result.CreatedAt.After(after.Add(time.Second)) {
		t.Errorf("CreatedAt = %v, want ~now (%v..%v)", result.CreatedAt, before, after)
	}

	payload := payloadOf(t, result)
	if payload["type"] != "success" || payload["trigger"] != "cron" {
		t.Errorf("payload type/trigger = %v/%v, want success/cron", payload["type"], payload["trigger"])
	}
	if payload["schedule"] != "1h" {
		t.Errorf("payload schedule = %v, want 1h", payload["schedule"])
	}
	datetime, ok := payload["datetime"].(string)
	if !ok {
		t.Fatalf("payload datetime missing: %v", payload)
	}
	parsed, err := time.Parse(time.RFC3339, datetime)
	if err != nil {
		t.Fatalf("payload datetime %q is not RFC3339: %v", datetime, err)
	}
	if parsed.Before(before.Add(-2*time.Second)) || parsed.After(after.Add(2*time.Second)) {
		t.Errorf("payload datetime = %v, want ~now", parsed)
	}
	ts, ok := payload["timestamp"].(int64)
	if !ok {
		t.Fatalf("payload timestamp type %T, want int64", payload["timestamp"])
	}
	if diff := parsed.Unix() - ts; diff < -1 || diff > 1 {
		t.Errorf("timestamp %d does not match datetime %v", ts, parsed)
	}
	if _, ok := payload["timestamp_ms"].(int64); !ok {
		t.Errorf("payload timestamp_ms type %T, want int64", payload["timestamp_ms"])
	}
	if payload["format"] != time.RFC3339 {
		t.Errorf("payload format = %v, want RFC3339", payload["format"])
	}
	if payload["timezone"] != "Local" {
		t.Errorf("payload timezone = %v, want Local", payload["timezone"])
	}
}

func TestExecuteFiresOnSchedule(t *testing.T) {
	trigger, events := newTestTrigger(t, map[string]string{"interval": "1s"})
	stop := startExecute(t, trigger, "")
	defer stop()

	start := time.Now()
	result := receive(t, events, 3*time.Second)
	if fire := time.Since(start); fire > 3*time.Second {
		t.Errorf("first fire took %v", fire)
	}
	payload := payloadOf(t, result)
	ts, ok := payload["timestamp"].(int64)
	if !ok {
		t.Fatalf("payload timestamp type %T", payload["timestamp"])
	}
	if delta := time.Now().Unix() - ts; delta < -2 || delta > 2 {
		t.Errorf("payload timestamp %d is %ds away from now", ts, delta)
	}
	// A second fire proves the trigger keeps looping.
	second := receive(t, events, 3*time.Second)
	if payloadOf(t, second)["timestamp"].(int64) == ts {
		t.Errorf("second fire repeated the first timestamp %d", ts)
	}
}

func TestExecuteWithoutRunOnStartWaits(t *testing.T) {
	trigger, events := newTestTrigger(t, map[string]string{"interval": "1h"})
	stop := startExecute(t, trigger, "")
	defer stop()

	select {
	case r := <-events:
		t.Fatalf("trigger fired immediately without run_on_start: %+v", r)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestStopEndsExecuteAndIsIdempotent(t *testing.T) {
	trigger, _ := newTestTrigger(t, nil) // "* * * * *": up to 60s until next fire
	stop := startExecute(t, trigger, "")
	stop()
	if err := trigger.Stop(); err != nil {
		t.Errorf("second Stop error: %v", err)
	}
}

func TestFormatAndTimezoneVariants(t *testing.T) {
	cases := []struct {
		args map[string]string
		want func(t *testing.T, payload map[string]any)
	}{
		{
			args: map[string]string{"format": "unix", "run_on_start": "true", "interval": "1h"},
			want: func(t *testing.T, payload map[string]any) {
				if _, err := strconv.ParseInt(payload["datetime"].(string), 10, 64); err != nil {
					t.Errorf("datetime %v is not epoch seconds", payload["datetime"])
				}
				if payload["format"] != "unix" {
					t.Errorf("format = %v, want unix", payload["format"])
				}
			},
		},
		{
			args: map[string]string{"format": "unix_ms", "run_on_start": "true", "interval": "1h"},
			want: func(t *testing.T, payload map[string]any) {
				if _, err := strconv.ParseInt(payload["datetime"].(string), 10, 64); err != nil {
					t.Errorf("datetime %v is not epoch millis", payload["datetime"])
				}
			},
		},
		{
			args: map[string]string{"format": "2006-01-02", "timezone": "UTC", "run_on_start": "true", "interval": "1h"},
			want: func(t *testing.T, payload map[string]any) {
				if _, err := time.Parse("2006-01-02", payload["datetime"].(string)); err != nil {
					t.Errorf("datetime %v does not match layout: %v", payload["datetime"], err)
				}
				if payload["timezone"] != "UTC" || payload["format"] != "2006-01-02" {
					t.Errorf("timezone/format = %v/%v", payload["timezone"], payload["format"])
				}
				// Rendered in UTC: the epoch second must agree with the day.
				day := payload["datetime"].(string)
				utc := time.Unix(payload["timestamp"].(int64), 0).UTC().Format("2006-01-02")
				if day != utc {
					t.Errorf("datetime %q is the local day, want UTC day %q", day, utc)
				}
			},
		},
	}
	for _, c := range cases {
		trigger, events := newTestTrigger(t, c.args)
		stop := startExecute(t, trigger, "")
		c.want(t, payloadOf(t, receive(t, events, 2*time.Second)))
		stop()
	}
}

func TestArgsToMapAcceptsNilAndTypedMaps(t *testing.T) {
	if got, err := argsToMap(nil); err != nil || len(got) != 0 {
		t.Errorf("argsToMap(nil) = %v, %v; want empty map, nil", got, err)
	}
	if got, err := argsToMap(map[string]any{"cron": "5 * * * *"}); err != nil || got["cron"] != "5 * * * *" {
		t.Errorf("argsToMap(map[string]any) = %v, %v", got, err)
	}
	if _, err := argsToMap("not a map"); err == nil {
		t.Errorf("argsToMap(string) succeeded, want error")
	}
}

func TestParseBool(t *testing.T) {
	for _, truthy := range []string{"true", "TRUE", "1", "yes", "on"} {
		if got, err := parseBool(truthy); err != nil || !got {
			t.Errorf("parseBool(%q) = %v, %v; want true", truthy, got, err)
		}
	}
	for _, falsy := range []string{"", "0", "false", "no", "off", "  "} {
		if got, err := parseBool(falsy); err != nil || got {
			t.Errorf("parseBool(%q) = %v, %v; want false", falsy, got, err)
		}
	}
	if _, err := parseBool("yep"); err == nil {
		t.Errorf("parseBool(yep) succeeded, want error")
	}
}
