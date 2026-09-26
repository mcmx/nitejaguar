package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/labstack/echo/v4"
	"github.com/mcmx/nitejaguar/cmd/web"
	"github.com/mcmx/nitejaguar/internal/database"
	"github.com/mcmx/nitejaguar/internal/workflow"
)

// clientsPageData loads the clients + enrollment-token listing, tenant
// filtered for non-admins (open-bootstrap mode sees everything).
func (s *Server) clientsPageData(c echo.Context) *web.ClientsPageData {
	user, open := s.webCurrentUser(c)
	data := &web.ClientsPageData{CurrentUser: s.navUser(c)}
	for _, client := range s.registry().list() {
		if !open && user != nil && user.Role != database.RoleAdmin && client.TenantID != "" && client.TenantID != user.TenantID {
			continue
		}
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
	if toks, err := s.db.ListEnrollmentTokens(); err == nil {
		for _, tok := range toks {
			if !open && user != nil && user.Role != database.RoleAdmin && tok.TenantID != user.TenantID {
				continue
			}
			exp := "never"
			if tok.ExpiresAt != nil {
				exp = tok.ExpiresAt.Format("2006-01-02 15:04:05 MST")
			}
			data.Tokens = append(data.Tokens, web.EnrollmentTokenView{
				ID: tok.ID, TenantID: tok.TenantID, Label: tok.Label,
				ExpiresAt: exp, MaxUses: tok.MaxUses, UseCount: tok.UseCount,
				Revoked: tok.Revoked, CreatedAt: tok.CreatedAt.Format("2006-01-02 15:04:05 MST"),
			})
		}
	}
	return data
}

func (s *Server) renderClients(c echo.Context, data *web.ClientsPageData) error {
	templ.Handler(web.ClientsPage(data)).ServeHTTP(c.Response(), c.Request())
	return nil
}

// usersPageData loads the user roster, tenant filtered for non-admins.
func (s *Server) usersPageData(c echo.Context) *web.UsersPageData {
	user, open := s.webCurrentUser(c)
	data := &web.UsersPageData{CurrentUser: s.navUser(c)}
	rows, err := s.db.ListUsers("")
	if err != nil {
		data.Error = "Unable to load users"
		return data
	}
	for _, row := range rows {
		if !open && user != nil && user.Role != database.RoleAdmin && row.TenantID != user.TenantID {
			continue
		}
		data.Users = append(data.Users, web.UserView{
			ID: row.ID, TenantID: row.TenantID, Username: row.Username,
			Role: row.Role, Groups: append([]string(nil), row.Groups...),
			Revoked: row.Revoked, CreatedAt: row.CreatedAt.Format("2006-01-02 15:04:05 MST"),
		})
	}
	return data
}

func (s *Server) renderUsers(c echo.Context, data *web.UsersPageData) error {
	templ.Handler(web.UsersPage(data)).ServeHTTP(c.Response(), c.Request())
	return nil
}

// credentialsPageData loads credential metadata, tenant filtered for
// non-admins. Secrets are never exposed.
func (s *Server) credentialsPageData(c echo.Context) *web.CredentialsPageData {
	user, open := s.webCurrentUser(c)
	data := &web.CredentialsPageData{CurrentUser: s.navUser(c), Types: database.SupportedCredentialTypes}
	tenant := ""
	if !open && user != nil && user.Role != database.RoleAdmin {
		tenant = user.TenantID
	}
	rows, err := s.db.ListCredentials(tenant)
	if err != nil {
		data.Error = "Unable to load credentials"
		return data
	}
	for _, row := range rows {
		owner := row.OwnerID
		if owner == "" {
			owner = "—"
		}
		desc := row.Description
		if desc == "" {
			desc = "—"
		}
		data.Credentials = append(data.Credentials, web.CredentialView{
			ID: row.ID, TenantID: row.TenantID, Name: row.Name, Type: row.Type,
			Scope: row.Scope, OwnerID: owner, Description: desc,
			CreatedAt: row.CreatedAt.Format("2006-01-02 15:04:05 MST"),
		})
	}
	return data
}

func (s *Server) credentialsPage(c echo.Context) error {
	templ.Handler(web.CredentialsPage(s.credentialsPageData(c))).ServeHTTP(c.Response(), c.Request())
	return nil
}

// createCredentialWeb stores a credential from the /credentials form.
// Requires operator+; the credential lands in the caller's tenant
// (admins use the API for cross-tenant work).
func (s *Server) createCredentialWeb(c echo.Context) error {
	caller, actor, err := s.webActor(c, database.RoleOperator)
	if err != nil {
		if he, ok := err.(*echo.HTTPError); ok && he.Code == http.StatusUnauthorized {
			return c.Redirect(http.StatusSeeOther, "/login")
		}
		return err
	}
	tenantID := "default"
	if caller != nil {
		tenantID = caller.TenantID
	}
	name := strings.TrimSpace(c.FormValue("name"))
	credType := strings.TrimSpace(c.FormValue("type"))
	scope := strings.TrimSpace(c.FormValue("scope"))
	ownerID := strings.TrimSpace(c.FormValue("owner_id"))
	secret := c.FormValue("secret")
	description := strings.TrimSpace(c.FormValue("description"))
	data := s.credentialsPageData(c)
	if name == "" || secret == "" {
		data.Error = "Name and secret are required."
		templ.Handler(web.CredentialsPage(data)).ServeHTTP(c.Response(), c.Request())
		return nil
	}
	row, err := s.db.CreateCredential(tenantID, name, credType, scope, ownerID, secret, description)
	if err != nil {
		data.Error = "Failed to store credential: " + err.Error()
		templ.Handler(web.CredentialsPage(data)).ServeHTTP(c.Response(), c.Request())
		return nil
	}
	_ = s.db.LogAudit("credential.create", row.TenantID, actor, row.ID, "name="+row.Name+" scope="+row.Scope+" type="+row.Type+" via web")
	log.Printf("credential created via web: id=%s tenant=%s name=%q actor=%s", row.ID, row.TenantID, row.Name, actor)
	data = s.credentialsPageData(c)
	data.Success = "Credential " + name + " stored."
	templ.Handler(web.CredentialsPage(data)).ServeHTTP(c.Response(), c.Request())
	return nil
}

// deleteCredentialWeb removes a credential. Requires operator+
// (cross-tenant requires admin).
func (s *Server) deleteCredentialWeb(c echo.Context) error {
	caller, actor, err := s.webActor(c, database.RoleOperator)
	if err != nil {
		if he, ok := err.(*echo.HTTPError); ok && he.Code == http.StatusUnauthorized {
			return c.Redirect(http.StatusSeeOther, "/login")
		}
		return err
	}
	id := c.Param("id")
	row, err := s.db.GetCredential(id)
	if err != nil {
		data := s.credentialsPageData(c)
		data.Error = "Credential not found."
		templ.Handler(web.CredentialsPage(data)).ServeHTTP(c.Response(), c.Request())
		return nil
	}
	if caller != nil && caller.Role != database.RoleAdmin && row.TenantID != caller.TenantID {
		return c.String(http.StatusForbidden, "cross-tenant operation requires admin")
	}
	if err := s.db.DeleteCredential(id); err != nil {
		data := s.credentialsPageData(c)
		data.Error = "Failed to delete credential: " + err.Error()
		templ.Handler(web.CredentialsPage(data)).ServeHTTP(c.Response(), c.Request())
		return nil
	}
	_ = s.db.LogAudit("credential.delete", row.TenantID, actor, id, "name="+row.Name+" via web")
	log.Printf("credential deleted via web: id=%s tenant=%s actor=%s", id, row.TenantID, actor)
	data := s.credentialsPageData(c)
	data.Success = "Credential " + row.Name + " deleted."
	templ.Handler(web.CredentialsPage(data)).ServeHTTP(c.Response(), c.Request())
	return nil
}

// enrollmentCommand builds the copy-paste client enrollment command. The
// binary name is `nitejaguar` (matching the goreleaser build); --server
// points back at this server's origin.
func enrollmentCommand(c echo.Context, clientName, token string) string {
	origin := strings.TrimSuffix(c.Scheme()+"://"+c.Request().Host, "/")
	name := strings.TrimSpace(clientName)
	if strings.ContainsAny(name, " \t\"'") {
		name = strconv.Quote(name)
	}
	return fmt.Sprintf("nitejaguar client --server %s --name %s --enrollment-token %s", origin, name, token)
}

// createEnrollmentTokenWeb mints a join token for one client enrollment and
// shows the ready-to-run command (plaintext shown once only). Requires
// operator+.
func (s *Server) createEnrollmentTokenWeb(c echo.Context) error {
	caller, actor, err := s.webActor(c, database.RoleOperator)
	if err != nil {
		if he, ok := err.(*echo.HTTPError); ok && he.Code == http.StatusUnauthorized {
			return c.Redirect(http.StatusSeeOther, "/login")
		}
		return err
	}
	tenantID := "default"
	if caller != nil {
		tenantID = caller.TenantID
	}
	clientName := strings.TrimSpace(c.FormValue("client_name"))
	label := strings.TrimSpace(c.FormValue("label"))
	data := s.clientsPageData(c)
	if clientName == "" {
		data.Error = "Client name is required."
		return s.renderClients(c, data)
	}
	var expiresAt *time.Time
	if raw := strings.TrimSpace(c.FormValue("expires_in_hours")); raw != "" {
		hours, perr := strconv.Atoi(raw)
		if perr != nil || hours <= 0 {
			data.Error = "Expires in hours must be a positive number."
			return s.renderClients(c, data)
		}
		t := time.Now().Add(time.Duration(hours) * time.Hour)
		expiresAt = &t
	}
	// Empty max_uses means one enrollment: the form generates a command
	// for a single client.
	maxUses := 1
	if raw := strings.TrimSpace(c.FormValue("max_uses")); raw != "" {
		n, perr := strconv.Atoi(raw)
		if perr != nil || n < 0 {
			data.Error = "Max uses cannot be negative."
			return s.renderClients(c, data)
		}
		maxUses = n
	}
	tok, plaintext, err := s.db.CreateEnrollmentToken(tenantID, label, expiresAt, maxUses)
	if err != nil {
		data.Error = "Failed to create enrollment token: " + err.Error()
		return s.renderClients(c, data)
	}
	_ = s.db.LogAudit("enrollment.create", tenantID, actor, tok.ID, "label="+label+" via web for client="+clientName)
	log.Printf("enrollment token created via web: id=%s tenant=%s actor=%s", tok.ID, tok.TenantID, actor)
	data = s.clientsPageData(c)
	data.Enroll = &web.EnrollResult{
		ClientName: clientName, TokenID: tok.ID, Label: label,
		Token: plaintext, Command: enrollmentCommand(c, clientName, plaintext),
	}
	return s.renderClients(c, data)
}

// revokeEnrollmentTokenWeb revokes a join token to block new joins.
// Requires operator+ (cross-tenant requires admin).
func (s *Server) revokeEnrollmentTokenWeb(c echo.Context) error {
	caller, actor, err := s.webActor(c, database.RoleOperator)
	if err != nil {
		if he, ok := err.(*echo.HTTPError); ok && he.Code == http.StatusUnauthorized {
			return c.Redirect(http.StatusSeeOther, "/login")
		}
		return err
	}
	id := c.Param("id")
	tenantID := "default"
	if toks, lerr := s.db.ListEnrollmentTokens(); lerr == nil {
		for _, tok := range toks {
			if tok.ID == id {
				tenantID = tok.TenantID
			}
		}
	}
	if caller != nil && caller.Role != database.RoleAdmin && tenantID != caller.TenantID {
		return c.String(http.StatusForbidden, "cross-tenant operation requires admin")
	}
	if err := s.db.RevokeEnrollmentToken(id); err != nil {
		data := s.clientsPageData(c)
		data.Error = "Failed to revoke token: " + err.Error()
		return s.renderClients(c, data)
	}
	_ = s.db.LogAudit("enrollment.revoke", tenantID, actor, id, "revoked via web")
	data := s.clientsPageData(c)
	data.Success = "Enrollment token revoked."
	return s.renderClients(c, data)
}

// revokeClientWeb revokes a client; its token stops authenticating.
// Requires operator+ (cross-tenant requires admin).
func (s *Server) revokeClientWeb(c echo.Context) error {
	caller, actor, err := s.webActor(c, database.RoleOperator)
	if err != nil {
		if he, ok := err.(*echo.HTTPError); ok && he.Code == http.StatusUnauthorized {
			return c.Redirect(http.StatusSeeOther, "/login")
		}
		return err
	}
	id := c.Param("id")
	tenantID := "default"
	if cl, ok := s.registry().getClient(id); ok {
		tenantID = cl.TenantID
	}
	if caller != nil && caller.Role != database.RoleAdmin && tenantID != caller.TenantID {
		return c.String(http.StatusForbidden, "cross-tenant operation requires admin")
	}
	if err := s.db.RevokeClient(id); err != nil {
		data := s.clientsPageData(c)
		data.Error = "Failed to revoke client: " + err.Error()
		return s.renderClients(c, data)
	}
	_ = s.db.LogAudit("client.revoke", tenantID, actor, id, "revoked via web")
	data := s.clientsPageData(c)
	data.Success = "Client revoked."
	return s.renderClients(c, data)
}

// splitGroups parses a comma-separated groups field.
func splitGroups(raw string) []string {
	var out []string
	for _, g := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(g); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// createUserWeb creates a user from the /users form. Admin-only (open until
// the first user exists, so the bootstrap admin can be created from the UI).
func (s *Server) createUserWeb(c echo.Context) error {
	caller, actor, err := s.webActor(c, database.RoleAdmin)
	if err != nil {
		if he, ok := err.(*echo.HTTPError); ok && he.Code == http.StatusUnauthorized {
			return c.Redirect(http.StatusSeeOther, "/login")
		}
		return err
	}
	username := strings.TrimSpace(c.FormValue("username"))
	password := c.FormValue("password")
	role := strings.TrimSpace(c.FormValue("role"))
	if role == "" {
		role = database.RoleViewer
	}
	groups := splitGroups(c.FormValue("groups"))
	data := s.usersPageData(c)
	if username == "" || password == "" {
		data.Error = "Username and password are required."
		return s.renderUsers(c, data)
	}
	tenantID := "default"
	if caller != nil {
		tenantID = caller.TenantID
	}
	if requested := strings.TrimSpace(c.FormValue("tenant_id")); requested != "" {
		if caller != nil && caller.Role != database.RoleAdmin && requested != caller.TenantID {
			data.Error = "Cross-tenant user creation requires admin."
			return s.renderUsers(c, data)
		}
		tenantID = requested
	}
	row, err := s.db.CreateUser(tenantID, username, password, role, groups)
	if err != nil {
		data.Error = "Failed to create user: " + err.Error()
		return s.renderUsers(c, data)
	}
	_ = s.db.LogAudit("user.create", row.TenantID, actor, row.ID, "username="+row.Username+" role="+row.Role+" via web")
	data = s.usersPageData(c)
	data.Success = "User " + username + " created."
	return s.renderUsers(c, data)
}

// revokeUserWeb revokes a user and its sessions. Admin-only; self-revoke is
// refused (mirrors the API).
func (s *Server) revokeUserWeb(c echo.Context) error {
	caller, actor, err := s.webActor(c, database.RoleAdmin)
	if err != nil {
		if he, ok := err.(*echo.HTTPError); ok && he.Code == http.StatusUnauthorized {
			return c.Redirect(http.StatusSeeOther, "/login")
		}
		return err
	}
	id := c.Param("id")
	if caller != nil && caller.ID == id {
		data := s.usersPageData(c)
		data.Error = "You cannot revoke your own account."
		return s.renderUsers(c, data)
	}
	row, err := s.db.GetUser(id)
	if err != nil {
		data := s.usersPageData(c)
		data.Error = "User not found."
		return s.renderUsers(c, data)
	}
	if err := s.db.RevokeUser(id); err != nil {
		data := s.usersPageData(c)
		data.Error = "Failed to revoke user: " + err.Error()
		return s.renderUsers(c, data)
	}
	_ = s.db.LogAudit("user.revoke", row.TenantID, actor, id, "username="+row.Username+" via web")
	data := s.usersPageData(c)
	data.Success = "User " + row.Username + " revoked."
	return s.renderUsers(c, data)
}

// profilePage renders the signed-in user's profile + password form.
func (s *Server) profilePage(c echo.Context) error {
	user, open := s.webCurrentUser(c)
	if open {
		return c.Redirect(http.StatusSeeOther, "/users")
	}
	if user == nil {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	data := &web.ProfilePageData{CurrentUser: s.navUser(c), User: web.UserView{
		ID: user.ID, TenantID: user.TenantID, Username: user.Username,
		Role: user.Role, Groups: append([]string(nil), user.Groups...),
		Revoked: user.Revoked, CreatedAt: user.CreatedAt.Format("2006-01-02 15:04:05 MST"),
	}}
	templ.Handler(web.ProfilePage(data)).ServeHTTP(c.Response(), c.Request())
	return nil
}

// changePasswordWeb handles the /profile password form: current password is
// always required, and the confirmation must match.
func (s *Server) changePasswordWeb(c echo.Context) error {
	user, open := s.webCurrentUser(c)
	if open {
		return c.Redirect(http.StatusSeeOther, "/users")
	}
	if user == nil {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	render := func(errMsg, okMsg string) error {
		data := &web.ProfilePageData{CurrentUser: s.navUser(c), Error: errMsg, Success: okMsg, User: web.UserView{
			ID: user.ID, TenantID: user.TenantID, Username: user.Username,
			Role: user.Role, Groups: append([]string(nil), user.Groups...),
			Revoked: user.Revoked, CreatedAt: user.CreatedAt.Format("2006-01-02 15:04:05 MST"),
		}}
		templ.Handler(web.ProfilePage(data)).ServeHTTP(c.Response(), c.Request())
		return nil
	}
	current := c.FormValue("current_password")
	next := c.FormValue("new_password")
	confirm := c.FormValue("confirm_password")
	if next == "" || current == "" {
		return render("Current and new passwords are required.", "")
	}
	if next != confirm {
		return render("New passwords do not match.", "")
	}
	if err := s.db.ChangePassword(user.ID, current, next); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "invalid credentials") {
			_ = s.db.LogAudit("user.password_change", user.TenantID, user.ID, user.ID, "failed via web: bad current password")
			return render("Current password is incorrect.", "")
		}
		return render("Failed to change password: "+msg, "")
	}
	_ = s.db.LogAudit("user.password_change", user.TenantID, user.ID, user.ID, "changed via web")
	return render("", "Password changed.")
}

// workflowTenant resolves the tenant owning a workflow row (definition
// tenant wins, mirroring setWorkflowEnabled).
func workflowTenant(rowTenant, jsonDef string) string {
	var def workflow.Workflow
	if err := json.Unmarshal([]byte(jsonDef), &def); err == nil && def.TenantID != "" {
		return def.TenantID
	}
	if rowTenant != "" {
		return rowTenant
	}
	return "default"
}

// deleteWorkflowWeb removes a workflow. Requires operator+ (cross-tenant
// requires admin); audited as workflow.delete.
func (s *Server) deleteWorkflowWeb(c echo.Context) error {
	caller, actor, err := s.webActor(c, database.RoleOperator)
	if err != nil {
		if he, ok := err.(*echo.HTTPError); ok && he.Code == http.StatusUnauthorized {
			return c.Redirect(http.StatusSeeOther, "/login")
		}
		return err
	}
	id := c.Param("id")
	row, err := s.db.GetWorkflow(id)
	if err != nil {
		return c.String(http.StatusNotFound, "workflow not found")
	}
	tenantID := workflowTenant(row.TenantID, row.JSONDefinition)
	if caller != nil && caller.Role != database.RoleAdmin && tenantID != caller.TenantID {
		return c.String(http.StatusForbidden, "cross-tenant operation requires admin")
	}
	if err := s.db.DeleteWorkflow(id); err != nil {
		return c.String(http.StatusNotFound, err.Error())
	}
	_ = s.db.LogAudit("workflow.delete", tenantID, actor, id, "deleted via web")
	log.Printf("workflow deleted via web: id=%s tenant=%s actor=%s", id, tenantID, actor)
	return c.Redirect(http.StatusSeeOther, "/?deleted=1")
}

// cloneWorkflowWeb saves an independent copy with fresh ids (mirroring the
// clone API) and lands on the new workflow. Requires operator+.
func (s *Server) cloneWorkflowWeb(c echo.Context) error {
	caller, actor, err := s.webActor(c, database.RoleOperator)
	if err != nil {
		if he, ok := err.(*echo.HTTPError); ok && he.Code == http.StatusUnauthorized {
			return c.Redirect(http.StatusSeeOther, "/login")
		}
		return err
	}
	id := c.Param("id")
	row, err := s.db.GetWorkflow(id)
	if err != nil {
		return c.String(http.StatusNotFound, "workflow not found")
	}
	tenantID := workflowTenant(row.TenantID, row.JSONDefinition)
	if caller != nil && caller.Role != database.RoleAdmin && tenantID != caller.TenantID {
		return c.String(http.StatusForbidden, "cross-tenant operation requires admin")
	}
	newID, err := s.wm.CloneWorkflowJSON(row.JSONDefinition)
	if err != nil {
		return c.String(http.StatusBadRequest, "Failed to clone workflow: "+err.Error())
	}
	_ = s.db.LogAudit("workflow.clone", tenantID, actor, newID, "cloned from "+id+" via web")
	log.Printf("workflow cloned via web: id=%s from=%s tenant=%s actor=%s", newID, id, tenantID, actor)
	return c.Redirect(http.StatusSeeOther, "/workflows/"+newID)
}
