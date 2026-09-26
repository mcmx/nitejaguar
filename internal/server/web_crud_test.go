package server

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/labstack/echo/v4"
	"github.com/mcmx/nitejaguar/cmd/web/modules"
	"github.com/mcmx/nitejaguar/internal/database"
	"github.com/mcmx/nitejaguar/internal/workflow"
)

func TestChangePasswordAPI(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	wm := workflow.NewWorkflowManager(false, db)
	s := &Server{db: db, wm: wm}
	_, api := humatest.New(t)
	addApiRoutes(api, s)

	tenant := fmt.Sprintf("pwdchange%d", testAdminSeq.Add(1))
	row, err := db.CreateUser(tenant, "bob", "password-12345", database.RoleOperator, nil)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	_, plaintext, err := db.CreateSession(row.ID, 0)
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	auth := "Authorization: Bearer " + plaintext

	// Wrong current password -> 401.
	resp := api.Post("/api/auth/password", auth, map[string]any{
		"current_password": "nope-wrong", "new_password": "password-67890",
	})
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("wrong current status = %v, want 401", resp.Code)
	}

	// Short new password -> 400.
	resp = api.Post("/api/auth/password", auth, map[string]any{
		"current_password": "password-12345", "new_password": "short",
	})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("short password status = %v, want 400", resp.Code)
	}

	// Happy path.
	resp = api.Post("/api/auth/password", auth, map[string]any{
		"current_password": "password-12345", "new_password": "password-67890",
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("change status = %v, body = %s", resp.Code, resp.Body.String())
	}

	// Old password no longer works; new one does.
	resp = api.Post("/api/auth/login", map[string]any{
		"username": "bob", "password": "password-12345", "tenant_id": tenant,
	})
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("old password login status = %v, want 401", resp.Code)
	}
	resp = api.Post("/api/auth/login", map[string]any{
		"username": "bob", "password": "password-67890", "tenant_id": tenant,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("new password login status = %v, body = %s", resp.Code, resp.Body.String())
	}
}

func TestAdminResetOtherPasswordAPI(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	wm := workflow.NewWorkflowManager(false, db)
	s := &Server{db: db, wm: wm}
	_, api := humatest.New(t)
	addApiRoutes(api, s)

	tenant := fmt.Sprintf("pwdreset%d", testAdminSeq.Add(1))
	adminAuth := ensureRoleToken(t, db, tenant, database.RoleAdmin)
	operatorAuth := ensureRoleToken(t, db, tenant, database.RoleOperator)
	victim, err := db.CreateUser(tenant, "victim", "password-12345", database.RoleViewer, nil)
	if err != nil {
		t.Fatalf("seed victim: %v", err)
	}

	// Operator cannot reset someone else's password.
	resp := api.Post("/api/auth/password", operatorAuth, map[string]any{
		"user_id": victim.ID, "new_password": "password-99999",
	})
	if resp.Code != http.StatusForbidden {
		t.Fatalf("operator reset status = %v, want 403", resp.Code)
	}

	// Admin can (no current password needed).
	resp = api.Post("/api/auth/password", adminAuth, map[string]any{
		"user_id": victim.ID, "new_password": "password-99999",
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("admin reset status = %v, body = %s", resp.Code, resp.Body.String())
	}
	resp = api.Post("/api/auth/login", map[string]any{
		"username": "victim", "password": "password-99999", "tenant_id": tenant,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("victim login after reset status = %v, body = %s", resp.Code, resp.Body.String())
	}
}

func TestDeleteWorkflowAPI(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	wm := workflow.NewWorkflowManager(false, db)
	s := &Server{db: db, wm: wm}
	_, api := humatest.New(t)
	addApiRoutes(api, s)

	tenant := fmt.Sprintf("wfdelete%d", testAdminSeq.Add(1))
	adminAuth := ensureRoleToken(t, db, tenant, database.RoleAdmin)
	viewerAuth := ensureRoleToken(t, db, tenant, database.RoleViewer)
	wfBody := map[string]any{
		"id": "workflow_01kwfdelete00000001", "name": "Delete me", "tenant_id": tenant,
		"nodes": map[string]any{},
	}
	resp := api.Post("/api/workflows/import", adminAuth, wfBody)
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("import status = %v, body = %s", resp.Code, resp.Body.String())
	}

	// Viewer cannot delete.
	resp = api.Delete("/api/workflows/workflow_01kwfdelete00000001", viewerAuth)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("viewer delete status = %v, want 403", resp.Code)
	}

	// Admin can; afterwards the workflow is gone.
	resp = api.Delete("/api/workflows/workflow_01kwfdelete00000001", adminAuth)
	if resp.Code != http.StatusOK {
		t.Fatalf("admin delete status = %v, body = %s", resp.Code, resp.Body.String())
	}
	resp = api.Get("/api/workflows/workflow_01kwfdelete00000001")
	if resp.Code != http.StatusNotFound {
		t.Fatalf("get after delete status = %v, want 404", resp.Code)
	}

	// Deleting again is 404.
	resp = api.Delete("/api/workflows/workflow_01kwfdelete00000001", adminAuth)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("second delete status = %v, want 404", resp.Code)
	}
}

func TestDatabasePasswordAndWorkflowDelete(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	tenant := fmt.Sprintf("dbpwd%d", testAdminSeq.Add(1))
	row, err := db.CreateUser(tenant, "carol", "password-12345", database.RoleViewer, nil)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := db.ChangePassword(row.ID, "wrong", "password-67890"); err == nil {
		t.Fatalf("ChangePassword with wrong current succeeded, want error")
	}
	if err := db.ChangePassword(row.ID, "password-12345", "short"); err == nil {
		t.Fatalf("ChangePassword with short password succeeded, want error")
	}
	if err := db.ChangePassword(row.ID, "password-12345", "password-67890"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if _, err := db.VerifyUser(tenant, "carol", "password-67890"); err != nil {
		t.Fatalf("verify after change: %v", err)
	}
	if err := db.UpdatePassword("user_doesnotexist", "password-11111"); err == nil {
		t.Fatalf("UpdatePassword on missing user succeeded, want error")
	}
	if err := db.DeleteWorkflow("workflow_doesnotexist"); err == nil {
		t.Fatalf("DeleteWorkflow on missing workflow succeeded, want error")
	}
}

func TestNavbarGating(t *testing.T) {
	var anon bytes.Buffer
	if err := modules.Navbar(nil).Render(context.Background(), &anon); err != nil {
		t.Fatalf("render anonymous navbar: %v", err)
	}
	out := anon.String()
	if !strings.Contains(out, "/login") {
		t.Fatalf("anonymous navbar missing Login link: %s", out)
	}
	for _, hidden := range []string{"Workflows", "Designer", "Clients", "Credentials", "Audit", "Users", "/logout"} {
		if strings.Contains(out, hidden) {
			t.Fatalf("anonymous navbar leaks %q: %s", hidden, out)
		}
	}
	var signed bytes.Buffer
	user := &modules.NavUser{ID: "user_1", Username: "alice", TenantID: "default", Role: "operator", LoggedIn: true}
	if err := modules.Navbar(user).Render(context.Background(), &signed); err != nil {
		t.Fatalf("render signed-in navbar: %v", err)
	}
	sout := signed.String()
	for _, want := range []string{"Workflows", "Designer", "Clients", "Credentials", "Audit", "Users", "alice", "/logout", "/profile"} {
		if !strings.Contains(sout, want) {
			t.Fatalf("signed-in navbar missing %q: %s", want, sout)
		}
	}
	if strings.Contains(sout, "/login") {
		t.Fatalf("signed-in navbar shows Login link: %s", sout)
	}
}

func TestRequireWebLoginRedirect(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	if _, err := db.CreateUser(fmt.Sprintf("navgate%d", testAdminSeq.Add(1)), "gatekeeper", "password-12345", database.RoleViewer, nil); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	s := &Server{db: db, wm: workflow.NewWorkflowManager(false, db)}
	e := echo.New()
	ok := func(c echo.Context) error { return c.String(http.StatusOK, "ok") }

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	if err := s.requireWebLogin(ok)(c); err != nil {
		t.Fatalf("middleware error: %v", err)
	}
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("anonymous status = %v, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("anonymous redirect = %q, want /login", loc)
	}
}

func TestEnrollmentCommand(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/enrollment/tokens", nil)
	req.Host = "example.com:8080"
	c := e.NewContext(req, httptest.NewRecorder())
	got := enrollmentCommand(c, "edge-worker-1", "TOKEN123")
	want := "nitejaguar client --server http://example.com:8080 --name edge-worker-1 --enrollment-token TOKEN123"
	if got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
	got = enrollmentCommand(c, "my worker", "TOKEN123")
	if !strings.Contains(got, `--name "my worker"`) {
		t.Fatalf("command with spaces not quoted: %q", got)
	}
}
