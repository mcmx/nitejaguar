package server

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mcmx/nitejaguar/common"
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
	_, api := newTestAPI(t)
	addApiRoutes(api, s)
	adminAuth := ensureAdminToken(t, db)

	// Create a tenant-scoped credential for tenant acme.
	resp := api.Post("/api/credentials", adminAuth, map[string]any{
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
	resp = api.Post("/api/credentials", adminAuth, map[string]any{
		"tenant_id": "credacme", "name": "api-token", "secret": "other",
	})
	if resp.Code != http.StatusBadRequest && resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate create status = %v, want 400/422", resp.Code)
	}

	// Unknown credential types are rejected.
	resp = api.Post("/api/credentials", adminAuth, map[string]any{
		"tenant_id": "credacme", "name": "bad-type", "type": "nonsense", "secret": "x",
	})
	if resp.Code != http.StatusBadRequest && resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad type status = %v, want 400/422", resp.Code)
	}

	// Group scope without an owner is rejected.
	resp = api.Post("/api/credentials", adminAuth, map[string]any{
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

	// List and get expose metadata only (viewer-gated, tenant-scoped;
	// the default-tenant superuser sees everything).
	resp = api.Get("/api/credentials?tenant_id=credacme", adminAuth)
	if resp.Code != http.StatusOK {
		t.Fatalf("list status = %v", resp.Code)
	}
	if strings.Contains(resp.Body.String(), "s3cr3t-acme") || strings.Contains(resp.Body.String(), "secret_encrypted") {
		t.Fatalf("list leaks secret material: %s", resp.Body.String())
	}
	resp = api.Get("/api/credentials/"+created.ID, adminAuth)
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
	resp = api.Get("/api/audit?limit=500", adminAuth)
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
	resp = api.Do(http.MethodDelete, "/api/credentials/"+created.ID, adminAuth)
	if resp.Code != http.StatusOK {
		t.Fatalf("delete status = %v, body = %s", resp.Code, resp.Body.String())
	}
	resp = api.Get("/api/credentials/"+created.ID+"/fetch", "Authorization: Bearer "+registered.Token)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("fetch after delete status = %v, want 404", resp.Code)
	}
}

func TestZZExternalCredentialBackendAPIAndWorkflow(t *testing.T) {
	const secret = "vault-backed-api-secret"
	wrapKey := []byte("vault-test-wrap-key-32-byte-long")
	vault := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read Vault request: %v", err)
			return
		}
		if strings.Contains(string(body), secret) {
			t.Errorf("Vault received credential plaintext")
		}
		switch r.URL.Path {
		case "/v1/transit/encrypt/credentials":
			var request struct {
				Plaintext string `json:"plaintext"`
				Context   string `json:"context"`
			}
			if err := json.Unmarshal(body, &request); err != nil {
				t.Errorf("decode Vault encrypt request: %v", err)
				return
			}
			dek, err := base64.StdEncoding.DecodeString(request.Plaintext)
			if err != nil || len(dek) != 32 {
				t.Errorf("Vault encrypt received plaintext other than a DEK")
				return
			}
			sealed, err := sealVaultTestKey(wrapKey, dek, []byte(request.Context))
			if err != nil {
				t.Errorf("seal DEK: %v", err)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{
				"ciphertext": "vault:v1:" + base64.StdEncoding.EncodeToString(sealed),
			}})
		case "/v1/transit/decrypt/credentials":
			var request struct {
				Ciphertext string `json:"ciphertext"`
				Context    string `json:"context"`
			}
			if err := json.Unmarshal(body, &request); err != nil {
				t.Errorf("decode Vault decrypt request: %v", err)
				return
			}
			sealed, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(request.Ciphertext, "vault:v1:"))
			if err != nil {
				http.Error(w, "bad encrypted key", http.StatusBadRequest)
				return
			}
			dek, err := openVaultTestKey(wrapKey, sealed, []byte(request.Context))
			if err != nil {
				http.Error(w, "bad encryption context", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{
				"plaintext": base64.StdEncoding.EncodeToString(dek),
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer vault.Close()

	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	t.Setenv("CREDENTIALS_BACKEND", "vault")
	t.Setenv("VAULT_ADDR", vault.URL)
	t.Setenv("VAULT_TOKEN", "test-token")
	t.Setenv("VAULT_TRANSIT_KEY", "credentials")
	db, err := database.New()
	if err != nil {
		t.Fatalf("initialize database: %v", err)
	}
	wm := workflow.NewWorkflowManager(true, db)
	server := &Server{db: db, wm: wm}
	_, api := newTestAPI(t)
	addApiRoutes(api, server)
	adminAuth := ensureAdminToken(t, db)

	resp := api.Post("/api/credentials", adminAuth, map[string]any{
		"name": "vault-api-token", "type": "token", "secret": secret,
	})
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", resp.Code, resp.Body.String())
	}
	var created struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &created)
	row, err := db.GetCredential(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(row.SecretEncrypted, secret) || !strings.Contains(row.SecretEncrypted, "wrapped_dek") {
		t.Fatalf("stored credential is not an encrypted DEK envelope")
	}

	joinToken := mintEnrollmentToken(t, db, "default", 0)
	resp = api.Post("/api/clients/register", map[string]any{"name": "vault-worker", "enrollment_token": joinToken})
	var registered struct {
		Token string `json:"token"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &registered)
	resp = api.Get("/api/credentials/"+created.Name+"/fetch", "Authorization: Bearer "+registered.Token)
	if resp.Code != http.StatusOK {
		t.Fatalf("fetch status = %d, body = %s", resp.Code, resp.Body.String())
	}
	var fetched struct {
		Secret string `json:"secret"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &fetched)
	if fetched.Secret != secret {
		t.Fatalf("fetched secret = %q, want original", fetched.Secret)
	}

	lookup, ok := db.(common.CredentialSecretLookup)
	if !ok {
		t.Fatal("database service does not provide workflow credential lookup")
	}
	workflowSecret, _, err := lookup.ResolveCredentialSecret("default", created.ID)
	if err != nil || workflowSecret != secret {
		t.Fatalf("workflow credential resolution = %q, %v; want original secret", workflowSecret, err)
	}
}

func sealVaultTestKey(key, plaintext, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, aad), nil
}

func openVaultTestKey(key, sealed, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, io.ErrUnexpectedEOF
	}
	return gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], aad)
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
	_, api := newTestAPI(t)
	addApiRoutes(api, s)
	scopeAuth := ensureAdminToken(t, db)

	// Same name in all three scopes. Multi-field types store a JSON
	// object carrying every required field (see common.CredentialTypeDefs).
	for _, c := range []map[string]any{
		{"tenant_id": "scopeacme", "name": "db-pass", "type": "username_password", "secret": `{"username":"svc","password":"tenant-secret"}`},
		{"tenant_id": "scopeacme", "name": "db-pass", "type": "username_password", "scope": "group", "owner_id": "ops", "secret": `{"username":"svc","password":"group-secret"}`},
		{"tenant_id": "scopeacme", "name": "db-pass", "type": "username_password", "scope": "user", "owner_id": "alice", "secret": `{"username":"alice","password":"user-secret"}`},
	} {
		resp := api.Post("/api/credentials", scopeAuth, c)
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

	// fetchPassword resolves the credential and returns the password
	// field of its JSON secret object.
	fetchPassword := func(query string) string {
		t.Helper()
		resp := api.Get("/api/credentials/db-pass/fetch"+query, "Authorization: Bearer "+registered.Token)
		if resp.Code != http.StatusOK {
			t.Fatalf("fetch %q status = %v, body = %s", query, resp.Code, resp.Body.String())
		}
		var out struct {
			Secret string `json:"secret"`
		}
		decodeBody(t, strings.NewReader(resp.Body.String()), &out)
		var obj map[string]string
		if err := json.Unmarshal([]byte(out.Secret), &obj); err != nil {
			t.Fatalf("fetch %q secret is not a JSON object: %v (%q)", query, err, out.Secret)
		}
		return obj["password"]
	}
	fetch := fetchPassword

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

func TestCredentialFetchEnforcesCollectionType(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	t.Setenv("CREDENTIALS_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	wm := workflow.NewWorkflowManager(false, db)
	s := &Server{db: db, wm: wm}
	_, api := newTestAPI(t)
	addApiRoutes(api, s)
	typeAuth := ensureAdminToken(t, db)

	const tenant = "typeten"
	// One AWS-collection node (action s3) and one core node (action file).
	wfDef := workflow.Workflow{
		Id:       "workflow_01htypeenforce00000001",
		Name:     "type enforcement",
		TenantID: tenant,
		Nodes: map[string]workflow.Node{
			"action_01htypeenforce00000001": {
				Id: "action_01htypeenforce00000001", Name: "backup",
				ActionType: "action", ActionName: "s3", CredentialRef: "prod-aws",
			},
			"action_01htypeenforce00000002": {
				Id: "action_01htypeenforce00000002", Name: "local",
				ActionType: "action", ActionName: "file", CredentialRef: "api-token",
			},
		},
	}
	raw, err := json.Marshal(wfDef)
	if err != nil {
		t.Fatalf("marshal workflow: %v", err)
	}
	if err := db.SaveWorkflow(wfDef.Id, string(raw), tenant); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	for _, c := range []map[string]any{
		{"tenant_id": tenant, "name": "prod-aws", "type": "aws", "secret_fields": map[string]any{"access_key_id": "AKIAEXAMPLE", "secret_access_key": "aws-secret"}},
		{"tenant_id": tenant, "name": "api-token", "type": "token", "secret": "token-secret"},
	} {
		resp := api.Post("/api/credentials", typeAuth, c)
		if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
			t.Fatalf("create %v status = %v, body = %s", c, resp.Code, resp.Body.String())
		}
	}

	// The legacy s3 type is rejected at creation: S3 uses the aws type.
	resp := api.Post("/api/credentials", typeAuth, map[string]any{
		"tenant_id": tenant, "name": "legacy-s3", "type": "s3", "secret": "x",
	})
	if resp.Code != http.StatusBadRequest && resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("legacy s3 create status = %v, want 400/422", resp.Code)
	}

	joinToken := mintEnrollmentToken(t, db, tenant, 0)
	resp = api.Post("/api/clients/register", map[string]any{"name": "type-worker", "enrollment_token": joinToken})
	var registered struct {
		ClientID string `json:"client_id"`
		Token    string `json:"token"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &registered)
	auth := "Authorization: Bearer " + registered.Token
	nodeCtx := "?workflow_id=" + wfDef.Id + "&node_id=action_01htypeenforce00000001"
	coreCtx := "?workflow_id=" + wfDef.Id + "&node_id=action_01htypeenforce00000002"

	// Matching type for the AWS node fetches fine.
	resp = api.Get("/api/credentials/prod-aws/fetch"+nodeCtx, auth)
	if resp.Code != http.StatusOK {
		t.Fatalf("matching fetch status = %v, body = %s", resp.Code, resp.Body.String())
	}

	// Mismatched type for the AWS node fails closed.
	resp = api.Get("/api/credentials/api-token/fetch"+nodeCtx, auth)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("mismatched fetch status = %v, want 403, body = %s", resp.Code, resp.Body.String())
	}
	if strings.Contains(resp.Body.String(), "token-secret") {
		t.Fatalf("denied fetch leaks the secret: %s", resp.Body.String())
	}

	// Core nodes declare no type: any credential type is allowed.
	resp = api.Get("/api/credentials/api-token/fetch"+coreCtx, auth)
	if resp.Code != http.StatusOK {
		t.Fatalf("core-node fetch status = %v, body = %s", resp.Code, resp.Body.String())
	}

	// Unknown nodes and missing node context fail open (dangling refs).
	resp = api.Get("/api/credentials/api-token/fetch?workflow_id="+wfDef.Id+"&node_id=action_01hnope000000000000", auth)
	if resp.Code != http.StatusOK {
		t.Fatalf("unknown-node fetch status = %v, body = %s", resp.Code, resp.Body.String())
	}
	resp = api.Get("/api/credentials/api-token/fetch", auth)
	if resp.Code != http.StatusOK {
		t.Fatalf("context-free fetch status = %v, body = %s", resp.Code, resp.Body.String())
	}
}

func TestCredentialSecretShapesEnforced(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	t.Setenv("CREDENTIALS_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	db, err := database.New()
	if err != nil {
		t.Fatalf("failed initializing database: %v", err)
	}
	wm := workflow.NewWorkflowManager(false, db)
	s := &Server{db: db, wm: wm}
	_, api := newTestAPI(t)
	addApiRoutes(api, s)
	auth := ensureAdminToken(t, db)

	bad := []map[string]any{
		// Raw non-JSON secret for a multi-field type.
		{"tenant_id": "shapeten", "name": "raw-pair", "type": "username_password", "secret": "just-a-string"},
		// JSON missing a required field.
		{"tenant_id": "shapeten", "name": "no-user", "type": "username_password", "secret": `{"password":"x"}`},
		{"tenant_id": "shapeten", "name": "no-key", "type": "aws", "secret": `{"access_key_id":"AKIAEXAMPLE"}`},
		// Structured path missing a required field.
		{"tenant_id": "shapeten", "name": "fields-nouser", "type": "username_password", "secret_fields": map[string]any{"password": "x"}},
		// Unknown structured field (typo guard).
		{"tenant_id": "shapeten", "name": "fields-typo", "type": "aws", "secret_fields": map[string]any{"access_key_id": "A", "secret_access_key": "B", "regoin": "eu-west-1"}},
	}
	for _, c := range bad {
		resp := api.Post("/api/credentials", auth, c)
		if resp.Code != http.StatusBadRequest && resp.Code != http.StatusUnprocessableEntity {
			t.Fatalf("create %v status = %v, want 400/422, body = %s", c, resp.Code, resp.Body.String())
		}
	}

	// Structured happy paths: multi-field JSON is assembled server-side.
	good := []map[string]any{
		{"tenant_id": "shapeten", "name": "pair", "type": "username_password", "secret_fields": map[string]any{"username": "svc", "password": "pw"}},
		{"tenant_id": "shapeten", "name": "key", "type": "ssh_key", "secret_fields": map[string]any{"private_key": "PEM-DATA"}},
		{"tenant_id": "shapeten", "name": "iam", "type": "aws", "secret_fields": map[string]any{"access_key_id": "AKIAEXAMPLE", "secret_access_key": "shh", "region": "eu-west-1"}},
	}
	for _, c := range good {
		resp := api.Post("/api/credentials", auth, c)
		if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
			t.Fatalf("create %v status = %v, body = %s", c, resp.Code, resp.Body.String())
		}
		if strings.Contains(resp.Body.String(), "PEM-DATA") || strings.Contains(resp.Body.String(), `"password":"pw"`) {
			t.Fatalf("create response leaks the secret: %s", resp.Body.String())
		}
	}
}
