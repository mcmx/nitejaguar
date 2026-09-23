package workflow

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/database"
)

const (
	workflow1Path    = "../../examples/workflow1.json"
	workflow2Path    = "../../examples/workflow2.json"
	pocDownloadsPath = "../../examples/workflow-poc-downloads.json"

	workflow1ID   = "workflow_01jq9c43qhejts98g7n1krqpgd"
	workflow1Name = "First Workflow from json"
	workflow1Node = "trigger_01jq9c43qhejtacw2p66k2ke5s"
)

const mergeWorkflowJSON = `{
  "id": "workflow_01mergeinputtest00000001",
  "name": "Merge input workflow",
  "nodes": {
    "trigger_01mergeinputtest000001": {
      "id": "trigger_01mergeinputtest000001",
      "name": "Trigger",
      "action_type": "trigger",
      "action_name": "filechange",
      "arguments": {"path": "/tmp"},
      "conditions": {"entries": {}},
      "dependencies": null
    },
    "action_01mergeinputtest0000001": {
      "id": "action_01mergeinputtest0000001",
      "name": "Action",
      "action_type": "action",
      "action_name": "file",
      "arguments": {"action": "create", "file": "/tmp/out.txt"},
      "conditions": {"entries": {}},
      "dependencies": ["trigger_01mergeinputtest000001"],
      "merge_input": true
    }
  }
}`

func readWorkflow(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

func savedWorkflows(t *testing.T, db database.Service) []Workflow {
	t.Helper()
	rows, err := db.GetWorkflows(true, true)
	if err != nil {
		t.Fatalf("get workflows: %v", err)
	}
	wfs := make([]Workflow, 0, len(rows))
	for _, row := range rows {
		var wf Workflow
		if err := json.Unmarshal([]byte(row.JSONDefinition), &wf); err != nil {
			t.Fatalf("unmarshal saved workflow: %v", err)
		}
		wfs = append(wfs, wf)
	}
	return wfs
}

func assertEdgeIntegrity(t *testing.T, wf Workflow) {
	t.Helper()
	for nodeID, n := range wf.Nodes {
		if n.Conditions != nil {
			for _, entry := range n.Conditions.Entries {
				for _, next := range entry.Nexts {
					if _, ok := wf.Nodes[next]; !ok {
						t.Errorf("node %s: condition nexts references unknown node %q", nodeID, next)
					}
				}
			}
		}
		for _, dep := range n.Dependencies {
			if _, ok := wf.Nodes[dep]; !ok {
				t.Errorf("node %s: dependency references unknown node %q", nodeID, dep)
			}
		}
	}
}

func TestDownloadsPOCDefinition(t *testing.T) {
	var wf Workflow
	if err := json.Unmarshal([]byte(readWorkflow(t, pocDownloadsPath)), &wf); err != nil {
		t.Fatalf("unmarshal Downloads POC: %v", err)
	}
	if wf.Id == "" || len(wf.Nodes) != 3 {
		t.Fatalf("invalid POC workflow identity or node count: %q, %d", wf.Id, len(wf.Nodes))
	}
	var trigger, stamp, action Node
	for _, node := range wf.Nodes {
		switch {
		case node.ActionType == "trigger":
			trigger = node
		case node.ActionName == "datetime":
			stamp = node
		default:
			action = node
		}
	}
	if trigger.ActionName != "filechange" || trigger.Arguments["path"] != "~/Downloads" || trigger.Arguments["event_type"] != "create,write" {
		t.Fatalf("unexpected POC trigger: %+v", trigger)
	}
	entry := trigger.Conditions.Entries["pdf_file"]
	if entry.Condition.Operator != "=~" || entry.Condition.LeftOperand != "$result.file" || regexp.MustCompile(entry.Condition.RightOperand.(string)).MatchString("report-20260920.pdf") {
		t.Fatalf("POC condition does not exclude dated PDFs: %+v", entry.Condition)
	}
	if len(entry.Nexts) != 1 || entry.Nexts[0] != stamp.Id {
		t.Fatalf("POC trigger must route to the datetime node: %+v", entry.Nexts)
	}
	if stamp.Arguments["operation"] != "getCurrentDate" || stamp.Arguments["format"] != "20060102" || stamp.Arguments["output_field"] != "now" {
		t.Fatalf("unexpected POC datetime args: %+v", stamp.Arguments)
	}
	if !stamp.MergeInput {
		t.Fatalf("POC datetime node must merge input to carry the trigger file forward: %+v", stamp)
	}
	if action.ActionName != "file" || action.Arguments["action"] != "rename" || action.Arguments["file"] != "$input.file" || action.Arguments["new_file"] != "{{stem}}-$input.now{{ext}}" {
		t.Fatalf("unexpected POC action: %+v", action)
	}
	assertEdgeIntegrity(t, wf)
}

func TestWorkflowImportAndClone(t *testing.T) {
	// internal/database caches its connection in a package singleton, so all
	// subtests must share a single in-memory database.
	t.Setenv("DB_URL", "file:test_import_clone?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	wmmInstance = nil
	wm := NewWorkflowManager(false, db)
	t.Cleanup(func() {
		wmmInstance = nil
		_ = db.Close()
	})

	t.Run("import keeps ids and name verbatim", func(t *testing.T) {
		raw := readWorkflow(t, workflow1Path)
		if _, err := wm.ImportWorkflowJSON(raw); err != nil {
			t.Fatalf("import workflow: %v", err)
		}

		var imported *Workflow
		for _, wf := range savedWorkflows(t, db) {
			if wf.Id == workflow1ID {
				imported = &wf
				break
			}
		}
		if imported == nil {
			t.Fatalf("workflow %q not found in db", workflow1ID)
		}
		if imported.Name != workflow1Name {
			t.Errorf("workflow name not preserved verbatim: got %q, want %q", imported.Name, workflow1Name)
		}
		if len(imported.Nodes) != 1 {
			t.Fatalf("expected 1 node, got %d", len(imported.Nodes))
		}
		if _, ok := imported.Nodes[workflow1Node]; !ok {
			t.Errorf("node id %q not preserved verbatim", workflow1Node)
		}

		// Re-import is an idempotent overwrite
		if _, err := wm.ImportWorkflowJSON(raw); err != nil {
			t.Fatalf("re-import workflow: %v", err)
		}
		count := 0
		for _, wf := range savedWorkflows(t, db) {
			if wf.Id == workflow1ID {
				count++
			}
		}
		if count != 1 {
			t.Errorf("expected 1 workflow with id %q after re-import, got %d", workflow1ID, count)
		}
	})

	t.Run("clone mints new ids and rewrites edges", func(t *testing.T) {
		raw := readWorkflow(t, workflow2Path)
		var original Workflow
		if err := json.Unmarshal([]byte(raw), &original); err != nil {
			t.Fatalf("unmarshal %s: %v", workflow2Path, err)
		}
		originalNodeIDs := make(map[string]bool, len(original.Nodes))
		for id := range original.Nodes {
			originalNodeIDs[id] = true
		}

		if _, err := wm.CloneWorkflowJSON(raw); err != nil {
			t.Fatalf("clone workflow: %v", err)
		}

		var clone *Workflow
		for _, wf := range savedWorkflows(t, db) {
			if strings.HasPrefix(wf.Name, "Clone of: ") {
				clone = &wf
				break
			}
		}
		if clone == nil {
			t.Fatal("cloned workflow not found in db")
		}
		if clone.Id == original.Id {
			t.Errorf("cloned workflow id should differ from original")
		}
		if !strings.HasPrefix(clone.Id, "workflow_") {
			t.Errorf("cloned workflow id %q missing workflow_ prefix", clone.Id)
		}
		if !strings.Contains(clone.Name, original.Name) {
			t.Errorf("cloned name %q should keep the original name %q", clone.Name, original.Name)
		}
		if len(clone.Nodes) != len(original.Nodes) {
			t.Fatalf("expected %d nodes in clone, got %d", len(original.Nodes), len(clone.Nodes))
		}
		for _, n := range clone.Nodes {
			if originalNodeIDs[n.Id] {
				t.Errorf("node id %q was not regenerated", n.Id)
			}
			prefix := "action_"
			if n.ActionType == "trigger" {
				prefix = "trigger_"
			}
			if !strings.HasPrefix(n.Id, prefix) {
				t.Errorf("node %q missing %q prefix", n.Id, prefix)
			}
		}
		assertEdgeIntegrity(t, *clone)

		// Cloning the same file again produces a second distinct workflow
		if _, err := wm.CloneWorkflowJSON(raw); err != nil {
			t.Fatalf("clone workflow again: %v", err)
		}
		seenIDs := make(map[string]bool)
		for _, wf := range savedWorkflows(t, db) {
			if !strings.HasPrefix(wf.Name, "Clone of: ") {
				continue
			}
			if seenIDs[wf.Id] {
				t.Errorf("duplicate cloned workflow id %q", wf.Id)
			}
			seenIDs[wf.Id] = true
			assertEdgeIntegrity(t, wf)
		}
		if len(seenIDs) != 2 {
			t.Errorf("expected 2 distinct clones, got %d", len(seenIDs))
		}
	})

	t.Run("merge_input survives import and clone", func(t *testing.T) {
		var wf Workflow
		if err := json.Unmarshal([]byte(mergeWorkflowJSON), &wf); err != nil {
			t.Fatalf("unmarshal merge workflow: %v", err)
		}
		if !wf.Nodes["action_01mergeinputtest0000001"].MergeInput {
			t.Fatal("expected merge_input=true on action node")
		}
		if wf.Nodes["trigger_01mergeinputtest000001"].MergeInput {
			t.Fatal("trigger should default to merge_input=false")
		}

		if _, err := wm.ImportWorkflowJSON(mergeWorkflowJSON); err != nil {
			t.Fatalf("import merge workflow: %v", err)
		}
		saved := savedWorkflows(t, db)
		found := false
		for _, sw := range saved {
			if sw.Id == "workflow_01mergeinputtest00000001" {
				found = true
				if !sw.Nodes["action_01mergeinputtest0000001"].MergeInput {
					t.Error("merge_input flag lost on import")
				}
			}
		}
		if !found {
			t.Fatal("merge workflow not found in db after import")
		}

		if _, err := wm.CloneWorkflowJSON(mergeWorkflowJSON); err != nil {
			t.Fatalf("clone merge workflow: %v", err)
		}
		kept := false
		for _, sw := range savedWorkflows(t, db) {
			if !strings.HasPrefix(sw.Name, "Clone of: Merge input workflow") {
				continue
			}
			for _, n := range sw.Nodes {
				if n.ActionType == "action" && n.MergeInput {
					kept = true
				}
			}
		}
		if !kept {
			t.Error("merge_input flag lost on clone")
		}
	})

	t.Run("ingest applies merge_input server-side", func(t *testing.T) {
		if _, err := wm.ImportWorkflowJSON(mergeWorkflowJSON); err != nil {
			t.Fatalf("import merge workflow: %v", err)
		}
		triggerRes, _, err := wm.IngestResult(common.ResultData{
			ActionID:   "trigger_01mergeinputtest000001",
			ActionType: "trigger",
			ActionName: "filechange",
			Payload:    map[string]any{"Name": "Sergio", "LastName": "Who"},
		})
		if err != nil {
			t.Fatalf("ingest trigger: %v", err)
		}
		if triggerRes.ExecutionID == "" {
			t.Fatal("expected execution id to be minted for trigger")
		}

		// Reported by a client that did NOT merge: server re-applies it.
		stored, _, err := wm.IngestResult(common.ResultData{
			ExecutionID: triggerRes.ExecutionID,
			ActionID:    "action_01mergeinputtest0000001",
			ActionType:  "action",
			ActionName:  "file",
			Payload:     map[string]any{"Name": "Dr", "Phone": "555-768790"},
		})
		if err != nil {
			t.Fatalf("ingest action: %v", err)
		}
		merged, ok := stored.Payload.(map[string]any)
		if !ok {
			t.Fatalf("expected map payload, got %T", stored.Payload)
		}
		if merged["Name"] != "Dr" || merged["LastName"] != "Who" || merged["Phone"] != "555-768790" {
			t.Fatalf("unexpected merged payload: %v", merged)
		}
	})
}
