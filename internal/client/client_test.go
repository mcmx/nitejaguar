package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mcmx/nitejaguar/common"
)

func TestAPIProtocol(t *testing.T) {
	var got common.ResultData
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/clients/register" {
			_ = json.NewEncoder(w).Encode(RegisterResponse{ClientID: "client_test"})
			return
		}
		if r.URL.Path == "/api/clients/client_test/assignments" {
			_ = json.NewEncoder(w).Encode(AssignmentsResponse{ClientID: "client_test", Workflows: []Workflow{}})
			return
		}
		if r.URL.Path == "/api/results" {
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
	assign, err := a.Assignments(context.Background(), reg.ClientID)
	if err != nil || assign.ClientID != reg.ClientID {
		t.Fatalf("assignments: %#v %v", assign, err)
	}
	nexts, err := a.PostResult(context.Background(), common.ResultData{WorkflowID: "workflow_1", ActionID: "trigger_1", ActionName: "filechangeTrigger", Payload: map[string]any{"file": "x.pdf"}})
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
