package server

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/mcmx/nitejaguar/internal/database"
	"github.com/mcmx/nitejaguar/internal/workflow"
)

var testAdminSeq atomic.Int64

// ensureAdminToken creates a fresh admin user (unique username) and returns
// a session Bearer header value. Tests share one in-memory DB via the
// database singleton, so every caller mints its own user to avoid
// username collisions; once any user exists the server leaves
// open-bootstrap mode and gated endpoints require these tokens.
func ensureAdminToken(t *testing.T, db database.Service) string {
	t.Helper()
	n := testAdminSeq.Add(1)
	username := fmt.Sprintf("test-admin-%d", n)
	row, err := db.CreateUser("default", username, "password-12345", database.RoleAdmin, nil)
	if err != nil {
		t.Fatalf("create test admin: %v", err)
	}
	_, plaintext, err := db.CreateSession(row.ID, 0)
	if err != nil {
		t.Fatalf("create test admin session: %v", err)
	}
	return "Authorization: Bearer " + plaintext
}

// ensureRoleToken creates a user with the given tenant/role and returns its
// session Bearer header.
func ensureRoleToken(t *testing.T, db database.Service, tenant, role string) string {
	t.Helper()
	n := testAdminSeq.Add(1)
	username := fmt.Sprintf("test-%s-%d", strings.ReplaceAll(role, "_", "-"), n)
	if tenant == "" {
		tenant = "default"
	}
	row, err := db.CreateUser(tenant, username, "password-12345", role, nil)
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}
	_, plaintext, err := db.CreateSession(row.ID, 0)
	if err != nil {
		t.Fatalf("create test user session: %v", err)
	}
	return "Authorization: Bearer " + plaintext
}

func TestAuthLoginLogoutMe(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	wm := workflow.NewWorkflowManager(false, db)
	s := &Server{db: db, wm: wm}
	_, api := humatest.New(t)
	addApiRoutes(api, s)

	tenant := fmt.Sprintf("rbaclogin%d", testAdminSeq.Add(1))
	// Bootstrap the tenant via open user creation only if no users exist;
	// otherwise mint directly through the DB (same effect, deterministic).
	if _, err = db.CreateUser(tenant, "alice", "password-12345", database.RoleOperator, []string{"ops"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// Wrong password -> 401.
	resp := api.Post("/api/auth/login", map[string]any{
		"username": "alice", "password": "wrong-pass", "tenant_id": tenant,
	})
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("bad password status = %v, want 401", resp.Code)
	}

	// Login -> session token.
	resp = api.Post("/api/auth/login", map[string]any{
		"username": "alice", "password": "password-12345", "tenant_id": tenant,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("login status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var logged struct {
		Token string `json:"token"`
		User  struct {
			ID       string `json:"id"`
			TenantID string `json:"tenant_id"`
			Username string `json:"username"`
			Role     string `json:"role"`
		} `json:"user"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &logged)
	if logged.Token == "" || logged.User.Username != "alice" || logged.User.Role != database.RoleOperator {
		t.Fatalf("unexpected login response: %s", resp.Body.String())
	}
	if strings.Contains(resp.Body.String(), "password-12345") {
		t.Fatalf("login response leaks password: %s", resp.Body.String())
	}
	auth := "Authorization: Bearer " + logged.Token

	// Me resolves the caller.
	resp = api.Get("/api/auth/me", auth)
	if resp.Code != http.StatusOK {
		t.Fatalf("me status = %v, body = %s", resp.Code, resp.Body.String())
	}

	// Logout revokes the session: me afterwards is 401.
	resp = api.Post("/api/auth/logout", auth)
	if resp.Code != http.StatusOK {
		t.Fatalf("logout status = %v, body = %s", resp.Code, resp.Body.String())
	}
	resp = api.Get("/api/auth/me", auth)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("me after logout status = %v, want 401", resp.Code)
	}
}

func TestRBACRoleGating(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	wm := workflow.NewWorkflowManager(false, db)
	s := &Server{db: db, wm: wm}
	_, api := humatest.New(t)
	addApiRoutes(api, s)

	tenant := fmt.Sprintf("rbacgate%d", testAdminSeq.Add(1))
	adminAuth := ensureRoleToken(t, db, tenant, database.RoleAdmin)
	operatorAuth := ensureRoleToken(t, db, tenant, database.RoleOperator)
	viewerAuth := ensureRoleToken(t, db, tenant, database.RoleViewer)

	// Unauthenticated token issuance is rejected once users exist.
	resp := api.Post("/api/enrollment/tokens", map[string]any{"tenant_id": tenant})
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated token create status = %v, want 401", resp.Code)
	}

	// Viewer cannot mint enrollment tokens (403); operator and admin can.
	resp = api.Post("/api/enrollment/tokens", viewerAuth, map[string]any{"tenant_id": tenant})
	if resp.Code != http.StatusForbidden {
		t.Fatalf("viewer token create status = %v, want 403", resp.Code)
	}
	resp = api.Post("/api/enrollment/tokens", operatorAuth, map[string]any{"tenant_id": tenant, "label": "op"})
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("operator token create status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var opTok struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &opTok)

	// Cross-tenant: operator denied, admin allowed.
	resp = api.Post("/api/enrollment/tokens", operatorAuth, map[string]any{"tenant_id": "other-tenant-x"})
	if resp.Code != http.StatusForbidden {
		t.Fatalf("operator cross-tenant status = %v, want 403", resp.Code)
	}
	resp = api.Post("/api/enrollment/tokens", adminAuth, map[string]any{"tenant_id": "other-tenant-x"})
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("admin cross-tenant status = %v, body = %s", resp.Code, resp.Body.String())
	}

	// Client revoke is operator+: viewer denied. Register a client via the
	// API (open endpoint) then revoke it.
	_, freshPlain, err := db.CreateEnrollmentToken(tenant, "revoke-test-2", nil, 0)
	if err != nil {
		t.Fatalf("mint fresh join token: %v", err)
	}
	resp = api.Post("/api/clients/register", map[string]any{"name": "rbac-revoke-target", "enrollment_token": freshPlain})
	var reg struct {
		ClientID string `json:"client_id"`
		Token    string `json:"token"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &reg)
	resp = api.Post("/api/clients/"+reg.ClientID+"/revoke", viewerAuth, map[string]any{})
	if resp.Code != http.StatusForbidden {
		t.Fatalf("viewer client revoke status = %v, want 403", resp.Code)
	}
	resp = api.Post("/api/clients/"+reg.ClientID+"/revoke", operatorAuth, map[string]any{})
	if resp.Code != http.StatusOK {
		t.Fatalf("operator client revoke status = %v, body = %s", resp.Code, resp.Body.String())
	}

	// Workflow import/clone are operator+: viewer denied, unauth denied.
	wfBody := map[string]any{
		"id": "workflow_01krbacgating00000001", "name": "RBAC gating workflow",
		"nodes": map[string]any{},
	}
	resp = api.Post("/api/workflows/import", map[string]any{"id": "x", "name": "y", "nodes": map[string]any{}})
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("unauth import status = %v, want 401", resp.Code)
	}
	resp = api.Post("/api/workflows/import", viewerAuth, wfBody)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("viewer import status = %v, want 403", resp.Code)
	}
	resp = api.Post("/api/workflows/import", operatorAuth, wfBody)
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("operator import status = %v, body = %s", resp.Code, resp.Body.String())
	}

	// Credential create/delete are operator+: viewer denied.
	resp = api.Post("/api/credentials", viewerAuth, map[string]any{
		"tenant_id": tenant, "name": "rbac-cred", "secret": "s",
	})
	if resp.Code != http.StatusForbidden {
		t.Fatalf("viewer credential create status = %v, want 403", resp.Code)
	}
	resp = api.Post("/api/credentials", operatorAuth, map[string]any{
		"tenant_id": tenant, "name": "rbac-cred", "secret": "s",
	})
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("operator credential create status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var cred struct {
		ID string `json:"id"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &cred)
	resp = api.Do(http.MethodDelete, "/api/credentials/"+cred.ID, viewerAuth)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("viewer credential delete status = %v, want 403", resp.Code)
	}
	resp = api.Do(http.MethodDelete, "/api/credentials/"+cred.ID, operatorAuth)
	if resp.Code != http.StatusOK {
		t.Fatalf("operator credential delete status = %v, body = %s", resp.Code, resp.Body.String())
	}

	// Audit requires auth; viewers can read their own tenant.
	resp = api.Get("/api/audit?limit=500")
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("unauth audit status = %v, want 401", resp.Code)
	}
	resp = api.Get("/api/audit?limit=500", viewerAuth)
	if resp.Code != http.StatusOK {
		t.Fatalf("viewer audit status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var audit struct {
		Entries []struct {
			Action   string `json:"action"`
			TenantID string `json:"tenant_id"`
		} `json:"entries"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &audit)
	for _, e := range audit.Entries {
		if e.TenantID != tenant {
			t.Fatalf("viewer audit leaked foreign tenant %q", e.TenantID)
		}
	}
	seen := map[string]bool{}
	for _, e := range audit.Entries {
		seen[e.Action] = true
	}
	for _, want := range []string{"enrollment.create", "client.revoke", "workflow.import", "credential.create", "credential.delete"} {
		if !seen[want] {
			t.Fatalf("audit missing %q (tenant %s): %s", want, tenant, resp.Body.String())
		}
	}
}

func TestUserManagement(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	wm := workflow.NewWorkflowManager(false, db)
	s := &Server{db: db, wm: wm}
	_, api := humatest.New(t)
	addApiRoutes(api, s)

	tenant := fmt.Sprintf("rbacusers%d", testAdminSeq.Add(1))
	adminAuth := ensureRoleToken(t, db, tenant, database.RoleAdmin)
	operatorAuth := ensureRoleToken(t, db, tenant, database.RoleOperator)
	viewerAuth := ensureRoleToken(t, db, tenant, database.RoleViewer)

	// Operator cannot create users (admin only).
	resp := api.Post("/api/users", operatorAuth, map[string]any{
		"username": "nope", "password": "password-12345", "tenant_id": tenant,
	})
	if resp.Code != http.StatusForbidden {
		t.Fatalf("operator user create status = %v, want 403", resp.Code)
	}
	// Viewer cannot list users.
	resp = api.Get("/api/users", viewerAuth)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("viewer user list status = %v, want 403", resp.Code)
	}
	// Admin creates a viewer.
	resp = api.Post("/api/users", adminAuth, map[string]any{
		"username": "managed-viewer", "password": "password-12345", "tenant_id": tenant, "role": "viewer",
	})
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("admin user create status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var created struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &created)
	if strings.Contains(resp.Body.String(), "password-12345") {
		t.Fatalf("user create leaks password: %s", resp.Body.String())
	}
	// Duplicate username rejected.
	resp = api.Post("/api/users", adminAuth, map[string]any{
		"username": "managed-viewer", "password": "password-12345", "tenant_id": tenant,
	})
	if resp.Code != http.StatusBadRequest && resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate user status = %v, want 400/422", resp.Code)
	}
	// Short password rejected.
	resp = api.Post("/api/users", adminAuth, map[string]any{
		"username": "shortpw", "password": "short", "tenant_id": tenant,
	})
	if resp.Code != http.StatusBadRequest && resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("short password status = %v, want 400/422", resp.Code)
	}
	// Operator lists tenant users.
	resp = api.Get("/api/users", operatorAuth)
	if resp.Code != http.StatusOK {
		t.Fatalf("operator user list status = %v", resp.Code)
	}
	// Admin revokes the managed user; it can no longer log in.
	resp = api.Post("/api/users/"+created.ID+"/revoke", adminAuth, map[string]any{})
	if resp.Code != http.StatusOK {
		t.Fatalf("revoke status = %v, body = %s", resp.Code, resp.Body.String())
	}
	resp = api.Post("/api/auth/login", map[string]any{
		"username": "managed-viewer", "password": "password-12345", "tenant_id": tenant,
	})
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("revoked login status = %v, want 401", resp.Code)
	}
	// Admin cannot revoke itself.
	var me struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	resp = api.Get("/api/auth/me", adminAuth)
	decodeBody(t, strings.NewReader(resp.Body.String()), &me)
	resp = api.Post("/api/users/"+me.User.ID+"/revoke", adminAuth, map[string]any{})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("self-revoke status = %v, want 400", resp.Code)
	}
}
