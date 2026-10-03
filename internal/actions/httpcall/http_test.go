package httpcall

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
	return common.ActionArgs{Id: "action_test", Name: "http", ActionType: "action", ActionName: "http", Args: args}
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

func TestNewRejectsBadMethod(t *testing.T) {
	args := map[string]any{"url": "https://example.com/hook", "method": "SMOKE"}
	if _, err := New(make(chan common.ResultData, 1), testArgs(args)); err == nil {
		t.Error("New with bad method succeeded, want error")
	}
}

func TestNewRejectsBadURL(t *testing.T) {
	args := map[string]any{"url": "ftp://example.com/hook"}
	if _, err := New(make(chan common.ResultData, 1), testArgs(args)); err == nil {
		t.Error("New with ftp url succeeded, want error")
	}
}

func TestNewAllowsTemplatedValues(t *testing.T) {
	args := map[string]any{"url": "$input.hook", "method": "$input.verb"}
	if _, err := New(make(chan common.ResultData, 1), testArgs(args)); err != nil {
		t.Errorf("New with templated values error: %v", err)
	}
}

func TestExecuteRequiresURL(t *testing.T) {
	payload := runExecute(map[string]any{"method": "POST"}, nil)
	if payload["type"] != "error" {
		t.Errorf("expected error for missing url, got %v", payload)
	}
}

func TestExecuteRejectsResultRef(t *testing.T) {
	payload := runExecute(map[string]any{"url": "$result.hook", "method": "POST"}, nil)
	if payload["type"] != "error" {
		t.Errorf("expected error for $result ref, got %v", payload)
	}
}

func TestExecutePostJSON(t *testing.T) {
	var gotMethod, gotCT, gotCustom string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotCT = r.Header.Get("Content-Type")
		gotCustom = r.Header.Get("X-Alert-Source")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	payload := runExecute(map[string]any{
		"url": srv.URL, "method": "POST",
		"headers": `{"X-Alert-Source": "nitejaguar"}`,
		"body":    `{"text": "hi"}`,
	}, nil)
	if payload["type"] != "success" {
		t.Fatalf("expected success, got %v", payload)
	}
	if payload["status"] != http.StatusOK {
		t.Errorf("status = %v, want 200", payload["status"])
	}
	if gotMethod != "POST" {
		t.Errorf("method = %q", gotMethod)
	}
	if gotCT != "application/json" {
		t.Errorf("content-type = %q, want application/json", gotCT)
	}
	if gotCustom != "nitejaguar" {
		t.Errorf("custom header = %q", gotCustom)
	}
	if gotBody["text"] != "hi" {
		t.Errorf("posted body = %v", gotBody)
	}
	if body, ok := payload["body"].(map[string]any); !ok || body["ok"] != true {
		t.Errorf("response body should JSON-decode, got %v", payload["body"])
	}
}

func TestExecutePlainTextBody(t *testing.T) {
	var gotCT string
	var gotRaw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		gotRaw = string(raw)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ack"))
	}))
	defer srv.Close()

	payload := runExecute(map[string]any{"url": srv.URL, "body": "plain alert"}, nil)
	if payload["type"] != "success" {
		t.Fatalf("expected success, got %v", payload)
	}
	if !strings.HasPrefix(gotCT, "text/plain") {
		t.Errorf("content-type = %q, want text/plain", gotCT)
	}
	if gotRaw != "plain alert" {
		t.Errorf("raw body = %q", gotRaw)
	}
	if payload["body"] != "ack" {
		t.Errorf("string response should stay a string, got %v", payload["body"])
	}
}

func TestExecuteGetIgnoresBody(t *testing.T) {
	var gotRaw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotRaw = string(raw)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	payload := runExecute(map[string]any{
		"url": srv.URL + "/ping?src=nj", "method": "GET", "body": "must-not-send",
	}, nil)
	if payload["type"] != "success" {
		t.Fatalf("expected success, got %v", payload)
	}
	if gotRaw != "" {
		t.Errorf("GET sent a body: %q", gotRaw)
	}
}

func TestExecuteNon2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"upstream down"}`))
	}))
	defer srv.Close()

	payload := runExecute(map[string]any{"url": srv.URL}, nil)
	if payload["type"] != "error" {
		t.Fatalf("expected error for 502, got %v", payload)
	}
	if payload["status"] != http.StatusBadGateway {
		t.Errorf("status = %v, want 502", payload["status"])
	}
	if body, ok := payload["body"].(map[string]any); !ok || body["error"] != "upstream down" {
		t.Errorf("error should carry decoded body, got %v", payload["body"])
	}
}

func TestExecuteBearerAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	inputs := common.WithCredential(nil, common.CredentialBinding{Ref: "hook-token", Type: "token", Secret: "s3cret"})
	payload := runExecute(map[string]any{"url": srv.URL, "auth": "bearer"}, inputs)
	if payload["type"] != "success" {
		t.Fatalf("expected success, got %v", payload)
	}
	if gotAuth != "Bearer s3cret" {
		t.Errorf("authorization = %q", gotAuth)
	}
}

func TestExecuteBearerWithoutCredentialFails(t *testing.T) {
	payload := runExecute(map[string]any{"url": "https://example.com/hook", "auth": "bearer"}, nil)
	if payload["type"] != "error" {
		t.Errorf("expected error for bearer auth without credential, got %v", payload)
	}
}

func TestExecuteCredentialTypeMismatch(t *testing.T) {
	secret, _ := json.Marshal(map[string]string{"username": "u", "password": "p"})
	inputs := common.WithCredential(nil, common.CredentialBinding{Ref: "up", Type: "username_password", Secret: string(secret)})
	payload := runExecute(map[string]any{"url": "https://example.com/hook", "auth": "bearer"}, inputs)
	if payload["type"] != "error" {
		t.Errorf("expected error for username_password credential, got %v", payload)
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

	inputs := []any{common.ResultData{Payload: map[string]any{"version": "v2", "result": "boom"}}}
	payload := runExecute(map[string]any{
		"url":  srv.URL + "/{{ $input.version }}",
		"body": `{"text": "Deploy {{ $input.version | upper }} failed: $input.result"}`,
	}, inputs)
	if payload["type"] != "success" {
		t.Fatalf("expected success, got %v", payload)
	}
	if !strings.HasSuffix(payload["url"].(string), "/v2") {
		t.Errorf("url templating failed: %v", payload["url"])
	}
	if gotBody["text"] != "Deploy V2 failed: boom" {
		t.Errorf("body templating failed: %v", gotBody)
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

func TestExampleHTTPWorkflowLoads(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/workflow-http.json")
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	var wf struct {
		Nodes map[string]struct {
			ID         string            `json:"id"`
			ActionType string            `json:"action_type"`
			ActionName string            `json:"action_name"`
			Arguments  map[string]string `json:"arguments"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("parse example: %v", err)
	}
	found := false
	for _, n := range wf.Nodes {
		if n.ActionType != "action" || n.ActionName != "http" {
			continue
		}
		found = true
		a, err := New(make(chan common.ResultData, 1), common.ActionArgs{
			Id: n.ID, Name: n.ID, ActionType: n.ActionType,
			ActionName: n.ActionName, Args: n.Arguments,
		})
		if err != nil {
			t.Fatalf("New(http example) error: %v", err)
		}
		if a == nil {
			t.Fatal("expected action")
		}
	}
	if !found {
		t.Fatal("example workflow has no http action node")
	}
}
