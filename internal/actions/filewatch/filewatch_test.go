package filewatch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mcmx/nitejaguar/common"
)

// newTestTrigger builds the trigger with the given args and a buffered
// event channel the test drains itself.
func newTestTrigger(t *testing.T, args any) (*watchdog, chan common.ResultData) {
	t.Helper()
	events := make(chan common.ResultData, 8)
	a, err := New(events, common.ActionArgs{
		Id:         "trigger_test",
		Name:       "test",
		ActionType: "trigger",
		ActionName: "filewatch",
		Args:       args,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	return a.(*watchdog), events
}

// startExecute runs Execute in the background and returns a function that
// stops the trigger and waits for the loop to exit.
func startExecute(t *testing.T, trigger *watchdog, executionID string) func() {
	t.Helper()
	done := make(chan struct{})
	go func() {
		trigger.Execute(executionID, nil)
		close(done)
	}()
	// Give Execute a moment to install the watcher before the test acts.
	time.Sleep(200 * time.Millisecond)
	stopped := false
	return func() {
		if !stopped {
			stopped = true
			if err := trigger.Stop(); err != nil {
				t.Errorf("Stop error: %v", err)
			}
		}
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
		t.Fatalf("timed out after %v waiting for a filewatch result", timeout)
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

func TestNewRejectsBadConfig(t *testing.T) {
	dir := t.TempDir()
	good := map[string]string{"path": dir, "expect_within": "30m"}
	bad := map[string]map[string]string{
		"missing path":           {"expect_within": "30m"},
		"missing expect_within":  {"path": dir},
		"bad expect_within":      {"path": dir, "expect_within": "sometime"},
		"zero expect_within":     {"path": dir, "expect_within": "0s"},
		"negative expect_within": {"path": dir, "expect_within": "-5m"},
		"bad interval":           {"path": dir, "expect_within": "30m", "interval": "often"},
		"zero interval":          {"path": dir, "expect_within": "30m", "interval": "0s"},
		"bad cron":               {"path": dir, "expect_within": "30m", "cron": "not a schedule"},
		"bad timezone":           {"path": dir, "expect_within": "30m", "timezone": "Mars/Olympus"},
		"bad pattern":            {"path": dir, "expect_within": "30m", "pattern": "[unclosed"},
		"bad event_type":         {"path": dir, "expect_within": "30m", "event_type": "explode"},
		"bad debounce_ms":        {"path": dir, "expect_within": "30m", "debounce_ms": "lots"},
		"negative debounce_ms":   {"path": dir, "expect_within": "30m", "debounce_ms": "-1"},
	}
	for name, args := range bad {
		merged := map[string]string{}
		for k, v := range good {
			merged[k] = v
		}
		for k, v := range args {
			merged[k] = v
		}
		if _, ok := args["path"]; !ok {
			delete(merged, "path")
		}
		if _, ok := args["expect_within"]; !ok {
			delete(merged, "expect_within")
		}
		events := make(chan common.ResultData, 1)
		if _, err := New(events, common.ActionArgs{ActionName: "filewatch", Args: merged}); err == nil {
			t.Errorf("%s: New succeeded, want error", name)
		}
	}
}

func TestNewAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	trigger, _ := newTestTrigger(t, map[string]string{"path": dir, "expect_within": "30m"})
	if trigger.pattern != "*" {
		t.Errorf("pattern = %q, want %q", trigger.pattern, "*")
	}
	if trigger.expectWithin != 30*time.Minute {
		t.Errorf("expectWithin = %v, want 30m", trigger.expectWithin)
	}
	if trigger.data.ActionType != "trigger" {
		t.Errorf("ActionType = %q, want trigger", trigger.data.ActionType)
	}
	if trigger.schedule != nil {
		t.Errorf("schedule set in watch mode, want nil (no scheduled ticks)")
	}
	if trigger.debounceMs != 500 {
		t.Errorf("debounceMs = %d, want 500", trigger.debounceMs)
	}
}

func TestNewAcceptsIntervalOverCron(t *testing.T) {
	dir := t.TempDir()
	trigger, _ := newTestTrigger(t, map[string]string{
		"path": dir, "expect_within": "30m",
		"cron": "0 9 * * *", "interval": "1h",
	})
	if trigger.scheduleDesc != "1h" {
		t.Errorf("schedule = %q, want interval %q to win", trigger.scheduleDesc, "1h")
	}
}

func TestArrivalEmitsSuccess(t *testing.T) {
	dir := t.TempDir()
	trigger, events := newTestTrigger(t, map[string]string{
		"path": dir, "expect_within": "5s",
	})
	stop := startExecute(t, trigger, "exec_1")
	defer stop()

	if err := os.WriteFile(filepath.Join(dir, "drop.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	result := receive(t, events, 3*time.Second)

	if result.ActionID != "trigger_test" {
		t.Errorf("ActionID = %q, want trigger_test", result.ActionID)
	}
	if result.ActionType != "trigger" || result.ActionName != "filewatch" {
		t.Errorf("type/name = %q/%q, want trigger/filewatch", result.ActionType, result.ActionName)
	}
	if result.ExecutionID != "exec_1" {
		t.Errorf("ExecutionID = %q, want exec_1", result.ExecutionID)
	}
	payload := payloadOf(t, result)
	if payload["type"] != "success" || payload["trigger"] != "filewatch" {
		t.Errorf("payload type/trigger = %v/%v, want success/filewatch", payload["type"], payload["trigger"])
	}
	if payload["file"] != filepath.Join(dir, "drop.txt") {
		t.Errorf("payload file = %v, want the dropped file", payload["file"])
	}
	if payload["event"] != "create" {
		t.Errorf("payload event = %v, want create", payload["event"])
	}
}

func TestMissingEmitsTimeout(t *testing.T) {
	dir := t.TempDir()
	trigger, events := newTestTrigger(t, map[string]string{
		"path": dir, "expect_within": "100ms",
	})
	stop := startExecute(t, trigger, "")
	defer stop()

	result := receive(t, events, 3*time.Second)
	payload := payloadOf(t, result)
	if payload["type"] != "missing" || payload["trigger"] != "filewatch" {
		t.Errorf("payload type/trigger = %v/%v, want missing/filewatch", payload["type"], payload["trigger"])
	}
	if payload["expect_within"] != "100ms" {
		t.Errorf("payload expect_within = %v, want 100ms", payload["expect_within"])
	}
}

func TestWatchModeResetsWindowAfterArrival(t *testing.T) {
	dir := t.TempDir()
	trigger, events := newTestTrigger(t, map[string]string{
		"path": dir, "expect_within": "300ms",
	})
	stop := startExecute(t, trigger, "")
	defer stop()

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	first := receive(t, events, 3*time.Second)
	if payloadOf(t, first)["type"] != "success" {
		t.Fatalf("first result type = %v, want success", payloadOf(t, first)["type"])
	}
	// The arrival resets the clock: the next result is a missing timeout,
	// not an immediate expiry of the pre-arrival window.
	second := receive(t, events, 3*time.Second)
	if payloadOf(t, second)["type"] != "missing" {
		t.Fatalf("second result type = %v, want missing", payloadOf(t, second)["type"])
	}
}

func TestScheduleModeArrivalClosesWindow(t *testing.T) {
	dir := t.TempDir()
	trigger, events := newTestTrigger(t, map[string]string{
		"path": dir, "expect_within": "300ms", "interval": "10s",
	})
	stop := startExecute(t, trigger, "")
	defer stop()

	if err := os.WriteFile(filepath.Join(dir, "early.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if got := payloadOf(t, receive(t, events, 3*time.Second))["type"]; got != "success" {
		t.Fatalf("first result type = %v, want success", got)
	}
	// The window closed on arrival and the next tick is 10s away: no
	// missing timeout may fire within the old window.
	select {
	case r := <-events:
		t.Fatalf("unexpected result after arrival-closed window: %+v", r)
	case <-time.After(600 * time.Millisecond):
	}
}

func TestPatternFiltersNonMatchingFiles(t *testing.T) {
	dir := t.TempDir()
	trigger, events := newTestTrigger(t, map[string]string{
		"path": dir, "expect_within": "5s", "pattern": "*.pdf",
	})
	stop := startExecute(t, trigger, "")
	defer stop()

	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	select {
	case r := <-events:
		t.Fatalf("non-matching file produced a result: %+v", r)
	case <-time.After(400 * time.Millisecond):
	}
	if err := os.WriteFile(filepath.Join(dir, "statement.pdf"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	result := receive(t, events, 3*time.Second)
	if got := payloadOf(t, result)["type"]; got != "success" {
		t.Fatalf("matching file result type = %v, want success", got)
	}
}

func TestStopIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	trigger, _ := newTestTrigger(t, map[string]string{"path": dir, "expect_within": "30m"})
	stop := startExecute(t, trigger, "")
	stop()
	if err := trigger.Stop(); err != nil {
		t.Errorf("second Stop error: %v", err)
	}
}
