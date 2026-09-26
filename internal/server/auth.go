package server

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/mcmx/nitejaguar/ent"
	"github.com/mcmx/nitejaguar/internal/database"
)

// UserView is user metadata. Password hashes are never exposed.
type UserView struct {
	ID        string   `json:"id"`
	TenantID  string   `json:"tenant_id"`
	Username  string   `json:"username"`
	Role      string   `json:"role"`
	Groups    []string `json:"groups,omitempty"`
	Revoked   bool     `json:"revoked"`
	CreatedAt string   `json:"created_at"`
}

func toUserView(row *ent.AppUser) UserView {
	groups := append([]string(nil), row.Groups...)
	if groups == nil {
		groups = []string{}
	}
	return UserView{
		ID: row.ID, TenantID: row.TenantID, Username: row.Username,
		Role: row.Role, Groups: groups, Revoked: row.Revoked,
		CreatedAt: row.CreatedAt.String(),
	}
}

type LoginInput struct {
	Body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		TenantID string `json:"tenant_id,omitempty"`
	}
}

type LoginOutput struct {
	Body struct {
		Token     string   `json:"token"`
		ExpiresAt string   `json:"expires_at"`
		User      UserView `json:"user"`
	}
}

type MeInput struct {
	Authorization string `header:"Authorization"`
	SessionToken  string `header:"X-Auth-Token"`
}

type MeOutput struct {
	Body struct {
		User UserView `json:"user"`
	}
}

type LogoutInput struct {
	Authorization string `header:"Authorization"`
	SessionToken  string `header:"X-Auth-Token"`
}

type LogoutOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

type CreateUserInput struct {
	Authorization string `header:"Authorization"`
	SessionToken  string `header:"X-Auth-Token"`
	Body          struct {
		Username string   `json:"username"`
		Password string   `json:"password"`
		TenantID string   `json:"tenant_id,omitempty"`
		Role     string   `json:"role,omitempty"`
		Groups   []string `json:"groups,omitempty"`
	}
}

type CreateUserOutput struct {
	Body struct {
		UserView
	}
}

type ListUsersInput struct {
	Authorization string `header:"Authorization"`
	SessionToken  string `header:"X-Auth-Token"`
	TenantID      string `query:"tenant_id"`
}

type ListUsersOutput struct {
	Body struct {
		Users []UserView `json:"users"`
	}
}

type RevokeUserInput struct {
	Authorization string `header:"Authorization"`
	SessionToken  string `header:"X-Auth-Token"`
	ID            string `path:"id"`
}

type RevokeUserOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

type ChangePasswordInput struct {
	Authorization string `header:"Authorization"`
	SessionToken  string `header:"X-Auth-Token"`
	Body          struct {
		CurrentPassword string `json:"current_password,omitempty"`
		NewPassword     string `json:"new_password"`
		// UserID optionally targets another user (admin-only reset; the
		// current password is not required in that case). Empty means self.
		UserID string `json:"user_id,omitempty"`
	}
}

type ChangePasswordOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// sessionToken extracts a user session token from Authorization Bearer or
// X-Auth-Token. Client tokens (client_ sessions) are not accepted here;
// user sessions are minted via /api/auth/login.
func sessionToken(authorization, compat string) string {
	if v := bearerToken(authorization, ""); v != "" {
		return v
	}
	return strings.TrimSpace(compat)
}

// currentUser resolves the caller. When no users exist yet the server runs
// in open-bootstrap mode and returns (nil, true): mutating endpoints stay
// usable until the first admin is created, preserving fresh-install and
// existing-test flows.
func (s *Server) currentUser(authorization, compat string) (*ent.AppUser, bool, error) {
	if s.db == nil {
		return nil, false, huma.Error500InternalServerError("no database")
	}
	n, err := s.db.CountUsers()
	if err != nil {
		return nil, false, huma.Error500InternalServerError("failed to check users")
	}
	if n == 0 {
		return nil, true, nil
	}
	tok := sessionToken(authorization, compat)
	if tok == "" {
		return nil, false, huma.Error401Unauthorized("login required")
	}
	user, _, err := s.db.AuthenticateSession(tok)
	if err != nil {
		return nil, false, huma.Error401Unauthorized("invalid or expired session")
	}
	return user, false, nil
}

// requireRole enforces a minimum role. In open-bootstrap mode (no users)
// any call is allowed and actor is "api".
func (s *Server) requireRole(authorization, compat, minimum string) (*ent.AppUser, string, error) {
	user, open, err := s.currentUser(authorization, compat)
	if err != nil {
		return nil, "", err
	}
	if open {
		return nil, "api", nil
	}
	if !database.HasRole(user.Role, minimum) {
		return nil, "", huma.Error403Forbidden("role "+user.Role+" cannot perform this action (requires "+minimum+")")
	}
	return user, user.ID, nil
}

// scopedTenant defaults an empty request tenant to the caller's tenant and
// rejects cross-tenant requests from non-admins.
func scopedTenant(caller *ent.AppUser, requested string) (string, error) {
	if caller == nil {
		if requested == "" {
			return "default", nil
		}
		return requested, nil
	}
	if requested == "" {
		return caller.TenantID, nil
	}
	if requested != caller.TenantID && caller.Role != database.RoleAdmin {
		return "", huma.Error403Forbidden("cross-tenant operation requires admin")
	}
	return requested, nil
}

// Login verifies credentials and mints a session token.
func (s *Server) Login(_ context.Context, input *LoginInput) (*LoginOutput, error) {
	if strings.TrimSpace(input.Body.Username) == "" || input.Body.Password == "" {
		return nil, huma.Error400BadRequest("username and password are required")
	}
	tenantID := input.Body.TenantID
	if tenantID == "" {
		tenantID = "default"
	}
	user, err := s.db.VerifyUser(tenantID, input.Body.Username, input.Body.Password)
	if err != nil {
		_ = s.db.LogAudit("auth.login", tenantID, input.Body.Username, "", "failed")
		return nil, huma.Error401Unauthorized("invalid credentials")
	}
	sess, plaintext, err := s.db.CreateSession(user.ID, database.SessionTTL)
	if err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}
	_ = s.db.LogAudit("auth.login", user.TenantID, user.ID, user.ID, "username="+user.Username+" role="+user.Role)
	log.Printf("user login: id=%s tenant=%s username=%q role=%s", user.ID, user.TenantID, user.Username, user.Role)
	out := &LoginOutput{}
	out.Body.Token = plaintext
	if sess.ExpiresAt != nil {
		out.Body.ExpiresAt = sess.ExpiresAt.Format(time.RFC3339)
	}
	out.Body.User = toUserView(user)
	return out, nil
}

// Logout revokes the caller's session.
func (s *Server) Logout(_ context.Context, input *LogoutInput) (*LogoutOutput, error) {
	tok := sessionToken(input.Authorization, input.SessionToken)
	if tok == "" {
		return nil, huma.Error401Unauthorized("login required")
	}
	user, _, err := s.db.AuthenticateSession(tok)
	if err != nil {
		return nil, huma.Error401Unauthorized("invalid or expired session")
	}
	if err := s.db.RevokeSession(tok); err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}
	_ = s.db.LogAudit("auth.logout", user.TenantID, user.ID, user.ID, "username="+user.Username)
	out := &LogoutOutput{}
	out.Body.Ok = true
	return out, nil
}

// Me returns the caller's user profile.
func (s *Server) Me(_ context.Context, input *MeInput) (*MeOutput, error) {
	user, open, err := s.currentUser(input.Authorization, input.SessionToken)
	if err != nil {
		return nil, err
	}
	if open {
		return nil, huma.Error401Unauthorized("no users exist yet; create the first admin via POST /api/users")
	}
	out := &MeOutput{}
	out.Body.User = toUserView(user)
	return out, nil
}

// CreateUser creates a website/API user. Admin-only (open-bootstrap mode
// allows the very first admin without a session).
func (s *Server) CreateUser(_ context.Context, input *CreateUserInput) (*CreateUserOutput, error) {
	caller, actor, err := s.requireRole(input.Authorization, input.SessionToken, database.RoleAdmin)
	if err != nil {
		return nil, err
	}
	tenantID, err := scopedTenant(caller, input.Body.TenantID)
	if err != nil {
		return nil, err
	}
	role := input.Body.Role
	if role == "" {
		role = database.RoleViewer
	}
	row, err := s.db.CreateUser(tenantID, input.Body.Username, input.Body.Password, role, input.Body.Groups)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "already exists") || strings.Contains(msg, "password must") ||
			strings.Contains(msg, "unknown role") || strings.Contains(msg, "required") {
			return nil, huma.Error400BadRequest(msg)
		}
		return nil, huma.Error500InternalServerError(msg)
	}
	_ = s.db.LogAudit("user.create", row.TenantID, actor, row.ID, "username="+row.Username+" role="+row.Role)
	log.Printf("user created: id=%s tenant=%s username=%q role=%s", row.ID, row.TenantID, row.Username, row.Role)
	out := &CreateUserOutput{}
	out.Body.UserView = toUserView(row)
	return out, nil
}

// ListUsers lists users. Admin sees all (or the requested tenant);
// operators see their own tenant; viewers are denied.
func (s *Server) ListUsers(_ context.Context, input *ListUsersInput) (*ListUsersOutput, error) {
	caller, _, err := s.requireRole(input.Authorization, input.SessionToken, database.RoleOperator)
	if err != nil {
		return nil, err
	}
	tenant := input.TenantID
	if caller != nil {
		if caller.Role != database.RoleAdmin {
			tenant = caller.TenantID
		} else if tenant == "" {
			tenant = ""
		}
	}
	rows, err := s.db.ListUsers(tenant)
	if err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}
	out := &ListUsersOutput{}
	for _, row := range rows {
		// Non-admins only see their own tenant.
		if caller != nil && caller.Role != database.RoleAdmin && row.TenantID != caller.TenantID {
			continue
		}
		out.Body.Users = append(out.Body.Users, toUserView(row))
	}
	if out.Body.Users == nil {
		out.Body.Users = []UserView{}
	}
	return out, nil
}

// RevokeUser disables a user and revokes its sessions. Admin-only; users
// cannot revoke themselves.
func (s *Server) RevokeUser(_ context.Context, input *RevokeUserInput) (*RevokeUserOutput, error) {
	caller, actor, err := s.requireRole(input.Authorization, input.SessionToken, database.RoleAdmin)
	if err != nil {
		return nil, err
	}
	if input.ID == "" {
		return nil, huma.Error400BadRequest("id is required")
	}
	if caller != nil && caller.ID == input.ID {
		return nil, huma.Error400BadRequest("cannot revoke your own account")
	}
	row, err := s.db.GetUser(input.ID)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	if caller != nil && caller.Role != database.RoleAdmin && row.TenantID != caller.TenantID {
		return nil, huma.Error403Forbidden("cross-tenant operation requires admin")
	}
	if err := s.db.RevokeUser(input.ID); err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	_ = s.db.LogAudit("user.revoke", row.TenantID, actor, input.ID, "username="+row.Username)
	log.Printf("user revoked: id=%s tenant=%s", input.ID, row.TenantID)
	out := &RevokeUserOutput{}
	out.Body.Ok = true
	return out, nil
}

// ChangePassword lets a user change their own password (current password
// required) or lets an admin reset another user's password without knowing
// it (user_id set, current password not required). Every change is audited.
func (s *Server) ChangePassword(_ context.Context, input *ChangePasswordInput) (*ChangePasswordOutput, error) {
	caller, actor, err := s.requireRole(input.Authorization, input.SessionToken, database.RoleViewer)
	if err != nil {
		return nil, err
	}
	if caller == nil {
		return nil, huma.Error401Unauthorized("login required")
	}
	if strings.TrimSpace(input.Body.NewPassword) == "" {
		return nil, huma.Error400BadRequest("new_password is required")
	}
	targetID := strings.TrimSpace(input.Body.UserID)
	if targetID == "" || targetID == caller.ID {
		if input.Body.CurrentPassword == "" {
			return nil, huma.Error400BadRequest("current_password is required")
		}
		if err := s.db.ChangePassword(caller.ID, input.Body.CurrentPassword, input.Body.NewPassword); err != nil {
			msg := err.Error()
			if strings.Contains(msg, "invalid credentials") {
				_ = s.db.LogAudit("user.password_change", caller.TenantID, actor, caller.ID, "failed: bad current password")
				return nil, huma.Error401Unauthorized("invalid credentials")
			}
			if strings.Contains(msg, "at least 8 characters") {
				return nil, huma.Error400BadRequest(msg)
			}
			return nil, huma.Error500InternalServerError(msg)
		}
		_ = s.db.LogAudit("user.password_change", caller.TenantID, actor, caller.ID, "changed via API")
		log.Printf("user password changed: id=%s tenant=%s", caller.ID, caller.TenantID)
		out := &ChangePasswordOutput{}
		out.Body.Ok = true
		return out, nil
	}
	if caller.Role != database.RoleAdmin {
		return nil, huma.Error403Forbidden("resetting another user's password requires admin")
	}
	row, err := s.db.GetUser(targetID)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	if err := s.db.UpdatePassword(targetID, input.Body.NewPassword); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "at least 8 characters") || strings.Contains(msg, "user not found") {
			return nil, huma.Error400BadRequest(msg)
		}
		return nil, huma.Error500InternalServerError(msg)
	}
	_ = s.db.LogAudit("user.password_change", row.TenantID, actor, targetID, "reset by admin via API")
	log.Printf("user password reset by admin: id=%s tenant=%s actor=%s", targetID, row.TenantID, actor)
	out := &ChangePasswordOutput{}
	out.Body.Ok = true
	return out, nil
}
