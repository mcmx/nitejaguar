package server

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/mcmx/nitejaguar/internal/database"
	"github.com/mcmx/nitejaguar/internal/workflow"
)

func TestTenantLifecycle(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	wm := workflow.NewWorkflowManager(false, db)
	s := &Server{db: db, wm: wm}
	_, api := newTestAPI(t)
	addApiRoutes(api, s)

	superAuth := ensureRoleToken(t, db, "default", database.RoleAdmin)
	tenantName := fmt.Sprintf("Acme Tenant %d", testAdminSeq.Add(1))

	// Non-super tenant admin cannot create tenants.
	otherTenant := fmt.Sprintf("tenantadmin%d", testAdminSeq.Add(1))
	tenantAdminAuth := ensureRoleToken(t, db, otherTenant, database.RoleAdmin)
	resp := api.Post("/api/tenants", tenantAdminAuth, map[string]any{
		"name": "Nope Corp", "admin_email": "nope@example.com",
	})
	if resp.Code != http.StatusForbidden {
		t.Fatalf("tenant-admin create status = %v, want 403 (body %s)", resp.Code, resp.Body.String())
	}

	// Validation: missing name / email / bad email.
	for _, body := range []map[string]any{
		{"admin_email": "a@example.com"},
		{"name": "Nameless"},
		{"name": "Bad Email", "admin_email": "not-an-email"},
	} {
		resp = api.Post("/api/tenants", superAuth, body)
		if resp.Code != http.StatusBadRequest && resp.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid create status = %v, want 400/422 (body %s)", resp.Code, resp.Body.String())
		}
	}

	// Happy path: super creates tenant + admin (generated password).
	resp = api.Post("/api/tenants", superAuth, map[string]any{
		"name": tenantName, "admin_email": "boss@example.com",
	})
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("create status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var created struct {
		Slug          string `json:"slug"`
		Name          string `json:"name"`
		Status        string `json:"status"`
		AdminUserID   string `json:"admin_user_id"`
		AdminPassword string `json:"admin_password"`
		Generated     bool   `json:"generated_password"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &created)
	if created.Slug == "" || created.AdminUserID == "" {
		t.Fatalf("unexpected create response: %s", resp.Body.String())
	}
	if !created.Generated || created.AdminPassword == "" {
		t.Fatalf("expected one-time generated password: %s", resp.Body.String())
	}
	if created.Status != database.TenantActive {
		t.Fatalf("new tenant status = %q, want active", created.Status)
	}

	// Duplicate name rejected.
	resp = api.Post("/api/tenants", superAuth, map[string]any{
		"name": tenantName, "admin_email": "other@example.com",
	})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("duplicate create status = %v, want 400 (body %s)", resp.Code, resp.Body.String())
	}

	// New tenant admin can log in with the one-time password.
	resp = api.Post("/api/auth/login", map[string]any{
		"username": "admin", "password": created.AdminPassword, "tenant_id": created.Slug,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("tenant admin login status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var logged struct {
		Token string `json:"token"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &logged)
	tenantAdminSession := "Authorization: Bearer " + logged.Token

	// Tenant admin lists tenants: sees only its own.
	resp = api.Get("/api/tenants", tenantAdminSession)
	if resp.Code != http.StatusOK {
		t.Fatalf("tenant list status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var listed struct {
		Tenants []struct {
			Slug string `json:"slug"`
		} `json:"tenants"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &listed)
	if len(listed.Tenants) != 1 || listed.Tenants[0].Slug != created.Slug {
		t.Fatalf("tenant admin sees %v, want only %q", listed.Tenants, created.Slug)
	}

	// Super lists: sees the new tenant among others.
	resp = api.Get("/api/tenants", superAuth)
	if resp.Code != http.StatusOK {
		t.Fatalf("super list status = %v", resp.Code)
	}
	if !strings.Contains(resp.Body.String(), created.Slug) {
		t.Fatalf("super list missing new tenant: %s", resp.Body.String())
	}

	// Tenant admin cannot fetch another tenant.
	resp = api.Get("/api/tenants/default", tenantAdminSession)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant get status = %v, want 403", resp.Code)
	}

	// Tenant admin cannot create users cross-tenant (tenant-local admin scope).
	resp = api.Post("/api/users", tenantAdminAuth, map[string]any{
		"username": "sneaky", "password": "password-12345", "tenant_id": created.Slug,
	})
	if resp.Code != http.StatusForbidden {
		t.Fatalf("tenant-admin cross-tenant user create status = %v, want 403 (body %s)", resp.Code, resp.Body.String())
	}

	// Suspend blocks login; activate restores it.
	resp = api.Post("/api/tenants/"+created.Slug+"/suspend", superAuth, map[string]any{})
	if resp.Code != http.StatusOK {
		t.Fatalf("suspend status = %v, body = %s", resp.Code, resp.Body.String())
	}
	resp = api.Post("/api/auth/login", map[string]any{
		"username": "admin", "password": created.AdminPassword, "tenant_id": created.Slug,
	})
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("suspended login status = %v, want 401", resp.Code)
	}
	// Existing session is rejected while suspended.
	resp = api.Get("/api/auth/me", tenantAdminSession)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("suspended session status = %v, want 401", resp.Code)
	}
	resp = api.Post("/api/tenants/"+created.Slug+"/activate", superAuth, map[string]any{})
	if resp.Code != http.StatusOK {
		t.Fatalf("activate status = %v, body = %s", resp.Code, resp.Body.String())
	}
	resp = api.Post("/api/auth/login", map[string]any{
		"username": "admin", "password": created.AdminPassword, "tenant_id": created.Slug,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("post-activate login status = %v, body = %s", resp.Code, resp.Body.String())
	}

	// The default tenant can never be suspended or deleted.
	resp = api.Post("/api/tenants/default/suspend", superAuth, map[string]any{})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("default suspend status = %v, want 400", resp.Code)
	}
	resp = api.Do(http.MethodDelete, "/api/tenants/default", superAuth)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("default delete status = %v, want 400 (body %s)", resp.Code, resp.Body.String())
	}

	// Non-super cannot suspend.
	resp = api.Post("/api/tenants/"+created.Slug+"/suspend", tenantAdminSession, map[string]any{})
	if resp.Code != http.StatusForbidden {
		t.Fatalf("tenant suspend by non-super status = %v, want 403", resp.Code)
	}
}
