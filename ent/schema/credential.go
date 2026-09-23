package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// Credential is a stored secret referenced by workflow nodes via
// credential_ref. Nodes (actions AND triggers) hold only the reference;
// the plaintext secret is never stored in workflow JSON, logs, results, or
// assignment payloads. Secrets are encrypted at rest and delivered
// just-in-time to the executing client over an authenticated endpoint.
//
// Scoping: tenant, group, or user. Resolution is most-specific-wins:
// user > group > tenant. Group/user identities arrive with the RBAC slice;
// until then callers may pass user_id/group_ids on fetch and the server
// resolves against them.
type Credential struct {
	ent.Schema
}

// Fields of the Credential.
func (Credential) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			Immutable().
			Unique().
			NotEmpty(),
		field.String("tenant_id").
			Default("default"),
		field.String("name").
			NotEmpty().
			Comment("human reference name; resolved with user > group > tenant priority"),
		field.String("type").
			Default("generic").
			Comment("credential type declared by actions/providers: generic, token, username_password, aws, s3, ssh_key"),
		field.String("scope").
			Default("tenant").
			Comment("tenant, group, or user"),
		field.String("owner_id").
			Default("").
			Comment("group or user id for group/user scopes; empty for tenant scope"),
		field.Text("secret_encrypted").
			NotEmpty().
			Sensitive().
			Comment("AES-GCM ciphertext (nonce prepended), base64-encoded; never exposed via API"),
		field.String("description").
			Default(""),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

// Edges of the Credential.
func (Credential) Edges() []ent.Edge {
	return nil
}
