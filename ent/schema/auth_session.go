package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// AuthSession is a login session token for a user. Only the sha256 hash
// is persisted; the plaintext is returned once at login. Sessions expire
// and can be revoked on logout or user revoke.
type AuthSession struct {
	ent.Schema
}

// Table sets the table name for the AuthSession entity.
func (AuthSession) Table() string {
	return "auth_sessions"
}

// Fields of the AuthSession.
func (AuthSession) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			Immutable().
			Unique().
			NotEmpty(),
		field.String("user_id").
			NotEmpty(),
		field.String("tenant_id").
			Default("default"),
		field.String("token_hash").
			NotEmpty().
			Unique(),
		field.Time("expires_at").
			Optional().
			Nillable(),
		field.Bool("revoked").
			Default(false),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
	}
}

// Edges of the AuthSession.
func (AuthSession) Edges() []ent.Edge {
	return nil
}
