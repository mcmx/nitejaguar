package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// WorkflowResult persists a node execution result (common.ResultData).
// The workflow engine dual-writes every result: the legacy JSON file in
// ./results/<result_id>.json and one row here. The DB is the queryable
// store (filter by workflow/execution); the files stay as a human-readable
// backup. Condition evaluation (per-entry booleans, derived nexts, first
// error) is persisted alongside the payload so routing decisions are
// visible in results.
type WorkflowResult struct {
	ent.Schema
}

// Table sets the table name for the WorkflowResult entity.
func (WorkflowResult) Table() string {
	return "workflow_results"
}

// Fields of the WorkflowResult.
func (WorkflowResult) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			Immutable().
			Unique().
			NotEmpty().
			Comment("result_id (result_ typeid)"),
		field.String("tenant_id").
			Default("default"),
		field.String("workflow_id").
			NotEmpty(),
		field.String("execution_id").
			Default(""),
		field.String("action_id").
			NotEmpty().
			Comment("node id that produced the result"),
		field.String("action_type").
			Default(""),
		field.String("action_name").
			Default(""),
		field.String("executor_id").
			Default(""),
		field.Text("payload_json").
			Default("").
			Comment("ResultData payload, JSON-encoded"),
		field.Text("condition_results_json").
			Default("").
			Comment("per-entry condition outcomes (entry ID -> matched), JSON-encoded"),
		field.Strings("nexts").
			Optional().
			Comment("downstream node IDs whose entry condition matched"),
		field.String("condition_error").
			Default("").
			Comment("first condition evaluation error, if any"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
	}
}

// Indexes of the WorkflowResult.
func (WorkflowResult) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("workflow_id", "execution_id"),
		index.Fields("execution_id"),
	}
}

// Edges of the WorkflowResult.
func (WorkflowResult) Edges() []ent.Edge {
	return nil
}
