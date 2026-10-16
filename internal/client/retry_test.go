package client

import (
	"log/slog"
	"testing"
	"time"

	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/workflow"
)

// TestClientActionAppliesRetryPolicy proves remote clients honour a node's
// retry policy: the installed action is wrapped so a reliably failing node
// is retried 1+Max times, each intermediate attempt marked Retrying.
func TestClientActionAppliesRetryPolicy(t *testing.T) {
	r := newRunner(API{BaseURL: "http://localhost", HTTPClient: nil}, slog.Default())
	defer r.close()
	r.install(Workflow{ID: "workflow_client_retry", Name: "retry", Nodes: map[string]workflow.Node{
		"action_client_retry": {
			Id: "action_client_retry", Name: "retry", ActionType: "action", ActionName: "file",
			Arguments:       map[string]string{"action": "remove", "file": "/nonexistent/nitejaguar-client-retry"},
			RetryMax:        2,
			RetryBackoff:    "1ms",
			RetryBackoffMax: "5ms",
		},
	}})

	r.mu.Lock()
	a := r.nodeAction["action_client_retry"]
	r.mu.Unlock()
	if a == nil {
		t.Fatal("retry-policy action was not installed")
	}
	want := common.RetryPolicy{Max: 2, Backoff: time.Millisecond, BackoffMax: 5 * time.Millisecond}
	if got := a.GetArgs().Retry; got != want {
		t.Fatalf("installed policy = %+v, want %+v", got, want)
	}

	go a.Execute("exec_client_retry", nil)

	var got []common.ResultData
	deadline := time.After(2 * time.Second)
	for len(got) < 3 {
		select {
		case res := <-r.events:
			got = append(got, res)
		case <-deadline:
			t.Fatalf("got %d results, want 3 (1 + 2 retries)", len(got))
		}
	}
	for i, res := range got {
		if res.Attempt != i+1 {
			t.Errorf("result %d Attempt = %d, want %d", i, res.Attempt, i+1)
		}
		if wantRetrying := i < 2; res.Retrying != wantRetrying {
			t.Errorf("result %d Retrying = %v, want %v", i, res.Retrying, wantRetrying)
		}
	}
}
