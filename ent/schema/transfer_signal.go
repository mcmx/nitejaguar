package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// TransferSignal is one WebRTC signaling message (offer, answer, ICE
// candidate, bye) exchanged through the server control plane so two
// clients can establish a direct P2P DataChannel. Signals are
// store-and-forward: each side polls since an id. Payloads are SDP or
// ICE JSON blobs, size-capped.
type TransferSignal struct {
	ent.Schema
}

// Table sets the table name for the TransferSignal entity.
func (TransferSignal) Table() string {
	return "transfer_signals"
}

// Fields of the TransferSignal.
func (TransferSignal) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			Immutable().
			Unique().
			NotEmpty(),
		field.String("transfer_id").
			NotEmpty(),
		field.String("from_client_id").
			NotEmpty(),
		field.String("kind").
			NotEmpty().
			Comment("offer, answer, ice or bye"),
		field.Text("payload").
			Default("").
			Comment("SDP or ICE JSON, max 64KiB"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
	}
}

// Indexes of the TransferSignal.
func (TransferSignal) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("transfer_id", "created_at"),
	}
}

// Edges of the TransferSignal.
func (TransferSignal) Edges() []ent.Edge {
	return nil
}
