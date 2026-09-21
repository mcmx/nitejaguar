package client

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/workflow"
)

func TestAPIProtocol(t *testing.T) {
	var got common.ResultData
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/clients/register" {
			var request RegisterRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.Name != "test" || request.Tags == nil {
				t.Fatalf("registration request: %#v", request)
			}
			_ = json.NewEncoder(w).Encode(RegisterResponse{ClientID: "client_test", Token: "token_test"})
			return
		}
		if r.URL.Path == "/api/clients/client_test/assignments" {
			_ = json.NewEncoder(w).Encode(AssignmentsResponse{ClientID: "client_test", Workflows: []Workflow{}})
			return
		}
		if r.URL.Path == "/api/results" {
			if r.Header.Get("Authorization") != "Bearer token_test" || r.Header.Get("X-Client-Token") != "token_test" {
				t.Errorf("missing client token headers")
			}
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Error(err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"nexts": []string{"action_1"}})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	a := API{BaseURL: srv.URL, HTTPClient: srv.Client()}
	reg, err := a.Register(context.Background(), "test")
	if err != nil || reg.ClientID != "client_test" {
		t.Fatalf("register: %#v %v", reg, err)
	}
	a.Token = reg.Token
	assign, err := a.Assignments(context.Background(), reg.ClientID)
	if err != nil || assign.ClientID != reg.ClientID {
		t.Fatalf("assignments: %#v %v", assign, err)
	}
	nexts, err := a.PostResult(context.Background(), common.ResultData{WorkflowID: "workflow_1", ActionID: "trigger_1", ActionName: "filechangeTrigger", ExecutorID: "client_test", Payload: map[string]any{"file": "x.pdf"}})
	if err != nil || len(nexts) != 1 || got.WorkflowID != "workflow_1" {
		t.Fatalf("result: %#v %#v %v", nexts, got, err)
	}
}

func TestConfigDefaults(t *testing.T) {
	c := (Config{Server: "http://x/"}).normalized()
	if c.Server != "http://x" || c.PollInterval != 2*time.Second || c.RetryInitial != 500*time.Millisecond {
		t.Fatalf("defaults: %#v", c)
	}
}

// The runner (not the action) applies merge_input before reporting:
// upstream payload keys flow into the result, the result wins.
func TestRunnerAppliesMergeInput(t *testing.T) {
	var posted []common.ResultData
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got common.ResultData
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		posted = append(posted, got)
		_ = json.NewEncoder(w).Encode(map[string]any{"nexts": []string{}})
	}))
	defer srv.Close()

	r := newRunner(API{BaseURL: srv.URL, HTTPClient: srv.Client()}, slog.Default())
	r.install(Workflow{ID: "workflow_merge", Name: "merge", Nodes: map[string]workflow.Node{
		"action_merge": {
			Id: "action_merge", Name: "Action", ActionType: "action", ActionName: "fileAction",
			Arguments:    map[string]string{"action": "create", "file": "/tmp/out.txt"},
			Dependencies: []string{"trigger_merge"},
			MergeInput:   true,
		},
	}})

	ctx := context.Background()
	r.runResult(ctx, common.ResultData{
		WorkflowID: "workflow_merge", ExecutionID: "exec_merge", ActionID: "trigger_merge",
		Payload: map[string]any{"Name": "Sergio", "LastName": "Who"},
	})
	// The action reports without merging; the runner merges on its behalf.
	r.runResult(ctx, common.ResultData{
		WorkflowID: "workflow_merge", ExecutionID: "exec_merge", ActionID: "action_merge",
		Payload: map[string]any{"Name": "Dr", "Phone": "555-768790"},
	})

	if len(posted) != 2 {
		t.Fatalf("expected 2 posted results, got %d", len(posted))
	}
	// Trigger passes through untouched.
	if m, ok := posted[0].Payload.(map[string]any); !ok || m["Name"] != "Sergio" {
		t.Fatalf("trigger payload altered: %v", posted[0].Payload)
	}
	m, ok := posted[1].Payload.(map[string]any)
	if !ok {
		t.Fatalf("expected merged map payload, got %T", posted[1].Payload)
	}
	if m["Name"] != "Dr" || m["LastName"] != "Who" || m["Phone"] != "555-768790" {
		t.Fatalf("unexpected merged payload: %v", m)
	}
}
