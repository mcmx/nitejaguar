package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// Tenant is a multi-tenant isolation boundary. Every other tenant-scoped
// row (users, workflows, credentials, clients, results, audit) carries a
// `tenant_id` slug that references Tenant.Slug. The default tenant ("default")
// always exists and its admins act as superusers: only they can create,
// suspend, or delete tenants.
type Tenant struct {
	ent.Schema
}

// Table sets the table name for the Tenant entity.
func (Tenant) Table() string {
	return "tenants"
}

// Fields of the Tenant.
func (Tenant) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			Immutable().
			Unique().
			NotEmpty(),
		field.String("slug").
			Unique().
			NotEmpty().
			Comment("human login identifier used as tenant_id on all scoped rows; e.g. default, acme"),
		field.String("name").
			NotEmpty().
			Comment("display name; required at creation"),
		field.String("contact_email").
			Default("").
			Comment("tenant contact / billing email; required at creation"),
		field.String("admin_user_id").
			Default("").
			Comment("AppUser id of the tenant admin created with the tenant"),
		field.String("status").
			Default("active").
			Comment("active or suspended; suspended tenants cannot log in or enroll"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

// Edges of the Tenant.
func (Tenant) Edges() []ent.Edge {
	return nil
}
