package database

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mcmx/nitejaguar/common"
)

func TestSaveResultPersistsRouting(t *testing.T) {
	t.Setenv("DB_URL", "file:test_save_result?mode=memory&cache=shared&_fk=1")
	db, err := New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}

	input := common.ResultData{
		ResultID:         "result_01saveroutingtest00001",
		WorkflowID:       "workflow_01saveroutingtest01",
		ExecutionID:      "execution_01saveroutingtest1",
		ActionID:         "trigger_01saveroutingtest01",
		ActionType:       "trigger",
		ActionName:       "filechange",
		ExecutorID:       "server",
		Payload:          map[string]any{"file": "statement-jan.pdf"},
		ConditionResults: map[string]bool{"entry_pdf": true, "entry_other": false},
		Nexts:            []string{"action_01saveroutingtest01"},
		CreatedAt:        time.Now(),
	}
	saved, err := db.SaveResult(input)
	if err != nil {
		t.Fatalf("save result: %v", err)
	}
	if saved.ID != input.ResultID {
		t.Fatalf("saved row id = %q, want %q", saved.ID, input.ResultID)
	}

	// Re-saving the same result_id is idempotent (replayed results).
	again, err := db.SaveResult(input)
	if err != nil {
		t.Fatalf("re-save result: %v", err)
	}
	if again.ID != saved.ID {
		t.Fatalf("idempotent re-save returned %q, want %q", again.ID, saved.ID)
	}
	rows, err := db.ListResults("workflow_01saveroutingtest01", "", 0)
	if err != nil {
		t.Fatalf("list results: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row after idempotent re-save, got %d", len(rows))
	}

	got, err := db.GetResult(input.ResultID)
	if err != nil {
		t.Fatalf("get result: %v", err)
	}
	if got.WorkflowID != input.WorkflowID || got.ExecutionID != input.ExecutionID ||
		got.ActionID != input.ActionID || got.ExecutorID != input.ExecutorID {
		t.Fatalf("unexpected row identity: %+v", got)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(got.PayloadJSON), &payload); err != nil {
		t.Fatalf("payload json invalid: %v", err)
	}
	if payload["file"] != "statement-jan.pdf" {
		t.Fatalf("unexpected payload: %v", payload)
	}
	var outcomes map[string]bool
	if err := json.Unmarshal([]byte(got.ConditionResultsJSON), &outcomes); err != nil {
		t.Fatalf("condition results json invalid: %v", err)
	}
	if !outcomes["entry_pdf"] || outcomes["entry_other"] {
		t.Fatalf("unexpected condition outcomes: %v", outcomes)
	}
	if len(got.Nexts) != 1 || got.Nexts[0] != "action_01saveroutingtest01" {
		t.Fatalf("unexpected nexts: %v", got.Nexts)
	}

	// Execution-scoped listing filters.
	filtered, err := db.ListResults("workflow_01saveroutingtest01", "execution_doesnotexist", 0)
	if err != nil {
		t.Fatalf("filtered list: %v", err)
	}
	if len(filtered) != 0 {
		t.Fatalf("expected 0 rows for unknown execution, got %d", len(filtered))
	}
}
