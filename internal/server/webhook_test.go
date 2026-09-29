package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mcmx/nitejaguar/internal/database"
	"github.com/mcmx/nitejaguar/internal/workflow"
)

// webhookWorkflowJSON builds a webhook workflow with unique ids per test.
// Tests in this package share one in-memory DB (and one workflow-manager
// singleton), so reusing ids across tests leaks disabled/suspended state.
func webhookWorkflowJSON(workflowID, triggerID, actionID, tenantID string) string {
	tenant := ""
	if tenantID != "" {
		tenant = `"tenant_id": "` + tenantID + `", `
	}
	return `{
  "id": "` + workflowID + `",
  "name": "Webhook test workflow",
  ` + tenant + `"nodes": {
    "` + triggerID + `": {
      "id": "` + triggerID + `",
      "name": "Webhook trigger",
      "action_type": "trigger",
      "action_name": "webhook",
      "arguments": {"method": "ALL"},
      "conditions": {"entries": {"entry1": {"condition": {"leftOperand": "$result.body.event", "operator": "==", "rightOperand": "push"}, "nexts": ["` + actionID + `"]}}},
      "dependencies": null
    },
    "` + actionID + `": {
      "id": "` + actionID + `",
      "name": "Downstream action",
      "action_type": "action",
      "action_name": "file",
      "arguments": {"action": "create", "file": "/tmp/webhook-was-here.txt"},
      "conditions": {"entries": {}},
      "dependencies": ["` + triggerID + `"]
    }
  }
}`
}

const webhookTestWorkflow = `{
  "id": "workflow_01kwebhooktest000001",
  "name": "Webhook test workflow",
  "nodes": {
    "trigger_01kwebhooktest000001": {
      "id": "trigger_01kwebhooktest000001",
      "name": "Webhook trigger",
      "action_type": "trigger",
      "action_name": "webhook",
      "arguments": {"method": "ALL"},
      "conditions": {"entries": {"entry1": {"condition": {"leftOperand": "$result.body.event", "operator": "==", "rightOperand": "push"}, "nexts": ["action_01kwebhooktest000001"]}}},
      "dependencies": null
    },
    "action_01kwebhooktest000001": {
      "id": "action_01kwebhooktest000001",
      "name": "Downstream action",
      "action_type": "action",
      "action_name": "file",
      "arguments": {"action": "create", "file": "/tmp/webhook-was-here.txt"},
      "conditions": {"entries": {}},
      "dependencies": ["trigger_01kwebhooktest000001"]
    }
  }
}`

const webhookPostOnlyWorkflow = `{
  "id": "workflow_01kwebhookpostonly01",
  "name": "Webhook POST-only workflow",
  "nodes": {
    "trigger_01kwebhookpostonly01": {
      "id": "trigger_01kwebhookpostonly01",
      "name": "POST-only hook",
      "action_type": "trigger",
      "action_name": "webhook",
      "arguments": {"method": "POST"},
      "conditions": {"entries": {}},
      "dependencies": null
    }
  }
}`

func webhookTestServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	wm := workflow.NewWorkflowManager(false, db)
	s := &Server{db: db, wm: wm}
	return s, s.RegisterRoutes()
}

func callWebhook(h http.Handler, method, target, body, contentType string) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// POSTed JSON bodies land decoded in the result payload so conditions can
// route on $result.body.*.
func TestWebhookPostsPayloadAndRoutesConditions(t *testing.T) {
	s, h := webhookTestServer(t)
	if _, err := s.wm.ImportWorkflowJSON(webhookTestWorkflow); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}

	rec := callWebhook(h, http.MethodPost, "/webhook/trigger_01kwebhooktest000001?branch=main",
		`{"event":"push"}`, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("POST webhook status = %v, body = %s", rec.Code, rec.Body.String())
	}
	var ack struct {
		Ok          bool   `json:"ok"`
		WorkflowID  string `json:"workflow_id"`
		ExecutionID string `json:"execution_id"`
		TriggerID   string `json:"trigger_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ack); err != nil {
		t.Fatalf("decode ack: %v (body %s)", err, rec.Body.String())
	}
	if !ack.Ok || ack.WorkflowID != "workflow_01kwebhooktest000001" || ack.ExecutionID == "" {
		t.Fatalf("unexpected ack: %s", rec.Body.String())
	}

	rows, err := s.db.ListResults("", "", 0)
	if err != nil {
		t.Fatalf("list results: %v", err)
	}
	var found bool
	for _, row := range rows {
		if row.ActionID != "trigger_01kwebhooktest000001" || row.ExecutionID != ack.ExecutionID {
			continue
		}
		found = true
		var payload map[string]any
		if err := json.Unmarshal([]byte(row.PayloadJSON), &payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload["method"] != "POST" {
			t.Errorf("payload method = %v, want POST", payload["method"])
		}
		body, ok := payload["body"].(map[string]any)
		if !ok || body["event"] != "push" {
			t.Errorf("payload body = %v, want decoded {event:push}", payload["body"])
		}
		query, ok := payload["query"].(map[string]any)
		if !ok || query["branch"] != "main" {
			t.Errorf("payload query = %v, want branch=main", payload["query"])
		}
		// The $result.body.event == push condition matched, routing on.
		if len(row.Nexts) != 1 || row.Nexts[0] != "action_01kwebhooktest000001" {
			t.Errorf("nexts = %v, want downstream action (condition on $result.body)", row.Nexts)
		}
	}
	if !found {
		t.Fatalf("no stored result for the webhook execution %s", ack.ExecutionID)
	}

	// A non-matching body still fires but routes nowhere.
	rec = callWebhook(h, http.MethodPost, "/webhook/trigger_01kwebhooktest000001",
		`{"event":"ping"}`, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("POST ping status = %v, body = %s", rec.Code, rec.Body.String())
	}
}

// The method argument is selectable: mismatched verbs are 405 and never
// produce a result.
func TestWebhookMethodSelectable(t *testing.T) {
	s, h := webhookTestServer(t)
	if _, err := s.wm.ImportWorkflowJSON(webhookPostOnlyWorkflow); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}

	before, err := s.db.ListResults("", "", 0)
	if err != nil {
		t.Fatalf("list results: %v", err)
	}

	if rec := callWebhook(h, http.MethodGet, "/webhook/trigger_01kwebhookpostonly01", "", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET on POST-only webhook status = %v, want 405 (body %s)", rec.Code, rec.Body.String())
	}
	if rec := callWebhook(h, http.MethodDelete, "/webhook/trigger_01kwebhookpostonly01", "", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE on POST-only webhook status = %v, want 405 (body %s)", rec.Code, rec.Body.String())
	}

	after, err := s.db.ListResults("", "", 0)
	if err != nil {
		t.Fatalf("list results: %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("rejected methods must not produce results: before=%d after=%d", len(before), len(after))
	}

	if rec := callWebhook(h, http.MethodPost, "/webhook/trigger_01kwebhookpostonly01", `{"a":1}`, "application/json"); rec.Code != http.StatusOK {
		t.Errorf("POST on POST-only webhook status = %v, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if rec := callWebhook(h, http.MethodPut, "/webhook/trigger_01kwebhookpostonly01", `x`, "text/plain"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT on POST-only webhook status = %v, want 405", rec.Code)
	}
}

// Unknown ids and disabled workflows are 404 without an existence oracle.
func TestWebhookUnknownAndDisabledAre404(t *testing.T) {
	s, h := webhookTestServer(t)
	if _, err := s.wm.ImportWorkflowJSON(webhookTestWorkflow); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}

	if rec := callWebhook(h, http.MethodPost, "/webhook/trigger_doesnotexist000001", `{}`, "application/json"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown webhook status = %v, want 404 (body %s)", rec.Code, rec.Body.String())
	}

	if err := s.db.SetWorkflowEnabled("workflow_01kwebhooktest000001", false); err != nil {
		t.Fatalf("disable workflow: %v", err)
	}
	if rec := callWebhook(h, http.MethodPost, "/webhook/trigger_01kwebhooktest000001", `{}`, "application/json"); rec.Code != http.StatusNotFound {
		t.Errorf("disabled webhook status = %v, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}

// Suspended tenants fail closed: their webhooks answer 403 and never fire.
func TestWebhookSuspendedTenantRefused(t *testing.T) {
	s, h := webhookTestServer(t)
	const wfID = "workflow_01kwebhooksusp000001"
	const trigID = "trigger_01kwebhooksusp000001"
	const actID = "action_01kwebhooksusp000001"
	if _, err := s.wm.ImportWorkflowJSON(webhookWorkflowJSON(wfID, trigID, actID, "acme")); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	if _, _, _, err := s.db.CreateTenant("Acme", "acme", "", "admin", "admin@acme.test", "password-12345"); err != nil &&
		!strings.Contains(err.Error(), "already exists") && !strings.Contains(err.Error(), "already taken") {
		t.Fatalf("create tenant: %v", err)
	}
	if _, err := s.db.SuspendTenant("acme"); err != nil {
		t.Fatalf("suspend tenant: %v", err)
	}
	rec := callWebhook(h, http.MethodPost, "/webhook/"+trigID, `{}`, "application/json")
	if rec.Code != http.StatusForbidden {
		t.Errorf("suspended webhook status = %v, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	if _, err := s.db.ActivateTenant("acme"); err != nil {
		t.Fatalf("reactivate tenant: %v", err)
	}
	rec = callWebhook(h, http.MethodPost, "/webhook/"+trigID, `{"event":"push"}`, "application/json")
	if rec.Code != http.StatusOK {
		t.Errorf("reactivated webhook status = %v, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}

// Non-JSON bodies stay strings so form posts still reach conditions.
func TestWebhookPlainTextBodyStaysString(t *testing.T) {
	s, h := webhookTestServer(t)
	const wfID = "workflow_01kwebhooktext000001"
	const trigID = "trigger_01kwebhooktext000001"
	const actID = "action_01kwebhooktext000001"
	if _, err := s.wm.ImportWorkflowJSON(webhookWorkflowJSON(wfID, trigID, actID, "")); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	rec := callWebhook(h, http.MethodPost, "/webhook/"+trigID, `hello world`, "text/plain")
	if rec.Code != http.StatusOK {
		t.Fatalf("POST text status = %v, body = %s", rec.Code, rec.Body.String())
	}
	var ack struct {
		ExecutionID string `json:"execution_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ack); err != nil {
		t.Fatalf("decode ack: %v", err)
	}
	rows, err := s.db.ListResults("", "", 0)
	if err != nil {
		t.Fatalf("list results: %v", err)
	}
	for _, row := range rows {
		if row.ExecutionID != ack.ExecutionID {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(row.PayloadJSON), &payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload["body"] != "hello world" {
			t.Errorf("text body = %#v, want raw string", payload["body"])
		}
		return
	}
	t.Fatalf("no stored result for execution %s", ack.ExecutionID)
}
