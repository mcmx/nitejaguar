package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// AppUser is a website/API user for the RBAC slice. Users belong to a
// tenant, carry a role (admin > operator > viewer), and may hold group
// memberships used by credential resolution (user > group > tenant).
// Passwords are stored as bcrypt hashes; plaintext never persists.
type AppUser struct {
	ent.Schema
}

// Table sets the table name to avoid the reserved `user` keyword on
// some databases.
func (AppUser) Table() string {
	return "app_users"
}

// Fields of the AppUser.
func (AppUser) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			Immutable().
			Unique().
			NotEmpty(),
		field.String("tenant_id").
			Default("default"),
		field.String("username").
			NotEmpty().
			Comment("unique per tenant"),
		field.String("password_hash").
			NotEmpty().
			Sensitive(),
		field.String("role").
			Default("viewer").
			Comment("admin, operator, or viewer"),
		field.Strings("groups").
			Optional().
			Comment("group memberships for credential resolution"),
		field.Bool("revoked").
			Default(false),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

// Edges of the AppUser.
func (AppUser) Edges() []ent.Edge {
	return nil
}
