# Credentials

Secrets in Nitejaguar are stored server-side and referenced from workflow
nodes — never embedded in workflow definitions, logs, results, or
assignment payloads.

## Model

- **Credential entity** (`credential_` TypeIDs): `{id, tenant_id, name,
  type, scope, owner_id, secret_encrypted, description}`.
- **Node reference**: every node (actions AND triggers) may set
  `credential_ref` to a credential **id** (`credential_...`) or a
  credential **name**. The secret itself never appears in workflow JSON.
- **Scopes & resolution**: `tenant`, `group`, `user`. A name reference
  resolves most-specific-wins: **user > group > tenant**. An id reference
  resolves directly (tenant-isolated). Group/user identities arrive with
  the RBAC slice; until then fetch callers may pass `user_id`/`groups`
  and the server resolves against them.
- **Credential types** are declared by provider **collections** (strict
  **one-type-per-collection**: every action in a collection shares
  exactly the collection's type, no per-action overrides). Known types:
  `generic`, `token`, `username_password`, `aws`, `ssh_key`.
  The legacy `s3` type is rejected at creation — S3 lives inside the
   AWS collection and uses the `aws` type. The built-ins (`file`,
   `datetime`, `wait`, `transfer`, `email`, `filechange`, `filewatch`, `cron` and `webhook` triggers) form the `core` collection
  and need no credential. The collection registry
  (`Provider{Name, CredentialType, Actions[]}` in `common/providers.go`;
  `SupportedCredentialTypes` / `RequiredCredentialTypes` in
  `internal/actions/actions.go` delegate to it) is the source of truth.
  No new executables (EC2/S3) ship with the foundation — the AWS actions
  are registry placeholders for now.
- **Secret shapes** are declared per type in
  `common/credential_types.go` (`CredentialTypeDefs`), the single source
  of truth for the web form, API encoding, and creation-time validation:

  | Type | Stored secret | Fields |
  |---|---|---|
  | `generic` | raw string (verbatim) | `value` (required) |
  | `token` | raw string (verbatim) | `token` (required) |
  | `username_password` | JSON object | `username` (required), `password` (required) |
  | `ssh_key` | JSON object | `username` (optional), `private_key` PEM (required), `passphrase` (optional) |
  | `aws` | JSON object | `access_key_id` (required), `secret_access_key` (required), `region` (optional), `session_token` (optional, STS only) |

  Examples of stored (pre-encryption) secrets:

  ```json
  // username_password
  {"username": "svc", "password": "…"}
  // aws
  {"access_key_id": "AKIA…", "secret_access_key": "…", "region": "eu-west-1"}
  // ssh_key
  {"username": "deploy", "private_key": "-----BEGIN OPENSSH PRIVATE KEY-----…"}
  ```

  Fetch returns the same opaque string (`secret`); single-value types
  come back verbatim, multi-field types come back as the JSON object for
  the client to parse (see `DecodeCredentialSecret`).

## Type enforcement

- **Create**: the credential `type` must be a known type (generic
  families plus each collection's declared type); anything else
  (e.g. `s3`) is rejected with `400`. The secret must match the type's
  shape: raw non-empty for `generic`/`token`, a JSON object carrying
  every required field for `username_password`/`ssh_key`/`aws`
  (e.g. posting `"secret": "just-a-string"` for `username_password`
  fails with `400 missing required field "username"...`). Unknown
  `secret_fields` keys are rejected so typos fail fast.
- **Fetch**: when the request carries `workflow_id` + `node_id` resolving
  to a collection-typed node (e.g. an AWS `s3` node expects `aws`),
  the resolved credential's type must match, otherwise the fetch fails
  closed with `403` **before the secret is opened** (denials are logged
  server-side, never audited with secret material — successful fetches
  are audited as `credential.fetch`).
- **Fail open**: typeless (`core`) nodes, unknown workflows/nodes/actions,
  and fetches without node context impose no type constraint — dangling
  references stay allowed and older clients keep working.

## Encryption at rest

Secrets are sealed in a versioned envelope stored in `secret_encrypted`.
The server uses one configured backend for all tenants; tenants cannot
select a backend or provider key. External backends wrap a fresh random
AES-256 data-encryption key (DEK), while the secret itself is encrypted
locally and never sent to Vault or KMS. Tenant and credential IDs are
authenticated as encryption context and as AES-GCM additional data.

- Default backend: `CREDENTIALS_BACKEND=local` (or unset). The local
  backend writes a versioned AES-256-GCM envelope. `CREDENTIALS_KEY` accepts a
  64-char hex key, a base64 32-byte key, or any passphrase (hashed with
  SHA-256).
- When `CREDENTIALS_KEY` is unset, the server generates an ephemeral
  process-local key and logs a warning — stored secrets do not survive
  restarts. Set `CREDENTIALS_KEY` to a stable value for production.
- `CREDENTIALS_BACKEND=vault` uses Vault Transit. Configure
  `VAULT_ADDR`, `VAULT_TRANSIT_KEY`, and either `VAULT_TOKEN` or
  `VAULT_TOKEN_FILE`; optionally set `VAULT_TRANSIT_MOUNT` (default
  `transit`) and `VAULT_NAMESPACE`. The Transit key must be configured
  with `derived=true`, because the tenant/credential context is supplied
  to each wrap/unwrap operation. Use HTTPS in production. The Vault
  policy needs `update` on
  `<mount>/encrypt/<key>` and `<mount>/decrypt/<key>`. Vault token
  renewal and authentication are managed outside this application; a
  token file is read when the server starts.
- `CREDENTIALS_BACKEND=aws-kms` uses AWS KMS. Set `AWS_REGION` (or
  `AWS_DEFAULT_REGION`) and `AWS_KMS_KEY_ID` or `AWS_KMS_KEY_ARN`.
  Authentication uses the standard AWS SDK credential provider chain
  (environment credentials, shared config/profile, web identity, or
  workload/instance role); do not put AWS credentials in workflow or
  credential records. The runtime identity needs `kms:Encrypt` and
  `kms:Decrypt` on the configured key. Encryption context contains the
  tenant and credential IDs.
- External configuration is validated before serving requests. Provider
  operations fail closed; an outage prevents credential creation/fetch
  but does not prevent metadata-only list/get/delete operations. There
  is never a local-key fallback.
- Legacy rows written with the unversioned `base64(nonce|ciphertext)`
  format remain readable with the local backend. The first
  versioned-local envelope format is also readable for compatibility.

### Migration, rotation, and recovery

To move existing local credentials to an external backend:

1. Back up the database and record the working `CREDENTIALS_KEY`.
2. Configure and test provider access, then stop the server. Keep
   `CREDENTIALS_KEY` set to the old value while selecting the external
   backend; local envelopes still need the old key during migration.
3. Run `nitejaguar-server rewrap-credentials` against the same database.
   The command updates each row independently, skips rows already using
   the configured backend/key, and can be rerun after interruption. Use
   `--force` to rewrite all rows, for example after rotating a Vault
   Transit key version.
4. Start the server and verify JIT fetch and server-local workflow
   execution before removing the old key or old provider access.

Vault Transit ciphertext records the key version. Rotate the Transit key
using Vault's key-rotation operation; Vault retains old versions for
decryption. After verifying the new version is active, run
`rewrap-credentials --force` to wrap existing DEKs with the new version
before retiring old key versions. AWS KMS automatic key-material rotation
keeps old ciphertext decryptable by the same key; if changing KMS keys,
configure the new key and use the rewrap command while the old key remains
available for decrypt.

Database backups contain encrypted envelopes, not plaintext. A restore
requires the original local key or access to the exact Vault/KMS key and
its historical decryption versions. Keep key material, Vault key versions,
provider permissions, and AWS role access in the provider's recovery
system; a database backup alone cannot recover secrets if those are lost.
During a Vault/KMS outage, credential create/fetch and rewrap fail with a
sanitized error and no fallback; metadata-only operations continue.

## Just-in-time fetch

The server never ships secrets inside workflow assignments. The
executing client fetches the secret with its own token:

- `GET /api/credentials/{ref}/fetch?user_id=&groups=&workflow_id=&node_id=`
  (auth: `Authorization: Bearer <client-token>` or `X-Client-Token`).
- Tenant-isolated: a client only resolves credentials in its own tenant.
- Response: `{credential_id, name, type, secret, ttl_seconds}`.
  The client must hold the secret in memory only for `ttl_seconds`
  (60s) and never persist it to disk, logs, or results.
- Every fetch is audited (`credential.fetch`); creation and deletion are
  audited too (`credential.create`, `credential.delete`).
  See `GET /api/audit`.

Client SDK: `API.FetchCredential(ctx, ref, userID, groups, workflowID,
nodeID)` in `internal/client/client.go`, plus an in-memory TTL cache
helper on the runner for provider actions. Pending cross-client handoffs
resolve their node's `credential_ref` just-in-time before execution: a
failed fetch fails closed (the assignment stays pending server-side and
is retried on the next poll).

## Management API

Credential create/delete requires operator+ (own tenant; admin for
cross-tenant). See [RBAC](./rbac.md).

- `POST /api/credentials` — store a secret encrypted at rest
  (`{tenant_id?, name, type?, scope?, owner_id?, secret, secret_fields?,
  description?}`; `scope` is `tenant`/`group`/`user`, `owner_id`
  required for group/user; names are unique per tenant/scope/owner).
  Returns metadata only — the plaintext is accepted once and never
  returned. Send the secret either as `secret` (raw string for
  `generic`/`token`, JSON object string for multi-field types) or as
  structured `secret_fields` (`{username, password, …}` per the table
  above — the server validates and encodes it; unknown keys are
  rejected).
- `GET /api/credentials?tenant_id=` — list metadata (secrets never exposed).
- `GET /api/credentials/{id}` — single metadata (secrets never exposed).
- `DELETE /api/credentials/{id}` — delete a stored secret.

## Website

`GET /credentials` (navbar: **Credentials**) lists metadata with a
store form (name, type, scope, owner for group/user scope, per-type
secret fields, description) and per-credential delete buttons —
operator+ (own tenant; cross-tenant via the API as admin). The type
select drives the secret inputs (Alpine toggle, specs from
`CredentialTypeDefs`): `generic`/`token` show one password field,
`username_password` shows username + password, `ssh_key` shows optional
username + PEM textarea + optional passphrase, `aws` shows access key ID
+ secret access key + optional region/session token. Hidden inputs are
disabled so they never block submit or leak into the request; the owner
field only appears for `group`/`user` scope. Secrets are never
displayed; fields are accepted once and only metadata returns.

## Workflow usage

AWS-collection shape (the registry and enforcement exist; the EC2/S3
executables ship later — `ec2`/`s3` are registry placeholders today):

```json
{
  "id": "action_01h...",
  "action_type": "action",
  "action_name": "ec2",
  "credential_ref": "prod-aws",
  "arguments": {"region": "eu-west-1"},
  "conditions": {"entries": {}},
  "dependencies": ["trigger_01h..."]
}
```

An S3 node likewise lives in the AWS collection and references an
`aws`-type credential (there is no separate generic S3 collection).

- Import (`server -i`, `POST /api/workflows/import`) and clone
  (`server -c`, `POST /api/workflows/clone`) preserve `credential_ref`.
- The designer edits `credential_ref` per node as a name-or-id text
  field — no secret entry. Dangling references are allowed (the
  credential may be created after the workflow); the executing client
  fails closed at fetch time.
- Behavioral edge cases:
  - Name refs with no matching identity fall back to the tenant-scoped
    credential; if only group/user-scoped rows exist and the caller
    supplies no matching identity, fetch returns 404.
  - Id refs are strictly tenant-isolated: cross-tenant ids return 404.
  - Assignment polling and result payloads never contain secrets —
    only the reference travels in workflow definitions.
