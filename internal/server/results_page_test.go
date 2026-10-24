package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/database"
	"github.com/mcmx/nitejaguar/internal/workflow"
)

// TestResultsPageWorkflowFilter guards the ?workflow= filter the designer's
// "View executions" link uses: only the requested workflow's results may be
// listed, and without the param the page still shows everything.
func TestResultsPageWorkflowFilter(t *testing.T) {
	t.Setenv("DB_URL", "file:resultsfilter.db?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	now := time.Now()
	for _, r := range []common.ResultData{
		{ResultID: "result_filtertesta00000001", WorkflowID: "workflow_filtera000000001", ExecutionID: "exec_filter1", ActionID: "action_filtera0000000001", ActionName: "file", CreatedAt: now},
		{ResultID: "result_filtertestb00000001", WorkflowID: "workflow_filterb000000001", ExecutionID: "exec_filter2", ActionID: "action_filterb0000000001", ActionName: "file", CreatedAt: now.Add(time.Second)},
	} {
		if _, err := db.SaveResult(r); err != nil {
			t.Fatalf("seed result %s: %v", r.ResultID, err)
		}
	}
	s := &Server{db: db, wm: workflow.NewWorkflowManager(false, db)}
	h := s.RegisterRoutes()

	admin, err := db.CreateUser("default", fmt.Sprintf("results-filter-%d", testAdminSeq.Add(1)), "password-12345", "admin", nil)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	_, sessionPlain, err := db.CreateSession(admin.ID, 0)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	get := func(target string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionPlain})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %v", target, rec.Code)
		}
		return rec.Body.String()
	}

	// Unfiltered: both workflows present.
	body := get("/results")
	if !strings.Contains(body, "workflow_filtera000000001") || !strings.Contains(body, "workflow_filterb000000001") {
		t.Fatalf("unfiltered /results missing seeded results")
	}

	// Filtered: only workflow A, with the filter banner + clear link.
	body = get("/results?workflow=workflow_filtera000000001")
	if !strings.Contains(body, "workflow_filtera000000001") {
		t.Errorf("filtered /results drops the requested workflow")
	}
	if strings.Contains(body, "workflow_filterb000000001") {
		t.Errorf("filtered /results leaks another workflow's results")
	}
	if !strings.Contains(body, "Filtered to workflow") || !strings.Contains(body, `href="/results"`) {
		t.Errorf("filtered /results missing filter banner / clear link")
	}

	// Unknown workflow: empty state, not an error.
	body = get("/results?workflow=workflow_nosuch0000000001")
	if !strings.Contains(body, "No results recorded for this workflow yet.") {
		t.Errorf("empty filtered /results missing filtered empty-state message")
	}
}
