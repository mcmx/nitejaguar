package server

import (
	"log"
	"net/http"
	"strings"

	"github.com/a-h/templ"
	"github.com/labstack/echo/v4"
	"github.com/mcmx/nitejaguar/cmd/web"
	"github.com/mcmx/nitejaguar/cmd/web/modules"
	"github.com/mcmx/nitejaguar/ent"
	"github.com/mcmx/nitejaguar/internal/database"
)

// sessionCookieName is the web login cookie holding the session plaintext.
const sessionCookieName = "nitejaguar_session"

// webCurrentUser resolves the web caller from the session cookie. It returns
// open=true when no users exist yet (fresh install / bootstrap mode).
func (s *Server) webCurrentUser(c echo.Context) (*ent.AppUser, bool) {
	if s.db == nil {
		return nil, false
	}
	n, err := s.db.CountUsers()
	if err != nil || n == 0 {
		return nil, true
	}
	cookie, err := c.Cookie(sessionCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return nil, false
	}
	user, _, err := s.db.AuthenticateSession(strings.TrimSpace(cookie.Value))
	if err != nil {
		return nil, false
	}
	return user, false
}

// requireWebLogin redirects anonymous browsers to /login once users exist.
// API, assets, health, docs, websocket, and the login page itself stay open.
func (s *Server) requireWebLogin(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		path := c.Request().URL.Path
		if path == "/login" || strings.HasPrefix(path, "/assets") ||
			strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/docs") ||
			strings.HasPrefix(path, "/openapi") || path == "/health" ||
			path == "/websocket" {
			return next(c)
		}
		user, open := s.webCurrentUser(c)
		if open || user != nil {
			return next(c)
		}
		// Users exist and the browser holds no valid session.
		return c.Redirect(http.StatusSeeOther, "/login")
	}
}

// navUser builds the navbar identity for a browser request. It returns nil
// for anonymous callers (menu hidden); in open-bootstrap mode (no users
// yet) it returns a synthetic admin so the menu stays usable until the
// first admin is created.
func (s *Server) navUser(c echo.Context) *modules.NavUser {
	user, open := s.webCurrentUser(c)
	if open {
		return &modules.NavUser{Username: "bootstrap", TenantID: "default", Role: database.RoleAdmin, LoggedIn: true}
	}
	if user == nil {
		return nil
	}
	return &modules.NavUser{ID: user.ID, Username: user.Username, TenantID: user.TenantID, Role: user.Role, LoggedIn: true}
}

// webActor returns the audit actor for web form posts.
func (s *Server) webActor(c echo.Context, minimum string) (*ent.AppUser, string, error) {
	user, open := s.webCurrentUser(c)
	if open {
		return nil, "api", nil
	}
	if user == nil {
		return nil, "", echo.NewHTTPError(http.StatusUnauthorized, "login required")
	}
	if !database.HasRole(user.Role, minimum) {
		return nil, "", echo.NewHTTPError(http.StatusForbidden, "role "+user.Role+" cannot perform this action (requires "+minimum+")")
	}
	return user, user.ID, nil
}

func (s *Server) loginPage(c echo.Context) error {
	if user, open := s.webCurrentUser(c); !open && user != nil {
		return c.Redirect(http.StatusSeeOther, "/")
	}
	templ.Handler(web.LoginPage(&web.LoginPageData{})).ServeHTTP(c.Response(), c.Request())
	return nil
}

func (s *Server) loginSubmit(c echo.Context) error {
	username := strings.TrimSpace(c.FormValue("username"))
	password := c.FormValue("password")
	tenantID := strings.TrimSpace(c.FormValue("tenant_id"))
	if tenantID == "" {
		tenantID = "default"
	}
	if username == "" || password == "" {
		templ.Handler(web.LoginPage(&web.LoginPageData{Error: "username and password are required", TenantID: tenantID})).ServeHTTP(c.Response(), c.Request())
		return nil
	}
	user, err := s.db.VerifyUser(tenantID, username, password)
	if err != nil {
		_ = s.db.LogAudit("auth.login", tenantID, username, "", "failed via web")
		templ.Handler(web.LoginPage(&web.LoginPageData{Error: "invalid credentials", TenantID: tenantID})).ServeHTTP(c.Response(), c.Request())
		return nil
	}
	_, plaintext, err := s.db.CreateSession(user.ID, database.SessionTTL)
	if err != nil {
		templ.Handler(web.LoginPage(&web.LoginPageData{Error: "failed to create session", TenantID: tenantID})).ServeHTTP(c.Response(), c.Request())
		return nil
	}
	_ = s.db.LogAudit("auth.login", user.TenantID, user.ID, user.ID, "username="+user.Username+" via web")
	log.Printf("user login via web: id=%s tenant=%s", user.ID, user.TenantID)
	c.SetCookie(&http.Cookie{
		Name:     sessionCookieName,
		Value:    plaintext,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(database.SessionTTL.Seconds()),
	})
	return c.Redirect(http.StatusSeeOther, "/")
}

func (s *Server) logoutWeb(c echo.Context) error {
	if cookie, err := c.Cookie(sessionCookieName); err == nil && strings.TrimSpace(cookie.Value) != "" {
		if user, _, err := s.db.AuthenticateSession(strings.TrimSpace(cookie.Value)); err == nil {
			_ = s.db.LogAudit("auth.logout", user.TenantID, user.ID, user.ID, "username="+user.Username+" via web")
		}
		_ = s.db.RevokeSession(strings.TrimSpace(cookie.Value))
	}
	c.SetCookie(&http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	return c.Redirect(http.StatusSeeOther, "/login")
}

func (s *Server) auditPage(c echo.Context) error {
	user, open := s.webCurrentUser(c)
	if !open && user == nil {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	data := &web.AuditPageData{CurrentUser: s.navUser(c)}
	logs, err := s.db.ListAuditLogs(200)
	if err != nil {
		data.Error = "Unable to load audit trail"
	} else {
		for _, l := range logs {
			if !open && user != nil && user.Role != database.RoleAdmin && l.TenantID != user.TenantID {
				continue
			}
			data.Entries = append(data.Entries, web.AuditView{
				ID: l.ID, Action: l.Action, TenantID: l.TenantID,
				Actor: l.Actor, Target: l.Target, Detail: l.Detail,
				CreatedAt: l.CreatedAt.Format("2006-01-02 15:04:05 MST"),
			})
		}
	}
	templ.Handler(web.AuditPage(data)).ServeHTTP(c.Response(), c.Request())
	return nil
}

func (s *Server) usersPage(c echo.Context) error {
	user, open := s.webCurrentUser(c)
	if !open {
		if user == nil {
			return c.Redirect(http.StatusSeeOther, "/login")
		}
		if !database.HasRole(user.Role, database.RoleOperator) {
			return c.String(http.StatusForbidden, "operator role required")
		}
	}
	templ.Handler(web.UsersPage(s.usersPageData(c))).ServeHTTP(c.Response(), c.Request())
	return nil
}
