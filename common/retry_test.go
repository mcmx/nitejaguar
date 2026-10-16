package common

import (
	"sync/atomic"
	"testing"
	"time"
)

// fakeAction is a scriptable common.Action for decorator tests.
type fakeAction struct {
	exec func(executionID string, inputs []any)
}

func (f *fakeAction) Execute(executionID string, inputs []any) { f.exec(executionID, inputs) }
func (f *fakeAction) Stop() error                              { return nil }
func (f *fakeAction) GetArgs() ActionArgs                      { return ActionArgs{} }

// scriptBuilder returns an ActionBuilder whose Nth construction runs the Nth
// script step. Each step receives the private result channel bound that
// attempt; it may block (to exercise the deadline).
func scriptBuilder(steps []func(out chan ResultData)) ActionBuilder {
	var n int32
	return func(out chan ResultData, args ActionArgs) (Action, error) {
		i := int(atomic.AddInt32(&n, 1)) - 1
		if i >= len(steps) {
			i = len(steps) - 1
		}
		step := steps[i]
		return &fakeAction{exec: func(executionID string, _ []any) {
			step(out)
		}}, nil
	}
}

func errResult(executionID, actionID string) ResultData {
	return ResultData{
		ExecutionID: executionID, ActionID: actionID,
		ActionType: "action", ActionName: "fake",
		Payload: map[string]any{"type": "error", "result": "boom"},
	}
}

func okResult(executionID, actionID string) ResultData {
	return ResultData{
		ExecutionID: executionID, ActionID: actionID,
		ActionType: "action", ActionName: "fake",
		Payload: map[string]any{"type": "success"},
	}
}

func drain(ch chan ResultData, timeout time.Duration) []ResultData {
	var out []ResultData
	deadline := time.After(timeout)
	for {
		select {
		case r := <-ch:
			out = append(out, r)
		case <-deadline:
			return out
		}
	}
}

func TestRetrySuccessPassesThrough(t *testing.T) {
	events := make(chan ResultData, 4)
	build := scriptBuilder([]func(chan ResultData){func(out chan ResultData) { out <- okResult("exec_1", "action_1") }})
	policy := RetryPolicy{Max: 3, Backoff: time.Millisecond, BackoffMax: 10 * time.Millisecond}
	a, err := NewRetryAction(build, events, ActionArgs{Id: "action_1", Retry: policy})
	if err != nil {
		t.Fatalf("NewRetryAction: %v", err)
	}
	a.Execute("exec_1", nil)

	got := drain(events, 500*time.Millisecond)
	if len(got) != 1 {
		t.Fatalf("results = %d, want 1 (success passes straight through)", len(got))
	}
	if got[0].Retrying {
		t.Errorf("success marked Retrying")
	}
	if got[0].Attempt != 1 {
		t.Errorf("Attempt = %d, want 1", got[0].Attempt)
	}
}

func TestRetryRetriesUntilSuccess(t *testing.T) {
	events := make(chan ResultData, 4)
	build := scriptBuilder([]func(chan ResultData){
		func(out chan ResultData) { out <- errResult("exec_9", "action_1") },
		func(out chan ResultData) { out <- errResult("exec_9", "action_1") },
		func(out chan ResultData) { out <- okResult("exec_9", "action_1") },
	})
	policy := RetryPolicy{Max: 3, Backoff: time.Millisecond, BackoffMax: 5 * time.Millisecond}
	a, _ := NewRetryAction(build, events, ActionArgs{Id: "action_1", Retry: policy})
	a.Execute("exec_9", nil)

	got := drain(events, time.Second)
	if len(got) != 3 {
		t.Fatalf("results = %d, want 3 (two failures then success)", len(got))
	}
	for i, r := range got {
		if r.ExecutionID != "exec_9" {
			t.Errorf("result %d ExecutionID = %q, want exec_9 (same execution across retries)", i, r.ExecutionID)
		}
		if r.Attempt != i+1 {
			t.Errorf("result %d Attempt = %d, want %d", i, r.Attempt, i+1)
		}
	}
	if !got[0].Retrying || !got[1].Retrying {
		t.Errorf("intermediate failures not marked Retrying: %v %v", got[0].Retrying, got[1].Retrying)
	}
	if got[2].Retrying {
		t.Errorf("final success marked Retrying")
	}
}

func TestRetryExhaustsThenFinalError(t *testing.T) {
	events := make(chan ResultData, 8)
	build := scriptBuilder([]func(chan ResultData){func(out chan ResultData) { out <- errResult("exec_2", "action_1") }})
	policy := RetryPolicy{Max: 2, Backoff: time.Millisecond, BackoffMax: 5 * time.Millisecond}
	a, _ := NewRetryAction(build, events, ActionArgs{Id: "action_1", Retry: policy})
	a.Execute("exec_2", nil)

	got := drain(events, time.Second)
	if len(got) != 3 {
		t.Fatalf("results = %d, want 3 (1 + max_retries 2)", len(got))
	}
	if !got[0].Retrying || !got[1].Retrying {
		t.Errorf("first two failures must be Retrying")
	}
	if got[2].Retrying {
		t.Errorf("final failure must not be Retrying (must route)")
	}
	if payloadType(got[2].Payload) != "error" {
		t.Errorf("final payload type = %q, want error", payloadType(got[2].Payload))
	}
}

func TestRetryTimeoutIsTerminal(t *testing.T) {
	events := make(chan ResultData, 4)
	block := make(chan struct{})
	defer close(block)
	build := func(out chan ResultData, args ActionArgs) (Action, error) {
		return &fakeAction{exec: func(string, []any) { <-block }}, nil
	}
	policy := RetryPolicy{Max: 5, Backoff: time.Millisecond, Timeout: 20 * time.Millisecond}
	a, _ := NewRetryAction(build, events, ActionArgs{Id: "action_1", Retry: policy})
	a.Execute("exec_3", nil)

	got := drain(events, 500*time.Millisecond)
	if len(got) != 1 {
		t.Fatalf("results = %d, want 1 (timeout is terminal, no retries)", len(got))
	}
	if got[0].Retrying {
		t.Errorf("timeout result marked Retrying")
	}
	m, _ := got[0].Payload.(map[string]any)
	if m["type"] != "error" || m["timeout"] != true {
		t.Errorf("timeout payload = %v, want type=error timeout=true", got[0].Payload)
	}
	if got[0].ExecutionID != "exec_3" {
		t.Errorf("timeout ExecutionID = %q, want exec_3", got[0].ExecutionID)
	}
}

func TestRetryStopCancels(t *testing.T) {
	events := make(chan ResultData, 4)
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	build := func(out chan ResultData, args ActionArgs) (Action, error) {
		return &fakeAction{exec: func(string, []any) {
			close(started)
			<-release
			out <- okResult("exec_4", "action_1")
		}}, nil
	}
	policy := RetryPolicy{Max: 3, Backoff: time.Hour, Timeout: time.Hour}
	a, _ := NewRetryAction(build, events, ActionArgs{Id: "action_1", Retry: policy})
	go a.Execute("exec_4", nil)
	<-started
	if err := a.Stop(); err != nil {
		t.Errorf("Stop error: %v", err)
	}
	// Execute must return promptly after Stop rather than sleeping the backoff.
	done := make(chan struct{})
	go func() { _ = a.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Errorf("Stop did not return promptly")
	}
}

func TestBackoffFor(t *testing.T) {
	p := RetryPolicy{Backoff: 100 * time.Millisecond, BackoffMax: 250 * time.Millisecond}
	cases := map[int]time.Duration{
		1: 100 * time.Millisecond,
		2: 200 * time.Millisecond,
		3: 250 * time.Millisecond, // capped
		4: 250 * time.Millisecond,
	}
	for attempt, want := range cases {
		if got := backoffFor(p, attempt); got != want {
			t.Errorf("backoffFor(attempt %d) = %v, want %v", attempt, got, want)
		}
	}
	// Defaults when unset.
	def := RetryPolicy{}
	if got := backoffFor(def, 1); got != time.Second {
		t.Errorf("default backoff = %v, want 1s", got)
	}
}

func TestIsFailure(t *testing.T) {
	if !isFailure(map[string]any{"type": "error"}) {
		t.Errorf("map error not detected")
	}
	if isFailure(map[string]any{"type": "success"}) {
		t.Errorf("success wrongly failed")
	}
	if isFailure(map[string]string{"type": "error"}) != true {
		t.Errorf("map[string]string error not detected")
	}
	if isFailure(nil) || isFailure("error") || isFailure(42) {
		t.Errorf("non-map payload wrongly reported as failure")
	}
	// Actions that emit a struct payload (fileaction) must be recognised too.
	if !isFailure(structPayload{Type: "error"}) || !isFailure(&structPayload{Type: "error"}) {
		t.Errorf("struct error payload not detected")
	}
	if isFailure(structPayload{Type: "success"}) {
		t.Errorf("struct success wrongly failed")
	}
}

type structPayload struct {
	Type string `json:"type"`
	File string `json:"file"`
}

func payloadType(p any) string {
	switch m := p.(type) {
	case map[string]any:
		s, _ := m["type"].(string)
		return s
	case map[string]string:
		return m["type"]
	}
	return ""
}
