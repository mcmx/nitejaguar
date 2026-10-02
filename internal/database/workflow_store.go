package database

import (
	"github.com/mcmx/nitejaguar/common"
)

// Compile-time guarantee that the database service satisfies the workflow
// engine's persistence boundary. The client imports internal/workflow for its
// types but never links this package (no ent/sqlite in the client binary).
// The interface itself lives in common (leaf package) to avoid a
// workflow <-> database import cycle.
var _ common.WorkflowStore = (*service)(nil)
var _ common.CredentialSecretLookup = (*service)(nil)

// ListWorkflowRecords returns stored workflow definitions as plain records
// (no ent types) for the workflow engine.
func (s *service) ListWorkflowRecords(all, enabled bool) ([]common.WorkflowRecord, error) {
	rows, err := s.GetWorkflows(all, enabled)
	if err != nil {
		return nil, err
	}
	out := make([]common.WorkflowRecord, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		out = append(out, common.WorkflowRecord{
			ID:             row.ID,
			JSONDefinition: row.JSONDefinition,
			TenantID:       row.TenantID,
		})
	}
	return out, nil
}

// SaveResultRecord persists a node result, discarding the ent row.
func (s *service) SaveResultRecord(r common.ResultData) error {
	_, err := s.SaveResult(r)
	return err
}

// EnqueueNodeAssignmentRecord persists a pending node assignment,
// discarding the ent row.
func (s *service) EnqueueNodeAssignmentRecord(tenantID, workflowID, executionID, nodeID, parentActionID string, payload any) error {
	_, err := s.EnqueueNodeAssignment(tenantID, workflowID, executionID, nodeID, parentActionID, payload)
	return err
}

// ResolveCredentialSecret opens the secret for a server-local execution
// inside the workflow tenant (tenant-scoped fallback; no user/group
// identity). The plaintext lives only in the returned string — callers
// must keep it in memory, never log or persist it.
func (s *service) ResolveCredentialSecret(tenantID, ref string) (string, string, error) {
	row, err := s.ResolveCredential(tenantID, ref, "", nil)
	if err != nil {
		return "", "", err
	}
	secret, err := s.DecryptCredentialSecret(row)
	if err != nil {
		return "", "", err
	}
	return secret, row.Type, nil
}
