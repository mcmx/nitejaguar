package common

// Provider is a collection (Ansible-collection analog): a named group of
// actions that share exactly one credential type. The rule is strict —
// every action in the collection uses the collection's type, with no
// per-action overrides. An empty CredentialType means the collection
// needs no credential (the `core` built-ins).
type Provider struct {
	// Name is the collection name (e.g. "core", "aws").
	Name string
	// CredentialType is the single credential type shared by every
	// action in the collection. Empty means credential-free.
	CredentialType string
	// Actions lists the canonical action_names in the collection.
	// Some entries (e.g. AWS) are registry placeholders whose
	// executables ship in a later slice.
	Actions []string
}

// Providers returns the known provider collections. The registry is the
// source of truth for credential types: types are provider-declared,
// not a hardcoded list.
func Providers() []Provider {
	return []Provider{
		{
			Name:           "core",
			CredentialType: "",
			Actions:        []string{"file", "datetime", "wait", "filechange"},
		},
		{
			Name:           "aws",
			CredentialType: "aws",
			Actions:        []string{"ec2", "s3"},
		},
	}
}

// ProviderForAction resolves an action_name to its collection.
// It reports false for unknown actions.
func ProviderForAction(actionName string) (Provider, bool) {
	for _, p := range Providers() {
		for _, a := range p.Actions {
			if a == actionName {
				return p, true
			}
		}
	}
	return Provider{}, false
}

// CredentialTypeForAction returns the credential type shared by the
// action's collection. An empty type with known=true (the `core`
// collection) means the action runs without a credential. known=false
// means the action is not in any collection.
func CredentialTypeForAction(actionName string) (ctype string, known bool) {
	p, ok := ProviderForAction(actionName)
	if !ok {
		return "", false
	}
	return p.CredentialType, true
}

// GenericCredentialTypes are credential families usable by any node as an
// optional extra (every node may set credential_ref). They belong to no
// collection; collection-scoped fetches are still strictly enforced.
func GenericCredentialTypes() []string {
	return []string{"generic", "token", "username_password", "ssh_key"}
}

// KnownCredentialTypes returns every credential type accepted at
// creation: the generic families plus each collection's declared type
// (deduplicated, stable order). The legacy `s3` type is intentionally
// absent — S3 lives in the AWS collection and uses the `aws` type.
func KnownCredentialTypes() []string {
	seen := make(map[string]struct{})
	var out []string
	for _, t := range GenericCredentialTypes() {
		if _, ok := seen[t]; !ok {
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	for _, p := range Providers() {
		if p.CredentialType == "" {
			continue
		}
		if _, ok := seen[p.CredentialType]; !ok {
			seen[p.CredentialType] = struct{}{}
			out = append(out, p.CredentialType)
		}
	}
	return out
}
