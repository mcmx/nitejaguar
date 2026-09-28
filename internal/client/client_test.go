package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/workflow"
)

func TestClientWorkflowVersioningAndSync(t *testing.T) {
	r := newRunner(API{BaseURL: "http://localhost", HTTPClient: http.DefaultClient}, slog.Default())
	defer r.close()

	// 1. Install initial workflow revision_1
	w1 := Workflow{
		ID:       "workflow_test1",
		Name:     "Test Flow",
		Revision: "revision_1",
		Nodes: map[string]workflow.Node{
			"trigger_1": {
				Id:         "trigger_1",
				Name:       "Trigger",
				ActionType: "trigger",
				ActionName: "filechange",
				Arguments:  map[string]string{"path": "/tmp"},
			},
		},
	}
	r.syncAssignments([]Workflow{w1})

	r.mu.Lock()
	if r.activeWorkflows["workflow_test1"] == nil || r.activeWorkflows["workflow_test1"].revision != "revision_1" {
		r.mu.Unlock()
		t.Fatalf("expected workflow_test1 revision_1 active")
	}
	r.mu.Unlock()

	// 2. Update to revision_2
	w2 := Workflow{
		ID:       "workflow_test1",
		Name:     "Test Flow",
		Revision: "revision_2",
		Nodes: map[string]workflow.Node{
			"trigger_1": {
				Id:         "trigger_1",
				Name:       "Trigger Updated",
				ActionType: "trigger",
				ActionName: "filechange",
				Arguments:  map[string]string{"path": "/tmp"},
			},
		},
	}
	r.syncAssignments([]Workflow{w2})

	r.mu.Lock()
	if r.activeWorkflows["workflow_test1"].revision != "revision_2" {
		r.mu.Unlock()
		t.Fatalf("expected workflow_test1 revision_2 active")
	}
	if r.retired["workflow_test1"] == nil || r.retired["workflow_test1"]["revision_1"] == nil {
		r.mu.Unlock()
		t.Fatalf("expected revision_1 retired")
	}
	r.mu.Unlock()

	// 3. Disable / remove workflow (empty assignments)
	r.syncAssignments([]Workflow{})

	r.mu.Lock()
	if r.activeWorkflows["workflow_test1"] != nil {
		r.mu.Unlock()
		t.Fatalf("expected workflow_test1 removed from active workflows")
	}
	if r.retired["workflow_test1"] == nil || r.retired["workflow_test1"]["revision_2"] == nil {
		r.mu.Unlock()
		t.Fatalf("expected revision_2 retired")
	}
	r.mu.Unlock()
}

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
	nexts, err := a.PostResult(context.Background(), common.ResultData{WorkflowID: "workflow_1", ActionID: "trigger_1", ActionName: "filechange", ExecutorID: "client_test", Payload: map[string]any{"file": "x.pdf"}})
	if err != nil || len(nexts) != 1 || got.WorkflowID != "workflow_1" {
		t.Fatalf("result: %#v %#v %v", nexts, got, err)
	}
}

func TestAPIWorkflowUpsert(t *testing.T) {
	var gotPath string
	var gotBody workflow.Workflow
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer token_test" || r.Header.Get("X-Client-Token") != "token_test" {
			t.Errorf("missing client token headers")
		}
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Error(err)
		}
		_ = json.NewEncoder(w).Encode(UpsertWorkflowResponse{Ok: true, WorkflowID: "workflow_upserted"})
	}))
	defer srv.Close()
	a := API{BaseURL: srv.URL, Token: "token_test", HTTPClient: srv.Client()}
	wf := workflow.Workflow{
		Id:   "workflow_01testupsert00000001",
		Name: "Upsert test",
		Nodes: map[string]workflow.Node{
			"trigger_01testupsert00000001": {
				Id: "trigger_01testupsert00000001", Name: "Trigger",
				ActionType: "trigger", ActionName: "filechange",
				Arguments: map[string]string{"path": "/tmp"},
			},
		},
	}
	res, err := a.ImportWorkflow(context.Background(), wf)
	if err != nil || !res.Ok || res.WorkflowID != "workflow_upserted" {
		t.Fatalf("import: %#v %v", res, err)
	}
	if gotPath != "/api/workflows/import" || gotBody.Id != wf.Id {
		t.Fatalf("import request: path=%s body=%#v", gotPath, gotBody)
	}
	res, err = a.CloneWorkflow(context.Background(), wf)
	if err != nil || !res.Ok || res.WorkflowID != "workflow_upserted" {
		t.Fatalf("clone: %#v %v", res, err)
	}
	if gotPath != "/api/workflows/clone" || gotBody.Id != wf.Id {
		t.Fatalf("clone request: path=%s body=%#v", gotPath, gotBody)
	}
}

func TestConfigDefaults(t *testing.T) {
	c := (Config{Server: "http://x/"}).normalized()
	if c.Server != "http://x" || c.PollInterval != 2*time.Second || c.RetryInitial != 500*time.Millisecond {
		t.Fatalf("defaults: %#v", c)
	}
	if c.StateFile != defaultStateFileName {
		t.Fatalf("default state file: %#v", c)
	}
}

func TestClientStateRoundTrip(t *testing.T) {
	path := t.TempDir() + "/client_state.json"
	saveClientState(path, clientState{ClientID: "client_1", Token: "tok", Server: "http://s:8080", Name: "worker-1"})
	got := loadClientState(path)
	if got.ClientID != "client_1" || got.Token != "tok" || got.Server != "http://s:8080" || got.Name != "worker-1" {
		t.Fatalf("round trip: %#v", got)
	}
	if resolveStateFile("") != defaultStateFileName {
		t.Fatalf("empty state file should resolve to default")
	}
}

func TestRestoreIdentity(t *testing.T) {
	base := Config{Server: "http://a:8080", Name: "worker-1"}
	saved := clientState{ClientID: "client_1", Token: "tok", Server: "http://a:8080", Name: "worker-1"}

	// Same server+name: reuse, no warning.
	cfg, _, warn := restoreIdentity(base, saved)
	if warn != "" || cfg.ClientID != "client_1" || cfg.Token != "tok" {
		t.Fatalf("reuse: %#v %q", cfg, warn)
	}

	// Name drift: keep existing registration, warn.
	cfg, _, warn = restoreIdentity(Config{Server: "http://a:8080", Name: "worker-2"}, saved)
	if warn == "" || cfg.ClientID != "client_1" || cfg.Token != "tok" {
		t.Fatalf("name drift: %#v %q", cfg, warn)
	}

	// Server drift: discard identity, warn, re-register path.
	cfg, st, warn := restoreIdentity(Config{Server: "http://b:8080", Name: "worker-1"}, saved)
	if warn == "" || cfg.ClientID != "" || cfg.Token != "" || st.ClientID != "" {
		t.Fatalf("server drift: %#v %#v %q", cfg, st, warn)
	}
}

func TestApplyStateDefaultsAdoptsSavedRegistration(t *testing.T) {
	// Bare restart (no flag/env): saved server+name win silently.
	saved := clientState{ClientID: "client_1", Token: "tok", Server: "http://127.0.0.1:8083", Name: "ceres"}
	cfg := applyStateDefaults(Config{}, saved)
	if cfg.Server != "http://127.0.0.1:8083" || cfg.Name != "ceres" {
		t.Fatalf("adopt state: %#v", cfg)
	}
	cfg, _, warn := restoreIdentity(cfg, saved)
	if warn != "" || cfg.ClientID != "client_1" || cfg.Token != "tok" {
		t.Fatalf("reuse after adopt: %#v %q", cfg, warn)
	}

	// Explicit values are never overwritten by state.
	cfg = applyStateDefaults(Config{Server: "http://127.0.0.1:8080", Name: "other"}, saved)
	if cfg.Server != "http://127.0.0.1:8080" || cfg.Name != "other" {
		t.Fatalf("explicit kept: %#v", cfg)
	}

	// First run (no state): built-in defaults.
	cfg = applyStateDefaults(Config{}, clientState{})
	if cfg.Server != defaultServerURL || cfg.Name != defaultClientName {
		t.Fatalf("first-run defaults: %#v", cfg)
	}
}

func TestDropIdentityOnlyOnUnauthorized(t *testing.T) {
	// 401 from the server means the registration is dead.
	srv401 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"title":"Unauthorized"}`))
	}))
	defer srv401.Close()
	a401 := API{BaseURL: srv401.URL, Token: "stale", HTTPClient: srv401.Client()}
	if err := a401.Heartbeat(context.Background(), "client_dead"); !dropIdentity(err) {
		t.Fatalf("401 should drop identity: %v", err)
	}
	if !errors.Is(a401.Heartbeat(context.Background(), "client_dead"), ErrUnauthorized) {
		t.Fatal("401 should wrap ErrUnauthorized")
	}

	// 500 is transient: keep identity.
	srv500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv500.Close()
	a500 := API{BaseURL: srv500.URL, Token: "tok", HTTPClient: srv500.Client()}
	if err := a500.Heartbeat(context.Background(), "client_1"); dropIdentity(err) {
		t.Fatalf("500 must keep identity: %v", err)
	}

	// Connection refused (server down) is transient: keep identity.
	closed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := closed.URL
	closed.Close()
	adead := API{BaseURL: deadURL, Token: "tok", HTTPClient: http.DefaultClient}
	if err := adead.Heartbeat(context.Background(), "client_1"); dropIdentity(err) {
		t.Fatalf("connection refused must keep identity: %v", err)
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
			Id: "action_merge", Name: "Action", ActionType: "action", ActionName: "file",
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

func TestRunnerDrainsPendingOnce(t *testing.T) {
	r := newRunner(API{BaseURL: "http://localhost", HTTPClient: http.DefaultClient}, slog.Default())
	defer r.close()
	r.install(Workflow{ID: "workflow_pending", Name: "pending", Nodes: map[string]workflow.Node{
		"action_pending": {
			Id: "action_pending", Name: "Action", ActionType: "action", ActionName: "file",
			Arguments: map[string]string{"action": "create", "file": "/tmp/out.txt"},
		},
	}})
	r.mu.Lock()
	if r.nodeAction["action_pending"] == nil {
		r.mu.Unlock()
		t.Fatal("expected pending action installed")
	}
	r.mu.Unlock()

	pending := []PendingAssignment{{
		ID: "assign_test", WorkflowID: "workflow_pending", ExecutionID: "exec_pending",
		NodeID: "action_pending", ParentActionID: "trigger_pending",
		Payload: map[string]any{"file": "/tmp/x"},
	}}
	r.drainPending(context.Background(), pending)
	r.drainPending(context.Background(), pending) // second poll must not re-execute
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.pendingSeen["assign_test"] {
		t.Fatal("expected pending marked seen")
	}
	if got := r.history["exec_pending"]["trigger_pending"]; got == nil {
		t.Fatalf("expected parent payload seeded for merge_input, got %v", r.history["exec_pending"])
	}
}

func TestNewLoggerHonorsLevel(t *testing.T) {
	ctx := context.Background()
	debug, err := NewLogger("debug")
	if err != nil {
		t.Fatalf("NewLogger(debug): %v", err)
	}
	if !debug.Enabled(ctx, slog.LevelDebug) {
		t.Fatal("debug level should enable debug records")
	}
	info, err := NewLogger("info")
	if err != nil {
		t.Fatalf("NewLogger(info): %v", err)
	}
	if info.Enabled(ctx, slog.LevelDebug) {
		t.Fatal("info level must drop the debug-only execution trace")
	}
	if !info.Enabled(ctx, slog.LevelInfo) {
		t.Fatal("info level should still emit info records")
	}
	if _, err := NewLogger(""); err != nil {
		t.Fatalf("empty level should default to info, got %v", err)
	}
	if _, err := NewLogger("chatty"); err == nil {
		t.Fatal("expected an error for an unknown level")
	}
}

// The per-node execution trace is debug-only: it must be emitted when the
// level allows it and dropped otherwise, so a normal run stays quiet.
func TestExecuteNodeTraceIsDebugOnly(t *testing.T) {
	capture := func(level slog.Level) string {
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: level}))
		r := newRunner(API{BaseURL: "http://localhost", HTTPClient: http.DefaultClient}, logger)
		defer r.close()
		r.install(Workflow{ID: "workflow_trace", Name: "trace", Nodes: map[string]workflow.Node{
			"action_trace": {
				Id: "action_trace", Name: "Action", ActionType: "action", ActionName: "datetime",
				Arguments: map[string]string{"operation": "getCurrentDate", "format": "20060102"},
			},
		}})
		r.executeNode("action_trace", "exec_trace", nil)
		// The action runs in a goroutine; the trace is emitted before it.
		deadline := time.Now().Add(2 * time.Second)
		for !strings.Contains(buf.String(), "executing node") && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		return buf.String()
	}

	debugOut := capture(slog.LevelDebug)
	if !strings.Contains(debugOut, "executing node") {
		t.Fatalf("debug run missing execution trace: %q", debugOut)
	}
	if !strings.Contains(debugOut, "action_trace") || !strings.Contains(debugOut, "datetime") {
		t.Fatalf("trace should name the node and its action: %q", debugOut)
	}
	if infoOut := capture(slog.LevelInfo); strings.Contains(infoOut, "executing node") {
		t.Fatalf("info run must not emit the debug trace: %q", infoOut)
	}
}

// The client builds triggers by action_name, so a remote runner can host the
// same trigger set as the server instead of forcing everything through
// filechange.
func TestNewClientTriggerDispatch(t *testing.T) {
	events := make(chan common.ResultData, 1)

	cronTrigger, err := newClientTrigger(events, common.ActionArgs{
		ActionType: "trigger", ActionName: "cron",
		Args: map[string]string{"interval": "1h"},
	})
	if err != nil {
		t.Fatalf("newClientTrigger(cron) error: %v", err)
	}
	defer func() {
		if err := cronTrigger.Stop(); err != nil {
			t.Errorf("cron trigger Stop error: %v", err)
		}
	}()
	if cronTrigger.GetArgs().ActionName != "cron" || cronTrigger.GetArgs().ActionType != "trigger" {
		t.Errorf("cron trigger args = %+v", cronTrigger.GetArgs())
	}

	filechangeTrigger, err := newClientTrigger(events, common.ActionArgs{
		ActionType: "trigger", ActionName: "filechange",
		Args: map[string]string{"path": "/tmp"},
	})
	if err != nil {
		t.Fatalf("newClientTrigger(filechange) error: %v", err)
	}
	defer func() {
		if err := filechangeTrigger.Stop(); err != nil {
			t.Errorf("filechange trigger Stop error: %v", err)
		}
	}()

	if _, err := newClientTrigger(events, common.ActionArgs{ActionType: "trigger", ActionName: "nope"}); err == nil {
		t.Errorf("newClientTrigger(nope) succeeded, want unknown trigger error")
	}
}
