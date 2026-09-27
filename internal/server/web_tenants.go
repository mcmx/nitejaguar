package server

import (
	"net/http"
	"strings"

	"github.com/a-h/templ"
	"github.com/labstack/echo/v4"
	"github.com/mcmx/nitejaguar/cmd/web"
	"github.com/mcmx/nitejaguar/internal/database"
)

// tenantsPageData loads the tenant registry, filtered to the caller's own
// tenant for non-superusers (open-bootstrap mode sees everything).
func (s *Server) tenantsPageData(c echo.Context) *web.TenantsPageData {
	user, open := s.webCurrentUser(c)
	data := &web.TenantsPageData{CurrentUser: s.navUser(c)}
	if open || database.IsSuperUser(user) {
		data.IsSuperuser = true
	}
	if c.QueryParam("created") != "" {
		data.Success = "Tenant created: " + c.QueryParam("created")
	}
	rows, err := s.db.ListTenants()
	if err != nil {
		data.Error = "Unable to load tenants"
		return data
	}
	for _, row := range rows {
		if !open && user != nil && !database.IsSuperUser(user) && row.Slug != user.TenantID {
			continue
		}
		contact := row.ContactEmail
		if contact == "" {
			contact = "—"
		}
		data.Tenants = append(data.Tenants, web.TenantView{
			ID: row.ID, Slug: row.Slug, Name: row.Name,
			ContactEmail: contact, AdminUserID: row.AdminUserID,
			Status: row.Status,
			CreatedAt: row.CreatedAt.Format("2006-01-02 15:04:05 MST"),
		})
	}
	return data
}

func (s *Server) tenantsPage(c echo.Context) error {
	user, open := s.webCurrentUser(c)
	if !open {
		if user == nil {
			return c.Redirect(http.StatusSeeOther, "/login")
		}
		if !database.HasRole(user.Role, database.RoleViewer) {
			return c.String(http.StatusForbidden, "login required")
		}
	}
	templ.Handler(web.TenantsPage(s.tenantsPageData(c))).ServeHTTP(c.Response(), c.Request())
	return nil
}

// createTenantWeb provisions a tenant + admin from the /tenants form.
// Superuser-only (open until the first user exists, mirroring user bootstrap).
func (s *Server) createTenantWeb(c echo.Context) error {
	user, open := s.webCurrentUser(c)
	actor := "api"
	if !open {
		if user == nil {
			return c.Redirect(http.StatusSeeOther, "/login")
		}
		if !database.IsSuperUser(user) {
			data := s.tenantsPageData(c)
			data.Error = "Tenant management requires a superuser (admin of the default tenant)."
			templ.Handler(web.TenantsPage(data)).ServeHTTP(c.Response(), c.Request())
			return nil
		}
		actor = user.ID
	}
	name := strings.TrimSpace(c.FormValue("name"))
	slug := strings.TrimSpace(c.FormValue("slug"))
	contactEmail := strings.TrimSpace(c.FormValue("contact_email"))
	adminUsername := strings.TrimSpace(c.FormValue("admin_username"))
	adminEmail := strings.TrimSpace(c.FormValue("admin_email"))
	adminPassword := c.FormValue("admin_password")
	data := s.tenantsPageData(c)
	if name == "" || adminEmail == "" {
		data.Error = "Tenant name and admin email are required."
		templ.Handler(web.TenantsPage(data)).ServeHTTP(c.Response(), c.Request())
		return nil
	}
	generated := strings.TrimSpace(adminPassword) == ""
	trow, tenantAdmin, plaintext, err := s.db.CreateTenant(name, slug, contactEmail, adminUsername, adminEmail, adminPassword)
	if err != nil {
		data.Error = "Failed to create tenant: " + err.Error()
		templ.Handler(web.TenantsPage(data)).ServeHTTP(c.Response(), c.Request())
		return nil
	}
	_ = s.db.LogAudit("tenant.create", trow.Slug, actor, trow.ID,
		"name="+trow.Name+" slug="+trow.Slug+" admin="+tenantAdmin.ID+" via web")
	if generated {
		data.NewPassword = plaintext
		data.NewTenant = trow.Slug
	}
	data.Success = "Tenant created: " + trow.Name + " (slug: " + trow.Slug + ", admin: " + tenantAdmin.Username + ")."
	// Refresh the listing (re-query so the new tenant appears).
	*data = *s.tenantsPageData(c)
	if generated {
		data.NewPassword = plaintext
		data.NewTenant = trow.Slug
	}
	data.Success = "Tenant created: " + trow.Name + " (slug: " + trow.Slug + ", admin: " + tenantAdmin.Username + ")."
	templ.Handler(web.TenantsPage(data)).ServeHTTP(c.Response(), c.Request())
	return nil
}

// suspendTenantWeb blocks a tenant. Superuser-only.
func (s *Server) suspendTenantWeb(c echo.Context) error {
	return s.setTenantStatusWeb(c, true)
}

// activateTenantWeb re-enables a tenant. Superuser-only.
func (s *Server) activateTenantWeb(c echo.Context) error {
	return s.setTenantStatusWeb(c, false)
}

func (s *Server) setTenantStatusWeb(c echo.Context, suspend bool) error {
	user, open := s.webCurrentUser(c)
	actor := "api"
	if !open {
		if user == nil {
			return c.Redirect(http.StatusSeeOther, "/login")
		}
		if !database.IsSuperUser(user) {
			return c.String(http.StatusForbidden, "tenant management requires a superuser")
		}
		actor = user.ID
	}
	id := c.Param("id")
	if strings.TrimSpace(id) == "" {
		return c.String(http.StatusBadRequest, "tenant id is required")
	}
	var action string
	if suspend {
		row, err := s.db.SuspendTenant(id)
		if err != nil {
			return c.String(http.StatusBadRequest, "Failed to suspend tenant: "+err.Error())
		}
		action = "tenant.suspend"
		_ = s.db.LogAudit(action, row.Slug, actor, row.ID, "suspended via web")
	} else {
		row, err := s.db.ActivateTenant(id)
		if err != nil {
			return c.String(http.StatusBadRequest, "Failed to activate tenant: "+err.Error())
		}
		action = "tenant.activate"
		_ = s.db.LogAudit(action, row.Slug, actor, row.ID, "activated via web")
	}
	return c.Redirect(http.StatusSeeOther, "/tenants")
}
