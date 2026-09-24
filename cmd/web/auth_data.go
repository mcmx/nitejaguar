package web

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
	Entries []AuditView
	Error   string
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
	Users []UserView
	Error string
}
