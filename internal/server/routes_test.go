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
      "action_name": "filechangeTrigger",
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
      "action_name": "fileAction",
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

	if err := wm.ImportWorkflowJSON(assignmentFlowWorkflow); err != nil {
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
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &registered)
	if !strings.HasPrefix(registered.ClientID, "client_") {
		t.Fatalf("client id %q missing client_ prefix", registered.ClientID)
	}

	// heartbeat
	resp = api.Post("/api/clients/heartbeat", map[string]any{
		"client_id": registered.ClientID,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("heartbeat status = %v, body = %s", resp.Code, resp.Body.String())
	}

	// assignments for gpu client: broadcast trigger + gpu action
	resp = api.Get("/api/clients/" + registered.ClientID + "/assignments")
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
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &cpuRegistered)
	resp = api.Get("/api/clients/" + cpuRegistered.ClientID + "/assignments")
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
	resp = api.Post("/api/results", map[string]any{
		"action_id":   "trigger_01kassignmentbroadcast00001",
		"action_type": "trigger",
		"action_name": "filechangeTrigger",
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
	found := false
	for _, n := range resultOut.Nexts {
		if n == "action_01kassignmentgpufilter0001" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected nexts to contain gpu action, got %v", resultOut.Nexts)
	}

	// unknown action -> 404
	resp = api.Post("/api/results", map[string]any{"action_id": "action_doesnotexist00000001"})
	if resp.Code != http.StatusNotFound {
		t.Errorf("unknown action result status = %v, want 404", resp.Code)
	}

	// unknown client heartbeat -> 404
	resp = api.Post("/api/clients/heartbeat", map[string]any{"client_id": "client_doesnotexist00001"})
	if resp.Code != http.StatusNotFound {
		t.Errorf("unknown client heartbeat status = %v, want 404", resp.Code)
	}

	// unknown client assignments -> 404
	resp = api.Get("/api/clients/client_doesnotexist00001/assignments")
	if resp.Code != http.StatusNotFound {
		t.Errorf("unknown client assignments status = %v, want 404", resp.Code)
	}
}
