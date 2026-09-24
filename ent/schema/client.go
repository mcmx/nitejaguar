package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// RemoteClient holds the schema definition for the RemoteClient entity.
type RemoteClient struct {
	ent.Schema
}

// Table sets the table name for the RemoteClient entity.
func (RemoteClient) Table() string {
	return "clients"
}

// Fields of the RemoteClient.
func (RemoteClient) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			Immutable().
			Unique().
			NotEmpty(),
		field.String("name").
			NotEmpty(),
		field.String("tenant_id").
			Default("default"),
		field.Strings("tags"),
		field.String("token_hash").
			NotEmpty(),
		field.Time("registered_at").
			Default(time.Now).
			Immutable(),
		field.Time("last_heartbeat").
			Default(time.Now).
			UpdateDefault(time.Now),
		field.Time("last_poll").
			Default(time.Now).
			UpdateDefault(time.Now),
		field.Bool("revoked").
			Default(false),
		field.Time("revoked_at").
			Optional().
			Nillable(),
		field.Text("dial_info").
			Default("").
			Comment("client-advertised dial info for P2P signaling (JSON, server never dials it)"),
	}
}

// Edges of the RemoteClient.
func (RemoteClient) Edges() []ent.Edge {
	return nil
}
