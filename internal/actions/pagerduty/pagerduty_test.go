package pagerduty

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mcmx/nitejaguar/common"
)

func testArgs(args map[string]any) common.ActionArgs {
	return common.ActionArgs{Id: "action_test", Name: "pagerduty", ActionType: "action", ActionName: "pagerduty", Args: args}
}

func runExecute(args map[string]any, inputs []any) map[string]any {
	events := make(chan common.ResultData, 1)
	a, err := New(events, testArgs(args))
	if err != nil {
		panic(err)
	}
	a.Execute("exec_test", inputs)
	select {
	case r := <-events:
		m, _ := r.Payload.(map[string]any)
		return m
	case <-time.After(5 * time.Second):
		panic("no result")
	}
}

func TestNewRejectsBadEventAction(t *testing.T) {
	args := map[string]any{"routing_key": "key", "event_action": "explode", "summary": "x"}
	if _, err := New(make(chan common.ResultData, 1), testArgs(args)); err == nil {
		t.Error("New with bad event_action succeeded, want error")
	}
}

func TestNewRejectsBadSeverity(t *testing.T) {
	args := map[string]any{"routing_key": "key", "severity": "emergency", "summary": "x"}
	if _, err := New(make(chan common.ResultData, 1), testArgs(args)); err == nil {
		t.Error("New with bad severity succeeded, want error")
	}
}

func TestNewAllowsTemplatedValues(t *testing.T) {
	args := map[string]any{"routing_key": "$input.key", "severity": "$input.sev", "summary": "x"}
	if _, err := New(make(chan common.ResultData, 1), testArgs(args)); err != nil {
		t.Errorf("New with templated values error: %v", err)
	}
}

func TestExecuteRequiresRoutingKey(t *testing.T) {
	payload := runExecute(map[string]any{"summary": "x"}, nil)
	if payload["type"] != "error" {
		t.Errorf("expected error for missing routing_key without credential, got %v", payload)
	}
}

func TestExecuteTriggerRequiresSummary(t *testing.T) {
	payload := runExecute(map[string]any{"routing_key": "key"}, nil)
	if payload["type"] != "error" {
		t.Errorf("expected error for trigger without summary, got %v", payload)
	}
}

func TestExecuteAckRequiresDedupKey(t *testing.T) {
	payload := runExecute(map[string]any{
		"routing_key": "key", "event_action": "acknowledge",
		"api_url": "https://example.com/x",
	}, nil)
	if payload["type"] != "error" {
		t.Errorf("expected error for acknowledge without dedup_key, got %v", payload)
	}
}

func TestExecuteRejectsResultRef(t *testing.T) {
	payload := runExecute(map[string]any{"routing_key": "key", "summary": "$result.x"}, nil)
	if payload["type"] != "error" {
		t.Errorf("expected error for $result ref, got %v", payload)
	}
}

func TestExecuteTriggerPostsEvent(t *testing.T) {
	var gotEvent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type = %q", r.Header.Get("Content-Type"))
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotEvent)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"success","message":"Event processed","dedup_key":"srv-dedup-1"}`))
	}))
	defer srv.Close()

	payload := runExecute(map[string]any{
		"routing_key": "key123", "summary": "archive failed",
		"severity": "critical", "source": "edge-1",
		"dedup_key": "nj-archive", "api_url": srv.URL,
	}, nil)
	if payload["type"] != "success" {
		t.Fatalf("expected success, got %v", payload)
	}
	if payload["event_action"] != "trigger" {
		t.Errorf("event_action = %v", payload["event_action"])
	}
	if payload["dedup_key"] != "srv-dedup-1" {
		t.Errorf("dedup_key should prefer the server response, got %v", payload["dedup_key"])
	}
	if gotEvent["routing_key"] != "key123" || gotEvent["event_action"] != "trigger" || gotEvent["dedup_key"] != "nj-archive" {
		t.Errorf("event envelope = %v", gotEvent)
	}
	pl, _ := gotEvent["payload"].(map[string]any)
	if pl["summary"] != "archive failed" || pl["severity"] != "critical" || pl["source"] != "edge-1" {
		t.Errorf("event payload = %v", pl)
	}
}

func TestExecuteResolveKeepsLocalDedupKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer srv.Close()

	payload := runExecute(map[string]any{
		"routing_key": "key", "event_action": "resolve",
		"dedup_key": "nj-archive", "api_url": srv.URL,
	}, nil)
	if payload["type"] != "success" {
		t.Fatalf("expected success, got %v", payload)
	}
	if payload["dedup_key"] != "nj-archive" {
		t.Errorf("dedup_key = %v, want local key when server omits it", payload["dedup_key"])
	}
}

func TestExecuteRejectionIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":"invalid event","message":"routing key missing"}`))
	}))
	defer srv.Close()

	payload := runExecute(map[string]any{
		"routing_key": "bad", "summary": "x", "api_url": srv.URL,
	}, nil)
	if payload["type"] != "error" {
		t.Fatalf("expected error for 400, got %v", payload)
	}
	if payload["status"] != http.StatusBadRequest {
		t.Errorf("status = %v, want 400", payload["status"])
	}
	if !strings.Contains(payload["result"].(string), "routing key missing") {
		t.Errorf("result should carry upstream message, got %v", payload["result"])
	}
}

func TestExecuteTemplating(t *testing.T) {
	var gotEvent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotEvent)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	inputs := []any{common.ResultData{Payload: map[string]any{"workflow": "deploy", "result": "boom"}}}
	payload := runExecute(map[string]any{
		"routing_key": "key", "api_url": srv.URL,
		"summary":   "Workflow {{ $input.workflow | upper }} failed: $input.result",
		"dedup_key": "nj-{{ $input.workflow }}-archive",
	}, inputs)
	if payload["type"] != "success" {
		t.Fatalf("expected success, got %v", payload)
	}
	pl, _ := gotEvent["payload"].(map[string]any)
	if pl["summary"] != "Workflow DEPLOY failed: boom" {
		t.Errorf("summary templating failed: %v", pl)
	}
	if gotEvent["dedup_key"] != "nj-deploy-archive" {
		t.Errorf("dedup_key templating failed: %v", gotEvent)
	}
}

func TestExecuteCredentialRoutingKey(t *testing.T) {
	var count int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	inputs := common.WithCredential(nil, common.CredentialBinding{Ref: "pd-key", Type: "generic", Secret: "int-key"})
	payload := runExecute(map[string]any{"summary": "x", "api_url": srv.URL}, inputs)
	if payload["type"] != "success" {
		t.Fatalf("expected success via credential routing key, got %v", payload)
	}
	if count != 1 {
		t.Errorf("post count = %d, want 1", count)
	}
}

func TestExecuteCredentialTypeMismatch(t *testing.T) {
	secret, _ := json.Marshal(map[string]string{"username": "u", "password": "p"})
	inputs := common.WithCredential(nil, common.CredentialBinding{Ref: "up", Type: "username_password", Secret: string(secret)})
	payload := runExecute(map[string]any{"summary": "x"}, inputs)
	if payload["type"] != "error" {
		t.Errorf("expected error for username_password credential, got %v", payload)
	}
}

func TestExecuteCredentialResolveError(t *testing.T) {
	inputs := common.WithCredential(nil, common.CredentialBinding{Ref: "missing", Err: "credential not found"})
	payload := runExecute(map[string]any{"summary": "x"}, inputs)
	if payload["type"] != "error" {
		t.Errorf("expected error for unresolvable credential, got %v", payload)
	}
}

func TestAddActionDispatches(t *testing.T) {
	events := make(chan common.ResultData, 1)
	a, err := New(events, testArgs(CatalogEntry().Args))
	if err != nil {
		t.Fatalf("New with catalog defaults error: %v", err)
	}
	if a == nil {
		t.Fatal("expected action")
	}
}

func TestExamplePagerDutyWorkflowLoads(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/workflow-pagerduty.json")
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	var wf struct {
		Nodes map[string]struct {
			ID            string            `json:"id"`
			ActionType    string            `json:"action_type"`
			ActionName    string            `json:"action_name"`
			Arguments     map[string]string `json:"arguments"`
			CredentialRef string            `json:"credential_ref"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("parse example: %v", err)
	}
	found := false
	for _, n := range wf.Nodes {
		if n.ActionType != "action" || n.ActionName != "pagerduty" {
			continue
		}
		found = true
		a, err := New(make(chan common.ResultData, 1), common.ActionArgs{
			Id: n.ID, Name: n.ID, ActionType: n.ActionType,
			ActionName: n.ActionName, Args: n.Arguments,
		})
		if err != nil {
			t.Fatalf("New(pagerduty example) error: %v", err)
		}
		if a == nil {
			t.Fatal("expected action")
		}
		if n.CredentialRef == "" {
			t.Error("example pagerduty node should carry credential_ref (routing key via store, not inline)")
		}
	}
	if !found {
		t.Fatal("example workflow has no pagerduty action node")
	}
}
