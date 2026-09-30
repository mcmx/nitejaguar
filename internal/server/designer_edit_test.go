package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mcmx/nitejaguar/cmd/web"
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

// TestDesignerInitPreservesConditions guards the conditions round-trip:
// the designer loader must surface every conditions entry (not a
// flattened nexts list) so the inspector can edit them and save them
// back verbatim.
func TestDesignerInitPreservesConditions(t *testing.T) {
	var def workflow.Workflow
	if err := json.Unmarshal([]byte(`{
		"id": "workflow_condtest",
		"name": "cond test",
		"nodes": {
			"trigger_1": {
				"id": "trigger_1", "name": "t", "action_type": "trigger", "action_name": "filechange",
				"arguments": {"path": "/tmp"},
				"conditions": {"entries": {
					"pdf_file": {"condition": {"leftOperand": "$result.file", "operator": "=~", "rightOperand": ".*\\.pdf$"}, "nexts": ["action_1"]},
					"always": {"condition": {"leftOperand": true, "operator": "", "rightOperand": null}, "nexts": ["action_2"]}
				}},
				"dependencies": []
			},
			"action_1": {"id": "action_1", "name": "a1", "action_type": "action", "action_name": "file", "arguments": {}, "conditions": {"entries": {}}, "dependencies": ["trigger_1"]},
			"action_2": {"id": "action_2", "name": "a2", "action_type": "action", "action_name": "file", "arguments": {}, "conditions": {"entries": {}}, "dependencies": ["trigger_1"]}
		}
	}`), &def); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	init := web.DesignerInitFromWorkflow(def)
	if len(init.Nodes) != 3 {
		t.Fatalf("nodes = %d, want 3", len(init.Nodes))
	}
	var trig *web.DesignerNodeInit
	for i := range init.Nodes {
		if init.Nodes[i].ID == "trigger_1" {
			trig = &init.Nodes[i]
		}
	}
	if trig == nil {
		t.Fatalf("trigger_1 missing from init")
	}
	if len(trig.Conditions) != 2 {
		t.Fatalf("trigger conditions = %+v, want 2 entries", trig.Conditions)
	}
	// Sorted by entry id: always, pdf_file.
	if trig.Conditions[0].ID != "always" || trig.Conditions[1].ID != "pdf_file" {
		t.Fatalf("condition ids = %q, %q, want always, pdf_file", trig.Conditions[0].ID, trig.Conditions[1].ID)
	}
	if trig.Conditions[1].Left != "$result.file" || trig.Conditions[1].Operator != "=~" || trig.Conditions[1].Right != ".*\\.pdf$" {
		t.Fatalf("pdf_file condition = %+v", trig.Conditions[1])
	}
	if trig.Conditions[1].Nexts != "action_1" || trig.Conditions[0].Nexts != "action_2" {
		t.Fatalf("condition nexts = %q, %q", trig.Conditions[0].Nexts, trig.Conditions[1].Nexts)
	}
	// The embedded designer JSON must carry the entries to the browser.
	raw := web.DesignerInitJSON(init)
	if !strings.Contains(raw, "pdf_file") || !strings.Contains(raw, "$result.file") {
		t.Errorf("designer init JSON drops conditions: %s", raw)
	}
}

// TestDesignerGraphClientLogic guards the visual-designer
// regressions: drag listeners that never detached (cards stuck to the
// cursor), edges rendered via <template x-for> inside <svg> (template
// content parses in the HTML namespace, so <path> never paints), and the
// root "+ Add node" picker that never opened (pickerFor=null collided
// with the closed state).
func TestDesignerGraphClientLogic(t *testing.T) {
	var buf bytes.Buffer
	if err := web.DesignerPage(&web.DesignerPageData{InitJSON: "{}"}).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render designer: %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		`x-html="edgeSVG()"`,
		"pointercancel",
		"removeEventListener('pointermove', move)",
		"url(#nj-arrow)",
		"Add action",
		"Add node",
		"designer-initial",
		"addCondition",
		"moveChild",
		"conditionLabel",
		"#16a34a",
		"→+",
		"pickerOpen",
		"startPan",
		"panX",
		"nj-client-list",
		"nj-default-client-list",
		"cannot have parents",
		"Type and action are fixed after creation",
		"h-[720px]",
		"designerTypeidSuffix",
		"abcdefghjkmnpqrstvwxyz",
		"webhookPath",
		"webhookOwnerNote",
		"clientHookById",
		"Webhook endpoint",
		"/webhook/",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("designer page missing %q", want)
		}
	}
	if strings.Contains(body, `x-for="e in edgePaths()"`) {
		t.Errorf("designer still uses <template x-for> for svg edges (paths would not paint)")
	}
	if strings.Contains(body, `stroke="hsl(0 72.2% 50.6%)"`) {
		t.Errorf("designer edges still use the old red stroke (want green #16a34a)")
	}
	if strings.Contains(body, "Save & Import Workflow") {
		t.Errorf("designer still renders the bottom save button (should only save from the top bar)")
	}
	if strings.Contains(body, `x-model="selected().action_type"`) || strings.Contains(body, `x-model="selected().action_name"`) {
		t.Errorf("designer inspector still allows changing type/action (must be read-only)")
	}
}
