package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mcmx/nitejaguar/internal/database"
	"github.com/mcmx/nitejaguar/internal/workflow"
)

func extractDesignerInitial(t *testing.T, body string) map[string]any {
	t.Helper()
	startTag := `<script id="designer-initial" type="application/json">`
	start := strings.Index(body, startTag)
	if start == -1 {
		t.Fatalf("designer-initial script tag not found")
	}
	rest := body[start+len(startTag):]
	end := strings.Index(rest, "</script>")
	if end == -1 {
		t.Fatalf("designer-initial closing tag not found")
	}
	raw := strings.TrimSpace(rest[:end])
	var init map[string]any
	if err := json.Unmarshal([]byte(raw), &init); err != nil {
		t.Fatalf("designer-initial JSON invalid: %v\nraw:\n%s", err, raw)
	}
	return init
}

// TestDesignerEditShowsRequestedWorkflow guards against a regression where
// the designer always rendered the hardcoded sample nodes: the templ
// compiler treats <script> contents as raw text, so an expression written
// inside a <script> block is never evaluated. The embedded initial JSON
// must carry the requested workflow's data.
func TestDesignerEditShowsRequestedWorkflow(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	// Seed the shared assignment workflow (same ID as the other tests, so
	// the upsert is idempotent and nothing leaks between tests).
	wm := workflow.NewWorkflowManager(false, db)
	if _, err := wm.ImportWorkflowJSON(assignmentFlowWorkflow); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	s := &Server{db: db, wm: wm}
	h := s.RegisterRoutes()

	// The server bootstraps a default admin on New(), so browser pages
	// require a session cookie. Mint a unique test admin session.
	designerAdmin, err := db.CreateUser("default", "designer-test-admin", "password-12345", "admin", nil)
	if err != nil {
		// Shared-DB runs may already hold this username; use a unique one.
		designerAdmin, err = db.CreateUser("default", fmt.Sprintf("designer-test-admin-%d", testAdminSeq.Add(1)), "password-12345", "admin", nil)
		if err != nil {
			t.Fatalf("create designer test admin: %v", err)
		}
	}
	_, sessionPlain, err := db.CreateSession(designerAdmin.ID, 0)
	if err != nil {
		t.Fatalf("create designer test session: %v", err)
	}

	for _, target := range []string{
		"/designer/workflow_01kassignmentsync1test0001",
		"/designer?workflow_id=workflow_01kassignmentsync1test0001",
	} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionPlain})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %v", target, rec.Code)
		}
		init := extractDesignerInitial(t, rec.Body.String())
		if init["workflowId"] != "workflow_01kassignmentsync1test0001" {
			t.Errorf("GET %s: workflowId = %v", target, init["workflowId"])
		}
		if init["name"] != "Assignment flow workflow" {
			t.Errorf("GET %s: name = %v", target, init["name"])
		}
		nodes, ok := init["nodes"].([]any)
		if !ok || len(nodes) != 2 {
			t.Fatalf("GET %s: nodes = %v, want 2 nodes", target, init["nodes"])
		}
		raw, _ := json.Marshal(nodes)
		if !strings.Contains(string(raw), "action_01kassignmentgpufilter0001") {
			t.Errorf("GET %s: nodes missing gpu action: %s", target, raw)
		}
		if !strings.Contains(string(raw), "/tmp/gpu-was-here.txt") {
			t.Errorf("GET %s: nodes missing workflow arguments: %s", target, raw)
		}
		if !strings.Contains(rec.Body.String(), "Edit Workflow") {
			t.Errorf("GET %s: missing edit-mode title", target)
		}
	}
}
