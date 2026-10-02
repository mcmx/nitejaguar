package email

import (
	"encoding/base64"
	"encoding/json"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mcmx/nitejaguar/common"
)

func testArgs(args map[string]any) common.ActionArgs {
	return common.ActionArgs{Id: "action_test", Name: "email", ActionType: "action", ActionName: "email", Args: args}
}

func validArgs() map[string]any {
	return map[string]any{
		"host": "smtp.example.com", "port": "587",
		"from": "noreply@example.com", "to": "ops@example.com",
		"subject": "hi", "body": "hello",
	}
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
	case <-time.After(2 * time.Second):
		panic("no result")
	}
}

func withFakeSend(t *testing.T, fn func(host string, port int, from string, to []string, msg []byte, auth smtp.Auth) error) {
	t.Helper()
	old := sendMailFunc
	sendMailFunc = fn
	t.Cleanup(func() { sendMailFunc = old })
}

func TestNewRejectsBadPort(t *testing.T) {
	args := validArgs()
	args["port"] = "notaport"
	if _, err := New(make(chan common.ResultData, 1), testArgs(args)); err == nil {
		t.Error("New with bad port succeeded, want error")
	}
}

func TestNewAllowsTemplatedHost(t *testing.T) {
	args := validArgs()
	args["host"] = "$input.smtp_host"
	if _, err := New(make(chan common.ResultData, 1), testArgs(args)); err != nil {
		t.Errorf("New with templated host error: %v", err)
	}
}

func TestExecuteRequiresRecipient(t *testing.T) {
	withFakeSend(t, func(string, int, string, []string, []byte, smtp.Auth) error { return nil })
	args := validArgs()
	delete(args, "to")
	payload := runExecute(args, nil)
	if payload["type"] != "error" {
		t.Errorf("expected error for missing to, got %v", payload)
	}
}

func TestExecuteRejectsResultRef(t *testing.T) {
	withFakeSend(t, func(string, int, string, []string, []byte, smtp.Auth) error { return nil })
	args := validArgs()
	args["subject"] = "$result.foo"
	payload := runExecute(args, nil)
	if payload["type"] != "error" {
		t.Errorf("expected error for $result ref, got %v", payload)
	}
}

func TestExecuteAnonymousSend(t *testing.T) {
	var gotAuth smtp.Auth
	var gotTo []string
	var gotMsg []byte
	withFakeSend(t, func(_ string, port int, _ string, to []string, msg []byte, auth smtp.Auth) error {
		gotAuth, gotTo, gotMsg = auth, to, msg
		if port != 587 {
			t.Errorf("port = %d, want 587", port)
		}
		return nil
	})
	payload := runExecute(validArgs(), nil)
	if payload["type"] != "success" {
		t.Fatalf("expected success, got %v", payload)
	}
	if gotAuth != nil {
		t.Error("expected nil auth for anonymous send")
	}
	if len(gotTo) != 1 || gotTo[0] != "ops@example.com" {
		t.Errorf("to = %v", gotTo)
	}
	if !strings.Contains(string(gotMsg), "Subject: hi") {
		t.Errorf("message missing subject: %q", string(gotMsg))
	}
}

func TestExecuteTemplating(t *testing.T) {
	var gotMsg []byte
	withFakeSend(t, func(string, int, string, []string, []byte, smtp.Auth) error { return nil })
	// capture via second hook
	old := sendMailFunc
	sendMailFunc = func(host string, port int, from string, to []string, msg []byte, auth smtp.Auth) error {
		gotMsg = msg
		return old(host, port, from, to, msg, auth)
	}
	args := validArgs()
	args["subject"] = "Hello {{ $input.name | upper }}"
	args["body"] = "Hi $input.name, build $input.build"
	inputs := []any{common.ResultData{Payload: map[string]any{"name": "ada", "build": "42"}}}
	payload := runExecute(args, inputs)
	if payload["type"] != "success" {
		t.Fatalf("expected success, got %v", payload)
	}
	if !strings.Contains(string(gotMsg), "Hello ADA") {
		t.Errorf("subject template not expanded: %q", string(gotMsg))
	}
	if !strings.Contains(string(gotMsg), "Hi ada, build 42") {
		t.Errorf("body refs not expanded: %q", string(gotMsg))
	}
}

func TestExecuteHTMLAlternative(t *testing.T) {
	var gotMsg []byte
	withFakeSend(t, func(_ string, _ int, _ string, _ []string, msg []byte, _ smtp.Auth) error {
		gotMsg = msg
		return nil
	})
	args := validArgs()
	args["html"] = "<b>hi</b>"
	payload := runExecute(args, nil)
	if payload["type"] != "success" {
		t.Fatalf("expected success, got %v", payload)
	}
	s := string(gotMsg)
	if !strings.Contains(s, "multipart/alternative") || !strings.Contains(s, "text/html") || !strings.Contains(s, "text/plain") {
		t.Errorf("expected multipart/alternative with both parts: %q", s)
	}
}

func TestExecuteFileAttachment(t *testing.T) {
	var gotMsg []byte
	withFakeSend(t, func(_ string, _ int, _ string, _ []string, msg []byte, _ smtp.Auth) error {
		gotMsg = msg
		return nil
	})
	dir := t.TempDir()
	path := filepath.Join(dir, "report.txt")
	if err := os.WriteFile(path, []byte("data-123"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := validArgs()
	args["attachments"] = path
	payload := runExecute(args, nil)
	if payload["type"] != "success" {
		t.Fatalf("expected success, got %v", payload)
	}
	if !strings.Contains(string(gotMsg), `filename="report.txt"`) {
		t.Errorf("missing attachment part: %q", string(gotMsg))
	}
	names, _ := payload["attachments"].([]string)
	if len(names) != 1 || names[0] != "report.txt" {
		t.Errorf("attachments = %v", payload["attachments"])
	}
}

func TestExecuteInlineAttachment(t *testing.T) {
	withFakeSend(t, func(string, int, string, []string, []byte, smtp.Auth) error { return nil })
	raw := base64.StdEncoding.EncodeToString([]byte("inline-bytes"))
	spec, _ := json.Marshal([]any{map[string]any{"name": "{{ $input.f }}", "content_base64": raw}})
	args := validArgs()
	args["attachments"] = string(spec)
	inputs := []any{common.ResultData{Payload: map[string]any{"f": "note.txt"}}}
	payload := runExecute(args, inputs)
	if payload["type"] != "success" {
		t.Fatalf("expected success, got %v", payload)
	}
	names, _ := payload["attachments"].([]string)
	if len(names) != 1 || names[0] != "note.txt" {
		t.Errorf("attachments = %v (name should be templated)", payload["attachments"])
	}
}

func TestExecuteAttachmentCap(t *testing.T) {
	withFakeSend(t, func(string, int, string, []string, []byte, smtp.Auth) error { return nil })
	big := base64.StdEncoding.EncodeToString(make([]byte, maxAttachmentsBytes+1))
	spec, _ := json.Marshal([]any{map[string]any{"name": "big.bin", "content_base64": big}})
	args := validArgs()
	args["attachments"] = string(spec)
	payload := runExecute(args, nil)
	if payload["type"] != "error" {
		t.Errorf("expected error for oversize attachments, got %v", payload)
	}
}

func TestExecuteCredentialAuth(t *testing.T) {
	var gotAuth smtp.Auth
	withFakeSend(t, func(_ string, _ int, _ string, _ []string, _ []byte, auth smtp.Auth) error {
		gotAuth = auth
		return nil
	})
	secret, _ := json.Marshal(map[string]string{"username": "svc", "password": "pw"})
	inputs := common.WithCredential(nil, common.CredentialBinding{Ref: "smtp", Type: "username_password", Secret: string(secret)})
	payload := runExecute(validArgs(), inputs)
	if payload["type"] != "success" {
		t.Fatalf("expected success, got %v", payload)
	}
	if gotAuth == nil {
		t.Error("expected auth from credential, got nil")
	}
}

func TestExecuteCredentialTypeMismatch(t *testing.T) {
	withFakeSend(t, func(string, int, string, []string, []byte, smtp.Auth) error { return nil })
	inputs := common.WithCredential(nil, common.CredentialBinding{Ref: "tok", Type: "token", Secret: "abc"})
	payload := runExecute(validArgs(), inputs)
	if payload["type"] != "error" {
		t.Errorf("expected error for token credential, got %v", payload)
	}
}

func TestExecuteCredentialResolveError(t *testing.T) {
	withFakeSend(t, func(string, int, string, []string, []byte, smtp.Auth) error {
		t.Error("send must not run when credential resolution failed")
		return nil
	})
	inputs := common.WithCredential(nil, common.CredentialBinding{Ref: "missing", Err: "credential not found"})
	payload := runExecute(validArgs(), inputs)
	if payload["type"] != "error" {
		t.Errorf("expected error for unresolvable credential, got %v", payload)
	}
}

func TestAddActionDispatches(t *testing.T) {
	events := make(chan common.ResultData, 1)
	// Dispatch through the constructor (registry coverage lives in
	// catalog_test with these same designer defaults).
	a, err := New(events, testArgs(CatalogEntry().Args))
	if err != nil {
		t.Fatalf("New with catalog defaults error: %v", err)
	}
	if a == nil {
		t.Fatal("expected action")
	}
}

func TestAddActionRejectsBadConfig(t *testing.T) {
	args := validArgs()
	args["port"] = "99999"
	if _, err := New(make(chan common.ResultData, 1), testArgs(args)); err == nil {
		t.Error("New with out-of-range port succeeded, want error")
	}
}

func TestExampleEmailWorkflowLoads(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/workflow-email.json")
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
		if n.ActionType != "action" || n.ActionName != "email" {
			continue
		}
		found = true
		a, err := New(make(chan common.ResultData, 1), common.ActionArgs{
			Id: n.ID, Name: n.ID, ActionType: n.ActionType,
			ActionName: n.ActionName, Args: n.Arguments,
		})
		if err != nil {
			t.Fatalf("New(email example) error: %v", err)
		}
		if a == nil {
			t.Fatal("expected action")
		}
		if n.CredentialRef == "" {
			t.Error("example email node should carry credential_ref (host/port in args, auth via store)")
		}
	}
	if !found {
		t.Fatal("example workflow has no email action node")
	}
}
