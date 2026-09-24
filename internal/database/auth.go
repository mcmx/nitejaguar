package database

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/mcmx/nitejaguar/ent"
	"github.com/mcmx/nitejaguar/ent/appuser"
	"github.com/mcmx/nitejaguar/ent/authsession"
	"go.jetify.com/typeid"
	"golang.org/x/crypto/bcrypt"
)

// RBAC roles and permissions (roadmap slice 4).
const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"

	// SessionTTL is the default login session lifetime.
	SessionTTL = 24 * time.Hour
)

// ValidRoles are the accepted user roles.
var ValidRoles = []string{RoleAdmin, RoleOperator, RoleViewer}

func validRole(r string) bool {
	for _, known := range ValidRoles {
		if r == known {
			return true
		}
	}
	return false
}

// roleRank orders roles by privilege: viewer < operator < admin.
func roleRank(r string) int {
	switch r {
	case RoleAdmin:
		return 3
	case RoleOperator:
		return 2
	case RoleViewer:
		return 1
	default:
		return 0
	}
}

// HasRole reports whether userRole meets the minimum required role.
func HasRole(userRole, minimum string) bool {
	return roleRank(userRole) >= roleRank(minimum)
}

// hashPassword bcrypt-hashes a plaintext password.
func hashPassword(plaintext string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("failed to hash password: %w", err)
	}
	return string(hash), nil
}

// CreateUser creates a website/API user. Usernames are unique per tenant.
// Role defaults to viewer; groups are optional memberships for credential
// resolution.
func (s *service) CreateUser(tenantID, username, password, role string, groups []string) (*ent.AppUser, error) {
	tenantID = normalizeTenant(tenantID)
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, fmt.Errorf("username is required")
	}
	if len(password) < 8 {
		return nil, fmt.Errorf("password must be at least 8 characters")
	}
	if role == "" {
		role = RoleViewer
	}
	if !validRole(role) {
		return nil, fmt.Errorf("unknown role %q (supported: admin, operator, viewer)", role)
	}
	ctx := context.Background()
	existing, err := s.client.AppUser.Query().
		Where(appuser.TenantID(tenantID), appuser.Username(username)).
		Only(ctx)
	if err == nil && existing != nil {
		return nil, fmt.Errorf("user %q already exists in tenant %q", username, tenantID)
	}
	if !ent.IsNotFound(err) {
		return nil, fmt.Errorf("failed to check existing user: %w", err)
	}
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	uid, _ := typeid.WithPrefix("user")
	create := s.client.AppUser.Create().
		SetID(uid.String()).
		SetTenantID(tenantID).
		SetUsername(username).
		SetPasswordHash(hash).
		SetRole(role)
	if len(groups) > 0 {
		create.SetGroups(groups)
	}
	row, err := create.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}
	return row, nil
}

// ListUsers returns users, optionally filtered by tenant (empty lists all).
func (s *service) ListUsers(tenantID string) ([]*ent.AppUser, error) {
	q := s.client.AppUser.Query()
	if tenantID != "" {
		q.Where(appuser.TenantID(tenantID))
	}
	rows, err := q.Order(ent.Asc(appuser.FieldUsername)).All(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}
	return rows, nil
}

// GetUser returns a user by id.
func (s *service) GetUser(id string) (*ent.AppUser, error) {
	row, err := s.client.AppUser.Get(context.Background(), id)
	if err != nil {
		return nil, fmt.Errorf("user not found: %w", err)
	}
	return row, nil
}

// CountUsers returns the total number of users (for open-mode detection).
func (s *service) CountUsers() (int, error) {
	n, err := s.client.AppUser.Query().Count(context.Background())
	if err != nil {
		return 0, fmt.Errorf("failed to count users: %w", err)
	}
	return n, nil
}

// RevokeUser disables a user's logins and revokes all its sessions.
func (s *service) RevokeUser(id string) error {
	ctx := context.Background()
	if _, err := s.client.AppUser.Get(ctx, id); err != nil {
		return fmt.Errorf("user not found: %w", err)
	}
	if err := s.client.AppUser.UpdateOneID(id).SetRevoked(true).Exec(ctx); err != nil {
		return fmt.Errorf("failed to revoke user: %w", err)
	}
	// Revoke all live sessions for the user (best effort).
	sessions, err := s.client.AuthSession.Query().
		Where(authsession.UserID(id), authsession.Revoked(false)).
		All(ctx)
	if err == nil {
		for _, sess := range sessions {
			_ = s.client.AuthSession.UpdateOneID(sess.ID).SetRevoked(true).Exec(ctx)
		}
	}
	return nil
}

// VerifyUser checks a username/password within a tenant. It returns the user
// row on success; revoked users never authenticate.
func (s *service) VerifyUser(tenantID, username, password string) (*ent.AppUser, error) {
	tenantID = normalizeTenant(tenantID)
	row, err := s.client.AppUser.Query().
		Where(appuser.TenantID(tenantID), appuser.Username(username)).
		Only(context.Background())
	if err != nil {
		return nil, fmt.Errorf("invalid credentials")
	}
	if row.Revoked {
		return nil, fmt.Errorf("invalid credentials")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(row.PasswordHash), []byte(password)); err != nil {
		return nil, fmt.Errorf("invalid credentials")
	}
	return row, nil
}

// CreateSession mints a login session for a user. The plaintext token is
// returned once; only its hash is stored.
func (s *service) CreateSession(userID string, ttl time.Duration) (*ent.AuthSession, string, error) {
	ctx := context.Background()
	user, err := s.client.AppUser.Get(ctx, userID)
	if err != nil {
		return nil, "", fmt.Errorf("user not found: %w", err)
	}
	if user.Revoked {
		return nil, "", fmt.Errorf("user is revoked")
	}
	if ttl <= 0 {
		ttl = SessionTTL
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, "", fmt.Errorf("failed to generate session token: %w", err)
	}
	plaintext := hex.EncodeToString(buf)
	expires := time.Now().Add(ttl)
	sid, _ := typeid.WithPrefix("session")
	row, err := s.client.AuthSession.Create().
		SetID(sid.String()).
		SetUserID(user.ID).
		SetTenantID(user.TenantID).
		SetTokenHash(hashToken(plaintext)).
		SetExpiresAt(expires).
		Save(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create session: %w", err)
	}
	return row, plaintext, nil
}

// AuthenticateSession resolves a session plaintext to its user. Expired or
// revoked sessions (or sessions of revoked users) never authenticate.
func (s *service) AuthenticateSession(token string) (*ent.AppUser, *ent.AuthSession, error) {
	if strings.TrimSpace(token) == "" {
		return nil, nil, fmt.Errorf("missing session token")
	}
	ctx := context.Background()
	sess, err := s.client.AuthSession.Query().
		Where(authsession.TokenHash(hashToken(token))).
		Only(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid session")
	}
	if sess.Revoked {
		return nil, nil, fmt.Errorf("invalid session")
	}
	if sess.ExpiresAt != nil && time.Now().After(*sess.ExpiresAt) {
		return nil, nil, fmt.Errorf("session expired")
	}
	user, err := s.client.AppUser.Get(ctx, sess.UserID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid session")
	}
	if user.Revoked {
		return nil, nil, fmt.Errorf("invalid session")
	}
	// Tenant drift guard: the session tenant must still match the user.
	if user.TenantID != sess.TenantID {
		return nil, nil, fmt.Errorf("invalid session")
	}
	return user, sess, nil
}

// RevokeSession revokes a single session by plaintext token.
func (s *service) RevokeSession(token string) error {
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("missing session token")
	}
	ctx := context.Background()
	sess, err := s.client.AuthSession.Query().
		Where(authsession.TokenHash(hashToken(token))).
		Only(ctx)
	if err != nil {
		return fmt.Errorf("session not found: %w", err)
	}
	if sess.Revoked {
		return nil
	}
	if err := s.client.AuthSession.UpdateOneID(sess.ID).SetRevoked(true).Exec(ctx); err != nil {
		return fmt.Errorf("failed to revoke session: %w", err)
	}
	return nil
}

// EnsureDefaultAdmin bootstraps the `default` tenant with an admin user when
// no users exist. It returns the plaintext password only when a user was
// newly created. ADMIN_PASSWORD (min 8 chars) pins the password; otherwise a
// random one is generated and must be rotated after first login.
func (s *service) EnsureDefaultAdmin() (username, plaintext string, created bool, err error) {
	n, err := s.CountUsers()
	if err != nil {
		return "", "", false, err
	}
	if n > 0 {
		return "", "", false, nil
	}
	password := strings.TrimSpace(os.Getenv("ADMIN_PASSWORD"))
	if password == "" {
		buf := make([]byte, 18)
		if _, err := rand.Read(buf); err != nil {
			return "", "", false, fmt.Errorf("failed to generate admin password: %w", err)
		}
		password = hex.EncodeToString(buf)
	} else if len(password) < 8 {
		return "", "", false, fmt.Errorf("ADMIN_PASSWORD must be at least 8 characters")
	}
	row, err := s.CreateUser("default", "admin", password, RoleAdmin, nil)
	if err != nil {
		// A concurrent bootstrap may have won the race; treat duplicates
		// as already-bootstrapped.
		if strings.Contains(err.Error(), "already exists") {
			return "", "", false, nil
		}
		return "", "", false, err
	}
	_ = s.LogAudit("user.create", "default", "system", row.ID, "bootstrap admin user=admin role=admin")
	return row.Username, password, true, nil
}

// EnsureDefaultAdminForTest is a tiny wrapper so server tests can log it.
func (s *service) ensureDefaultAdminLogged() {
	if username, password, created, err := s.EnsureDefaultAdmin(); err != nil {
		log.Printf("failed bootstrapping default admin: %v", err)
	} else if created {
		fmt.Printf("BOOTSTRAP admin user (tenant=default, username=%s): %s\n", username, password)
		log.Printf("BOOTSTRAP admin user created for tenant=default (see stdout for one-time password)")
	}
}
