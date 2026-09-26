package modules

// NavUser carries the minimal signed-in identity the navbar and pages
// need. A nil *NavUser means anonymous (logged out).
type NavUser struct {
	ID       string
	Username string
	TenantID string
	Role     string
	LoggedIn bool
}

// IsAdmin reports whether the nav user is an admin.
func (u *NavUser) IsAdmin() bool {
	return u != nil && u.LoggedIn && u.Role == "admin"
}

// IsOperator reports whether the nav user meets operator+ (operator/admin).
func (u *NavUser) IsOperator() bool {
	return u != nil && u.LoggedIn && (u.Role == "admin" || u.Role == "operator")
}
