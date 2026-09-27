# Tenants & Superusers

Multi-tenant isolation: every user, workflow, credential, client, result,
transfer, enrollment token, and audit row belongs to exactly one tenant
(`tenant_id`). The `tenants` table is the registry of those boundaries.

## Superusers

Admins of the `default` tenant are **superusers**. The bootstrap admin
(`admin` in `default`, created on first run) is the initial superuser.

- Only superusers manage the tenant registry (create, suspend, activate,
  delete) and perform **cross-tenant** operations.
- Tenant-local admins (`admin` in any other tenant) manage everything
  **inside their own tenant** — users, passwords, tokens, clients,
  credentials, workflows — but get `403` outside it.
- Operators and viewers are always scoped to their own tenant
  (a missing `tenant_id` defaults to the caller's tenant).

## Provisioning

`POST /api/tenants` (superuser only) requires a tenant **name** and an
**admin email**, and creates the tenant plus its default admin user:

```bash
AUTH="Authorization: Bearer <superuser-token>"

curl -s localhost:8080/api/tenants -H "$AUTH" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Acme Corp","admin_email":"boss@acme.example"}'
# → {"id":"tenant_...","slug":"acme-corp","name":"Acme Corp",
#    "status":"active","admin_user_id":"user_...",
#    "admin_password":"<one-time>","generated_password":true}
```

- `slug` is auto-derived from the name (`Acme Corp` → `acme-corp`) and
  becomes the `tenant_id` used at login and on every scoped row. Pass an
  explicit `slug` to pin it (lowercase letters, digits, hyphens).
- `admin_username` defaults to `admin`; `contact_email` defaults to the
  admin email.
- Omit `admin_password` to have the server generate one — returned
  **once** as `admin_password` (never stored in plaintext, never echoed
  for caller-supplied passwords). Hand it to the tenant admin, who must
  change it after first login (`POST /api/auth/password`).
- Names and slugs are unique; `default` is reserved. Failures roll back
  so no half-provisioned tenant remains.
- Audited as `tenant.create` (actor = the superuser).

The website mirrors this at `GET /tenants` (navbar: **Tenants**):
superusers get a create-tenant form plus per-tenant suspend/activate
buttons; everyone else sees only their own tenant card. A generated
password is shown once on the page after provisioning.

## Lifecycle

| Operation | Endpoint | Effect |
|---|---|---|
| List | `GET /api/tenants` (viewer+) | Superusers see all; others see only their own tenant |
| Get | `GET /api/tenants/{id}` (viewer+, id or slug) | Same scoping; cross-tenant reads need a superuser |
| Suspend | `POST /api/tenants/{id}/suspend` (superuser) | Blocks new logins, sessions, enrollment mint/consume, and client registration; existing sessions stop authenticating |
| Activate | `POST /api/tenants/{id}/activate` (superuser) | Re-enables a suspended tenant |
| Delete | `DELETE /api/tenants/{id}` (superuser) | Removes the registry row only — scoped rows keep their `tenant_id` as superuser-visible orphans (no cascade) |

The `default` tenant can never be suspended or deleted.

Suspended tenants fail closed: `VerifyUser`, `CreateSession`,
`AuthenticateSession`, `CreateEnrollmentToken`,
`ConsumeEnrollmentToken`, and `RegisterClient` all refuse with
`invalid credentials` / `tenant is suspended`, so suspended users are
locked out even with a previously valid session. Unknown `tenant_id`
strings (pre-registry legacy data) are treated as active so old rows
keep working.

## Read/write binding

Every read and write is tenant-bound (verified by
`TestAnonymousReadsDenied`, `TestCrossTenantReadsDenied`,
`TestCrossTenantWritesDenied`, `TestWebCrossTenantDenials`):

- Management reads (`workflows`, `clients`, `credentials`, `users`,
  `audit`, `tenants`, results/clients/users web pages) require a
  session (viewer+); superusers see all, everyone else sees only their
  own tenant. Foreign single objects return `404`, never `403`, so one
  tenant cannot probe another's ids. Anonymous calls get `401` once any
  user exists.
- Writes resolve the tenant from the authenticated caller, never from
  client input: `POST /api/results` and the designer save path ignore a
  client-supplied `tenant_id`, the reported workflow/action must belong
  to the caller's tenant, and workflow ids cannot be hijacked across
  tenants on import (global id namespace, guarded per-tenant).
- The client plane (assignments, pending transfers, chunk/signal relay,
  just-in-time credential fetch) matches tenants strictly —
  `default` is an ordinary tenant, not a wildcard — and every chunk,
  signal, and fetch re-checks participant + tenant visibility, so
  tenant A clients and users can never read or write tenant B data.

## User emails

Users carry an optional `email` (visible in `UserView`, settable via
`POST /api/users` `email` or `SetUserEmail`). Tenant provisioning sets
the new admin's email; all other flows treat it as metadata.

## Audit trail

- `tenant.create`, `tenant.suspend`, `tenant.activate`,
  `tenant.delete` — actor is the superuser (or `"api"` in
  open-bootstrap mode), target is the tenant id.
- `GET /api/audit` stays tenant-scoped: non-superusers see only their
  own tenant's entries.
