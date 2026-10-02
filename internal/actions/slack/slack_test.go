package slack

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
	return common.ActionArgs{Id: "action_test", Name: "slack", ActionType: "action", ActionName: "slack", Args: args}
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

func TestNewRejectsBadURL(t *testing.T) {
	args := map[string]any{"webhook_url": "not a url with spaces", "text": "hi"}
	if _, err := New(make(chan common.ResultData, 1), testArgs(args)); err == nil {
		t.Error("New with bad webhook_url succeeded, want error")
	}
}

func TestNewRejectsNonHTTPScheme(t *testing.T) {
	args := map[string]any{"webhook_url": "ftp://example.com/hook", "text": "hi"}
	if _, err := New(make(chan common.ResultData, 1), testArgs(args)); err == nil {
		t.Error("New with ftp webhook_url succeeded, want error")
	}
}

func TestNewAllowsTemplatedURL(t *testing.T) {
	args := map[string]any{"webhook_url": "$input.hook", "text": "hi"}
	if _, err := New(make(chan common.ResultData, 1), testArgs(args)); err != nil {
		t.Errorf("New with templated webhook_url error: %v", err)
	}
}

func TestNewAllowsMissingURLForCredential(t *testing.T) {
	args := map[string]any{"text": "hi"}
	if _, err := New(make(chan common.ResultData, 1), testArgs(args)); err != nil {
		t.Errorf("New without webhook_url error: %v (URL may come from credential_ref)", err)
	}
}

func TestExecuteRequiresText(t *testing.T) {
	payload := runExecute(map[string]any{"webhook_url": "https://hooks.slack.com/x"}, nil)
	if payload["type"] != "error" {
		t.Errorf("expected error for missing text, got %v", payload)
	}
}

func TestExecuteRequiresURL(t *testing.T) {
	payload := runExecute(map[string]any{"text": "hi"}, nil)
	if payload["type"] != "error" {
		t.Errorf("expected error for missing webhook_url without credential, got %v", payload)
	}
}

func TestExecuteRejectsResultRef(t *testing.T) {
	payload := runExecute(map[string]any{"webhook_url": "https://hooks.slack.com/x", "text": "$result.foo"}, nil)
	if payload["type"] != "error" {
		t.Errorf("expected error for $result ref, got %v", payload)
	}
}

func TestExecutePostsJSON(t *testing.T) {
	var gotBody map[string]any
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	payload := runExecute(map[string]any{
		"webhook_url": srv.URL, "text": "deploy failed",
		"channel": "#alerts", "username": "nitejaguar",
	}, nil)
	if payload["type"] != "success" {
		t.Fatalf("expected success, got %v", payload)
	}
	if payload["status"] != http.StatusOK {
		t.Errorf("status = %v, want 200", payload["status"])
	}
	if gotCT != "application/json" {
		t.Errorf("content-type = %q, want application/json", gotCT)
	}
	if gotBody["text"] != "deploy failed" || gotBody["channel"] != "#alerts" || gotBody["username"] != "nitejaguar" {
		t.Errorf("posted body = %v", gotBody)
	}
}

func TestExecuteNon2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("channel_not_found"))
	}))
	defer srv.Close()

	payload := runExecute(map[string]any{"webhook_url": srv.URL, "text": "hi"}, nil)
	if payload["type"] != "error" {
		t.Fatalf("expected error for 404, got %v", payload)
	}
	if payload["status"] != http.StatusNotFound {
		t.Errorf("status = %v, want 404", payload["status"])
	}
	if !strings.Contains(payload["result"].(string), "channel_not_found") {
		t.Errorf("result should carry upstream body, got %v", payload["result"])
	}
}

func TestExecuteTemplating(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	inputs := []any{common.ResultData{Payload: map[string]any{"workflow": "deploy", "result": "boom"}}}
	payload := runExecute(map[string]any{
		"webhook_url": srv.URL,
		"text":        "Workflow {{ $input.workflow | upper }} failed: $input.result",
	}, inputs)
	if payload["type"] != "success" {
		t.Fatalf("expected success, got %v", payload)
	}
	if gotBody["text"] != "Workflow DEPLOY failed: boom" {
		t.Errorf("text = %v", gotBody["text"])
	}
}

func TestExecuteCredentialURL(t *testing.T) {
	var count int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	inputs := common.WithCredential(nil, common.CredentialBinding{Ref: "slack-hook", Type: "token", Secret: srv.URL})
	payload := runExecute(map[string]any{"text": "hi"}, inputs)
	if payload["type"] != "success" {
		t.Fatalf("expected success via credential URL, got %v", payload)
	}
	if count != 1 {
		t.Errorf("post count = %d, want 1", count)
	}
}

func TestExecuteCredentialTypeMismatch(t *testing.T) {
	secret, _ := json.Marshal(map[string]string{"username": "svc", "password": "pw"})
	inputs := common.WithCredential(nil, common.CredentialBinding{Ref: "smtp", Type: "username_password", Secret: string(secret)})
	payload := runExecute(map[string]any{"text": "hi"}, inputs)
	if payload["type"] != "error" {
		t.Errorf("expected error for username_password credential, got %v", payload)
	}
}

func TestExecuteCredentialResolveError(t *testing.T) {
	inputs := common.WithCredential(nil, common.CredentialBinding{Ref: "missing", Err: "credential not found"})
	payload := runExecute(map[string]any{"text": "hi"}, inputs)
	if payload["type"] != "error" {
		t.Errorf("expected error for unresolvable credential, got %v", payload)
	}
}

func TestExecuteBadTimeout(t *testing.T) {
	payload := runExecute(map[string]any{"webhook_url": "https://hooks.slack.com/x", "text": "hi", "timeout": "99"}, nil)
	if payload["type"] != "error" {
		t.Errorf("expected error for out-of-range timeout, got %v", payload)
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

func TestExampleSlackWorkflowLoads(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/workflow-slack.json")
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
		if n.ActionType != "action" || n.ActionName != "slack" {
			continue
		}
		found = true
		a, err := New(make(chan common.ResultData, 1), common.ActionArgs{
			Id: n.ID, Name: n.ID, ActionType: n.ActionType,
			ActionName: n.ActionName, Args: n.Arguments,
		})
		if err != nil {
			t.Fatalf("New(slack example) error: %v", err)
		}
		if a == nil {
			t.Fatal("expected action")
		}
		if n.CredentialRef == "" {
			t.Error("example slack node should carry credential_ref (webhook URL via store, not inline)")
		}
	}
	if !found {
		t.Fatal("example workflow has no slack action node")
	}
}
