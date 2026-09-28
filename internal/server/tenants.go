package server

import (
	"context"
	"log"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/mcmx/nitejaguar/ent"
	"github.com/mcmx/nitejaguar/internal/database"
)

// TenantView is tenant registry metadata. It never includes passwords;
// the tenant admin's one-time password is returned only by the create
// endpoint at provisioning time.
type TenantView struct {
	ID           string `json:"id"`
	Slug         string `json:"slug"`
	Name         string `json:"name"`
	ContactEmail string `json:"contact_email,omitempty"`
	AdminUserID  string `json:"admin_user_id,omitempty"`
	Status       string `json:"status"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

func toTenantView(row *ent.Tenant) TenantView {
	return TenantView{
		ID: row.ID, Slug: row.Slug, Name: row.Name,
		ContactEmail: row.ContactEmail, AdminUserID: row.AdminUserID,
		Status:    row.Status,
		CreatedAt: row.CreatedAt.String(), UpdatedAt: row.UpdatedAt.String(),
	}
}

type CreateTenantInput struct {
	Authorization string `header:"Authorization"`
	SessionToken  string `header:"X-Auth-Token"`
	Body          struct {
		Name          string `json:"name"`
		Slug          string `json:"slug,omitempty"`
		ContactEmail  string `json:"contact_email,omitempty"`
		AdminUsername string `json:"admin_username,omitempty"`
		AdminEmail    string `json:"admin_email"`
		AdminPassword string `json:"admin_password,omitempty"`
	}
}

type CreateTenantOutput struct {
	Body struct {
		TenantView
		AdminUserID string `json:"admin_user_id"`
		// AdminPassword is the one-time plaintext password, present only
		// when the server generated it (caller-supplied passwords are
		// never echoed back).
		AdminPassword string `json:"admin_password,omitempty"`
		Generated     bool   `json:"generated_password"`
	}
}

type ListTenantsInput struct {
	Authorization string `header:"Authorization"`
	SessionToken  string `header:"X-Auth-Token"`
}

type ListTenantsOutput struct {
	Body struct {
		Tenants []TenantView `json:"tenants"`
	}
}

type GetTenantInput struct {
	Authorization string `header:"Authorization"`
	SessionToken  string `header:"X-Auth-Token"`
	ID            string `path:"id"`
}

type GetTenantOutput struct {
	Body struct {
		TenantView
	}
}

type TenantActionInput struct {
	Authorization string `header:"Authorization"`
	SessionToken  string `header:"X-Auth-Token"`
	ID            string `path:"id"`
}

type TenantActionOutput struct {
	Body struct {
		TenantView
	}
}

type DeleteTenantOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// requireSuper enforces superuser access: an admin of the default tenant.
// In open-bootstrap mode (no users yet) the call is allowed with actor
// "api" so fresh installs keep working.
func (s *Server) requireSuper(authorization, compat string) (*ent.AppUser, string, error) {
	caller, actor, err := s.requireRole(authorization, compat, database.RoleAdmin)
	if err != nil {
		return nil, "", err
	}
	if caller == nil {
		return nil, actor, nil // open-bootstrap mode
	}
	if !database.IsSuperUser(caller) {
		return nil, "", huma.Error403Forbidden("tenant management requires a superuser (admin of the default tenant)")
	}
	return caller, actor, nil
}

// CreateTenant provisions a tenant plus its default admin user.
// Superuser-only. Requires name + admin_email; the admin username defaults
// to "admin" and the password is generated when omitted (returned once).
func (s *Server) CreateTenant(_ context.Context, input *CreateTenantInput) (*CreateTenantOutput, error) {
	_, actor, err := s.requireSuper(input.Authorization, input.SessionToken)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Body.Name) == "" {
		return nil, huma.Error400BadRequest("name is required")
	}
	if strings.TrimSpace(input.Body.AdminEmail) == "" {
		return nil, huma.Error400BadRequest("admin_email is required")
	}
	generated := strings.TrimSpace(input.Body.AdminPassword) == ""
	trow, user, plaintext, err := s.db.CreateTenant(
		input.Body.Name, input.Body.Slug, input.Body.ContactEmail,
		input.Body.AdminUsername, input.Body.AdminEmail, input.Body.AdminPassword,
	)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "required") || strings.Contains(msg, "invalid") ||
			strings.Contains(msg, "already exists") || strings.Contains(msg, "already taken") ||
			strings.Contains(msg, "reserved") || strings.Contains(msg, "slug") ||
			strings.Contains(msg, "at least 8 characters") || strings.Contains(msg, "at most 100") {
			return nil, huma.Error400BadRequest(msg)
		}
		return nil, huma.Error500InternalServerError(msg)
	}
	_ = s.db.LogAudit("tenant.create", trow.Slug, actor, trow.ID,
		"name="+trow.Name+" slug="+trow.Slug+" admin="+user.ID+" email="+user.Email)
	log.Printf("tenant created: id=%s slug=%s name=%q admin=%s actor=%s", trow.ID, trow.Slug, trow.Name, user.ID, actor)
	out := &CreateTenantOutput{}
	out.Body.TenantView = toTenantView(trow)
	out.Body.AdminUserID = user.ID
	if generated {
		out.Body.AdminPassword = plaintext
		out.Body.Generated = true
	}
	return out, nil
}

// ListTenants lists tenants. Superusers see all; everyone else sees only
// their own tenant. Requires viewer+.
func (s *Server) ListTenants(_ context.Context, input *ListTenantsInput) (*ListTenantsOutput, error) {
	caller, _, err := s.requireRole(input.Authorization, input.SessionToken, database.RoleViewer)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.ListTenants()
	if err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}
	out := &ListTenantsOutput{}
	for _, row := range rows {
		if caller != nil && !database.CanCrossTenant(caller) && row.Slug != caller.TenantID {
			continue
		}
		out.Body.Tenants = append(out.Body.Tenants, toTenantView(row))
	}
	if out.Body.Tenants == nil {
		out.Body.Tenants = []TenantView{}
	}
	return out, nil
}

// GetTenant returns one tenant. Superusers may fetch any; others only
// their own. Requires viewer+.
func (s *Server) GetTenant(_ context.Context, input *GetTenantInput) (*GetTenantOutput, error) {
	caller, _, err := s.requireRole(input.Authorization, input.SessionToken, database.RoleViewer)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.ID) == "" {
		return nil, huma.Error400BadRequest("id is required")
	}
	row, err := s.db.GetTenant(input.ID)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	if caller != nil && !database.CanCrossTenant(caller) && row.Slug != caller.TenantID {
		return nil, huma.Error403Forbidden("cross-tenant operation requires a superuser")
	}
	out := &GetTenantOutput{}
	out.Body.TenantView = toTenantView(row)
	return out, nil
}

// SuspendTenant blocks new logins, sessions, enrollment, and client
// registration for a tenant. Superuser-only. The default tenant can never
// be suspended.
func (s *Server) SuspendTenant(_ context.Context, input *TenantActionInput) (*TenantActionOutput, error) {
	_, actor, err := s.requireSuper(input.Authorization, input.SessionToken)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.ID) == "" {
		return nil, huma.Error400BadRequest("id is required")
	}
	row, err := s.db.SuspendTenant(input.ID)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "not found") {
			return nil, huma.Error404NotFound(msg)
		}
		if strings.Contains(msg, "cannot be suspended") || strings.Contains(msg, "unknown tenant status") {
			return nil, huma.Error400BadRequest(msg)
		}
		return nil, huma.Error500InternalServerError(msg)
	}
	_ = s.db.LogAudit("tenant.suspend", row.Slug, actor, row.ID, "name="+row.Name+" via API")
	log.Printf("tenant suspended: id=%s slug=%s actor=%s", row.ID, row.Slug, actor)
	out := &TenantActionOutput{}
	out.Body.TenantView = toTenantView(row)
	return out, nil
}

// ActivateTenant re-enables a suspended tenant. Superuser-only.
func (s *Server) ActivateTenant(_ context.Context, input *TenantActionInput) (*TenantActionOutput, error) {
	_, actor, err := s.requireSuper(input.Authorization, input.SessionToken)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.ID) == "" {
		return nil, huma.Error400BadRequest("id is required")
	}
	row, err := s.db.ActivateTenant(input.ID)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "not found") {
			return nil, huma.Error404NotFound(msg)
		}
		if strings.Contains(msg, "unknown tenant status") {
			return nil, huma.Error400BadRequest(msg)
		}
		return nil, huma.Error500InternalServerError(msg)
	}
	_ = s.db.LogAudit("tenant.activate", row.Slug, actor, row.ID, "name="+row.Name+" via API")
	log.Printf("tenant activated: id=%s slug=%s actor=%s", row.ID, row.Slug, actor)
	out := &TenantActionOutput{}
	out.Body.TenantView = toTenantView(row)
	return out, nil
}

// DeleteTenant removes a tenant registry row. Scoped rows keep their
// tenant_id as superuser-visible orphans (no cascade). Superuser-only;
// the default tenant can never be deleted.
func (s *Server) DeleteTenant(_ context.Context, input *TenantActionInput) (*DeleteTenantOutput, error) {
	_, actor, err := s.requireSuper(input.Authorization, input.SessionToken)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.ID) == "" {
		return nil, huma.Error400BadRequest("id is required")
	}
	row, err := s.db.GetTenant(input.ID)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	if err := s.db.DeleteTenant(input.ID); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "not found") {
			return nil, huma.Error404NotFound(msg)
		}
		if strings.Contains(msg, "cannot be deleted") {
			return nil, huma.Error400BadRequest(msg)
		}
		return nil, huma.Error500InternalServerError(msg)
	}
	_ = s.db.LogAudit("tenant.delete", row.Slug, actor, row.ID, "name="+row.Name+" via API")
	log.Printf("tenant deleted: id=%s slug=%s actor=%s", row.ID, row.Slug, actor)
	out := &DeleteTenantOutput{}
	out.Body.Ok = true
	return out, nil
}
