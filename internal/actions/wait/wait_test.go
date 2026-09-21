package wait

import (
	"testing"
	"time"

	"github.com/mcmx/nitejaguar/common"
)

func TestParseDurationSupportsFractionsAndFriendlyUnits(t *testing.T) {
	tests := map[string]time.Duration{
		"1.5s":            1500 * time.Millisecond,
		"1.2hours":        72 * time.Minute,
		"1h 20m":          80 * time.Minute,
		"2 weeks, 3 days": 17 * 24 * time.Hour,
		"1 fortnight":     14 * 24 * time.Hour,
		"2":               2 * time.Second,
	}
	for input, want := range tests {
		got, err := ParseDuration(input)
		if err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %v, %v; want %v", input, got, err, want)
		}
	}
}

func TestParseDurationRejectsInvalidValues(t *testing.T) {
	for _, input := range []string{"", "-1s", "1lightyear", "1h nope", "1h 2"} {
		if _, err := ParseDuration(input); err == nil {
			t.Errorf("ParseDuration(%q) unexpectedly succeeded", input)
		}
	}
}

func TestExecuteWaitsAndEmitsResult(t *testing.T) {
	events := make(chan common.ResultData, 1)
	a, err := New(events, common.ActionArgs{Id: "action_test", ActionName: "wait", Args: map[string]any{"duration": "2ms"}})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	a.Execute("exec_test", nil)
	if elapsed := time.Since(start); elapsed < time.Millisecond {
		t.Fatalf("wait returned too quickly: %v", elapsed)
	}
	result := <-events
	p := result.Payload.(map[string]any)
	if p["type"] != "success" {
		t.Fatalf("unexpected payload: %+v", p)
	}
}

func TestExecuteResolvesInputDuration(t *testing.T) {
	events := make(chan common.ResultData, 1)
	a, _ := New(events, common.ActionArgs{Id: "action_test", ActionName: "wait", Args: map[string]string{"duration": "$input.delay"}})
	a.Execute("exec_test", []any{common.ResultData{Payload: map[string]any{"delay": "0s"}}})
	p := (<-events).Payload.(map[string]any)
	if p["type"] != "success" {
		t.Fatalf("unexpected payload: %+v", p)
	}
}

func TestActionNameWaitIsAccepted(t *testing.T) {
	events := make(chan common.ResultData, 1)
	a, err := New(events, common.ActionArgs{Id: "action_test", ActionName: "wait", Args: map[string]string{"duration": "0s"}})
	if err != nil {
		t.Fatal(err)
	}
	a.Execute("exec_test", nil)
	if p := (<-events).Payload.(map[string]any); p["type"] != "success" {
		t.Fatalf("unexpected payload: %+v", p)
	}
}
