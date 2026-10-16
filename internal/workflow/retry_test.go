package workflow

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/mcmx/nitejaguar/common"
)

const retryNodeWorkflowJSON = `{
  "id": "workflow_01retrynodetest000001",
  "name": "Retry node workflow",
  "nodes": {
    "trigger_01retrynodetest0001": {
      "id": "trigger_01retrynodetest0001",
      "name": "Trigger",
      "action_type": "trigger",
      "action_name": "filechange",
      "arguments": {"path": "/tmp"},
      "conditions": {"entries": {}},
      "dependencies": null
    },
    "action_01retrynodetest00001": {
      "id": "action_01retrynodetest00001",
      "name": "Retrying action",
      "action_type": "action",
      "action_name": "file",
      "arguments": {"action": "create", "file": "/tmp/out.txt"},
      "conditions": {"entries": {
        "entry_ok": {
          "condition": {"leftOperand": true, "operator": "", "rightOperand": null},
          "nexts": ["action_01retrynodetest00002"]
        }
      }},
      "dependencies": ["trigger_01retrynodetest0001"]
    },
    "action_01retrynodetest00002": {
      "id": "action_01retrynodetest00002",
      "name": "Downstream action",
      "action_type": "action",
      "action_name": "file",
      "arguments": {"action": "create", "file": "/tmp/down.txt"},
      "conditions": {"entries": {}},
      "dependencies": ["action_01retrynodetest00001"]
    }
  }
}`

func TestPolicyFromNode(t *testing.T) {
	t.Run("defaults are zero", func(t *testing.T) {
		p, err := PolicyFromNode(Node{Id: "n1"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Enabled() {
			t.Errorf("empty node should yield a disabled policy, got %+v", p)
		}
	})

	t.Run("parses durations", func(t *testing.T) {
		p, err := PolicyFromNode(Node{
			Id:              "n1",
			RetryMax:        3,
			RetryBackoff:    "250ms",
			RetryBackoffMax: "2s",
			Timeout:         "5s",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Max != 3 || p.Backoff != 250*time.Millisecond || p.BackoffMax != 2*time.Second || p.Timeout != 5*time.Second {
			t.Errorf("unexpected policy: %+v", p)
		}
	})

	t.Run("rejects invalid values", func(t *testing.T) {
		for name, n := range map[string]Node{
			"negative retry_max": {Id: "n1", RetryMax: -1},
			"bad retry_backoff":  {Id: "n1", RetryBackoff: "nope"},
			"zero retry_backoff": {Id: "n1", RetryBackoff: "0s"},
			"bad backoff_max":    {Id: "n1", RetryBackoffMax: "x"},
			"bad timeout":        {Id: "n1", Timeout: "5"},
			"negative timeout":   {Id: "n1", Timeout: "-1s"},
		} {
			if _, err := PolicyFromNode(n); err == nil {
				t.Errorf("%s: expected error, got nil", name)
			}
		}
	})
}

func TestIngestResultSkipsRoutingWhileRetrying(t *testing.T) {
	var def Workflow
	if err := json.Unmarshal([]byte(retryNodeWorkflowJSON), &def); err != nil {
		t.Fatalf("unmarshal workflow: %v", err)
	}
	store := &retryRecordingStore{}
	wm := &workflowManager{
		Workflows: map[string]WorkflowInt{
			def.Id: {Id: def.Id, Name: def.Name, Definition: def},
		},
		Actions2Workflow: map[string]string{},
		db:               store,
		executorID:       "server",
		execHistory:      map[string]map[string]any{},
	}
	actionID := "action_01retrynodetest00001"
	downstreamID := "action_01retrynodetest00002"

	// Intermediate failure: persisted, but no routing and no enqueue.
	stored, nexts, err := wm.IngestResult(common.ResultData{
		ExecutionID: "exec_retry_test",
		ActionID:    actionID,
		ActionType:  "action",
		ActionName:  "file",
		ExecutorID:  "client_retry",
		Attempt:     1,
		Retrying:    true,
		Payload:     map[string]any{"type": "error", "result": "boom"},
	})
	if err != nil {
		t.Fatalf("ingest retrying result: %v", err)
	}
	if len(nexts) != 0 {
		t.Fatalf("retrying result routed to %v, want none", nexts)
	}
	if len(stored.Nexts) != 0 || len(stored.ConditionResults) != 0 {
		t.Errorf("retrying result evaluated routing: nexts=%v conditions=%v", stored.Nexts, stored.ConditionResults)
	}
	if store.enqueued != 0 {
		t.Errorf("retrying result enqueued %d assignments, want 0", store.enqueued)
	}
	if len(store.saved) != 1 || !store.saved[0].Retrying {
		t.Fatalf("retrying result not persisted with Retrying=true: %+v", store.saved)
	}

	// Final attempt: routing runs normally and the next node is dispatched.
	stored, nexts, err = wm.IngestResult(common.ResultData{
		ExecutionID: "exec_retry_test",
		ActionID:    actionID,
		ActionType:  "action",
		ActionName:  "file",
		ExecutorID:  "client_retry",
		Attempt:     2,
		Retrying:    false,
		Payload:     map[string]any{"type": "success"},
	})
	if err != nil {
		t.Fatalf("ingest final result: %v", err)
	}
	if len(nexts) != 1 || nexts[0] != downstreamID {
		t.Fatalf("final result nexts = %v, want [%s]", nexts, downstreamID)
	}
	if !stored.ConditionResults["entry_ok"] {
		t.Errorf("final result did not evaluate routing: %v", stored.ConditionResults)
	}
}

// TestExampleRetryWorkflowLoads guards examples/workflow-retry.json: the
// retry/deadline fields must survive the JSON round-trip and validate.
func TestExampleRetryWorkflowLoads(t *testing.T) {
	raw, err := os.ReadFile("../../examples/workflow-retry.json")
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	var wf Workflow
	if err := json.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("unmarshal example: %v", err)
	}
	n, ok := wf.Nodes["action_01retryexample0000001"]
	if !ok {
		t.Fatal("retry node missing from example")
	}
	if n.RetryMax != 3 || n.RetryBackoff != "1s" || n.RetryBackoffMax != "30s" || n.Timeout != "10s" {
		t.Fatalf("unexpected retry fields: %+v", n)
	}
	p, err := PolicyFromNode(n)
	if err != nil {
		t.Fatalf("PolicyFromNode: %v", err)
	}
	if p.Max != 3 || p.Backoff != time.Second || p.BackoffMax != 30*time.Second || p.Timeout != 10*time.Second {
		t.Fatalf("unexpected policy: %+v", p)
	}
	// Trigger nodes carry no policy in the example.
	if trig := wf.Nodes["trigger_01retryexample0000001"]; trig.RetryMax != 0 || trig.Timeout != "" {
		t.Fatalf("trigger node should have no retry policy: %+v", trig)
	}
}

// retryRecordingStore records persistence calls and counts enqueues.
type retryRecordingStore struct {
	baseStore
	saved    []common.ResultData
	enqueued int
}

func (s *retryRecordingStore) SaveResultRecord(r common.ResultData) error {
	s.saved = append(s.saved, r)
	return nil
}

func (s *retryRecordingStore) EnqueueNodeAssignmentRecord(string, string, string, string, string, any) error {
	s.enqueued++
	return nil
}
