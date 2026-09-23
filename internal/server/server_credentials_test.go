package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/mcmx/nitejaguar/internal/database"
	"github.com/mcmx/nitejaguar/internal/workflow"
)

// NOTE: this file is named server_credentials_test.go so it runs after
// routes_test.go. internal/database caches its connection in a package
// singleton shared by all tests in this package, and the client-count
// assertion in TestClientAssignmentFlow expects only its own clients to
// exist; the tests below therefore run last and scope every assertion by
// tenant/id instead of counting globally.

func TestCredentialsLifecycle(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	t.Setenv("CREDENTIALS_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	wm := workflow.NewWorkflowManager(false, db)
	s := &Server{db: db, wm: wm}
	_, api := humatest.New(t)
	addApiRoutes(api, s)

	// Create a tenant-scoped credential for tenant acme.
	resp := api.Post("/api/credentials", map[string]any{
		"tenant_id": "credacme", "name": "api-token", "type": "token", "secret": "s3cr3t-acme",
	})
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("create credential status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var created struct {
		ID       string `json:"id"`
		TenantID string `json:"tenant_id"`
		Name     string `json:"name"`
		Type     string `json:"type"`
		Scope    string `json:"scope"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &created)
	if !strings.HasPrefix(created.ID, "credential_") {
		t.Fatalf("credential id %q missing credential_ prefix", created.ID)
	}
	if created.Scope != "tenant" {
		t.Fatalf("default scope = %q, want tenant", created.Scope)
	}
	// The create response must never carry the secret.
	if strings.Contains(resp.Body.String(), "s3cr3t-acme") {
		t.Fatalf("create response leaks the secret: %s", resp.Body.String())
	}

	// Duplicate name in the same tenant/scope/owner is rejected.
	resp = api.Post("/api/credentials", map[string]any{
		"tenant_id": "credacme", "name": "api-token", "secret": "other",
	})
	if resp.Code != http.StatusBadRequest && resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate create status = %v, want 400/422", resp.Code)
	}

	// Unknown credential types are rejected.
	resp = api.Post("/api/credentials", map[string]any{
		"tenant_id": "credacme", "name": "bad-type", "type": "nonsense", "secret": "x",
	})
	if resp.Code != http.StatusBadRequest && resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad type status = %v, want 400/422", resp.Code)
	}

	// Group scope without an owner is rejected.
	resp = api.Post("/api/credentials", map[string]any{
		"tenant_id": "credacme", "name": "ownerless", "scope": "group", "secret": "x",
	})
	if resp.Code != http.StatusBadRequest && resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("ownerless group scope status = %v, want 400/422", resp.Code)
	}

	// Secret is encrypted at rest: the stored envelope differs from plaintext.
	row, err := db.GetCredential(created.ID)
	if err != nil {
		t.Fatalf("get credential row: %v", err)
	}
	if row.SecretEncrypted == "" || strings.Contains(row.SecretEncrypted, "s3cr3t-acme") {
		t.Fatalf("secret is not encrypted at rest")
	}

	// List and get expose metadata only.
	resp = api.Get("/api/credentials?tenant_id=credacme")
	if resp.Code != http.StatusOK {
		t.Fatalf("list status = %v", resp.Code)
	}
	if strings.Contains(resp.Body.String(), "s3cr3t-acme") || strings.Contains(resp.Body.String(), "secret_encrypted") {
		t.Fatalf("list leaks secret material: %s", resp.Body.String())
	}
	resp = api.Get("/api/credentials/" + created.ID)
	if resp.Code != http.StatusOK {
		t.Fatalf("get status = %v, body = %s", resp.Code, resp.Body.String())
	}
	if strings.Contains(resp.Body.String(), "s3cr3t-acme") {
		t.Fatalf("get leaks the secret: %s", resp.Body.String())
	}

	// Register a client in the credential tenant (tenant from token).
	joinToken := mintEnrollmentToken(t, db, "credacme", 0)
	resp = api.Post("/api/clients/register", map[string]any{
		"name": "cred-worker", "enrollment_token": joinToken,
	})
	var registered struct {
		ClientID string `json:"client_id"`
		Token    string `json:"token"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &registered)

	// Unauthenticated fetch is rejected.
	resp = api.Get("/api/credentials/api-token/fetch")
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated fetch status = %v, want 401", resp.Code)
	}

	// Just-in-time fetch by name returns the secret with a TTL.
	resp = api.Get("/api/credentials/api-token/fetch?workflow_id=workflow_01kcredtest000001&node_id=action_01kcredtest0000001",
		"Authorization: Bearer "+registered.Token)
	if resp.Code != http.StatusOK {
		t.Fatalf("fetch status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var fetched struct {
		CredentialID string `json:"credential_id"`
		Name         string `json:"name"`
		Type         string `json:"type"`
		Secret       string `json:"secret"`
		TTLSeconds   int    `json:"ttl_seconds"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &fetched)
	if fetched.CredentialID != created.ID || fetched.Secret != "s3cr3t-acme" {
		t.Fatalf("unexpected fetch result: %s", resp.Body.String())
	}
	if fetched.TTLSeconds <= 0 {
		t.Fatalf("fetch must advertise a positive TTL: %s", resp.Body.String())
	}

	// Fetch by id resolves directly.
	resp = api.Get("/api/credentials/"+created.ID+"/fetch", "Authorization: Bearer "+registered.Token)
	if resp.Code != http.StatusOK {
		t.Fatalf("fetch by id status = %v, body = %s", resp.Code, resp.Body.String())
	}

	// Every fetch is audited.
	resp = api.Get("/api/audit?limit=500")
	var audit struct {
		Entries []struct {
			Action string `json:"action"`
			Target string `json:"target"`
			Detail string `json:"detail"`
		} `json:"entries"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &audit)
	seenCreate, seenFetch := false, false
	for _, e := range audit.Entries {
		if e.Action == "credential.create" && e.Target == created.ID {
			seenCreate = true
		}
		if e.Action == "credential.fetch" && e.Target == created.ID {
			seenFetch = true
			if strings.Contains(e.Detail, "s3cr3t-acme") {
				t.Fatalf("audit detail leaks the secret: %+v", e)
			}
		}
	}
	if !seenCreate || !seenFetch {
		t.Fatalf("audit missing credential.create=%v credential.fetch=%v: %s", seenCreate, seenFetch, resp.Body.String())
	}

	// Tenant isolation: a client from another tenant cannot fetch.
	otherJoin := mintEnrollmentToken(t, db, "othercred", 0)
	resp = api.Post("/api/clients/register", map[string]any{"name": "other-worker", "enrollment_token": otherJoin})
	var other struct {
		ClientID string `json:"client_id"`
		Token    string `json:"token"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &other)
	resp = api.Get("/api/credentials/api-token/fetch", "Authorization: Bearer "+other.Token)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant fetch status = %v, want 404", resp.Code)
	}

	// Delete removes the credential; fetching it afterwards 404s.
	resp = api.Do(http.MethodDelete, "/api/credentials/"+created.ID)
	if resp.Code != http.StatusOK {
		t.Fatalf("delete status = %v, body = %s", resp.Code, resp.Body.String())
	}
	resp = api.Get("/api/credentials/"+created.ID+"/fetch", "Authorization: Bearer "+registered.Token)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("fetch after delete status = %v, want 404", resp.Code)
	}
}

func TestCredentialScopeResolution(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	t.Setenv("CREDENTIALS_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	wm := workflow.NewWorkflowManager(false, db)
	s := &Server{db: db, wm: wm}
	_, api := humatest.New(t)
	addApiRoutes(api, s)

	// Same name in all three scopes.
	for _, c := range []map[string]any{
		{"tenant_id": "scopeacme", "name": "db-pass", "type": "username_password", "secret": "tenant-secret"},
		{"tenant_id": "scopeacme", "name": "db-pass", "type": "username_password", "scope": "group", "owner_id": "ops", "secret": "group-secret"},
		{"tenant_id": "scopeacme", "name": "db-pass", "type": "username_password", "scope": "user", "owner_id": "alice", "secret": "user-secret"},
	} {
		resp := api.Post("/api/credentials", c)
		if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
			t.Fatalf("create %v status = %v, body = %s", c, resp.Code, resp.Body.String())
		}
	}

	joinToken := mintEnrollmentToken(t, db, "scopeacme", 0)
	resp := api.Post("/api/clients/register", map[string]any{"name": "scope-worker", "enrollment_token": joinToken})
	var registered struct {
		ClientID string `json:"client_id"`
		Token    string `json:"token"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &registered)

	fetch := func(query string) string {
		t.Helper()
		resp := api.Get("/api/credentials/db-pass/fetch"+query, "Authorization: Bearer "+registered.Token)
		if resp.Code != http.StatusOK {
			t.Fatalf("fetch %q status = %v, body = %s", query, resp.Code, resp.Body.String())
		}
		var out struct {
			Secret string `json:"secret"`
		}
		decodeBody(t, strings.NewReader(resp.Body.String()), &out)
		return out.Secret
	}

	// Most-specific wins: user > group > tenant.
	if got := fetch(""); got != "tenant-secret" {
		t.Fatalf("bare fetch = %q, want tenant-secret", got)
	}
	if got := fetch("?groups=ops"); got != "group-secret" {
		t.Fatalf("group fetch = %q, want group-secret", got)
	}
	if got := fetch("?user_id=alice&groups=ops"); got != "user-secret" {
		t.Fatalf("user fetch = %q, want user-secret", got)
	}
	if got := fetch("?user_id=bob&groups=ops"); got != "group-secret" {
		t.Fatalf("unmatched user falls back to group = %q, want group-secret", got)
	}
}
