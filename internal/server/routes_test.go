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

	// register a gpu client
	resp := api.Post("/api/clients/register", map[string]any{
		"name": "gpu-worker",
		"tags": []string{"gpu"},
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
		"name": "cpu-worker",
		"tags": []string{"cpu"},
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
	resp := api.Post("/api/clients/register", map[string]any{"name": "disabled-test", "tags": []string{"gpu"}})
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
