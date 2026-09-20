package workflow

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/mcmx/nitejaguar/internal/database"
)

const (
	workflow1Path = "../../examples/workflow1.json"
	workflow2Path = "../../examples/workflow2.json"

	workflow1ID   = "workflow_01jq9c43qhejts98g7n1krqpgd"
	workflow1Name = "First Workflow from json"
	workflow1Node = "trigger_01jq9c43qhejtacw2p66k2ke5s"
)

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

func TestWorkflowImportAndClone(t *testing.T) {
	// internal/database caches its connection in a package singleton, so all
	// subtests must share a single in-memory database.
	t.Setenv("DB_URL", "file:test_import_clone?mode=memory&cache=shared&_fk=1")
	db := database.New()
	wmmInstance = nil
	wm := NewWorkflowManager(false, db)
	t.Cleanup(func() {
		wmmInstance = nil
		_ = db.Close()
	})

	t.Run("import keeps ids and name verbatim", func(t *testing.T) {
		raw := readWorkflow(t, workflow1Path)
		if err := wm.ImportWorkflowJSON(raw); err != nil {
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
		if err := wm.ImportWorkflowJSON(raw); err != nil {
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

		if err := wm.CloneWorkflowJSON(raw); err != nil {
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
		if err := wm.CloneWorkflowJSON(raw); err != nil {
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
}