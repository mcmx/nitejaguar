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

- Workflow selects default client(s); each node can override which client runs it.
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

## 3. RBAC + website auth & management

- Login for the website; users, tenants, groups, roles/permissions.
- Manage from the UI/API: clients, workflows, executions, logs, audits.
- Enrollment-token and client-revoke actions gated by role.
- Full audit trail (who did what, when): logins, token ops, client
  revoke, workflow import/clone/enable, credential fetch.

## 4. Credentials model

- New `Credential` entity; nodes (actions AND triggers) hold a
  **reference** (`credential_ref`), never the secret. Secrets never live
  in workflow JSON, logs, or results.
- Scopes: tenant, group, user. Resolution: user > group > tenant.
- Credential types are declared by actions/providers
  (e.g. AWS keys, S3, generic username/password, token, SSH key).
- Secrets encrypted at rest; fetched just-in-time by the executing
  client over an authenticated endpoint; short TTL; every fetch audited.
- Designer support: pick a credential reference per node, no secret entry.

## 5. Providers (= Ansible collections) & actions

- A **provider is a collection** (Ansible-collection analog): e.g. an AWS
  collection ships multiple actions (EC2, S3, …). **S3 stays generic**
  since many providers speak the S3 API.
- One canonical `action_name` per action (category-free, per AGENTS.md).
- Credential types are declared by the collection and/or its actions;
  each action declares which credential type(s) it needs.

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
2. Client targeting + cross-client handoff fix
3. Credentials model (reference + JIT fetch + resolution)
4. RBAC + web auth + management pages + audit
5. Provider actions (AWS EC2, generic S3, …)
6. Client-to-client transfer (P2P + relay fallback)

## Open questions (for later slices)

- Web auth method: local users first, OIDC later?
- Secret encryption backend: DB-column encryption vs external KMS/vault?
- P2P transport choice (QUIC/WebRTC/raw TLS) and NAT traversal scope.
