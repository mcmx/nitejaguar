package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// TransferChunk is one ordered slice of a TransferSession payload moving
// through the server relay (the guaranteed path when WebRTC P2P is
// unavailable). Chunks are immutable; receivers fetch from a sequence
// offset and reassemble in order.
type TransferChunk struct {
	ent.Schema
}

// Table sets the table name for the TransferChunk entity.
func (TransferChunk) Table() string {
	return "transfer_chunks"
}

// Fields of the TransferChunk.
func (TransferChunk) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			Immutable().
			Unique().
			NotEmpty(),
		field.String("transfer_id").
			NotEmpty(),
		field.Int("seq").
			Min(0),
		field.Bytes("data").
			MaxLen(128 * 1024).
			Comment("raw chunk bytes, max 128KiB"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
	}
}

// Indexes of the TransferChunk.
func (TransferChunk) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("transfer_id", "seq").Unique(),
	}
}

// Edges of the TransferChunk.
func (TransferChunk) Edges() []ent.Edge {
	return nil
}
