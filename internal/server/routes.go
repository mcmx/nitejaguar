package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"

	"fmt"
	"log"
	"time"

	"github.com/mcmx/nitejaguar/cmd/web"
	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/ent"
	"github.com/mcmx/nitejaguar/internal/database"
	"github.com/mcmx/nitejaguar/internal/workflow"
	"go.jetify.com/typeid"

	"github.com/a-h/templ"
	"github.com/coder/websocket"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

type HealthResponse struct {
	Body struct {
		Database *database.HealthResponse `json:"database"`
	}
}

type WorkflowsResponse struct {
	Body struct {
		Workflows []*ent.Workflow `json:"workflows"`
	}
}

type ClientStatus struct {
	ID            string    `json:"client_id"`
	Name          string    `json:"name"`
	TenantID      string    `json:"tenant_id"`
	Tags          []string  `json:"tags"`
	Revoked       bool      `json:"revoked"`
	RegisteredAt  time.Time `json:"registered_at"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	LastPoll      time.Time `json:"last_poll"`
	Online        bool      `json:"online"`
}

type ClientsResponse struct {
	Body struct {
		Clients []ClientStatus `json:"clients"`
	}
}

func (s *Server) RegisterRoutes() http.Handler {
	e := echo.New()
	config := huma.DefaultConfig(
		"NiteJaguar API",
		"1.0.0",
	)
	//	config.DocsPath = "/docs"

	api := humaecho.NewV4(e, config)
	addApiRoutes(api, s)

	e.Use(middleware.RequestLogger())
	e.Use(middleware.Recover())
	// Enforce login for browser pages once users exist. API routes stay
	// under Huma auth (per-endpoint roles); static assets and /login stay
	// open so users can sign in.
	e.Use(s.requireWebLogin)
	e.Static("/assets", "cmd/web/assets")

	e.GET("/login", s.loginPage)
	e.POST("/login", s.loginSubmit)
	e.POST("/logout", s.logoutWeb)
	e.GET("/logout", s.logoutWeb)

	e.GET("/", s.workflowsPage)
	e.GET("/workflows/:id", s.workflowPage)
	e.GET("/designer", s.designerPage)
	e.GET("/designer/:id", s.designerEditPage)
	e.POST("/designer/save", s.designerSaveWorkflow)
	e.GET("/results", s.resultsPage)
	e.GET("/clients", s.clientsPage)
	e.GET("/audit", s.auditPage)
	e.GET("/users", s.usersPage)
	e.POST("/workflows/:id/enabled", s.setWorkflowEnabled)
	e.POST("/triggers/stop", s.TriggerWebHandler)

	e.GET("/websocket", s.websocketHandler)

	return e
}

func addApiRoutes(api huma.API, s *Server) {
	huma.Get(api, "/health", s.HealthHandler)

	apiGrp := huma.NewGroup(api, "/api")
	huma.Register(apiGrp, huma.Operation{
		OperationID: "get-workflows",
		Method:      http.MethodGet,
		Path:        "/workflows",
		Summary:     "All workflows",
	}, s.GetWorkflows)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "get-workflow",
		Method:      http.MethodGet,
		Path:        "/workflows/{id}",
		Summary:     "Workflow by id",
	}, s.GetWorkflow)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "post-event",
		Method:      http.MethodPost,
		Path:        "/events",
		Summary:     "Workflow events",
	}, s.WorkflowEvents)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "register-client",
		Method:      http.MethodPost,
		Path:        "/clients/register",
		Summary:     "Register a polling client",
	}, s.RegisterClient)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "client-heartbeat",
		Method:      http.MethodPost,
		Path:        "/clients/heartbeat",
		Summary:     "Client heartbeat",
	}, s.ClientHeartbeat)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "get-assignments",
		Method:      http.MethodGet,
		Path:        "/clients/{id}/assignments",
		Summary:     "Poll workflow/node assignments for a client",
	}, s.GetAssignments)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "post-result",
		Method:      http.MethodPost,
		Path:        "/results",
		Summary:     "Ingest a node result and advance the workflow",
	}, s.PostResult)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "get-clients",
		Method:      http.MethodGet,
		Path:        "/clients",
		Summary:     "Registered polling clients and connection status",
	}, s.GetClients)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "create-enrollment-token",
		Method:      http.MethodPost,
		Path:        "/enrollment/tokens",
		Summary:     "Create a tenant-scoped enrollment token (operator+)",
	}, s.CreateEnrollmentToken)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "list-enrollment-tokens",
		Method:      http.MethodGet,
		Path:        "/enrollment/tokens",
		Summary:     "List enrollment tokens (hashes never exposed, operator+)",
	}, s.ListEnrollmentTokens)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "revoke-enrollment-token",
		Method:      http.MethodPost,
		Path:        "/enrollment/tokens/{id}/revoke",
		Summary:     "Revoke an enrollment token to block new joins (operator+)",
	}, s.RevokeEnrollmentToken)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "revoke-client",
		Method:      http.MethodPost,
		Path:        "/clients/{id}/revoke",
		Summary:     "Revoke a client; its token stops authenticating (operator+)",
	}, s.RevokeClient)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "list-audit",
		Method:      http.MethodGet,
		Path:        "/audit",
		Summary:     "Audit trail for auth, enrollment, lifecycle, workflow and credential ops (viewer+)",
	}, s.ListAudit)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "import-workflow",
		Method:      http.MethodPost,
		Path:        "/workflows/import",
		Summary:     "Import a workflow definition verbatim, upsert (operator+, audited)",
	}, s.ImportWorkflow)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "clone-workflow",
		Method:      http.MethodPost,
		Path:        "/workflows/clone",
		Summary:     "Clone a workflow definition with fresh ids (operator+, audited)",
	}, s.CloneWorkflow)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "create-credential",
		Method:      http.MethodPost,
		Path:        "/credentials",
		Summary:     "Store a credential secret encrypted at rest (operator+)",
	}, s.CreateCredential)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "list-credentials",
		Method:      http.MethodGet,
		Path:        "/credentials",
		Summary:     "List credential metadata (secrets never exposed)",
	}, s.ListCredentials)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "get-credential",
		Method:      http.MethodGet,
		Path:        "/credentials/{id}",
		Summary:     "Credential metadata by id (secrets never exposed)",
	}, s.GetCredential)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "delete-credential",
		Method:      http.MethodDelete,
		Path:        "/credentials/{id}",
		Summary:     "Delete a stored credential (operator+)",
	}, s.DeleteCredential)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "fetch-credential",
		Method:      http.MethodGet,
		Path:        "/credentials/{ref}/fetch",
		Summary:     "Just-in-time secret fetch for the executing client (audited, short TTL)",
	}, s.FetchCredential)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "login",
		Method:      http.MethodPost,
		Path:        "/auth/login",
		Summary:     "Login with username/password and mint a session token",
	}, s.Login)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "logout",
		Method:      http.MethodPost,
		Path:        "/auth/logout",
		Summary:     "Revoke the current session token",
	}, s.Logout)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "me",
		Method:      http.MethodGet,
		Path:        "/auth/me",
		Summary:     "Current user profile for a session token",
	}, s.Me)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "create-user",
		Method:      http.MethodPost,
		Path:        "/users",
		Summary:     "Create a user (admin only; open for the very first admin)",
	}, s.CreateUser)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "list-users",
		Method:      http.MethodGet,
		Path:        "/users",
		Summary:     "List users (operator+; non-admins see their own tenant)",
	}, s.ListUsers)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "revoke-user",
		Method:      http.MethodPost,
		Path:        "/users/{id}/revoke",
		Summary:     "Revoke a user and its sessions (admin only)",
	}, s.RevokeUser)
}

type RegisterClientInput struct {
	Body struct {
		Name            string   `json:"name"`
		TenantID        string   `json:"tenant_id,omitempty"`
		Tags            []string `json:"tags,omitempty"`
		EnrollmentToken string   `json:"enrollment_token,omitempty"`
		JoinToken       string   `json:"join_token,omitempty"`
	}
}

type RegisterClientOutput struct {
	Body struct {
		ClientID string `json:"client_id"`
		Token    string `json:"token"`
	}
}

type HeartbeatInput struct {
	Authorization string `header:"Authorization"`
	Token         string `header:"X-Client-Token"`
	Body          struct {
		ClientID string `json:"client_id"`
	}
}

type HeartbeatOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// WorkflowDefinition is the public workflow shape returned to polling clients.
// It intentionally has a distinct name from ent.Workflow so Huma can register
// both schemas without a name collision.
type WorkflowDefinition struct {
	ID                string                   `json:"id"`
	Name              string                   `json:"name"`
	Revision          string                   `json:"revision"`
	DefaultClient     string                   `json:"default_client,omitempty"`
	DefaultClientTags []string                 `json:"default_client_tags,omitempty"`
	Nodes             map[string]workflow.Node `json:"nodes"`
}

// PendingAssignment is a server-persisted downstream node ready for its
// owner to execute. Payload carries the parent result as $input.
type PendingAssignment struct {
	ID             string `json:"id"`
	WorkflowID     string `json:"workflow_id"`
	ExecutionID    string `json:"execution_id"`
	NodeID         string `json:"node_id"`
	ParentActionID string `json:"parent_action_id,omitempty"`
	Payload        any    `json:"payload,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type AssignmentsInput struct {
	ID            string `path:"id"`
	Authorization string `header:"Authorization"`
	Token         string `header:"X-Client-Token"`
}

type AssignmentsOutput struct {
	Body struct {
		ClientID  string               `json:"client_id"`
		Workflows []WorkflowDefinition `json:"workflows"`
		Pending   []PendingAssignment  `json:"pending,omitempty"`
	}
}

type PostResultInput struct {
	Authorization string `header:"Authorization"`
	Token         string `header:"X-Client-Token"`
	Body          struct {
		ResultID    string    `json:"result_id,omitempty"`
		WorkflowID  string    `json:"workflow_id,omitempty"`
		ExecutionID string    `json:"execution_id,omitempty"`
		ActionID    string    `json:"action_id,omitempty"`
		ActionType  string    `json:"action_type,omitempty"`
		ActionName  string    `json:"action_name,omitempty"`
		ExecutorID  string    `json:"executor_id,omitempty"`
		TenantID    string    `json:"tenant_id,omitempty"`
		CreatedAt   time.Time `json:"created_at,omitempty"`
		Payload     any       `json:"payload,omitempty"`
	}
}

type postResultRecord struct {
	workflowID, executionID string
	nexts                   []string
}

type PostResultOutput struct {
	Body struct {
		WorkflowID  string   `json:"workflow_id"`
		ExecutionID string   `json:"execution_id"`
		Nexts       []string `json:"nexts"`
	}
}

func (s *Server) RegisterClient(_ context.Context, input *RegisterClientInput) (*RegisterClientOutput, error) {
	if input.Body.Name == "" {
		return nil, huma.Error400BadRequest("name is required")
	}
	joinToken := input.Body.EnrollmentToken
	if joinToken == "" {
		joinToken = input.Body.JoinToken
	}
	if joinToken == "" {
		return nil, huma.Error401Unauthorized("enrollment token is required")
	}
	c, token, err := s.registry().register(input.Body.Name, input.Body.Tags, joinToken)
	if err != nil {
		return nil, huma.Error401Unauthorized(err.Error())
	}
	// Tenant comes from the enrollment token; a client-supplied tenant_id
	// is ignored and never honored.
	if input.Body.TenantID != "" && input.Body.TenantID != c.TenantID {
		log.Printf("client registration ignored mismatched tenant_id: supplied=%q token-tenant=%q id=%s",
			input.Body.TenantID, c.TenantID, c.ID)
	}
	log.Printf("client registered: id=%s name=%q tenant=%s tags=%v", c.ID, c.Name, c.TenantID, c.Tags)
	return &RegisterClientOutput{
		Body: struct {
			ClientID string `json:"client_id"`
			Token    string `json:"token"`
		}{ClientID: c.ID, Token: token},
	}, nil
}

func (s *Server) ClientHeartbeat(_ context.Context, input *HeartbeatInput) (*HeartbeatOutput, error) {
	if !s.registry().authenticate(input.Body.ClientID, bearerToken(input.Authorization, input.Token)) {
		return nil, huma.Error401Unauthorized("invalid or missing client token")
	}
	if input.Body.ClientID == "" {
		return nil, huma.Error400BadRequest("client_id is required")
	}
	c, ok := s.registry().heartbeat(input.Body.ClientID)
	if !ok {
		return nil, huma.Error404NotFound("client not found")
	}
	log.Printf("client heartbeat: id=%s name=%q", c.ID, c.Name)
	return &HeartbeatOutput{
		Body: struct {
			Ok bool `json:"ok"`
		}{Ok: true},
	}, nil
}

func (s *Server) GetAssignments(_ context.Context, input *AssignmentsInput) (*AssignmentsOutput, error) {
	if !s.registry().authenticate(input.ID, bearerToken(input.Authorization, input.Token)) {
		return nil, huma.Error401Unauthorized("invalid or missing client token")
	}
	client, ok := s.registry().poll(input.ID)
	if !ok {
		return nil, huma.Error404NotFound("client not found")
	}
	log.Printf("client assignment poll: id=%s name=%q", client.ID, client.Name)
	// Remote clients should only receive workflows that are currently enabled.
	rows, err := s.db.GetWorkflows(false, true)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to get workflows")
	}
	workflows := []WorkflowDefinition{}
	// Keep full definitions for pending filtering below.
	defsByID := make(map[string]workflow.Workflow, len(rows))
	for _, row := range rows {
		// Tenant isolation check
		if row.TenantID != "" && row.TenantID != "default" && client.TenantID != "" && client.TenantID != "default" && row.TenantID != client.TenantID {
			continue
		}
		var def workflow.Workflow
		if err := json.Unmarshal([]byte(row.JSONDefinition), &def); err != nil {
			continue
		}
		defsByID[def.Id] = def
		if def.Id == "" {
			defsByID[row.ID] = def
		}
		filtered := make(map[string]workflow.Node, len(def.Nodes))
		for id, n := range def.Nodes {
			if def.NodeAssignedTo(n, client.ID, client.Tags) {
				filtered[id] = n
			}
		}
		if len(filtered) == 0 {
			continue
		}
		def.Nodes = filtered
		workflows = append(workflows, WorkflowDefinition{
			ID:                def.Id,
			Name:              def.Name,
			Revision:          row.Revision,
			DefaultClient:     def.DefaultClient,
			DefaultClientTags: def.DefaultClientTags,
			Nodes:             def.Nodes,
		})
	}
	// Pending cross-client handoffs: only the owner's nodes, same tenant.
	pending := []PendingAssignment{}
	if assignRows, err := s.db.ListPendingAssignments(); err == nil {
		for _, a := range assignRows {
			if a.TenantID != "" && a.TenantID != "default" && client.TenantID != "" && client.TenantID != "default" && a.TenantID != client.TenantID {
				continue
			}
			def, ok := defsByID[a.WorkflowID]
			if !ok {
				// Workflow disabled/unknown: skip stale assignment.
				continue
			}
			n, ok := def.Nodes[a.NodeID]
			if !ok {
				// Node removed from the definition: complete it so it
				// does not linger forever.
				_ = s.db.CompleteNodeAssignment(a.WorkflowID, a.ExecutionID, a.NodeID)
				continue
			}
			if !def.NodeAssignedTo(n, client.ID, client.Tags) {
				continue
			}
			var payload any
			if a.PayloadJSON != "" {
				_ = json.Unmarshal([]byte(a.PayloadJSON), &payload)
			}
			pending = append(pending, PendingAssignment{
				ID:             a.ID,
				WorkflowID:     a.WorkflowID,
				ExecutionID:    a.ExecutionID,
				NodeID:         a.NodeID,
				ParentActionID: a.ParentActionID,
				Payload:        payload,
				CreatedAt:      a.CreatedAt,
			})
		}
	}
	return &AssignmentsOutput{
		Body: struct {
			ClientID  string               `json:"client_id"`
			Workflows []WorkflowDefinition `json:"workflows"`
			Pending   []PendingAssignment  `json:"pending,omitempty"`
		}{ClientID: client.ID, Workflows: workflows, Pending: pending},
	}, nil
}

func (s *Server) GetClients(_ context.Context, _ *struct{}) (*ClientsResponse, error) {
	now := time.Now()
	clients := s.registry().list()
	out := make([]ClientStatus, 0, len(clients))
	for _, c := range clients {
		out = append(out, ClientStatus{
			ID: c.ID, Name: c.Name, TenantID: c.TenantID, Tags: c.Tags, Revoked: c.Revoked,
			RegisteredAt: c.RegisteredAt,
			LastHeartbeat: c.LastHeartbeat, LastPoll: c.LastPoll,
			Online: !c.Revoked && now.Sub(c.LastHeartbeat) <= 15*time.Second,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return &ClientsResponse{Body: struct {
		Clients []ClientStatus `json:"clients"`
	}{Clients: out}}, nil
}

type CreateEnrollmentTokenInput struct {
	Authorization string `header:"Authorization"`
	SessionToken  string `header:"X-Auth-Token"`
	Body          struct {
		TenantID       string `json:"tenant_id,omitempty"`
		Label          string `json:"label,omitempty"`
		ExpiresInHours *int   `json:"expires_in_hours,omitempty"`
		MaxUses        *int   `json:"max_uses,omitempty"`
	}
}

type EnrollmentTokenView struct {
	ID        string     `json:"id"`
	TenantID  string     `json:"tenant_id"`
	Label     string     `json:"label"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	MaxUses   int        `json:"max_uses"`
	UseCount  int        `json:"use_count"`
	Revoked   bool       `json:"revoked"`
	CreatedAt time.Time  `json:"created_at"`
}

type CreateEnrollmentTokenOutput struct {
	Body struct {
		EnrollmentTokenView
		Token string `json:"token"`
	}
}

type ListEnrollmentTokensInput struct {
	Authorization string `header:"Authorization"`
	SessionToken  string `header:"X-Auth-Token"`
}

type ListEnrollmentTokensOutput struct {
	Body struct {
		Tokens []EnrollmentTokenView `json:"tokens"`
	}
}

type RevokeEnrollmentTokenInput struct {
	Authorization string `header:"Authorization"`
	SessionToken  string `header:"X-Auth-Token"`
	ID            string `path:"id"`
}

type RevokeEnrollmentTokenOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

type RevokeClientInput struct {
	Authorization string `header:"Authorization"`
	SessionToken  string `header:"X-Auth-Token"`
	ID            string `path:"id"`
}

type RevokeClientOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

type AuditEntry struct {
	ID        string    `json:"id"`
	Action    string    `json:"action"`
	TenantID  string    `json:"tenant_id"`
	Actor     string    `json:"actor"`
	Target    string    `json:"target"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"created_at"`
}

type ListAuditInput struct {
	Authorization string `header:"Authorization"`
	SessionToken  string `header:"X-Auth-Token"`
	Limit         int    `query:"limit"`
}

type ListAuditOutput struct {
	Body struct {
		Entries []AuditEntry `json:"entries"`
	}
}

// CreateEnrollmentToken mints a tenant-scoped join token. The plaintext is
// returned once; only its hash is stored. Requires operator+ (open-bootstrap
// mode allows issuance until the first user exists).
func (s *Server) CreateEnrollmentToken(_ context.Context, input *CreateEnrollmentTokenInput) (*CreateEnrollmentTokenOutput, error) {
	caller, actor, err := s.requireRole(input.Authorization, input.SessionToken, database.RoleOperator)
	if err != nil {
		return nil, err
	}
	tenantID, err := scopedTenant(caller, input.Body.TenantID)
	if err != nil {
		return nil, err
	}
	var expiresAt *time.Time
	if input.Body.ExpiresInHours != nil {
		if *input.Body.ExpiresInHours <= 0 {
			return nil, huma.Error400BadRequest("expires_in_hours must be positive")
		}
		t := time.Now().Add(time.Duration(*input.Body.ExpiresInHours) * time.Hour)
		expiresAt = &t
	}
	maxUses := 0
	if input.Body.MaxUses != nil {
		if *input.Body.MaxUses < 0 {
			return nil, huma.Error400BadRequest("max_uses cannot be negative")
		}
		maxUses = *input.Body.MaxUses
	}
	tok, plaintext, err := s.db.CreateEnrollmentToken(tenantID, input.Body.Label, expiresAt, maxUses)
	if err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}
	_ = s.db.LogAudit("enrollment.create", tenantID, actor, tok.ID, "label="+input.Body.Label)
	log.Printf("enrollment token created: id=%s tenant=%s max_uses=%d", tok.ID, tok.TenantID, tok.MaxUses)
	out := &CreateEnrollmentTokenOutput{}
	out.Body.ID = tok.ID
	out.Body.TenantID = tok.TenantID
	out.Body.Label = tok.Label
	out.Body.ExpiresAt = tok.ExpiresAt
	out.Body.MaxUses = tok.MaxUses
	out.Body.UseCount = tok.UseCount
	out.Body.Revoked = tok.Revoked
	out.Body.CreatedAt = tok.CreatedAt
	out.Body.Token = plaintext
	return out, nil
}

func (s *Server) ListEnrollmentTokens(_ context.Context, input *ListEnrollmentTokensInput) (*ListEnrollmentTokensOutput, error) {
	caller, _, err := s.requireRole(input.Authorization, input.SessionToken, database.RoleOperator)
	if err != nil {
		return nil, err
	}
	toks, err := s.db.ListEnrollmentTokens()
	if err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}
	out := &ListEnrollmentTokensOutput{}
	for _, tok := range toks {
		if caller != nil && caller.Role != database.RoleAdmin && tok.TenantID != caller.TenantID {
			continue
		}
		out.Body.Tokens = append(out.Body.Tokens, EnrollmentTokenView{
			ID: tok.ID, TenantID: tok.TenantID, Label: tok.Label,
			ExpiresAt: tok.ExpiresAt, MaxUses: tok.MaxUses, UseCount: tok.UseCount,
			Revoked: tok.Revoked, CreatedAt: tok.CreatedAt,
		})
	}
	if out.Body.Tokens == nil {
		out.Body.Tokens = []EnrollmentTokenView{}
	}
	return out, nil
}

func (s *Server) RevokeEnrollmentToken(_ context.Context, input *RevokeEnrollmentTokenInput) (*RevokeEnrollmentTokenOutput, error) {
	caller, actor, err := s.requireRole(input.Authorization, input.SessionToken, database.RoleOperator)
	if err != nil {
		return nil, err
	}
	if input.ID == "" {
		return nil, huma.Error400BadRequest("id is required")
	}
	toks, err := s.db.ListEnrollmentTokens()
	if err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}
	tenantID := "default"
	for _, tok := range toks {
		if tok.ID == input.ID {
			tenantID = tok.TenantID
		}
	}
	if caller != nil && caller.Role != database.RoleAdmin && tenantID != caller.TenantID {
		return nil, huma.Error403Forbidden("cross-tenant operation requires admin")
	}
	if err := s.db.RevokeEnrollmentToken(input.ID); err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	_ = s.db.LogAudit("enrollment.revoke", tenantID, actor, input.ID, "revoked via API")
	log.Printf("enrollment token revoked: id=%s", input.ID)
	out := &RevokeEnrollmentTokenOutput{}
	out.Body.Ok = true
	return out, nil
}

func (s *Server) RevokeClient(_ context.Context, input *RevokeClientInput) (*RevokeClientOutput, error) {
	caller, actor, err := s.requireRole(input.Authorization, input.SessionToken, database.RoleOperator)
	if err != nil {
		return nil, err
	}
	if input.ID == "" {
		return nil, huma.Error400BadRequest("id is required")
	}
	tenantID := "default"
	if c, ok := s.registry().getClient(input.ID); ok {
		tenantID = c.TenantID
	}
	if caller != nil && caller.Role != database.RoleAdmin && tenantID != caller.TenantID {
		return nil, huma.Error403Forbidden("cross-tenant operation requires admin")
	}
	if err := s.db.RevokeClient(input.ID); err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	_ = s.db.LogAudit("client.revoke", tenantID, actor, input.ID, "revoked via API")
	log.Printf("client revoked: id=%s", input.ID)
	out := &RevokeClientOutput{}
	out.Body.Ok = true
	return out, nil
}

func (s *Server) ListAudit(_ context.Context, input *ListAuditInput) (*ListAuditOutput, error) {
	caller, _, err := s.requireRole(input.Authorization, input.SessionToken, database.RoleViewer)
	if err != nil {
		return nil, err
	}
	limit := input.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	logs, err := s.db.ListAuditLogs(limit)
	if err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}
	out := &ListAuditOutput{}
	for _, l := range logs {
		if caller != nil && caller.Role != database.RoleAdmin && l.TenantID != caller.TenantID {
			continue
		}
		out.Body.Entries = append(out.Body.Entries, AuditEntry{
			ID: l.ID, Action: l.Action, TenantID: l.TenantID,
			Actor: l.Actor, Target: l.Target, Detail: l.Detail,
			CreatedAt: l.CreatedAt,
		})
	}
	if out.Body.Entries == nil {
		out.Body.Entries = []AuditEntry{}
	}
	return out, nil
}

func (s *Server) PostResult(_ context.Context, input *PostResultInput) (*PostResultOutput, error) {
	executorID, ok := s.registry().identity(bearerToken(input.Authorization, input.Token))
	if !ok {
		return nil, huma.Error401Unauthorized("invalid or missing client token")
	}
	if input.Body.ActionID == "" {
		return nil, huma.Error400BadRequest("action_id is required")
	}
	log.Printf("client result received: action=%s name=%q", input.Body.ActionID, input.Body.ActionName)
	result := common.ResultData{
		ResultID:    input.Body.ResultID,
		WorkflowID:  input.Body.WorkflowID,
		ExecutionID: input.Body.ExecutionID,
		ActionID:    input.Body.ActionID,
		ActionType:  input.Body.ActionType,
		ActionName:  input.Body.ActionName,
		ExecutorID:  executorID,
		TenantID:    input.Body.TenantID,
		CreatedAt:   input.Body.CreatedAt,
		Payload:     input.Body.Payload,
	}
	if result.ResultID == "" {
		id, _ := typeid.WithPrefix("result")
		result.ResultID = id.String()
	}
	s.resultMu.Lock()
	defer s.resultMu.Unlock()
	if s.results == nil {
		s.results = make(map[string]postResultRecord)
	}
	if prior, ok := s.results[result.ResultID]; ok {
		out := &PostResultOutput{}
		out.Body.WorkflowID, out.Body.ExecutionID, out.Body.Nexts = prior.workflowID, prior.executionID, append([]string(nil), prior.nexts...)
		return out, nil
	}
	stored, nexts, err := s.wm.IngestResult(result)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	// Cross-client handoff fix: route foreign nexts via persisted
	// assignments instead of direct local execution. Return only the
	// nexts owned by the reporting client; the rest are picked up via
	// assignment polling. Local server execution (best-effort in
	// IngestResult) is unchanged.
	owned := s.filterNextsForExecutor(stored.WorkflowID, nexts, executorID)
	_ = s.db.LogAudit("assignment.complete", stored.TenantID, executorID, stored.ActionID, "execution="+stored.ExecutionID+" workflow="+stored.WorkflowID)
	s.results[result.ResultID] = postResultRecord{stored.WorkflowID, stored.ExecutionID, append([]string(nil), owned...)}
	out := &PostResultOutput{}
	out.Body.WorkflowID = stored.WorkflowID
	out.Body.ExecutionID = stored.ExecutionID
	out.Body.Nexts = owned
	return out, nil
}

// filterNextsForExecutor keeps only the downstream nodes assigned to the
// reporting client (workflow defaults applied). Unknown workflows or nodes
// are kept so older definitions without targeting still execute locally.
func (s *Server) filterNextsForExecutor(workflowID string, nexts []string, executorID string) []string {
	if len(nexts) == 0 {
		return []string{}
	}
	var tags []string
	if c, ok := s.registry().getClient(executorID); ok {
		tags = c.Tags
	}
	def, err := s.loadWorkflowDef(workflowID)
	if err != nil || def == nil {
		return nexts
	}
	owned := make([]string, 0, len(nexts))
	for _, id := range nexts {
		n, ok := def.Nodes[id]
		if !ok {
			owned = append(owned, id)
			continue
		}
		if def.NodeAssignedTo(n, executorID, tags) {
			owned = append(owned, id)
		}
	}
	if owned == nil {
		owned = []string{}
	}
	return owned
}

func (s *Server) loadWorkflowDef(workflowID string) (*workflow.Workflow, error) {
	if workflowID == "" || s.db == nil {
		return nil, fmt.Errorf("workflow not found")
	}
	row, err := s.db.GetWorkflow(workflowID)
	if err != nil {
		return nil, err
	}
	var def workflow.Workflow
	if err := json.Unmarshal([]byte(row.JSONDefinition), &def); err != nil {
		return nil, err
	}
	return &def, nil
}

// WorkflowUpsertBody mirrors workflow.Workflow for the import/clone
// endpoints. It intentionally has a distinct Go type name from both
// workflow.Workflow and ent.Workflow so Huma can register all schemas
// without a name collision (see WorkflowDefinition above).
type WorkflowUpsertBody struct {
	ID                string                   `json:"id"`
	Name              string                   `json:"name"`
	TenantID          string                   `json:"tenant_id,omitempty"`
	DefaultClient     string                   `json:"default_client,omitempty"`
	DefaultClientTags []string                 `json:"default_client_tags,omitempty"`
	Nodes             map[string]workflow.Node `json:"nodes"`
}

// UpsertWorkflowInput carries a full workflow definition for the
// import/clone endpoints.
type UpsertWorkflowInput struct {
	Authorization string `header:"Authorization"`
	SessionToken  string `header:"X-Auth-Token"`
	Body          WorkflowUpsertBody
}

type UpsertWorkflowOutput struct {
	Body struct {
		Ok         bool   `json:"ok"`
		WorkflowID string `json:"workflow_id"`
	}
}

// ImportWorkflow saves a workflow definition verbatim (upsert),
// keeping the ids and name from the JSON. Requires operator+.
func (s *Server) ImportWorkflow(_ context.Context, input *UpsertWorkflowInput) (*UpsertWorkflowOutput, error) {
	caller, actor, err := s.requireRole(input.Authorization, input.SessionToken, database.RoleOperator)
	if err != nil {
		return nil, err
	}
	tenantID, err := scopedTenant(caller, input.Body.TenantID)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(workflow.Workflow{
		Id:                input.Body.ID,
		Name:              input.Body.Name,
		TenantID:          tenantID,
		DefaultClient:     input.Body.DefaultClient,
		DefaultClientTags: input.Body.DefaultClientTags,
		Nodes:             input.Body.Nodes,
	})
	if err != nil {
		return nil, huma.Error400BadRequest("invalid workflow definition")
	}
	id, err := s.wm.ImportWorkflowJSON(string(raw))
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	_ = s.db.LogAudit("workflow.import", tenantID, actor, id, "name="+input.Body.Name)
	log.Printf("workflow imported: id=%s tenant=%s actor=%s", id, tenantID, actor)
	out := &UpsertWorkflowOutput{}
	out.Body.Ok = true
	out.Body.WorkflowID = id
	return out, nil
}

// CloneWorkflow saves an independent copy of a workflow definition with
// fresh ids, rewritten edges, and a "Clone of: " name prefix.
// Requires operator+.
func (s *Server) CloneWorkflow(_ context.Context, input *UpsertWorkflowInput) (*UpsertWorkflowOutput, error) {
	caller, actor, err := s.requireRole(input.Authorization, input.SessionToken, database.RoleOperator)
	if err != nil {
		return nil, err
	}
	tenantID, err := scopedTenant(caller, input.Body.TenantID)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(workflow.Workflow{
		Id:                input.Body.ID,
		Name:              input.Body.Name,
		TenantID:          tenantID,
		DefaultClient:     input.Body.DefaultClient,
		DefaultClientTags: input.Body.DefaultClientTags,
		Nodes:             input.Body.Nodes,
	})
	if err != nil {
		return nil, huma.Error400BadRequest("invalid workflow definition")
	}
	id, err := s.wm.CloneWorkflowJSON(string(raw))
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	_ = s.db.LogAudit("workflow.clone", tenantID, actor, id, "name="+input.Body.Name)
	log.Printf("workflow cloned: id=%s tenant=%s actor=%s", id, tenantID, actor)
	out := &UpsertWorkflowOutput{}
	out.Body.Ok = true
	out.Body.WorkflowID = id
	return out, nil
}

func (s *Server) workflowsPage(c echo.Context) error {
	workflows, err := s.db.GetWorkflows(true, true)
	data := &web.WorkflowPageData{Workflows: workflows}
	if err != nil {
		data.Error = "Unable to load workflows"
	}
	templ.Handler(web.WorkflowsPage(data, "")).ServeHTTP(c.Response(), c.Request())
	return nil
}

func (s *Server) workflowPage(c echo.Context) error {
	row, err := s.db.GetWorkflow(c.Param("id"))
	data := &web.WorkflowDetailData{Workflow: row}
	if err != nil {
		data.Error = "Workflow not found"
	} else if err := json.Unmarshal([]byte(row.JSONDefinition), &data.Definition); err != nil {
		data.Error = "Workflow definition is invalid"
	}
	templ.Handler(web.WorkflowDetailPage(data)).ServeHTTP(c.Response(), c.Request())
	return nil
}

func (s *Server) resultsPage(c echo.Context) error {
	data := &web.ResultsPageData{}
	entries, err := os.ReadDir("./results")
	if err != nil && !os.IsNotExist(err) {
		data.Error = "Unable to read results"
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		name := entry.Name()
		if filepath.Base(name) != name {
			continue
		}
		contents, readErr := os.ReadFile(filepath.Join("./results", name))
		if readErr != nil {
			continue
		}
		var result common.ResultData
		if json.Unmarshal(contents, &result) == nil {
			data.Results = append(data.Results, result)
		}
	}
	sort.Slice(data.Results, func(i, j int) bool { return data.Results[i].CreatedAt.After(data.Results[j].CreatedAt) })
	templ.Handler(web.ResultsPage(data)).ServeHTTP(c.Response(), c.Request())
	return nil
}

func (s *Server) clientsPage(c echo.Context) error {
	data := &web.ClientsPageData{}
	for _, client := range s.registry().list() {
		data.Clients = append(data.Clients, web.ClientView{
			ID: client.ID, Name: client.Name, TenantID: client.TenantID, Tags: client.Tags,
			RegisteredAt:  client.RegisteredAt.Format("2006-01-02 15:04:05 MST"),
			LastHeartbeat: client.LastHeartbeat.Format("2006-01-02 15:04:05 MST"),
			LastPoll:      client.LastPoll.Format("2006-01-02 15:04:05 MST"),
			Online:        !client.Revoked && time.Since(client.LastHeartbeat) <= 15*time.Second,
			Revoked:       client.Revoked,
		})
	}
	sort.Slice(data.Clients, func(i, j int) bool { return data.Clients[i].Name < data.Clients[j].Name })
	templ.Handler(web.ClientsPage(data)).ServeHTTP(c.Response(), c.Request())
	return nil
}

func (s *Server) setWorkflowEnabled(c echo.Context) error {
	if _, _, err := s.webActor(c, database.RoleOperator); err != nil {
		if he, ok := err.(*echo.HTTPError); ok && he.Code == http.StatusUnauthorized {
			return c.Redirect(http.StatusSeeOther, "/login")
		}
		return err
	}
	enabled := c.FormValue("enabled") == "true"
	if err := s.db.SetWorkflowEnabled(c.Param("id"), enabled); err != nil {
		return c.String(http.StatusNotFound, "workflow not found")
	}
	actor := "api"
	if user, open := s.webCurrentUser(c); !open && user != nil {
		actor = user.ID
	}
	tenantID := "default"
	if row, err := s.db.GetWorkflow(c.Param("id")); err == nil {
		var def workflow.Workflow
		if json.Unmarshal([]byte(row.JSONDefinition), &def) == nil && def.TenantID != "" {
			tenantID = def.TenantID
		} else if row.TenantID != "" {
			tenantID = row.TenantID
		}
	}
	_ = s.db.LogAudit("workflow.enable", tenantID, actor, c.Param("id"), fmt.Sprintf("enabled=%t via web", enabled))
	return c.Redirect(http.StatusSeeOther, "/workflows/"+c.Param("id"))
}

func (s *Server) TriggerWebHandler(c echo.Context) error {
	value := c.FormValue("id")
	if value == "" {
		value = c.FormValue("name")
	}
	if value == "" {
		return c.JSON(http.StatusBadRequest, "Missing id or name")
	}
	fmt.Println("Form value Stopping Trigger:", value)
	t := s.wm.GetTriggerManager()
	id := value
	if resolved, ok := t.FindTriggerIDByName(value); ok {
		id = resolved
	}
	if err := t.RemoveTrigger(id); err != nil {
		return c.JSON(http.StatusNotFound, "Trigger not found")
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "trigger stopped", "id": id})
}

func (s *Server) GetWorkflows(c context.Context, input *struct{}) (*WorkflowsResponse, error) {
	workflows, err := s.db.GetWorkflows(true, true)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to get workflows")
	}
	return &WorkflowsResponse{
		Body: struct {
			Workflows []*ent.Workflow `json:"workflows"`
		}{
			Workflows: workflows,
		},
	}, nil
}

func (s *Server) GetWorkflow(c context.Context, input *struct {
	ID string `path:"id"`
}) (*struct {
	Body struct {
		Workflow *ent.Workflow `json:"workflow"`
	}
}, error) {
	fmt.Println("GetWorkflow", input)
	workflow, err := s.db.GetWorkflow(input.ID)
	if err != nil {
		return nil, huma.Error404NotFound("workflow not found")
	}
	return &struct {
		Body struct {
			Workflow *ent.Workflow `json:"workflow"`
		}
	}{
		Body: struct {
			Workflow *ent.Workflow `json:"workflow"`
		}{
			Workflow: workflow,
		},
	}, nil
}

func (s *Server) WorkflowEvents(c context.Context, input *struct{}) (*WorkflowsResponse, error) {
	workflows, err := s.db.GetWorkflows(true, true)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to get workflows")
	}
	return &WorkflowsResponse{
		Body: struct {
			Workflows []*ent.Workflow `json:"workflows"`
		}{
			Workflows: workflows,
		},
	}, nil
}

func (s *Server) HealthHandler(c context.Context, input *struct{}) (*HealthResponse, error) {
	return &HealthResponse{
		Body: struct {
			Database *database.HealthResponse `json:"database"`
		}{
			Database: s.db.Health(),
		},
	}, nil
}

func (s *Server) websocketHandler(c echo.Context) error {
	w := c.Response().Writer
	r := c.Request()
	socket, err := websocket.Accept(w, r, &websocket.AcceptOptions{})

	if err != nil {
		log.Printf("could not open websocket: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("could not open websocket"))
		return err
	}

	defer func() {
		err := socket.Close(websocket.StatusGoingAway, "server closing websocket")
		if err != nil {
			log.Printf("Error closing WebSocket: %v", err)
		}
	}()

	ctx := r.Context()
	socketCtx := socket.CloseRead(ctx)

	for {
		payload := fmt.Sprintf("server timestamp: %d", time.Now().UnixNano())
		err := socket.Write(socketCtx, websocket.MessageText, []byte(payload))
		if err != nil {
			break
		}
		time.Sleep(time.Second * 2)
	}
	return nil
}

func (s *Server) designerPage(c echo.Context) error {
	return s.renderDesigner(c, c.QueryParam("workflow_id"))
}

func (s *Server) designerEditPage(c echo.Context) error {
	id := c.Param("id")
	if id == "" {
		id = c.QueryParam("workflow_id")
	}
	return s.renderDesigner(c, id)
}

func (s *Server) renderDesigner(c echo.Context, workflowID string) error {
	data := &web.DesignerPageData{}
	if workflowID == "" {
		workflowID = c.QueryParam("id")
	}
	workflows, err := s.db.GetWorkflows(true, true)
	if err == nil {
		data.Workflows = workflows
	}
	if workflowID != "" {
		row, err := s.db.GetWorkflow(workflowID)
		if err != nil {
			data.Error = "Workflow not found"
		} else {
			var def workflow.Workflow
			if err := json.Unmarshal([]byte(row.JSONDefinition), &def); err != nil {
				data.Error = "Workflow definition is invalid"
			} else {
				data.Init = web.DesignerInitFromWorkflow(def)
				data.EditWorkflowID = workflowID
			}
		}
	}
	data.InitJSON = web.DesignerInitJSON(data.Init)
	templ.Handler(web.DesignerPage(data)).ServeHTTP(c.Response(), c.Request())
	return nil
}

func (s *Server) designerSaveWorkflow(c echo.Context) error {
	if _, _, err := s.webActor(c, database.RoleOperator); err != nil {
		if he, ok := err.(*echo.HTTPError); ok && he.Code == http.StatusUnauthorized {
			return c.Redirect(http.StatusSeeOther, "/login")
		}
		return err
	}
	jsonDef := c.FormValue("workflow_json")
	if jsonDef == "" {
		return c.String(http.StatusBadRequest, "workflow_json is required")
	}
	if _, err := s.wm.ImportWorkflowJSON(jsonDef); err != nil {
		return c.String(http.StatusBadRequest, "Failed to import workflow: "+err.Error())
	}
	var wf workflow.Workflow
	actor := "api"
	if user, open := s.webCurrentUser(c); !open && user != nil {
		actor = user.ID
	}
	tenantID := "default"
	if err := json.Unmarshal([]byte(jsonDef), &wf); err == nil {
		if wf.TenantID != "" {
			tenantID = wf.TenantID
		}
		_ = s.db.LogAudit("workflow.import", tenantID, actor, wf.Id, "name="+wf.Name+" via designer")
		if wf.Id != "" {
			return c.Redirect(http.StatusSeeOther, "/workflows/"+wf.Id)
		}
	}
	return c.Redirect(http.StatusSeeOther, "/")
}
