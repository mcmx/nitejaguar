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
- **Credential types** are declared by actions/providers:
  `generic`, `token`, `username_password`, `aws`, `s3`, `ssh_key`.
  The built-in local actions (`file`, `datetime`, `wait`, `filechange`
  trigger) need no credentials. Provider actions (e.g. AWS EC2, generic
  S3) declare what they need — see `RequiredCredentialTypes` in
  `internal/actions/actions.go`.

## Encryption at rest

Secrets are sealed with AES-256-GCM; only the
`base64(nonce|ciphertext)` envelope is stored in the `secret_encrypted`
column.

- Key source: `CREDENTIALS_KEY` env. Accepts a 64-char hex key, a
  base64 32-byte key, or any passphrase (hashed with SHA-256).
- When unset, the server generates an ephemeral process-local key and
  logs a warning — stored secrets do not survive restarts. Set
  `CREDENTIALS_KEY` to a stable value for production.

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

Credential issuance is not yet role-gated — that arrives with the RBAC
slice (same staging as enrollment-token issuance).

- `POST /api/credentials` — store a secret encrypted at rest
  (`{tenant_id?, name, type?, scope?, owner_id?, secret, description?}`;
  `scope` is `tenant`/`group`/`user`, `owner_id` required for
  group/user; names are unique per tenant/scope/owner). Returns metadata
  only — the plaintext is accepted once and never returned.
- `GET /api/credentials?tenant_id=` — list metadata (secrets never exposed).
- `GET /api/credentials/{id}` — single metadata (secrets never exposed).
- `DELETE /api/credentials/{id}` — delete a stored secret.

## Workflow usage

```json
{
  "id": "action_01h...",
  "action_type": "action",
  "action_name": "s3",
  "credential_ref": "prod-s3-backup",
  "arguments": {"bucket": "backups", "key": "db.sql"},
  "conditions": {"entries": {}},
  "dependencies": ["trigger_01h..."]
}
```

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
