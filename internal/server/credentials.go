package server

import (
	"context"
	"log"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/mcmx/nitejaguar/internal/database"
)

// CredentialView is credential metadata. The secret (plaintext or encrypted)
// is never exposed outside the just-in-time fetch endpoint.
type CredentialView struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenant_id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Scope       string `json:"scope"`
	OwnerID     string `json:"owner_id,omitempty"`
	Description string `json:"description,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func toCredentialView(id, tenantID, name, credType, scope, ownerID, description, createdAt, updatedAt string) CredentialView {
	return CredentialView{
		ID: id, TenantID: tenantID, Name: name, Type: credType,
		Scope: scope, OwnerID: ownerID, Description: description,
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
}

type CreateCredentialInput struct {
	Authorization string `header:"Authorization"`
	SessionToken  string `header:"X-Auth-Token"`
	Body          struct {
		TenantID    string `json:"tenant_id,omitempty"`
		Name        string `json:"name"`
		Type        string `json:"type,omitempty"`
		Scope       string `json:"scope,omitempty"`
		OwnerID     string `json:"owner_id,omitempty"`
		Secret      string `json:"secret"`
		Description string `json:"description,omitempty"`
	}
}

type CreateCredentialOutput struct {
	Body struct {
		CredentialView
	}
}

type ListCredentialsInput struct {
	TenantID string `query:"tenant_id"`
}

type ListCredentialsOutput struct {
	Body struct {
		Credentials []CredentialView `json:"credentials"`
	}
}

type GetCredentialInput struct {
	ID string `path:"id"`
}

type GetCredentialOutput struct {
	Body struct {
		CredentialView
	}
}

type DeleteCredentialInput struct {
	Authorization string `header:"Authorization"`
	SessionToken  string `header:"X-Auth-Token"`
	ID            string `path:"id"`
}

type DeleteCredentialOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

type FetchCredentialInput struct {
	Ref           string `path:"ref"`
	Authorization string `header:"Authorization"`
	Token         string `header:"X-Client-Token"`
	UserID        string `query:"user_id"`
	Groups        string `query:"groups"`
	WorkflowID    string `query:"workflow_id"`
	NodeID        string `query:"node_id"`
}

type FetchCredentialOutput struct {
	Body struct {
		CredentialID string `json:"credential_id"`
		Name         string `json:"name"`
		Type         string `json:"type"`
		Secret       string `json:"secret"`
		TTLSeconds   int    `json:"ttl_seconds"`
	}
}

// CreateCredential stores a secret encrypted at rest. The plaintext is
// accepted once and never returned; only metadata is exposed.
// Requires operator+ (open-bootstrap mode allows creation until the first
// user exists).
func (s *Server) CreateCredential(_ context.Context, input *CreateCredentialInput) (*CreateCredentialOutput, error) {
	caller, actor, err := s.requireRole(input.Authorization, input.SessionToken, database.RoleOperator)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Body.Name) == "" {
		return nil, huma.Error400BadRequest("name is required")
	}
	if input.Body.Secret == "" {
		return nil, huma.Error400BadRequest("secret is required")
	}
	tenantID, err := scopedTenant(caller, input.Body.TenantID)
	if err != nil {
		return nil, err
	}
	row, err := s.db.CreateCredential(tenantID, input.Body.Name, input.Body.Type, input.Body.Scope, input.Body.OwnerID, input.Body.Secret, input.Body.Description)
	if err != nil {
		// Duplicate names and validation errors are client errors; only
		// storage/crypto failures are 500s. Distinguish by message prefix.
		if strings.HasPrefix(err.Error(), "failed to ") {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		return nil, huma.Error400BadRequest(err.Error())
	}
	_ = s.db.LogAudit("credential.create", row.TenantID, actor, row.ID, "name="+row.Name+" scope="+row.Scope+" type="+row.Type)
	log.Printf("credential created: id=%s tenant=%s name=%q scope=%s", row.ID, row.TenantID, row.Name, row.Scope)
	out := &CreateCredentialOutput{}
	out.Body.CredentialView = toCredentialView(row.ID, row.TenantID, row.Name, row.Type, row.Scope, row.OwnerID, row.Description, row.CreatedAt.String(), row.UpdatedAt.String())
	return out, nil
}

// ListCredentials returns credential metadata (never secrets).
func (s *Server) ListCredentials(_ context.Context, input *ListCredentialsInput) (*ListCredentialsOutput, error) {
	rows, err := s.db.ListCredentials(input.TenantID)
	if err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}
	out := &ListCredentialsOutput{}
	for _, row := range rows {
		out.Body.Credentials = append(out.Body.Credentials, toCredentialView(row.ID, row.TenantID, row.Name, row.Type, row.Scope, row.OwnerID, row.Description, row.CreatedAt.String(), row.UpdatedAt.String()))
	}
	if out.Body.Credentials == nil {
		out.Body.Credentials = []CredentialView{}
	}
	return out, nil
}

// GetCredential returns a single credential's metadata (never the secret).
func (s *Server) GetCredential(_ context.Context, input *GetCredentialInput) (*GetCredentialOutput, error) {
	if input.ID == "" {
		return nil, huma.Error400BadRequest("id is required")
	}
	row, err := s.db.GetCredential(input.ID)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	out := &GetCredentialOutput{}
	out.Body.CredentialView = toCredentialView(row.ID, row.TenantID, row.Name, row.Type, row.Scope, row.OwnerID, row.Description, row.CreatedAt.String(), row.UpdatedAt.String())
	return out, nil
}

// DeleteCredential removes a stored secret. Requires operator+.
func (s *Server) DeleteCredential(_ context.Context, input *DeleteCredentialInput) (*DeleteCredentialOutput, error) {
	caller, actor, err := s.requireRole(input.Authorization, input.SessionToken, database.RoleOperator)
	if err != nil {
		return nil, err
	}
	if input.ID == "" {
		return nil, huma.Error400BadRequest("id is required")
	}
	row, err := s.db.GetCredential(input.ID)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	if caller != nil && caller.Role != database.RoleAdmin && row.TenantID != caller.TenantID {
		return nil, huma.Error403Forbidden("cross-tenant operation requires admin")
	}
	if err := s.db.DeleteCredential(input.ID); err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	_ = s.db.LogAudit("credential.delete", row.TenantID, actor, input.ID, "name="+row.Name)
	log.Printf("credential deleted: id=%s tenant=%s", input.ID, row.TenantID)
	out := &DeleteCredentialOutput{}
	out.Body.Ok = true
	return out, nil
}

// FetchCredential is the just-in-time secret delivery path: the executing
// client authenticates with its own token and receives the plaintext secret
// for a node reference. The server never ships secrets inside workflow
// assignments. Every fetch is audited. The client must hold the secret in
// memory only (TTLSeconds) and never persist it.
func (s *Server) FetchCredential(_ context.Context, input *FetchCredentialInput) (*FetchCredentialOutput, error) {
	clientID, ok := s.registry().identity(bearerToken(input.Authorization, input.Token))
	if !ok {
		return nil, huma.Error401Unauthorized("invalid or missing client token")
	}
	client, ok := s.registry().getClient(clientID)
	if !ok {
		return nil, huma.Error401Unauthorized("invalid or missing client token")
	}
	if input.Ref == "" {
		return nil, huma.Error400BadRequest("credential reference is required")
	}
	var groups []string
	if input.Groups != "" {
		for _, g := range strings.Split(input.Groups, ",") {
			if trimmed := strings.TrimSpace(g); trimmed != "" {
				groups = append(groups, trimmed)
			}
		}
	}
	row, err := s.db.ResolveCredential(client.TenantID, input.Ref, input.UserID, groups)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	secret, err := s.db.DecryptCredentialSecret(row)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to open credential")
	}
	detail := "workflow=" + input.WorkflowID + " node=" + input.NodeID
	if input.UserID != "" {
		detail += " user=" + input.UserID
	}
	_ = s.db.LogAudit("credential.fetch", row.TenantID, clientID, row.ID, detail)
	log.Printf("credential fetched: id=%s tenant=%s client=%s workflow=%s node=%s", row.ID, row.TenantID, clientID, input.WorkflowID, input.NodeID)
	out := &FetchCredentialOutput{}
	out.Body.CredentialID = row.ID
	out.Body.Name = row.Name
	out.Body.Type = row.Type
	out.Body.Secret = secret
	out.Body.TTLSeconds = database.CredentialFetchTTLSeconds
	return out, nil
}
