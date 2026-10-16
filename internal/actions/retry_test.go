package actions

import (
	"testing"
	"time"

	"github.com/mcmx/nitejaguar/common"
)

// TestAddActionAppliesRetryPolicy proves the ActionManager wraps a node in
// the retry decorator when its args carry a policy: a reliably failing
// action is retried 1+Max times, each intermediate attempt marked Retrying,
// and the final attempt left unmarked so the engine routes on it.
func TestAddActionAppliesRetryPolicy(t *testing.T) {
	am := NewActionManager(true)
	policy := common.RetryPolicy{Max: 2, Backoff: time.Millisecond, BackoffMax: 5 * time.Millisecond}
	action, id, err := am.AddAction(common.ActionArgs{
		Id:         "action_01retrytest00000000001",
		Name:       "always fails",
		ActionName: "file",
		Args:       map[string]string{"action": "remove", "file": "/nonexistent/nitejaguar-retry-test"},
		Retry:      policy,
	})
	if err != nil {
		t.Fatalf("AddAction: %v", err)
	}
	if got := action.GetArgs().Retry; got != policy {
		t.Fatalf("wrapped action lost policy: got %+v, want %+v", got, policy)
	}

	go action.Execute("exec_retry_test", nil)

	var got []common.ResultData
	deadline := time.After(2 * time.Second)
	for len(got) < 3 {
		select {
		case r := <-am.events:
			got = append(got, r)
		case <-deadline:
			t.Fatalf("got %d results, want 3 (1 + 2 retries): %+v", len(got), got)
		}
	}
	for i, r := range got {
		if r.Attempt != i+1 {
			t.Errorf("result %d Attempt = %d, want %d", i, r.Attempt, i+1)
		}
		wantRetrying := i < 2
		if r.Retrying != wantRetrying {
			t.Errorf("result %d Retrying = %v, want %v", i, r.Retrying, wantRetrying)
		}
	}

	am.RemoveAction(id)
}

// Without a policy the action is wrapped directly: no Attempt/Retrying
// metadata, exactly one result — the pre-retry behavior is unchanged.
func TestAddActionWithoutPolicyIsUnwrapped(t *testing.T) {
	am := NewActionManager(true)
	action, id, err := am.AddAction(common.ActionArgs{
		Id:         "action_01noretrytest0000000001",
		Name:       "fails once",
		ActionName: "file",
		Args:       map[string]string{"action": "remove", "file": "/nonexistent/nitejaguar-noretry-test"},
	})
	if err != nil {
		t.Fatalf("AddAction: %v", err)
	}
	go action.Execute("exec_noretry_test", nil)

	select {
	case r := <-am.events:
		if r.Attempt != 0 || r.Retrying {
			t.Errorf("unwrapped action produced retry metadata: %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no result from unwrapped action")
	}

	select {
	case r := <-am.events:
		t.Errorf("unwrapped action emitted a second result: %+v", r)
	case <-time.After(100 * time.Millisecond):
	}

	am.RemoveAction(id)
}
