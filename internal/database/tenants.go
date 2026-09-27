package database

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/mail"
	"strings"
	"time"

	"github.com/mcmx/nitejaguar/ent"
	"github.com/mcmx/nitejaguar/ent/appuser"
	"github.com/mcmx/nitejaguar/ent/tenant"
	"go.jetify.com/typeid"
)

// Multi-tenant model: Tenant rows are the registry of isolation boundaries.
// Every other tenant-scoped row carries a `tenant_id` slug referencing
// Tenant.Slug. The `default` tenant always exists; admins in the default
// tenant are superusers (the initial bootstrap admin is one of them).
const (
	DefaultTenantSlug = "default"

	TenantActive    = "active"
	TenantSuspended = "suspended"
)

// IsSuperUser reports whether a user is a superuser: an admin of the
// default tenant. Only superusers can create, suspend, or delete tenants
// and perform cross-tenant operations. Tenant-local admins (role admin in
// any other tenant) manage only their own tenant.
func IsSuperUser(user *ent.AppUser) bool {
	if user == nil || user.Revoked {
		return false
	}
	return user.Role == RoleAdmin && normalizeTenant(user.TenantID) == DefaultTenantSlug
}

// CanCrossTenant reports whether a user may operate outside its own tenant.
// In open-bootstrap mode (nil caller) everything is allowed.
func CanCrossTenant(user *ent.AppUser) bool {
	if user == nil {
		return true
	}
	return IsSuperUser(user)
}

// validateEmail rejects empty/malformed contact emails for tenant creation.
func validateEmail(email string) error {
	email = strings.TrimSpace(email)
	if email == "" {
		return fmt.Errorf("email is required")
	}
	if strings.Contains(email, " ") || !strings.Contains(email, "@") {
		return fmt.Errorf("invalid email %q", email)
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return fmt.Errorf("invalid email %q", email)
	}
	return nil
}

// slugifyTenant converts a display name into a URL/login-safe slug:
// lowercase alphanumerics joined by single hyphens.
func slugifyTenant(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	prevHyphen := true // avoid leading hyphens
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevHyphen = false
		default:
			if !prevHyphen {
				b.WriteRune('-')
				prevHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// validTenantSlug ensures an explicit slug is login-safe.
func validTenantSlug(slug string) error {
	if slug == "" {
		return fmt.Errorf("tenant slug is required")
	}
	if slug != strings.ToLower(slug) {
		return fmt.Errorf("tenant slug %q must be lowercase", slug)
	}
	for _, r := range slug {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return fmt.Errorf("tenant slug %q may only contain lowercase letters, digits, and hyphens", slug)
		}
	}
	if strings.HasPrefix(slug, "-") || strings.HasSuffix(slug, "-") || strings.Contains(slug, "--") {
		return fmt.Errorf("tenant slug %q must not start/end with a hyphen or contain double hyphens", slug)
	}
	return nil
}

func validTenantStatus(status string) bool {
	return status == TenantActive || status == TenantSuspended
}

// EnsureDefaultTenant creates the `default` tenant row when missing. It is
// idempotent and runs on server startup before the admin bootstrap.
func (s *service) EnsureDefaultTenant() (*ent.Tenant, error) {
	ctx := context.Background()
	if existing, err := s.client.Tenant.Query().Where(tenant.Slug(DefaultTenantSlug)).Only(ctx); err == nil {
		return existing, nil
	} else if !ent.IsNotFound(err) {
		return nil, fmt.Errorf("failed to check default tenant: %w", err)
	}
	tid, _ := typeid.WithPrefix("tenant")
	row, err := s.client.Tenant.Create().
		SetID(tid.String()).
		SetSlug(DefaultTenantSlug).
		SetName("Default").
		SetContactEmail("").
		SetStatus(TenantActive).
		Save(ctx)
	if err != nil {
		// A concurrent bootstrap may have won the race.
		if existing, rerr := s.client.Tenant.Query().Where(tenant.Slug(DefaultTenantSlug)).Only(ctx); rerr == nil {
			return existing, nil
		}
		return nil, fmt.Errorf("failed to create default tenant: %w", err)
	}
	return row, nil
}

// ListTenants returns all tenant rows ordered by name.
func (s *service) ListTenants() ([]*ent.Tenant, error) {
	rows, err := s.client.Tenant.Query().Order(ent.Asc(tenant.FieldName)).All(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to list tenants: %w", err)
	}
	return rows, nil
}

// GetTenant resolves a tenant by id or slug.
func (s *service) GetTenant(idOrSlug string) (*ent.Tenant, error) {
	idOrSlug = strings.TrimSpace(idOrSlug)
	if idOrSlug == "" {
		return nil, fmt.Errorf("tenant id is required")
	}
	ctx := context.Background()
	if row, err := s.client.Tenant.Get(ctx, idOrSlug); err == nil {
		return row, nil
	}
	row, err := s.client.Tenant.Query().Where(tenant.Slug(idOrSlug)).Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("tenant not found: %w", err)
	}
	return row, nil
}

// TenantStatus returns the status of a tenant slug. Unknown slugs (legacy
// tenant strings with no registry row) report active so pre-tenant data
// keeps working; the default tenant is never suspended.
func (s *service) TenantStatus(slug string) string {
	slug = normalizeTenant(slug)
	if slug == DefaultTenantSlug {
		return TenantActive
	}
	row, err := s.client.Tenant.Query().Where(tenant.Slug(slug)).Only(context.Background())
	if err != nil {
		return TenantActive
	}
	if row.Status == TenantSuspended {
		return TenantSuspended
	}
	return TenantActive
}

// IsTenantSuspended reports whether logins, sessions, enrollment, and client
// registration must be refused for a tenant slug.
func (s *service) IsTenantSuspended(slug string) bool {
	return s.TenantStatus(slug) == TenantSuspended
}

// CreateTenant provisions a new tenant plus its default admin user.
//
// Required: name and adminEmail. The slug is derived from the name
// (callers may pin it via slugOverride); adminUsername defaults to
// "admin"; when adminPassword is empty a random one is generated and
// returned once (never stored in plaintext).
//
// The tenant starts active. On admin-user failure the tenant row is rolled
// back so no half-provisioned tenant remains.
func (s *service) CreateTenant(name, slugOverride, contactEmail, adminUsername, adminEmail, adminPassword string) (*ent.Tenant, *ent.AppUser, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil, "", fmt.Errorf("tenant name is required")
	}
	if len(name) > 100 {
		return nil, nil, "", fmt.Errorf("tenant name must be at most 100 characters")
	}
	adminEmail = strings.TrimSpace(adminEmail)
	if err := validateEmail(adminEmail); err != nil {
		return nil, nil, "", err
	}
	contactEmail = strings.TrimSpace(contactEmail)
	if contactEmail == "" {
		contactEmail = adminEmail
	} else if err := validateEmail(contactEmail); err != nil {
		return nil, nil, "", err
	}
	adminUsername = strings.TrimSpace(adminUsername)
	if adminUsername == "" {
		adminUsername = "admin"
	}
	generatedPassword := false
	if adminPassword == "" {
		buf := make([]byte, 18)
		if _, err := rand.Read(buf); err != nil {
			return nil, nil, "", fmt.Errorf("failed to generate tenant admin password: %w", err)
		}
		adminPassword = hex.EncodeToString(buf)
		generatedPassword = true
	} else if len(adminPassword) < 8 {
		return nil, nil, "", fmt.Errorf("password must be at least 8 characters")
	}

	slug := strings.TrimSpace(slugOverride)
	if slug == "" {
		slug = slugifyTenant(name)
	}
	if slug == "" {
		return nil, nil, "", fmt.Errorf("tenant name %q yields an empty slug; provide an explicit slug", name)
	}
	if err := validTenantSlug(slug); err != nil {
		return nil, nil, "", err
	}
	if slug == DefaultTenantSlug {
		return nil, nil, "", fmt.Errorf("tenant slug %q is reserved", DefaultTenantSlug)
	}

	ctx := context.Background()
	if existing, err := s.client.Tenant.Query().Where(tenant.Slug(slug)).Only(ctx); err == nil && existing != nil {
		return nil, nil, "", fmt.Errorf("tenant %q already exists", slug)
	} else if !ent.IsNotFound(err) {
		return nil, nil, "", fmt.Errorf("failed to check existing tenant: %w", err)
	}
	if existing, err := s.client.Tenant.Query().Where(tenant.Name(name)).Only(ctx); err == nil && existing != nil {
		return nil, nil, "", fmt.Errorf("tenant name %q is already taken", name)
	} else if !ent.IsNotFound(err) {
		return nil, nil, "", fmt.Errorf("failed to check existing tenant name: %w", err)
	}

	tid, _ := typeid.WithPrefix("tenant")
	trow, err := s.client.Tenant.Create().
		SetID(tid.String()).
		SetSlug(slug).
		SetName(name).
		SetContactEmail(contactEmail).
		SetStatus(TenantActive).
		Save(ctx)
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to create tenant: %w", err)
	}

	user, err := s.CreateUser(slug, adminUsername, adminPassword, RoleAdmin, nil)
	if err != nil {
		_ = s.client.Tenant.DeleteOneID(trow.ID).Exec(ctx)
		if strings.Contains(err.Error(), "already exists") {
			return nil, nil, "", fmt.Errorf("tenant admin %q already exists in tenant %q: %w", adminUsername, slug, err)
		}
		return nil, nil, "", err
	}
	if err := s.client.AppUser.UpdateOneID(user.ID).SetEmail(adminEmail).Exec(ctx); err != nil {
		// Email is metadata; a failure here must not fail provisioning,
		// but it is logged for the operator.
		log.Printf("tenant provisioning: failed to set admin email for tenant=%s user=%s: %v", slug, user.ID, err)
	} else {
		user.Email = adminEmail
	}
	if err := s.client.Tenant.UpdateOneID(trow.ID).SetAdminUserID(user.ID).SetUpdatedAt(time.Now()).Exec(ctx); err != nil {
		log.Printf("tenant provisioning: failed to link admin user for tenant=%s user=%s: %v", slug, user.ID, err)
	} else {
		trow.AdminUserID = user.ID
	}
	_ = generatedPassword
	return trow, user, adminPassword, nil
}

// SetTenantStatus moves a tenant between active and suspended. The default
// tenant can never be suspended. Suspending does not revoke existing
// sessions immediately, but it blocks new logins, new sessions, enrollment
// token mint/consume, and client registration for that tenant.
func (s *service) SetTenantStatus(idOrSlug, status string) (*ent.Tenant, error) {
	if !validTenantStatus(status) {
		return nil, fmt.Errorf("unknown tenant status %q (supported: active, suspended)", status)
	}
	row, err := s.GetTenant(idOrSlug)
	if err != nil {
		return nil, err
	}
	if row.Slug == DefaultTenantSlug && status == TenantSuspended {
		return nil, fmt.Errorf("the default tenant cannot be suspended")
	}
	if row.Status == status {
		return row, nil
	}
	updated, err := s.client.Tenant.UpdateOneID(row.ID).
		SetStatus(status).
		SetUpdatedAt(time.Now()).
		Save(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to update tenant status: %w", err)
	}
	return updated, nil
}

// SuspendTenant marks a tenant suspended (see SetTenantStatus).
func (s *service) SuspendTenant(idOrSlug string) (*ent.Tenant, error) {
	return s.SetTenantStatus(idOrSlug, TenantSuspended)
}

// ActivateTenant marks a tenant active again.
func (s *service) ActivateTenant(idOrSlug string) (*ent.Tenant, error) {
	return s.SetTenantStatus(idOrSlug, TenantActive)
}

// DeleteTenant removes a tenant registry row. Scoped rows (users,
// workflows, credentials, clients, results, audit) keep their tenant_id
// and become superuser-visible orphans; they are not cascade-deleted.
// The default tenant can never be deleted.
func (s *service) DeleteTenant(idOrSlug string) error {
	row, err := s.GetTenant(idOrSlug)
	if err != nil {
		return err
	}
	if row.Slug == DefaultTenantSlug {
		return fmt.Errorf("the default tenant cannot be deleted")
	}
	if err := s.client.Tenant.DeleteOneID(row.ID).Exec(context.Background()); err != nil {
		return fmt.Errorf("failed to delete tenant: %w", err)
	}
	return nil
}

// SetUserEmail updates a user's contact email.
func (s *service) SetUserEmail(userID, email string) error {
	email = strings.TrimSpace(email)
	if email != "" {
		if err := validateEmail(email); err != nil {
			return err
		}
	}
	ctx := context.Background()
	if _, err := s.client.AppUser.Get(ctx, userID); err != nil {
		return fmt.Errorf("user not found: %w", err)
	}
	if err := s.client.AppUser.UpdateOneID(userID).SetEmail(email).Exec(ctx); err != nil {
		return fmt.Errorf("failed to update user email: %w", err)
	}
	return nil
}

// CountTenantUsers returns the number of users in a tenant slug.
func (s *service) CountTenantUsers(slug string) (int, error) {
	n, err := s.client.AppUser.Query().Where(appuser.TenantID(normalizeTenant(slug))).Count(context.Background())
	if err != nil {
		return 0, fmt.Errorf("failed to count tenant users: %w", err)
	}
	return n, nil
}
