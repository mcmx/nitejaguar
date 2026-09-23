package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// NodeAssignment is a pending node execution enqueued by the server when a
// workflow advances across client boundaries. The dispatcher persists one
// row per (workflow, execution, node); owning clients pick it up via
// assignment polling and it is marked done when the node's result arrives.
type NodeAssignment struct {
	ent.Schema
}

// Fields of the NodeAssignment.
func (NodeAssignment) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			Immutable().
			Unique().
			NotEmpty(),
		field.String("tenant_id").
			Default("default"),
		field.String("workflow_id").
			NotEmpty(),
		field.String("execution_id").
			NotEmpty(),
		field.String("node_id").
			NotEmpty(),
		field.Text("payload_json").
			Default("").
			Comment("parent result payload forwarded as $input, JSON-encoded"),
		field.String("parent_action_id").
			Default(""),
		field.String("status").
			Default("pending").
			Comment("pending or done"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

// Edges of the NodeAssignment.
func (NodeAssignment) Edges() []ent.Edge {
	return nil
}
