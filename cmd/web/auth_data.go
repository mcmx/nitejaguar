package web

import (
	"github.com/mcmx/nitejaguar/cmd/web/modules"
)

type LoginPageData struct {
	Error    string
	TenantID string
}

type AuditView struct {
	ID        string
	Action    string
	TenantID  string
	Actor     string
	Target    string
	Detail    string
	CreatedAt string
}

type AuditPageData struct {
	CurrentUser *modules.NavUser
	Entries     []AuditView
	Error       string
}

type UserView struct {
	ID        string
	TenantID  string
	Username  string
	Role      string
	Groups    []string
	Revoked   bool
	CreatedAt string
}

type UsersPageData struct {
	CurrentUser *modules.NavUser
	Users       []UserView
	Error       string
	Success     string
}

type EnrollmentTokenView struct {
	ID        string
	TenantID  string
	Label     string
	ExpiresAt string
	MaxUses   int
	UseCount  int
	Revoked   bool
	CreatedAt string
}

// EnrollResult carries a freshly minted enrollment token plaintext plus the
// ready-to-run client enrollment command (plaintext is shown once only).
type EnrollResult struct {
	ClientName string
	TokenID    string
	Label      string
	Token      string
	Command    string
}

type CredentialView struct {
	ID          string
	TenantID    string
	Name        string
	Type        string
	Scope       string
	OwnerID     string
	Description string
	CreatedAt   string
}

type CredentialsPageData struct {
	CurrentUser *modules.NavUser
	Credentials []CredentialView
	Types       []string
	Error       string
	Success     string
}

type ProfilePageData struct {
	CurrentUser *modules.NavUser
	User        UserView
	Error       string
	Success     string
}
