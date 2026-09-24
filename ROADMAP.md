# Nitejaguar Roadmap — Distributed Workflow Platform

Vision: Ansible-like flexibility, faster than n8n, with distributed clients
orchestrated through a central server.

## Locked decisions (2026-09-23)

- First slice: **tenant enrollment** (close open client registration).
- Credential resolution: **user > group > tenant** (most-specific wins).
- Secret delivery: **client fetches just-in-time** with its own token
  (server never ships secrets inside workflow assignments).
- Client-to-client transfer: **P2P first, server relay fallback**.
  Control plane always goes through the server.

## 1. Client targeting & distributed execution

Status: implemented. Workflows carry `default_client`/`default_client_tags`;
per-node `client`/`client_tags` override the default (empty = broadcast).
`IngestResult` persists one pending node assignment per downstream `next`
(idempotent per workflow/execution/node, `assign_` rows) and marks the
reporting node done; `POST /api/results` returns only caller-owned nexts
(foreign edges routed via assignments, not direct local execution).
`GET /api/clients/{id}/assignments` returns filtered definitions plus owned
`pending` handoffs with the parent payload as `$input`; clients execute
pending at most once per process and report back. Tenant-isolated, audited
(`assignment.enqueue`/`assignment.complete`). Designer + workflow JSON
support defaults and overrides; import/clone preserve them.
- Multiple clients execute different parts of the same workflow.
- Server is the dispatcher: it persists pending node assignments per client;
  clients pick up only their own nodes via assignment polling.
- Fix known gap: `PostResult` currently returns `nexts` to the executing
  client, so cross-client edges can silently drop. Nexts for foreign nodes
  must be routed via server-side assignments, not direct local execution.
- Designer + workflow JSON support for defaults and overrides.

## 2. Tenant enrollment & client lifecycle (FIRST) — DONE

Status: implemented. `POST /api/clients/register` requires an
`enrollment_token`/`join_token`; tenant is derived from the token
(client-supplied `tenant_id` ignored). Tokens are tenant-scoped with
expiry, `max_uses`, and revocation (`/api/enrollment/tokens*`);
clients can be revoked (`/api/clients/{id}/revoke`, auth rejected
thereafter). First server run bootstraps a one-time `default` token
(plaintext printed once). Every use/op is audit-logged (`/api/audit`).
Token issuance is not yet role-gated — that arrives with the RBAC slice.

- Clients self-register with a **tenant enrollment/join token**
  (scoped per tenant, expirable, limited uses, revocable).
- Tenant comes from the token, never from client-supplied input.
- Tenant (per RBAC) can create enrollment tokens, revoke clients,
  and block new joins by revoking the token.
- Bootstrap: first server run creates `default` tenant + one-time token.
- Audit every enrollment use. Closes today's open-registration hole
  (`POST /api/clients/register` accepts arbitrary tenant).

## 3. RBAC + website auth & management — DONE

Status: implemented. `AppUser` rows (bcrypt passwords, roles
`admin > operator > viewer`, optional `groups` for credential
resolution) and `AuthSession` tokens (24h TTL, `Authorization: Bearer`
/ `X-Auth-Token`, `HttpOnly` cookie on web). First server run
bootstraps a `default` admin (one-time password printed once;
`ADMIN_PASSWORD` pins it). Enrollment-token issuance/listing/revocation,
client revoke, credential create/delete, and workflow import/clone/enable
require operator+ (cross-tenant requires admin); user create/revoke is
admin-only; audit read is viewer+ (non-admins see their own tenant).
Before the first user the server runs in open-bootstrap mode. Website:
`/login`, `/logout`, `/audit`, `/users` pages; anonymous browsers
redirect to `/login` once users exist. Full audit trail (actor = session
user): `auth.login/logout`, `user.create/revoke`, `workflow.import/clone/
enable`, plus the existing enrollment, client, credential, and assignment
actions (`/api/audit`). Groups are lightweight membership strings for
now; a dedicated group API is a later slice.

## 4. Credentials model — DONE

Status: implemented. New `Credential` entity (`credential_` ids);
nodes (actions AND triggers) hold a `credential_ref` (id or name),
never the secret — workflow JSON, logs, results, and assignment
payloads carry only the reference; import/clone preserve it and the
designer edits it as a name-or-id field with no secret entry.
Scopes: tenant, group, user; name resolution is most-specific-wins
(user > group > tenant), id references resolve directly, strictly
tenant-isolated. Credential types are declared by actions/providers
(`generic`, `token`, `username_password`, `aws`, `s3`, `ssh_key`;
built-ins declare none). Secrets are AES-256-GCM encrypted at rest
(`CREDENTIALS_KEY`; ephemeral process-local key with a warning when
unset) and fetched just-in-time by the executing client over an
authenticated endpoint (`GET /api/credentials/{ref}/fetch`, client-token
auth, 60s memory-only TTL); the server never ships secrets inside
assignments. Management API (`POST`/`GET`/`DELETE /api/credentials`)
exposes metadata only. Every create/delete/fetch is audit-logged
(`credential.create`/`credential.delete`/`credential.fetch`).
Credential issuance is not yet role-gated — that arrives with the RBAC
slice.

## 5. Providers (= Ansible collections) — foundation only — DONE

Status: implemented. The collection foundation ships WITHOUT new
action executables (no EC2/S3 implementations yet).

- A **provider is a collection** (Ansible-collection analog) with a
  **strict one-credential-type-per-collection** rule: every action in
  the collection shares exactly the collection's credential type, no
  per-action overrides.
- Code registry: `Provider{Name, CredentialType, Actions[]}` in
  `common/providers.go` (`core` = `file`/`datetime`/`wait`/`filechange`
  with no credential; `aws` = `ec2`/`s3` placeholders with type `aws`);
  `SupportedCredentialTypes` / `RequiredCredentialTypes` in
  `internal/actions/actions.go` and the create-time type check in
  `internal/database/credentials.go` delegate to it. Types are
  provider-declared, not a static list.
- Built-ins (`file`, `datetime`, `wait`, `filechange` trigger) are the
  `core` collection with no credential.
- S3 lives **inside the AWS collection** and uses the `aws` credential
  type; the legacy `s3` credential type is rejected at creation.
- **Full enforcement**: `GET /api/credentials/{ref}/fetch` with
  `workflow_id` + `node_id` resolving to a collection-typed node
  requires the credential type to match (fails closed with `403`
  before the secret is opened). Typeless (`core`) nodes,
  unknown workflows/nodes/actions, and context-free fetches impose no
  constraint (dangling references stay allowed). Generic families
  (`generic`, `token`, `username_password`, `ssh_key`) remain usable
  anywhere.
- One canonical `action_name` per action (category-free, per AGENTS.md).
- Documented in `docs/providers.md` + `docs/credentials.md`
  (type enforcement); `docs/workflows.md` shows the AWS shape.

## 6. Client-to-client communication

- Example: copy a file from one client to another.
- Clients advertise dial info + online status (heartbeat); server is the
  signaling/control plane.
- Attempt direct P2P first; fall back to chunked relay through the server
  (the guaranteed path, consistent with "all data through the server").

## Guiding principles

- All control-plane data flows through the server.
- No secrets in workflow definitions, logs, or results — references only.
- Tenant isolation enforced server-side on every endpoint.
- Ansible-like expressiveness, n8n-beating performance, distributed by default.

## Build order

1. Tenant enrollment & client lifecycle ✅ done
2. Client targeting + cross-client handoff fix ✅ done
3. Credentials model (reference + JIT fetch + resolution) ✅ done
4. RBAC + web auth + management pages + audit ✅ done
5. Provider collections foundation (registry + shared credential type + enforcement; no new actions) ✅ done
6. Client-to-client transfer (P2P + relay fallback)

## Open questions (for later slices)

- Web auth method: local users first, OIDC later?
- Secret encryption backend: DB-column encryption vs external KMS/vault?
- P2P transport choice (QUIC/WebRTC/raw TLS) and NAT traversal scope.
