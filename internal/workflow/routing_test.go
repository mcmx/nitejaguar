package workflow

import (
	"testing"

	"github.com/mcmx/nitejaguar/common"
)

const routingResultsWorkflowJSON = `{
  "id": "workflow_01routingresultstest01",
  "name": "Routing results workflow",
  "nodes": {
    "trigger_01routingresultstest01": {
      "id": "trigger_01routingresultstest01",
      "name": "Trigger",
      "action_type": "trigger",
      "action_name": "filechange",
      "arguments": {"path": "/tmp"},
      "conditions": {"entries": {
        "entry_pdf": {
          "condition": {"leftOperand": "$result.file", "operator": "glob", "rightOperand": "statement-*.pdf"},
          "nexts": ["action_01routingresultstest01"]
        },
        "entry_other": {
          "condition": {"leftOperand": false, "operator": "", "rightOperand": null},
          "nexts": ["action_01routingresultsskipped1"]
        }
      }},
      "dependencies": null
    },
    "action_01routingresultstest01": {
      "id": "action_01routingresultstest01",
      "name": "PDF action",
      "action_type": "action",
      "action_name": "file",
      "arguments": {"action": "create", "file": "/tmp/out.txt"},
      "conditions": {"entries": {}},
      "dependencies": ["trigger_01routingresultstest01"]
    },
    "action_01routingresultsskipped1": {
      "id": "action_01routingresultsskipped1",
      "name": "Skipped action",
      "action_type": "action",
      "action_name": "file",
      "arguments": {"action": "create", "file": "/tmp/skipped.txt"},
      "conditions": {"entries": {}},
      "dependencies": ["trigger_01routingresultstest01"]
    }
  }
}`

func TestEvaluateRoutingRecordsPerEntry(t *testing.T) {
	n := Node{
		Id:         "trigger_01routingresultstest01",
		ActionType: "trigger",
		ActionName: "filechange",
		Conditions: newConditionDictionary(),
	}
	n.Conditions.addEntry("entry_pdf",
		newComparison("$result.file", "glob", "statement-*.pdf"),
		[]string{"action_01routingresultstest01"})
	n.Conditions.addEntry("entry_other",
		newBooleanCondition(false),
		[]string{"action_01routingresultsskipped1"})

	decision := n.EvaluateRouting(nil, common.ResultData{
		Payload: map[string]any{"file": "statement-jan.pdf"},
	})
	if !decision.Results["entry_pdf"] {
		t.Error("expected entry_pdf to match statement-jan.pdf")
	}
	if decision.Results["entry_other"] {
		t.Error("expected entry_other (false) not to match")
	}
	if len(decision.Nexts) != 1 || decision.Nexts[0] != "action_01routingresultstest01" {
		t.Fatalf("expected nexts [action_01routingresultstest01], got %v", decision.Nexts)
	}
	if decision.ConditionError != "" {
		t.Fatalf("expected no condition error, got %q", decision.ConditionError)
	}

	// Non-matching payload: nothing routes, but the per-entry outcomes
	// are still recorded.
	decision = n.EvaluateRouting(nil, common.ResultData{
		Payload: map[string]any{"file": "notes.txt"},
	})
	if decision.Results["entry_pdf"] {
		t.Error("expected entry_pdf not to match notes.txt")
	}
	if len(decision.Nexts) != 0 {
		t.Fatalf("expected no nexts for notes.txt, got %v", decision.Nexts)
	}
}

func TestEvaluateRoutingRecordsError(t *testing.T) {
	n := Node{
		Id:         "n1",
		ActionType: "trigger",
		ActionName: "filechange",
		Conditions: newConditionDictionary(),
	}
	n.Conditions.addEntry("entry_bad",
		newComparison("a", "bogus-operator", "b"),
		[]string{"action_next"})
	n.Conditions.addEntry("entry_ok",
		newBooleanCondition(true),
		[]string{"action_next"})

	decision := n.EvaluateRouting(nil, common.ResultData{})
	if decision.Results["entry_bad"] {
		t.Error("expected failing entry to record false")
	}
	if !decision.Results["entry_ok"] {
		t.Error("expected healthy entry to still evaluate true")
	}
	if decision.ConditionError == "" {
		t.Error("expected the evaluation error to be recorded")
	}
	if len(decision.Nexts) != 1 {
		t.Fatalf("expected routing to continue with the healthy entry, got %v", decision.Nexts)
	}
}

// NOTE: the ingest-to-DB assertion lives as a subtest of
// TestWorkflowImportAndClone in import_test.go: internal/database caches
// its connection in a package singleton (closed by that test's cleanup),
// so a standalone DB test in this package would reuse a closed handle
// depending on test order. The shared workflow JSON above is reused there.
