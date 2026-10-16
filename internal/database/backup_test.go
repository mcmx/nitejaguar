package database

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mcmx/nitejaguar/common"
)

// seedFailoverData writes one workflow, one pending assignment, and one
// result — the rows a failover must not lose.
func seedFailoverData(t *testing.T, db Service) {
	t.Helper()
	if err := db.SaveWorkflow("workflow_01failovertest01", `{"id":"workflow_01failovertest01"}`, "default"); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	if _, err := db.EnqueueNodeAssignment("default", "workflow_01failovertest01", "exec_failover_01", "action_01failovertest01", "", map[string]any{"file": "a.pdf"}); err != nil {
		t.Fatalf("enqueue assignment: %v", err)
	}
	_, err := db.SaveResult(common.ResultData{
		ResultID:    "result_01failovertest00001",
		WorkflowID:  "workflow_01failovertest01",
		ExecutionID: "exec_failover_01",
		ActionID:    "trigger_01failovertest01",
		ActionType:  "trigger",
		ActionName:  "cron",
		ExecutorID:  "server",
		Payload:     map[string]any{"tick": true},
		CreatedAt:   time.Now(),
	})
	if err != nil {
		t.Fatalf("save result: %v", err)
	}
}

func checkFailoverData(t *testing.T, db Service) {
	t.Helper()
	if _, err := db.GetWorkflow("workflow_01failovertest01"); err != nil {
		t.Fatalf("workflow lost: %v", err)
	}
	pending, err := db.ListPendingAssignments()
	if err != nil {
		t.Fatalf("list assignments: %v", err)
	}
	found := false
	for _, a := range pending {
		if a.WorkflowID == "workflow_01failovertest01" && a.ExecutionID == "exec_failover_01" {
			found = true
		}
	}
	if !found {
		t.Fatalf("pending assignment lost across restart (%d rows)", len(pending))
	}
	if _, err := db.GetResult("result_01failovertest00001"); err != nil {
		t.Fatalf("result lost: %v", err)
	}
}

// TestBackupSnapshotRoundTrip proves `backup` snapshots a live file DB and
// the snapshot is a usable database with the same rows.
func TestBackupSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DB_URL", filepath.Join(dir, "nite.db"))
	old := dbInstance
	dbInstance = nil
	t.Cleanup(func() {
		if dbInstance != nil {
			_ = dbInstance.Close()
		}
		dbInstance = old
	})

	db, err := New()
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	seedFailoverData(t, db)
	if db.DatabaseFilePath() == "" {
		t.Fatalf("expected file-backed database")
	}

	snap := filepath.Join(dir, "snapshots", "nite-snap.db")
	if err := db.Backup(snap); err != nil {
		t.Fatalf("backup: %v", err)
	}
	n, err := VerifyBackup(snap)
	if err != nil {
		t.Fatalf("verify backup: %v", err)
	}
	if n != 1 {
		t.Fatalf("backup workflows = %d, want 1", n)
	}

	// The snapshot stands alone: rows readable without the source.
	drv, raw, err := openTursoEnt(snap)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer func() { _ = raw.Close() }()
	_ = drv
	var execID string
	if err := raw.QueryRow("SELECT execution_id FROM node_assignments").Scan(&execID); err != nil {
		t.Fatalf("snapshot assignments unreadable: %v", err)
	}
	if execID != "exec_failover_01" {
		t.Fatalf("snapshot execution_id = %q, want exec_failover_01", execID)
	}

	// Backing up twice replaces the previous snapshot (idempotent CLI).
	if err := db.Backup(snap); err != nil {
		t.Fatalf("second backup: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// TestBackupRefusesMemory proves in-memory databases fail closed instead
// of writing an empty snapshot.
func TestBackupRefusesMemory(t *testing.T) {
	t.Setenv("DB_URL", "file:test_backup_memory?mode=memory&cache=shared&_fk=1")
	old := dbInstance
	dbInstance = nil
	t.Cleanup(func() {
		if dbInstance != nil {
			_ = dbInstance.Close()
		}
		dbInstance = old
	})
	db, err := New()
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Backup(filepath.Join(t.TempDir(), "snap.db")); err == nil {
		t.Fatalf("expected backup of in-memory DB to fail")
	}
}

// TestFailoverReopenSurvivesRestart proves the failover core: close the
// server's DB handle, reopen the same file, and every pending assignment,
// result, and workflow definition is still there for clients to repoll.
func TestFailoverReopenSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DB_URL", filepath.Join(dir, "nite.db"))
	old := dbInstance
	dbInstance = nil
	t.Cleanup(func() {
		if dbInstance != nil {
			_ = dbInstance.Close()
		}
		dbInstance = old
	})

	db, err := New()
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	seedFailoverData(t, db)

	reopened, err := FailoverReopen(db)
	if err != nil {
		t.Fatalf("failover reopen: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	checkFailoverData(t, reopened)
}
