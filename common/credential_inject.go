package common

// CredentialBinding carries a just-in-time secret into an action execution.
// It travels only in the in-memory inputs slice (never in workflow JSON,
// logs, results, or assignment payloads). The framework appends it just
// before Execute when the node sets credential_ref; actions that need auth
// (e.g. email) scan for it with FindCredential and fail closed on type
// mismatch. Actions that do not need it ignore extra inputs.
type CredentialBinding struct {
	// Ref is the node credential_ref (id or name) that produced this binding.
	Ref string
	// Type is the credential type (e.g. username_password).
	Type string
	// Secret is the plaintext secret (opaque per type; multi-field types
	// carry the JSON object for DecodeCredentialSecret).
	Secret string
	// Err carries a resolution failure (unknown ref, type fetch error).
	// When non-empty the action must fail closed with an error result
	// instead of falling back to anonymous execution.
	Err string
}

// WithCredential appends a credential binding to an execution inputs slice.
// Bindings with neither a secret nor an error are dropped so anonymous
// execution stays a plain single-element inputs slice.
func WithCredential(inputs []any, cred CredentialBinding) []any {
	if cred.Secret == "" && cred.Err == "" {
		return inputs
	}
	return append(inputs, cred)
}

// FindCredential returns the first credential binding in inputs, if any.
func FindCredential(inputs []any) *CredentialBinding {
	for _, in := range inputs {
		switch v := in.(type) {
		case CredentialBinding:
			c := v
			return &c
		case *CredentialBinding:
			if v != nil {
				return v
			}
		}
	}
	return nil
}

// CredentialSecretLookup is an optional WorkflowStore capability for
// server-local execution: resolve + open a credential inside the workflow
// tenant (no user/group identity; tenant-scoped fallback only). The
// database service implements it; fakes may omit it (email then runs
// anonymous). It lives in common so internal/workflow can use it without
// importing internal/database, keeping the client binary free of ent.
type CredentialSecretLookup interface {
	// ResolveCredentialSecret opens the secret for ref in tenantID.
	// It returns the plaintext secret and its credential type.
	ResolveCredentialSecret(tenantID, ref string) (secret, credType string, err error)
}
