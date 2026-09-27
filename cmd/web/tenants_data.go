package web

import (
	"github.com/mcmx/nitejaguar/cmd/web/modules"
)

// TenantView is registry metadata for the tenants page.
type TenantView struct {
	ID           string
	Slug         string
	Name         string
	ContactEmail string
	AdminUserID  string
	Status       string
	CreatedAt    string
}

// TenantsPageData drives the tenants page: the registry listing plus,
// for superusers, the provisioning form. NewPassword carries a freshly
// generated tenant-admin password exactly once after provisioning.
type TenantsPageData struct {
	CurrentUser *modules.NavUser
	Tenants     []TenantView
	IsSuperuser bool
	NewPassword string
	NewTenant   string
	Error       string
	Success     string
}
