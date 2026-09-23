package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// AuditLog records security-relevant events (enrollment use, token ops,
// client revoke). It backs the full audit trail required by the roadmap.
type AuditLog struct {
	ent.Schema
}

// Fields of the AuditLog.
func (AuditLog) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			Immutable().
			Unique().
			NotEmpty(),
		field.String("action").
			NotEmpty().
			Comment("e.g. enrollment.use, enrollment.create, enrollment.revoke, client.revoke"),
		field.String("tenant_id").
			Default("default"),
		field.String("actor").
			Default("").
			Comment("who performed the action (client id, token id, or system)"),
		field.String("target").
			Default("").
			Comment("affected entity id"),
		field.String("detail").
			Default(""),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
	}
}

// Edges of the AuditLog.
func (AuditLog) Edges() []ent.Edge {
	return nil
}
