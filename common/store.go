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
// keeping the client binary free of ent/sqlite/echo.
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
