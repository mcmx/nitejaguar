package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mcmx/nitejaguar/internal/database"
	"github.com/mcmx/nitejaguar/internal/workflow"

	"github.com/danielgtaylor/huma/v2/humatest"
)

func TestHandler(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	// req := httptest.NewRequest(http.MethodGet, "/", nil)
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	_, api := humatest.New(t)
	s := &Server{db: db, wm: workflow.NewWorkflowManager(false, db)}
	addApiRoutes(api, s)

	resp := api.Get("/health")

	if resp.Code != http.StatusOK {
		t.Errorf("handler() wrong status code = %v", resp.Code)
		return
	}
	//expected := map[string]string{"status": "up"}
	//var actual map[string]string
	// Decode the response body into the actual map
	//if err := json.NewDecoder(resp.Body).Decode(&actual); err != nil {
	//	t.Errorf("handler() error decoding response body: %v", err)
	//	return
	//}
	// Compare the decoded response with the expected value
	//if !reflect.DeepEqual(expected["status"], actual["status"]) {
	//	t.Errorf("handler() wrong response body. expected = %v, actual = %v", expected, actual)
	//	return
	//}
}

const assignmentFlowWorkflow = `{
  "id": "workflow_01kassignmentsync1test0001",
  "name": "Assignment flow workflow",
  "nodes": {
    "trigger_01kassignmentbroadcast00001": {
      "id": "trigger_01kassignmentbroadcast00001",
      "name": "Broadcast trigger",
      "description": "assigned to all clients",
      "action_type": "trigger",
      "action_name": "filechange",
      "arguments": {"path": "/tmp", "event_type": "create"},
      "conditions": {
        "entries": {
          "entry1": {
            "condition": {"leftOperand": true, "operator": "", "rightOperand": null},
            "nexts": ["action_01kassignmentgpufilter0001"]
          }
        }
      },
      "dependencies": null
    },
    "action_01kassignmentgpufilter0001": {
      "id": "action_01kassignmentgpufilter0001",
      "name": "GPU action",
      "description": "only gpu clients",
      "action_type": "action",
      "action_name": "file",
      "arguments": {"action": "create", "file": "/tmp/gpu-was-here.txt"},
      "conditions": {"entries": {}},
      "dependencies": ["trigger_01kassignmentbroadcast00001"],
      "client_tags": ["gpu"]
    }
  }
}`

func decodeBody(t *testing.T, respBody *strings.Reader, v any) {
	t.Helper()
	if err := json.NewDecoder(respBody).Decode(v); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
}

// mintEnrollmentToken creates a tenant-scoped join token for tests and
// returns its plaintext (the only time it is visible).
func mintEnrollmentToken(t *testing.T, db database.Service, tenant string, maxUses int) string {
	t.Helper()
	_, plaintext, err := db.CreateEnrollmentToken(tenant, "test", nil, maxUses)
	if err != nil {
		t.Fatalf("create enrollment token: %v", err)
	}
	return plaintext
}

func TestClientAssignmentFlow(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	wm := workflow.NewWorkflowManager(false, db)
	s := &Server{db: db, wm: wm}
	_, api := humatest.New(t)
	addApiRoutes(api, s)

	if _, err := wm.ImportWorkflowJSON(assignmentFlowWorkflow); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}

	// register a gpu client (tenant comes from the enrollment token)
	joinToken := mintEnrollmentToken(t, db, "default", 0)
	resp := api.Post("/api/clients/register", map[string]any{
		"name":             "gpu-worker",
		"tags":             []string{"gpu"},
		"enrollment_token": joinToken,
	})
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("register gpu client status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var registered struct {
		ClientID string `json:"client_id"`
		Token    string `json:"token"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &registered)
	if !strings.HasPrefix(registered.ClientID, "client_") {
		t.Fatalf("client id %q missing client_ prefix", registered.ClientID)
	}

	// protected routes reject missing credentials.
	resp = api.Post("/api/clients/heartbeat", map[string]any{"client_id": registered.ClientID})
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized heartbeat status = %v", resp.Code)
	}

	// heartbeat
	resp = api.Post("/api/clients/heartbeat", "Authorization: Bearer "+registered.Token, map[string]any{
		"client_id": registered.ClientID,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("heartbeat status = %v, body = %s", resp.Code, resp.Body.String())
	}

	// client status lists registration metadata without exposing credentials.
	resp = api.Get("/api/clients")
	if resp.Code != http.StatusOK {
		t.Fatalf("clients status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var clients struct {
		Clients []struct {
			ID            string `json:"client_id"`
			Name          string `json:"name"`
			Online        bool   `json:"online"`
			RegisteredAt  string `json:"registered_at"`
			LastHeartbeat string `json:"last_heartbeat"`
		} `json:"clients"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &clients)
	if len(clients.Clients) != 1 || clients.Clients[0].ID != registered.ClientID ||
		clients.Clients[0].Name != "gpu-worker" || !clients.Clients[0].Online ||
		clients.Clients[0].RegisteredAt == "" || clients.Clients[0].LastHeartbeat == "" {
		t.Fatalf("unexpected client status: %s", resp.Body.String())
	}

	// assignments for gpu client: broadcast trigger + gpu action
	resp = api.Get("/api/clients/"+registered.ClientID+"/assignments", "Authorization: Bearer "+registered.Token)
	if resp.Code != http.StatusOK {
		t.Fatalf("assignments status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var assignments struct {
		ClientID  string `json:"client_id"`
		Workflows []struct {
			ID    string `json:"id"`
			Nodes map[string]struct {
				ID         string   `json:"id"`
				Client     string   `json:"client"`
				ClientTags []string `json:"client_tags"`
			} `json:"nodes"`
		} `json:"workflows"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &assignments)
	foundTrigger, foundGPUAction := false, false
	for _, wf := range assignments.Workflows {
		if wf.ID != "workflow_01kassignmentsync1test0001" {
			continue
		}
		for id := range wf.Nodes {
			switch id {
			case "trigger_01kassignmentbroadcast00001":
				foundTrigger = true
			case "action_01kassignmentgpufilter0001":
				foundGPUAction = true
			}
		}
	}
	if !foundTrigger || !foundGPUAction {
		t.Fatalf("gpu client assignments missing nodes: trigger=%v gpuAction=%v body=%s", foundTrigger, foundGPUAction, resp.Body.String())
	}

	// register a cpu client: should only see the broadcast trigger
	resp = api.Post("/api/clients/register", map[string]any{
		"name":             "cpu-worker",
		"tags":             []string{"cpu"},
		"enrollment_token": mintEnrollmentToken(t, db, "default", 0),
	})
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("register cpu client status = %v", resp.Code)
	}
	var cpuRegistered struct {
		ClientID string `json:"client_id"`
		Token    string `json:"token"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &cpuRegistered)
	resp = api.Get("/api/clients/"+cpuRegistered.ClientID+"/assignments", "Authorization: Bearer "+cpuRegistered.Token)
	if resp.Code != http.StatusOK {
		t.Fatalf("cpu assignments status = %v", resp.Code)
	}
	var cpuAssignments struct {
		Workflows []struct {
			ID    string         `json:"id"`
			Nodes map[string]any `json:"nodes"`
		} `json:"workflows"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &cpuAssignments)
	for _, wf := range cpuAssignments.Workflows {
		if wf.ID != "workflow_01kassignmentsync1test0001" {
			continue
		}
		if _, ok := wf.Nodes["action_01kassignmentgpufilter0001"]; ok {
			t.Fatalf("cpu client should not be assigned the gpu node: %s", resp.Body.String())
		}
		if _, ok := wf.Nodes["trigger_01kassignmentbroadcast00001"]; !ok {
			t.Fatalf("cpu client should be assigned the broadcast trigger: %s", resp.Body.String())
		}
	}

	// post a result for the trigger: server advances to the gpu action
	resp = api.Post("/api/results", "Authorization: Bearer "+registered.Token, map[string]any{
		"action_id":   "trigger_01kassignmentbroadcast00001",
		"action_type": "trigger",
		"action_name": "filechange",
		"payload":     map[string]any{"file": "/tmp/x"},
	})
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("post result status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var resultOut struct {
		WorkflowID  string   `json:"workflow_id"`
		ExecutionID string   `json:"execution_id"`
		Nexts       []string `json:"nexts"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &resultOut)
	if resultOut.WorkflowID != "workflow_01kassignmentsync1test0001" {
		t.Errorf("expected workflow_id workflow_01kassignmentsync1test0001, got %q", resultOut.WorkflowID)
	}
	if resultOut.ExecutionID == "" {
		t.Errorf("expected minted execution_id, got empty")
	}
	if resultOut.WorkflowID == "" {
		t.Errorf("expected workflow id for client result")
	}
	found := false
	for _, n := range resultOut.Nexts {
		if n == "action_01kassignmentgpufilter0001" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected nexts to contain gpu action, got %v", resultOut.Nexts)
	}

	// Replaying the same result is idempotent and returns the stable response.
	duplicate := api.Post("/api/results", "Authorization: Bearer "+registered.Token, map[string]any{
		"result_id":   "result_duplicate_test",
		"action_id":   "trigger_01kassignmentbroadcast00001",
		"action_type": "trigger",
	})
	if duplicate.Code != http.StatusOK {
		t.Fatalf("duplicate seed status = %v", duplicate.Code)
	}
	replay := api.Post("/api/results", "Authorization: Bearer "+registered.Token, map[string]any{
		"result_id":   "result_duplicate_test",
		"action_id":   "trigger_01kassignmentbroadcast00001",
		"action_type": "trigger",
	})
	if replay.Code != http.StatusOK || replay.Body.String() != duplicate.Body.String() {
		t.Fatalf("duplicate result response changed: first=%s replay=%s", duplicate.Body.String(), replay.Body.String())
	}

	// unknown action -> 404
	resp = api.Post("/api/results", "Authorization: Bearer "+registered.Token, map[string]any{"action_id": "action_doesnotexist00000001"})
	if resp.Code != http.StatusNotFound {
		t.Errorf("unknown action result status = %v, want 404", resp.Code)
	}

	// unknown client heartbeat -> unauthorized without the unknown client token
	resp = api.Post("/api/clients/heartbeat", "Authorization: Bearer "+registered.Token, map[string]any{"client_id": "client_doesnotexist00001"})
	if resp.Code != http.StatusUnauthorized {
		t.Errorf("unknown client heartbeat status = %v, want 401", resp.Code)
	}

	// unknown client assignments -> unauthorized without the unknown client token
	resp = api.Get("/api/clients/client_doesnotexist00001/assignments", "Authorization: Bearer "+registered.Token)
	if resp.Code != http.StatusUnauthorized {
		t.Errorf("unknown client assignments status = %v, want 401", resp.Code)
	}
}

func TestClientAssignmentsExcludeDisabledWorkflows(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	wm := workflow.NewWorkflowManager(false, db)
	s := &Server{db: db, wm: wm}
	_, api := humatest.New(t)
	addApiRoutes(api, s)
	if _, err := wm.ImportWorkflowJSON(assignmentFlowWorkflow); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	if err := db.SetWorkflowEnabled("workflow_01kassignmentsync1test0001", false); err != nil {
		t.Fatalf("disable workflow: %v", err)
	}
	resp := api.Post("/api/clients/register", map[string]any{"name": "disabled-test", "tags": []string{"gpu"}, "enrollment_token": mintEnrollmentToken(t, db, "default", 0)})
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("register status = %v", resp.Code)
	}
	var registered struct {
		ClientID string `json:"client_id"`
		Token    string `json:"token"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &registered)
	resp = api.Get("/api/clients/"+registered.ClientID+"/assignments", "Authorization: Bearer "+registered.Token)
	if resp.Code != http.StatusOK {
		t.Fatalf("assignments status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var assignments struct {
		Workflows []any `json:"workflows"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &assignments)
	if len(assignments.Workflows) != 0 {
		t.Fatalf("disabled workflow was assigned: %s", resp.Body.String())
	}
}

func TestWorkflowImportCloneAPI(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	wm := workflow.NewWorkflowManager(false, db)
	s := &Server{db: db, wm: wm}
	_, api := humatest.New(t)
	addApiRoutes(api, s)

	var def map[string]any
	if err := json.Unmarshal([]byte(assignmentFlowWorkflow), &def); err != nil {
		t.Fatalf("unmarshal seed workflow: %v", err)
	}

	resp := api.Post("/api/workflows/import", def)
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("import status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var imported struct {
		Ok         bool   `json:"ok"`
		WorkflowID string `json:"workflow_id"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &imported)
	if !imported.Ok || imported.WorkflowID != "workflow_01kassignmentsync1test0001" {
		t.Fatalf("unexpected import response: %s", resp.Body.String())
	}
	if _, err := db.GetWorkflow(imported.WorkflowID); err != nil {
		t.Fatalf("imported workflow not in db: %v", err)
	}

	resp = api.Post("/api/workflows/clone", def)
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("clone status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var cloned struct {
		Ok         bool   `json:"ok"`
		WorkflowID string `json:"workflow_id"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &cloned)
	if !cloned.Ok || cloned.WorkflowID == "" || cloned.WorkflowID == "workflow_01kassignmentsync1test0001" {
		t.Fatalf("unexpected clone response: %s", resp.Body.String())
	}
	row, err := db.GetWorkflow(cloned.WorkflowID)
	if err != nil {
		t.Fatalf("cloned workflow not in db: %v", err)
	}
	var clonedDef workflow.Workflow
	if err := json.Unmarshal([]byte(row.JSONDefinition), &clonedDef); err != nil {
		t.Fatalf("unmarshal cloned workflow: %v", err)
	}
	if len(clonedDef.Name) < len("Clone of: ") || clonedDef.Name[:len("Clone of: ")] != "Clone of: " {
		t.Fatalf("cloned name missing prefix: %q", clonedDef.Name)
	}

	resp = api.Post("/api/workflows/import", map[string]any{})
	if resp.Code != http.StatusUnprocessableEntity && resp.Code != http.StatusBadRequest {
		t.Fatalf("invalid import status = %v, want 400 or 422", resp.Code)
	}
}

func TestTenantEnrollmentLifecycle(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	wm := workflow.NewWorkflowManager(false, db)
	s := &Server{db: db, wm: wm}
	_, api := humatest.New(t)
	addApiRoutes(api, s)

	// Open registration is closed: no token -> 401.
	resp := api.Post("/api/clients/register", map[string]any{"name": "no-token"})
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("register without token status = %v, want 401", resp.Code)
	}

	// Invalid token -> 401.
	resp = api.Post("/api/clients/register", map[string]any{"name": "bad-token", "enrollment_token": "not-a-real-token"})
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("register with bad token status = %v, want 401", resp.Code)
	}

	// Mint a token via the API for tenant acme.
	resp = api.Post("/api/enrollment/tokens", map[string]any{"tenant_id": "acme", "label": "test"})
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("create enrollment token status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var created struct {
		ID       string `json:"id"`
		TenantID string `json:"tenant_id"`
		Token    string `json:"token"`
		MaxUses  int    `json:"max_uses"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &created)
	if created.Token == "" || created.TenantID != "acme" {
		t.Fatalf("unexpected token response: %s", resp.Body.String())
	}

	// Tenant comes from the token: a mismatched client-supplied tenant_id
	// is ignored, the client lands in the token tenant.
	resp = api.Post("/api/clients/register", map[string]any{
		"name": "acme-worker", "tenant_id": "someone-else", "enrollment_token": created.Token,
	})
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("register with token status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var registered struct {
		ClientID string `json:"client_id"`
		Token    string `json:"token"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &registered)

	resp = api.Get("/api/clients")
	var clients struct {
		Clients []struct {
			ID       string `json:"client_id"`
			TenantID string `json:"tenant_id"`
		} `json:"clients"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &clients)
	tenantOf := ""
	for _, c := range clients.Clients {
		if c.ID == registered.ClientID {
			tenantOf = c.TenantID
		}
	}
	if tenantOf != "acme" {
		t.Fatalf("client tenant = %q, want acme (token tenant wins)", tenantOf)
	}

	// One-time token: second use is exhausted -> 401.
	resp = api.Post("/api/enrollment/tokens", map[string]any{"tenant_id": "acme", "max_uses": 1})
	var oneTime struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &oneTime)
	resp = api.Post("/api/clients/register", map[string]any{"name": "first", "enrollment_token": oneTime.Token})
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("first one-time use status = %v", resp.Code)
	}
	resp = api.Post("/api/clients/register", map[string]any{"name": "second", "enrollment_token": oneTime.Token})
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("exhausted token status = %v, want 401", resp.Code)
	}

	// Revoking a token blocks new joins.
	resp = api.Post("/api/enrollment/tokens", map[string]any{"tenant_id": "acme"})
	var revokable struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &revokable)
	resp = api.Post("/api/enrollment/tokens/" + revokable.ID + "/revoke", map[string]any{})
	if resp.Code != http.StatusOK {
		t.Fatalf("revoke token status = %v, body = %s", resp.Code, resp.Body.String())
	}
	resp = api.Post("/api/clients/register", map[string]any{"name": "after-revoke", "enrollment_token": revokable.Token})
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token status = %v, want 401", resp.Code)
	}

	// Revoking a client stops its token from authenticating.
	resp = api.Post("/api/clients/"+registered.ClientID+"/revoke", map[string]any{})
	if resp.Code != http.StatusOK {
		t.Fatalf("revoke client status = %v, body = %s", resp.Code, resp.Body.String())
	}
	resp = api.Post("/api/clients/heartbeat", "Authorization: Bearer "+registered.Token, map[string]any{
		"client_id": registered.ClientID,
	})
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("revoked client heartbeat status = %v, want 401", resp.Code)
	}
	resp = api.Get("/api/clients/"+registered.ClientID+"/assignments", "Authorization: Bearer "+registered.Token)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("revoked client assignments status = %v, want 401", resp.Code)
	}

	// Every enrollment use is audited.
	resp = api.Get("/api/audit?limit=500")
	if resp.Code != http.StatusOK {
		t.Fatalf("audit status = %v", resp.Code)
	}
	var audit struct {
		Entries []struct {
			Action string `json:"action"`
			Target string `json:"target"`
		} `json:"entries"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &audit)
	seen := map[string]bool{}
	for _, e := range audit.Entries {
		seen[e.Action] = true
	}
	for _, action := range []string{"enrollment.use", "enrollment.create", "enrollment.revoke", "client.revoke"} {
		if !seen[action] {
			t.Fatalf("audit missing action %q: %s", action, resp.Body.String())
		}
	}
}

const crossClientWorkflow = `{
  "id": "workflow_01kcrossclienthandoff0001",
  "name": "Cross-client handoff workflow",
  "nodes": {
    "trigger_01kcrossclienthandoff0001": {
      "id": "trigger_01kcrossclienthandoff0001",
      "name": "Broadcast trigger",
      "action_type": "trigger",
      "action_name": "filechange",
      "arguments": {"path": "/tmp", "event_type": "create"},
      "conditions": {"entries": {"entry1": {"condition": {"leftOperand": true, "operator": "", "rightOperand": null}, "nexts": ["action_01kcrossclienthandoff0001"]}}},
      "dependencies": null
    },
    "action_01kcrossclienthandoff0001": {
      "id": "action_01kcrossclienthandoff0001",
      "name": "GPU action",
      "action_type": "action",
      "action_name": "file",
      "arguments": {"action": "create", "file": "/tmp/gpu.txt"},
      "conditions": {"entries": {}},
      "dependencies": ["trigger_01kcrossclienthandoff0001"],
      "client_tags": ["gpu"]
    }
  }
}`

func TestCrossClientHandoff(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	wm := workflow.NewWorkflowManager(false, db)
	s := &Server{db: db, wm: wm}
	_, api := humatest.New(t)
	addApiRoutes(api, s)

	if _, err := wm.ImportWorkflowJSON(crossClientWorkflow); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}

	register := func(name string, tags []string) (string, string) {
		t.Helper()
		resp := api.Post("/api/clients/register", map[string]any{
			"name": name, "tags": tags, "enrollment_token": mintEnrollmentToken(t, db, "default", 0),
		})
		if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
			t.Fatalf("register %s status = %v", name, resp.Code)
		}
		var reg struct {
			ClientID string `json:"client_id"`
			Token    string `json:"token"`
		}
		decodeBody(t, strings.NewReader(resp.Body.String()), &reg)
		return reg.ClientID, reg.Token
	}
	cpuID, cpuToken := register("cpu-handoff", []string{"cpu"})
	gpuID, gpuToken := register("gpu-handoff", []string{"gpu"})

	// CPU executes the broadcast trigger. The gpu-owned next must NOT be
	// returned to the CPU client — it is routed via pending assignments.
	resp := api.Post("/api/results", "Authorization: Bearer "+cpuToken, map[string]any{
		"action_id":   "trigger_01kcrossclienthandoff0001",
		"action_type": "trigger",
		"action_name": "filechange",
		"payload":     map[string]any{"file": "/tmp/x"},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("cpu post result status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var cpuOut struct {
		WorkflowID  string   `json:"workflow_id"`
		ExecutionID string   `json:"execution_id"`
		Nexts       []string `json:"nexts"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &cpuOut)
	if cpuOut.ExecutionID == "" {
		t.Fatalf("expected execution id, got empty")
	}
	for _, n := range cpuOut.Nexts {
		if n == "action_01kcrossclienthandoff0001" {
			t.Fatalf("cpu client must not receive foreign next directly: %v", cpuOut.Nexts)
		}
	}

	// Server persisted the handoff for the gpu owner.
	pending, err := db.ListPendingAssignments()
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	found := false
	for _, p := range pending {
		if p.WorkflowID == "workflow_01kcrossclienthandoff0001" && p.ExecutionID == cpuOut.ExecutionID && p.NodeID == "action_01kcrossclienthandoff0001" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected pending assignment for gpu action, got %+v", pending)
	}

	// GPU poll sees the pending handoff; CPU poll does not.
	resp = api.Get("/api/clients/"+gpuID+"/assignments", "Authorization: Bearer "+gpuToken)
	if resp.Code != http.StatusOK {
		t.Fatalf("gpu assignments status = %v", resp.Code)
	}
	var gpuAssign struct {
		Pending []struct {
			WorkflowID  string `json:"workflow_id"`
			ExecutionID string `json:"execution_id"`
			NodeID      string `json:"node_id"`
		} `json:"pending"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &gpuAssign)
	found = false
	for _, p := range gpuAssign.Pending {
		if p.NodeID == "action_01kcrossclienthandoff0001" && p.ExecutionID == cpuOut.ExecutionID {
			found = true
		}
	}
	if !found {
		t.Fatalf("gpu poll missing pending handoff: %s", resp.Body.String())
	}
	resp = api.Get("/api/clients/"+cpuID+"/assignments", "Authorization: Bearer "+cpuToken)
	var cpuAssign struct {
		Pending []struct {
			NodeID string `json:"node_id"`
		} `json:"pending"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &cpuAssign)
	for _, p := range cpuAssign.Pending {
		if p.NodeID == "action_01kcrossclienthandoff0001" {
			t.Fatalf("cpu poll must not see gpu-owned pending: %s", resp.Body.String())
		}
	}

	// GPU executes its node with the same execution: pending completes.
	resp = api.Post("/api/results", "Authorization: Bearer "+gpuToken, map[string]any{
		"execution_id": cpuOut.ExecutionID,
		"action_id":    "action_01kcrossclienthandoff0001",
		"action_type":  "action",
		"action_name":  "file",
		"payload":      map[string]any{"ok": true},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("gpu post result status = %v, body = %s", resp.Code, resp.Body.String())
	}
	pending, err = db.ListPendingAssignments()
	if err != nil {
		t.Fatalf("list pending after complete: %v", err)
	}
	for _, p := range pending {
		if p.WorkflowID == "workflow_01kcrossclienthandoff0001" && p.ExecutionID == cpuOut.ExecutionID && p.NodeID == "action_01kcrossclienthandoff0001" {
			t.Fatalf("pending assignment should be done after gpu result: %+v", p)
		}
	}
}

const defaultTargetingWorkflow = `{
  "id": "workflow_01kdefaulttargeting000001",
  "name": "Default targeting workflow",
  "default_client_tags": ["gpu"],
  "nodes": {
    "trigger_01kdefaulttargeting000001": {
      "id": "trigger_01kdefaulttargeting000001",
      "name": "Defaulted trigger",
      "action_type": "trigger",
      "action_name": "filechange",
      "arguments": {"path": "/tmp"},
      "conditions": {"entries": {}},
      "dependencies": null
    },
    "action_01kdefaulttargetingoverride1": {
      "id": "action_01kdefaulttargetingoverride1",
      "name": "Override broadcast",
      "action_type": "action",
      "action_name": "file",
      "arguments": {"action": "create", "file": "/tmp/x"},
      "conditions": {"entries": {}},
      "dependencies": ["trigger_01kdefaulttargeting000001"],
      "client": "",
      "client_tags": []
    }
  }
}`

func TestWorkflowDefaultTargeting(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	wm := workflow.NewWorkflowManager(false, db)
	s := &Server{db: db, wm: wm}
	_, api := humatest.New(t)
	addApiRoutes(api, s)

	if _, err := wm.ImportWorkflowJSON(defaultTargetingWorkflow); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	register := func(name string, tags []string) (string, string) {
		t.Helper()
		resp := api.Post("/api/clients/register", map[string]any{
			"name": name, "tags": tags, "enrollment_token": mintEnrollmentToken(t, db, "default", 0),
		})
		var reg struct {
			ClientID string `json:"client_id"`
			Token    string `json:"token"`
		}
		decodeBody(t, strings.NewReader(resp.Body.String()), &reg)
		return reg.ClientID, reg.Token
	}
	gpuID, gpuToken := register("gpu-default", []string{"gpu"})
	cpuID, cpuToken := register("cpu-default", []string{"cpu"})

	resp := api.Get("/api/clients/"+gpuID+"/assignments", "Authorization: Bearer "+gpuToken)
	var gpuAssign struct {
		Workflows []struct {
			ID    string         `json:"id"`
			Nodes map[string]any `json:"nodes"`
		} `json:"workflows"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &gpuAssign)
	foundDefault := false
	for _, wf := range gpuAssign.Workflows {
		if wf.ID != "workflow_01kdefaulttargeting000001" {
			continue
		}
		if _, ok := wf.Nodes["trigger_01kdefaulttargeting000001"]; !ok {
			t.Fatalf("gpu should inherit the default-targeted trigger: %s", resp.Body.String())
		}
		foundDefault = true
	}
	if !foundDefault {
		t.Fatalf("gpu missing default workflow: %s", resp.Body.String())
	}

	resp = api.Get("/api/clients/"+cpuID+"/assignments", "Authorization: Bearer "+cpuToken)
	var cpuAssign struct {
		Workflows []struct {
			ID    string         `json:"id"`
			Nodes map[string]any `json:"nodes"`
		} `json:"workflows"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &cpuAssign)
	for _, wf := range cpuAssign.Workflows {
		if wf.ID != "workflow_01kdefaulttargeting000001" {
			continue
		}
		if _, ok := wf.Nodes["trigger_01kdefaulttargeting000001"]; ok {
			t.Fatalf("cpu must not inherit the gpu-defaulted trigger: %s", resp.Body.String())
		}
	}
}
