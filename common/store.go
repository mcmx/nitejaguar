package common

// WorkflowRecord is the persistence projection of a workflow definition.
// It mirrors the fields the workflow engine needs from the database without
// importing ent, so client builds never link the server DB stack.
type WorkflowRecord struct {
	ID             string
	JSONDefinition string
	TenantID       string
}

// WorkflowStore is the persistence boundary for the workflow engine.
// The server provides a database-backed implementation; tests can supply a
// fake. It lives in common (the shared-contracts leaf package) so that
// internal/workflow can use it without importing internal/database, and
// internal/database can implement it without creating an import cycle —
// keeping the client binary free of ent/db-driver/echo.
type WorkflowStore interface {
	// SaveWorkflow saves a workflow definition (upsert by id).
	SaveWorkflow(workflowID string, jsonDef string, tenantID string) error
	// ListWorkflowRecords returns stored workflow definitions.
	ListWorkflowRecords(all, enabled bool) ([]WorkflowRecord, error)
	// SaveResultRecord persists a node result (idempotent per result_id).
	SaveResultRecord(r ResultData) error
	// EnqueueNodeAssignmentRecord persists a pending node assignment
	// (idempotent per workflow/execution/node).
	EnqueueNodeAssignmentRecord(tenantID, workflowID, executionID, nodeID, parentActionID string, payload any) error
	// CompleteNodeAssignment marks a pending assignment done (no-op if unknown).
	CompleteNodeAssignment(workflowID, executionID, nodeID string) error
	// LogAudit appends an audit trail entry.
	LogAudit(action, tenantID, actor, target, detail string) error
}

// ClientTargetLookup is an optional WorkflowStore capability: it resolves
// the tag set of a registered client so the engine can evaluate node
// targeting when deciding which executor owns a downstream node.
//
// It is deliberately separate from WorkflowStore so client-side fakes and
// the client binary keep working unchanged. A store that does not
// implement it is treated as "tags unknown": tag-targeted nodes are then
// routed through persisted assignments rather than handed back to the
// reporting client. Dispatch stays single-owner either way — a node is
// never both returned and enqueued.
type ClientTargetLookup interface {
	// ClientTags returns the tags of a registered client. ok is false
	// when the id is unknown or the store cannot answer.
	ClientTags(clientID string) (tags []string, ok bool)
}
