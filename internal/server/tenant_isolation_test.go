package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/labstack/echo/v4"
	"github.com/mcmx/nitejaguar/internal/database"
	"github.com/mcmx/nitejaguar/internal/workflow"
)

// isoSetup seeds two isolated tenants (A and B) with an admin, operator,
// viewer, workflow (trigger + action), credential, client, and user each.
// It returns the tenant slugs and session header values.
type isoSetup struct {
	tenantA, tenantB       string
	adminA, operatorA      string
	adminB, operatorB      string
	viewerB                string
	workflowA, triggerA    string
	actionA                string
	credentialAID          string
	clientAID, clientATok  string
	clientBID, clientBTok  string
	userAID                string
}

func seedIsolation(t *testing.T, db database.Service, api humatest.TestAPI) *isoSetup {
	t.Helper()
	n := testAdminSeq.Add(1)
	s := &isoSetup{
		tenantA:  fmt.Sprintf("isoA%d", n),
		tenantB:  fmt.Sprintf("isoB%d", n),
		workflowA: fmt.Sprintf("workflow_01kisoA%06d0001", n),
		triggerA:  fmt.Sprintf("trigger_01kisoA%06d0001", n),
		actionA:   fmt.Sprintf("action_01kisoA%06d0001", n),
	}
	s.adminA = ensureRoleToken(t, db, s.tenantA, database.RoleAdmin)
	s.operatorA = ensureRoleToken(t, db, s.tenantA, database.RoleOperator)
	s.adminB = ensureRoleToken(t, db, s.tenantB, database.RoleAdmin)
	s.operatorB = ensureRoleToken(t, db, s.tenantB, database.RoleOperator)
	s.viewerB = ensureRoleToken(t, db, s.tenantB, database.RoleViewer)
	superAuth := ensureRoleToken(t, db, "default", database.RoleAdmin)

	// Tenant A workflow (imported by the superuser into tenant A).
	wfBody := map[string]any{
		"id": s.workflowA, "name": "Isolation A workflow", "tenant_id": s.tenantA,
		"nodes": map[string]any{
			s.triggerA: map[string]any{
				"id": s.triggerA, "name": "A trigger", "description": "A trigger",
				"action_type": "trigger", "action_name": "filechange",
				"arguments":    map[string]any{"path": "/tmp", "event_type": "create"},
				"conditions":   map[string]any{"entries": map[string]any{}},
				"dependencies": nil,
			},
			s.actionA: map[string]any{
				"id": s.actionA, "name": "A action", "description": "A action",
				"action_type": "action", "action_name": "file",
				"arguments":    map[string]any{"action": "create", "file": "/tmp/a.txt"},
				"conditions":   map[string]any{"entries": map[string]any{}},
				"dependencies": []string{s.triggerA},
			},
		},
	}
	resp := api.Post("/api/workflows/import", superAuth, wfBody)
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("seed workflow A status = %v, body = %s", resp.Code, resp.Body.String())
	}

	// Tenant A credential (created by the superuser into tenant A).
	resp = api.Post("/api/credentials", superAuth, map[string]any{
		"tenant_id": s.tenantA, "name": "iso-secret", "secret": "top-secret-A",
	})
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("seed credential A status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var cred struct {
		ID string `json:"id"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &cred)
	s.credentialAID = cred.ID

	// Tenant A user (created by tenant A's own admin).
	resp = api.Post("/api/users", s.adminA, map[string]any{
		"username": fmt.Sprintf("iso-user-%d", n), "password": "password-12345",
		"tenant_id": s.tenantA, "role": "viewer",
	})
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("seed user A status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var createdUser struct {
		ID string `json:"id"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &createdUser)
	s.userAID = createdUser.ID

	// Tenant A + B clients (tenant comes from the enrollment token).
	joinA := mintEnrollmentToken(t, db, s.tenantA, 0)
	resp = api.Post("/api/clients/register", map[string]any{"name": fmt.Sprintf("iso-client-a-%d", n), "enrollment_token": joinA})
	var regA struct {
		ClientID string `json:"client_id"`
		Token    string `json:"token"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &regA)
	s.clientAID, s.clientATok = regA.ClientID, regA.Token

	joinB := mintEnrollmentToken(t, db, s.tenantB, 0)
	resp = api.Post("/api/clients/register", map[string]any{"name": fmt.Sprintf("iso-client-b-%d", n), "enrollment_token": joinB})
	var regB struct {
		ClientID string `json:"client_id"`
		Token    string `json:"token"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &regB)
	s.clientBID, s.clientBTok = regB.ClientID, regB.Token

	return s
}

func TestAnonymousReadsDenied(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	wm := workflow.NewWorkflowManager(false, db)
	s := &Server{db: db, wm: wm}
	_, api := humatest.New(t)
	addApiRoutes(api, s)

	seed := seedIsolation(t, db, api)
	_ = seed
	_ = ensureRoleToken(t, db, "default", database.RoleAdmin) // close bootstrap

	anon := []struct {
		name string
		call func() *httptest.ResponseRecorder
	}{
		{"workflows", func() *httptest.ResponseRecorder { return api.Get("/api/workflows") }},
		{"workflow", func() *httptest.ResponseRecorder { return api.Get("/api/workflows/" + seed.workflowA) }},
		{"clients", func() *httptest.ResponseRecorder { return api.Get("/api/clients") }},
		{"credentials", func() *httptest.ResponseRecorder { return api.Get("/api/credentials") }},
		{"credential", func() *httptest.ResponseRecorder { return api.Get("/api/credentials/" + seed.credentialAID) }},
		{"users", func() *httptest.ResponseRecorder { return api.Get("/api/users") }},
		{"audit", func() *httptest.ResponseRecorder { return api.Get("/api/audit") }},
		{"tenants", func() *httptest.ResponseRecorder { return api.Get("/api/tenants") }},
	}
	for _, tc := range anon {
		if resp := tc.call(); resp.Code != http.StatusUnauthorized {
			t.Errorf("anonymous %s status = %v, want 401 (body %s)", tc.name, resp.Code, resp.Body.String())
		}
	}
}

func TestCrossTenantReadsDenied(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	wm := workflow.NewWorkflowManager(false, db)
	s := &Server{db: db, wm: wm}
	_, api := humatest.New(t)
	addApiRoutes(api, s)

	seed := seedIsolation(t, db, api)

	// Single-object reads from the foreign tenant hide existence (404).
	for _, target := range []string{
		"/api/workflows/" + seed.workflowA,
		"/api/credentials/" + seed.credentialAID,
	} {
		if resp := api.Get(target, seed.operatorB); resp.Code != http.StatusNotFound {
			t.Errorf("cross-tenant GET %s status = %v, want 404", target, resp.Code)
		}
		if resp := api.Get(target, seed.viewerB); resp.Code != http.StatusNotFound {
			t.Errorf("cross-tenant viewer GET %s status = %v, want 404", target, resp.Code)
		}
	}

	// Collection reads succeed but must not leak foreign rows.
	resp := api.Get("/api/workflows", seed.operatorB)
	if resp.Code != http.StatusOK {
		t.Fatalf("B workflows status = %v", resp.Code)
	}
	if strings.Contains(resp.Body.String(), seed.workflowA) {
		t.Errorf("tenant B workflow list leaks tenant A workflow: %s", resp.Body.String())
	}
	resp = api.Get("/api/clients", seed.operatorB)
	if resp.Code != http.StatusOK {
		t.Fatalf("B clients status = %v", resp.Code)
	}
	if strings.Contains(resp.Body.String(), seed.clientAID) {
		t.Errorf("tenant B client list leaks tenant A client: %s", resp.Body.String())
	}
	resp = api.Get("/api/credentials", seed.operatorB)
	if resp.Code != http.StatusOK {
		t.Fatalf("B credentials status = %v", resp.Code)
	}
	if strings.Contains(resp.Body.String(), seed.credentialAID) || strings.Contains(resp.Body.String(), "top-secret-A") {
		t.Errorf("tenant B credential list leaks tenant A credential: %s", resp.Body.String())
	}
	resp = api.Get("/api/users", seed.operatorB)
	if resp.Code != http.StatusOK {
		t.Fatalf("B users status = %v", resp.Code)
	}
	if strings.Contains(resp.Body.String(), seed.userAID) {
		t.Errorf("tenant B user list leaks tenant A user: %s", resp.Body.String())
	}
	resp = api.Get("/api/audit?limit=500", seed.operatorB)
	if resp.Code != http.StatusOK {
		t.Fatalf("B audit status = %v", resp.Code)
	}
	if strings.Contains(resp.Body.String(), seed.tenantA) {
		t.Errorf("tenant B audit leaks tenant A entries: %s", resp.Body.String())
	}

	// Client-plane reads: B cannot see A's assignments or transfers.
	resp = api.Get("/api/clients/"+seed.clientAID+"/assignments", "Authorization: Bearer "+seed.clientBTok)
	if resp.Code != http.StatusUnauthorized {
		t.Errorf("B assignments for A client status = %v, want 401", resp.Code)
	}

	// Credential fetch from another tenant: 404 (name and id forms).
	resp = api.Get("/api/credentials/iso-secret/fetch", "Authorization: Bearer "+seed.clientBTok)
	if resp.Code != http.StatusNotFound {
		t.Errorf("B fetch of A credential status = %v, want 404 (body %s)", resp.Code, resp.Body.String())
	}
}

func TestCrossTenantWritesDenied(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	wm := workflow.NewWorkflowManager(false, db)
	s := &Server{db: db, wm: wm}
	_, api := humatest.New(t)
	addApiRoutes(api, s)

	seed := seedIsolation(t, db, api)

	// Workflow id hijack: importing under A's id into B's tenant is 403.
	resp := api.Post("/api/workflows/import", seed.operatorB, map[string]any{
		"id": seed.workflowA, "name": "Hijack", "tenant_id": seed.tenantB,
		"nodes": map[string]any{},
	})
	if resp.Code != http.StatusForbidden {
		t.Errorf("workflow hijack status = %v, want 403 (body %s)", resp.Code, resp.Body.String())
	}
	if row, err := db.GetWorkflow(seed.workflowA); err != nil {
		t.Errorf("victim workflow missing after hijack attempt: %v", err)
	} else if !strings.Contains(row.JSONDefinition, "Isolation A workflow") {
		t.Errorf("victim workflow overwritten by hijack attempt: %s", row.JSONDefinition)
	}

	// Result spoofing: B clients cannot report A's actions (implicit,
	// explicit-workflow, or spoofed-tenant forms).
	bClient := "Authorization: Bearer " + seed.clientBTok
	for name, body := range map[string]any{
		"implicit": map[string]any{"action_id": seed.triggerA, "action_type": "trigger"},
		"explicit": map[string]any{"workflow_id": seed.workflowA, "action_id": seed.triggerA, "action_type": "trigger"},
		"spoofed":  map[string]any{"action_id": seed.triggerA, "action_type": "trigger", "tenant_id": seed.tenantA},
	} {
		resp = api.Post("/api/results", bClient, body)
		if resp.Code != http.StatusNotFound {
			t.Errorf("spoofed result (%s) status = %v, want 404 (body %s)", name, resp.Code, resp.Body.String())
		}
	}

	// The reporting client itself stays functional in its own tenant.
	resp = api.Post("/api/results", "Authorization: Bearer "+seed.clientATok, map[string]any{
		"action_id": seed.triggerA, "action_type": "trigger", "tenant_id": seed.tenantB,
	})
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Errorf("own-tenant result with spoofed tenant field status = %v, body = %s (spoof must be ignored, not honored)", resp.Code, resp.Body.String())
	}

	// Credential delete + cross-tenant create are 403 for B.
	resp = api.Do(http.MethodDelete, "/api/credentials/"+seed.credentialAID, seed.operatorB)
	if resp.Code != http.StatusForbidden {
		t.Errorf("cross-tenant credential delete status = %v, want 403", resp.Code)
	}
	resp = api.Post("/api/credentials", seed.operatorB, map[string]any{
		"tenant_id": seed.tenantA, "name": "sneaky", "secret": "x",
	})
	if resp.Code != http.StatusForbidden {
		t.Errorf("cross-tenant credential create status = %v, want 403", resp.Code)
	}

	// User revoke + password reset across tenants are 403 for B's admin.
	resp = api.Post("/api/users/"+seed.userAID+"/revoke", seed.adminB, map[string]any{})
	if resp.Code != http.StatusForbidden {
		t.Errorf("cross-tenant user revoke status = %v, want 403", resp.Code)
	}
	if row, err := db.GetUser(seed.userAID); err != nil || row.Revoked {
		t.Errorf("victim user revoked by cross-tenant call (err=%v)", err)
	}
	resp = api.Post("/api/auth/password", seed.adminB, map[string]any{
		"user_id": seed.userAID, "new_password": "password-99999",
	})
	if resp.Code != http.StatusForbidden {
		t.Errorf("cross-tenant password reset status = %v, want 403", resp.Code)
	}

	// Client revoke + enrollment mint across tenants are 403 for B.
	resp = api.Post("/api/clients/"+seed.clientAID+"/revoke", seed.operatorB, map[string]any{})
	if resp.Code != http.StatusForbidden {
		t.Errorf("cross-tenant client revoke status = %v, want 403", resp.Code)
	}
	resp = api.Post("/api/enrollment/tokens", seed.operatorB, map[string]any{"tenant_id": seed.tenantA})
	if resp.Code != http.StatusForbidden {
		t.Errorf("cross-tenant token mint status = %v, want 403", resp.Code)
	}

	// Workflow delete across tenants is 403 for B.
	resp = api.Delete("/api/workflows/"+seed.workflowA, seed.operatorB)
	if resp.Code != http.StatusForbidden {
		t.Errorf("cross-tenant workflow delete status = %v, want 403", resp.Code)
	}
}

// sessionCookieContext builds an echo context carrying a web session cookie.
func sessionCookieContext(t *testing.T, db database.Service, s *Server, method, target, sessionPlaintext string) (echo.Context, *httptest.ResponseRecorder) {
	t.Helper()
	e := echo.New()
	var req *http.Request
	if method == http.MethodPost {
		req = httptest.NewRequest(http.MethodPost, target, strings.NewReader("enabled=false"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(http.MethodGet, target, nil)
	}
	if sessionPlaintext != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionPlaintext})
	}
	rec := httptest.NewRecorder()
	return e.NewContext(req, rec), rec
}

func webSession(t *testing.T, db database.Service, authHeader string) string {
	t.Helper()
	const prefix = "Authorization: Bearer "
	if !strings.HasPrefix(authHeader, prefix) {
		t.Fatalf("bad auth header")
	}
	return strings.TrimPrefix(authHeader, prefix)
}

func TestWebCrossTenantDenials(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	wm := workflow.NewWorkflowManager(false, db)
	s := &Server{db: db, wm: wm}
	_, api := humatest.New(t)
	addApiRoutes(api, s)

	seed := seedIsolation(t, db, api)
	_ = seed.operatorA

	// Web user revoke across tenants is refused and the victim survives.
	c, rec := sessionCookieContext(t, db, s, http.MethodPost, "/", webSession(t, db, seed.adminB))
	c.SetParamNames("id")
	c.SetParamValues(seed.userAID)
	if err := s.revokeUserWeb(c); err != nil {
		t.Fatalf("revokeUserWeb error = %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("cross-tenant web revoke status = %v, want 200 with inline error", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "superuser") {
		t.Errorf("cross-tenant web revoke missing superuser error: %s", rec.Body.String())
	}
	if row, err := db.GetUser(seed.userAID); err != nil || row.Revoked {
		t.Errorf("victim user revoked via web cross-tenant call (err=%v)", err)
	}

	// Web workflow enable across tenants is 403 and the flag is unchanged.
	c2, rec2 := sessionCookieContext(t, db, s, http.MethodPost, "/", webSession(t, db, seed.operatorB))
	c2.SetParamNames("id")
	c2.SetParamValues(seed.workflowA)
	if err := s.setWorkflowEnabled(c2); err != nil {
		t.Fatalf("setWorkflowEnabled error = %v", err)
	}
	if rec2.Code != http.StatusForbidden {
		t.Errorf("cross-tenant web enable status = %v, want 403", rec2.Code)
	}
	if row, err := db.GetWorkflow(seed.workflowA); err != nil || !row.Enabled {
		t.Errorf("victim workflow toggled via web cross-tenant call (err=%v)", err)
	}

	// Trigger stop requires operator+: viewers are refused.
	c3, _ := sessionCookieContext(t, db, s, http.MethodPost, "/", webSession(t, db, seed.viewerB))
	if err := c3.Request().ParseForm(); err != nil {
		t.Fatalf("parse form: %v", err)
	}
	c3.Request().PostForm.Set("id", seed.triggerA)
	if err := s.TriggerWebHandler(c3); err == nil {
		t.Errorf("viewer TriggerWebHandler succeeded, want 403")
	} else if he, ok := err.(*echo.HTTPError); !ok || he.Code != http.StatusForbidden {
		t.Errorf("viewer TriggerWebHandler err = %v, want 403", err)
	}

	// Trigger ownership: the trigger belongs to tenant A only.
	ownedA, err := s.triggerOwnedByTenant(seed.triggerA, seed.triggerA, seed.tenantA)
	if err != nil || !ownedA {
		t.Errorf("triggerOwnedByTenant(A) = %v, %v; want true", ownedA, err)
	}
	ownedB, err := s.triggerOwnedByTenant(seed.triggerA, seed.triggerA, seed.tenantB)
	if err != nil || ownedB {
		t.Errorf("triggerOwnedByTenant(B) = %v, %v; want false", ownedB, err)
	}
}
