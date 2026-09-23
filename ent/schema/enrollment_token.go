package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// EnrollmentToken is a tenant-scoped join token used for client self-registration.
// The tenant is derived from the token, never from client-supplied input.
type EnrollmentToken struct {
	ent.Schema
}

// Fields of the EnrollmentToken.
func (EnrollmentToken) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			Immutable().
			Unique().
			NotEmpty(),
		field.String("tenant_id").
			Default("default"),
		field.String("label").
			Default("").
			StructTag(`json:"label"`),
		field.String("token_hash").
			NotEmpty().
			Unique(),
		field.Time("expires_at").
			Optional().
			Nillable(),
		field.Int("max_uses").
			Default(0).
			Comment("0 means unlimited uses"),
		field.Int("use_count").
			Default(0),
		field.Bool("revoked").
			Default(false),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
	}
}

// Edges of the EnrollmentToken.
func (EnrollmentToken) Edges() []ent.Edge {
	return nil
}
