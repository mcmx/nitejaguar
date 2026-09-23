package workflow

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mcmx/nitejaguar/internal/database"
)

const credentialRefWorkflowJSON = `{
  "id": "workflow_01kcredentialref000001",
  "name": "Credential ref workflow",
  "nodes": {
    "trigger_01kcredentialref000001": {
      "id": "trigger_01kcredentialref000001",
      "name": "Trigger",
      "action_type": "trigger",
      "action_name": "filechange",
      "arguments": {"path": "/tmp"},
      "conditions": {"entries": {"entry1": {"condition": {"leftOperand": true, "operator": "", "rightOperand": null}, "nexts": ["action_01kcredentialref000001"]}}},
      "dependencies": null,
      "credential_ref": "watchtower-token"
    },
    "action_01kcredentialref000001": {
      "id": "action_01kcredentialref000001",
      "name": "Action",
      "action_type": "action",
      "action_name": "file",
      "arguments": {"action": "create", "file": "/tmp/out.txt"},
      "conditions": {"entries": {}},
      "dependencies": ["trigger_01kcredentialref000001"],
      "credential_ref": "deploy-key"
    }
  }
}`

func TestCredentialRefSurvivesImportAndClone(t *testing.T) {
	t.Setenv("DB_URL", "file:test_credential_ref?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	wmmInstance = nil
	wm := NewWorkflowManager(false, db)
	// NOTE: do not close db here: internal/database caches the connection
	// in a package singleton shared with the other tests in this package.
	t.Cleanup(func() {
		wmmInstance = nil
	})

	if _, err := wm.ImportWorkflowJSON(credentialRefWorkflowJSON); err != nil {
		t.Fatalf("import credential workflow: %v", err)
	}
	saved := savedWorkflows(t, db)
	found := false
	for _, wf := range saved {
		if wf.Id != "workflow_01kcredentialref000001" {
			continue
		}
		found = true
		if wf.Nodes["action_01kcredentialref000001"].CredentialRef != "deploy-key" {
			t.Error("credential_ref lost on import (action node)")
		}
		if wf.Nodes["trigger_01kcredentialref000001"].CredentialRef != "watchtower-token" {
			t.Error("credential_ref lost on import (trigger node)")
		}
		row, err := db.GetWorkflow(wf.Id)
		if err != nil {
			t.Fatalf("get workflow row: %v", err)
		}
		// The definition carries the reference, never a secret.
		if !strings.Contains(row.JSONDefinition, "credential_ref") {
			t.Error("saved definition missing credential_ref")
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(row.JSONDefinition), &raw); err != nil {
			t.Fatalf("unmarshal saved definition: %v", err)
		}
	}
	if !found {
		t.Fatal("credential workflow not found in db after import")
	}

	cloneID, err := wm.CloneWorkflowJSON(credentialRefWorkflowJSON)
	if err != nil {
		t.Fatalf("clone credential workflow: %v", err)
	}
	kept := 0
	for _, wf := range savedWorkflows(t, db) {
		if wf.Id != cloneID {
			continue
		}
		for _, n := range wf.Nodes {
			if n.CredentialRef != "" {
				kept++
			}
		}
		assertEdgeIntegrity(t, wf)
	}
	if kept != 2 {
		t.Errorf("expected credential_ref on 2 cloned nodes, got %d", kept)
	}
	// Rename the clone so it no longer carries the "Clone of: " prefix:
	// the database connection is a shared singleton and the clone-count
	// assertions in TestWorkflowImportAndClone count that prefix globally.
	for _, wf := range savedWorkflows(t, db) {
		if wf.Id != cloneID {
			continue
		}
		wf.Name = "Credential ref workflow clone artifact"
		raw, err := json.Marshal(wf)
		if err != nil {
			t.Fatalf("marshal renamed clone: %v", err)
		}
		if _, err := wm.ImportWorkflowJSON(string(raw)); err != nil {
			t.Fatalf("rename clone artifact: %v", err)
		}
	}
}
