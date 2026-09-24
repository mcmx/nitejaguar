package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// TransferSession is one client-to-client file delivery. The sender opens
// the session (init), streams bytes through the server relay (or WebRTC
// P2P with the relay as fallback), and the receiver downloads and
// completes it. Control plane always goes through the server; sessions
// are strictly tenant-isolated. Status: offered -> uploading -> ready ->
// done (or failed).
type TransferSession struct {
	ent.Schema
}

// Table sets the table name for the TransferSession entity.
func (TransferSession) Table() string {
	return "transfer_sessions"
}

// Fields of the TransferSession.
func (TransferSession) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			Immutable().
			Unique().
			NotEmpty(),
		field.String("tenant_id").
			Default("default"),
		field.String("workflow_id").
			Default(""),
		field.String("execution_id").
			Default(""),
		field.String("node_id").
			Default(""),
		field.String("sender_client_id").
			NotEmpty(),
		field.String("receiver_client_id").
			Default(""),
		field.Strings("receiver_tags").
			Optional(),
		field.String("file_name").
			Default("").
			Comment("source basename, informational only"),
		field.String("destination_file").
			NotEmpty().
			Comment("receiver-side destination path, resolved by the receiver"),
		field.String("permissions").
			Default("").
			Comment("optional octal mode applied by the receiver"),
		field.Int64("size").
			Default(0),
		field.String("sha256").
			Default(""),
		field.String("status").
			Default("offered").
			Comment("offered, uploading, ready, done or failed"),
		field.Bool("via_p2p").
			Default(false).
			Comment("true when bytes moved over WebRTC instead of the relay"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

// Indexes of the TransferSession.
func (TransferSession) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "status"),
		index.Fields("receiver_client_id", "status"),
	}
}

// Edges of the TransferSession.
func (TransferSession) Edges() []ent.Edge {
	return nil
}
