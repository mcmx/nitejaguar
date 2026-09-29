package client

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mcmx/nitejaguar/internal/workflow"
)

func webhookTestRunner() *runner {
	return newRunner(API{BaseURL: "http://localhost", HTTPClient: http.DefaultClient}, slog.Default())
}

func webhookTestWorkflow(triggerID, method string) Workflow {
	return Workflow{
		ID: "workflow_webhook_client", Name: "webhook", Revision: "rev_1",
		Nodes: map[string]workflow.Node{
			triggerID: {
				Id: triggerID, Name: "Hook", ActionType: "trigger",
				ActionName: "webhook", Arguments: map[string]string{"method": method},
			},
		},
	}
}

func callClientWebhook(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The client fires only triggers assigned to it: the server already
// filtered assignments (explicit client, matching tags, workflow defaults),
// so presence in the installed set is the binding — and removal stops it.
func TestClientWebhookFiresAssignedTrigger(t *testing.T) {
	r := webhookTestRunner()
	defer r.close()
	r.syncAssignments([]Workflow{webhookTestWorkflow("trigger_client_hook1", "ALL")})
	h := r.webhookHTTPHandler()

	rec := callClientWebhook(t, h, http.MethodPost, "/webhook/trigger_client_hook1", `{"event":"push"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST assigned webhook status = %v, body = %s", rec.Code, rec.Body.String())
	}
	var ack struct {
		Ok         bool   `json:"ok"`
		WorkflowID string `json:"workflow_id"`
		TriggerID  string `json:"trigger_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ack); err != nil {
		t.Fatalf("decode ack: %v", err)
	}
	if !ack.Ok || ack.WorkflowID != "workflow_webhook_client" || ack.TriggerID != "trigger_client_hook1" {
		t.Fatalf("unexpected ack: %s", rec.Body.String())
	}

	select {
	case got := <-r.events:
		if got.ActionID != "trigger_client_hook1" || got.ActionName != "webhook" {
			t.Fatalf("event = %+v, want webhook trigger result", got)
		}
		p, ok := got.Payload.(map[string]any)
		if !ok {
			t.Fatalf("payload = %#v, want map", got.Payload)
		}
		if p["method"] != "POST" {
			t.Errorf("payload method = %v, want POST", p["method"])
		}
		body, ok := p["body"].(map[string]any)
		if !ok || body["event"] != "push" {
			t.Errorf("payload body = %v, want decoded event", p["body"])
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("assigned webhook must queue a trigger result")
	}

	// Workflow removed from assignments (disabled / unassigned): 404.
	r.syncAssignments([]Workflow{})
	if rec := callClientWebhook(t, h, http.MethodPost, "/webhook/trigger_client_hook1", `{}`); rec.Code != http.StatusNotFound {
		t.Errorf("unassigned webhook status = %v, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}

// Unknown trigger ids are 404 without leaking other workflows.
func TestClientWebhookUnknownIs404(t *testing.T) {
	r := webhookTestRunner()
	defer r.close()
	r.syncAssignments([]Workflow{webhookTestWorkflow("trigger_client_hook2", "ALL")})
	h := r.webhookHTTPHandler()
	if rec := callClientWebhook(t, h, http.MethodPost, "/webhook/trigger_nope00000001", `{}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown webhook status = %v, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}

// The method argument is enforced on the client listener too.
func TestClientWebhookMethodSelectable(t *testing.T) {
	r := webhookTestRunner()
	defer r.close()
	r.syncAssignments([]Workflow{webhookTestWorkflow("trigger_client_hook3", "POST")})
	h := r.webhookHTTPHandler()

	if rec := callClientWebhook(t, h, http.MethodGet, "/webhook/trigger_client_hook3", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET on POST-only webhook status = %v, want 405 (body %s)", rec.Code, rec.Body.String())
	}
	select {
	case got := <-r.events:
		t.Fatalf("rejected method must not queue a result: %+v", got)
	case <-time.After(100 * time.Millisecond):
	}

	if rec := callClientWebhook(t, h, http.MethodPost, "/webhook/trigger_client_hook3", `{"a":1}`); rec.Code != http.StatusOK {
		t.Errorf("POST on POST-only webhook status = %v, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	select {
	case <-r.events:
	case <-time.After(2 * time.Second):
		t.Fatalf("accepted method must queue a trigger result")
	}
}

// Query strings and methods reach the queued payload for $result routing.
func TestClientWebhookPayloadShape(t *testing.T) {
	r := webhookTestRunner()
	defer r.close()
	r.syncAssignments([]Workflow{webhookTestWorkflow("trigger_client_hook4", "GET")})
	h := r.webhookHTTPHandler()

	rec := callClientWebhook(t, h, http.MethodGet, "/webhook/trigger_client_hook4?branch=main", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET webhook status = %v, body = %s", rec.Code, rec.Body.String())
	}
	select {
	case got := <-r.events:
		p, ok := got.Payload.(map[string]any)
		if !ok {
			t.Fatalf("payload = %#v, want map", got.Payload)
		}
		if p["method"] != "GET" {
			t.Errorf("payload method = %v, want GET", p["method"])
		}
		q, ok := p["query"].(map[string]string)
		if !ok || q["branch"] != "main" {
			t.Errorf("payload query = %#v, want branch=main", p["query"])
		}
		if p["body"] != nil {
			t.Errorf("bodiless GET payload body = %#v, want nil", p["body"])
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("GET webhook must queue a trigger result")
	}
}
