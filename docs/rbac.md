# RBAC + Web Auth & Management

Role-based access control for the website and API. Users belong to a
tenant, carry a role (`admin` > `operator` > `viewer`), and may hold
group memberships used by [credential resolution](./credentials.md)
(`user > group > tenant`).

## Roles & permissions

| Capability | viewer | operator | admin |
|---|---|---|---|
| Read workflows, clients, results, credential metadata | ✅* | ✅ | ✅ |
| Read audit trail (own tenant; admin sees all) | ✅ | ✅ | ✅ |
| Mint / list / revoke enrollment tokens | ❌ | ✅ (own tenant) | ✅ (any tenant) |
| Revoke clients | ❌ | ✅ (own tenant) | ✅ (any tenant) |
| Create / delete credentials | ❌ | ✅ (own tenant) | ✅ (any tenant) |
| Import / clone / enable workflows | ❌ | ✅ | ✅ |
| Delete workflows | ❌ | ✅ (own tenant) | ✅ (any tenant) |
| Change own password | ✅ | ✅ | ✅ |
| Reset another user's password | ❌ | ❌ | ✅ |
| Create / revoke users | ❌ | ❌ | ✅ |
| List users | ❌ | ✅ (own tenant) | ✅ (all) |

Reads marked ✅* stay open without login for dashboard polling
(workflows, clients); every mutating endpoint requires a session once
any user exists.

Cross-tenant operations always require `admin`. Non-admins default to
their own tenant when the request omits `tenant_id`.

## Bootstrap

The first server run creates both bootstraps and prints each secret
**once** to stdout (never stored):

- `BOOTSTRAP enrollment token (tenant=default, one-time)` — enroll the
  first client.
- `BOOTSTRAP admin user (tenant=default, username=admin)` — log in to
  the website/API.

Set `ADMIN_PASSWORD` (min 8 chars) to pin the bootstrap admin password
instead of a random one. Rotate it after first login by creating a new
admin and revoking the bootstrap account (admins cannot revoke
themselves).

Before any user exists the server runs in open-bootstrap mode so the
very first admin can be created via `POST /api/users` without a
session. Once a user exists, all gated endpoints return `401` without a
session and `403` for insufficient roles.

## API

Sessions are opaque tokens (`session_` ids, sha256-stored, 24h TTL).
Pass them as `Authorization: Bearer <token>` or `X-Auth-Token: <token>`.
The website uses an `HttpOnly` cookie (`nitejaguar_session`) with the
same token.

- `POST /api/auth/login` — `{username, password, tenant_id?}` →
  `{token, expires_at, user}`. Audited as `auth.login` (failures too).
- `POST /api/auth/logout` — revokes the current session. Audited as
  `auth.logout`.
- `GET /api/auth/me` — current user profile.
- `POST /api/users` — admin only: `{username, password (≥8),
  tenant_id?, role?, groups?}`. Usernames are unique per tenant.
  Audited as `user.create`.
- `GET /api/users` — operator+: non-admins see their own tenant only.
- `POST /api/users/{id}/revoke` — admin only; revokes the user and all
  its sessions. Audited as `user.revoke`. Self-revoke is rejected.
- `POST /api/auth/password` — any role: `{current_password,
  new_password (≥8)}` changes your own password (current required).
  Admins may pass `{user_id}` to reset another user's password without
  knowing it. Audited as `user.password_change` (failures too).

Passwords are bcrypt hashes; user responses never include them.

### Example

```bash
# login
curl -s localhost:8080/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"...","tenant_id":"default"}'
# → {"token":"...","user":{"id":"user_...","role":"admin",...}}

AUTH="Authorization: Bearer <token>"

# create an operator
curl -s localhost:8080/api/users -H "$AUTH" \
  -H 'Content-Type: application/json' \
  -d '{"username":"ops","password":"long-enough","tenant_id":"default","role":"operator"}'

# mint an enrollment token (operator+)
curl -s localhost:8080/api/enrollment/tokens -H "$AUTH" \
  -H 'Content-Type: application/json' \
  -d '{"tenant_id":"default","label":"edge-1"}'
```

## Website

- `GET /login` — sign-in form (tenant, username, password). Logged-in
  users redirect to `/`.
- `POST /login` — form login; sets the session cookie, audits
  `auth.login`, redirects to `/`.
- `POST /logout` (or `GET /logout`) — revokes the session, clears the
  cookie, audits `auth.logout`.
- `GET /audit` — audit trail (newest 200; non-admins see their own
  tenant). Navbar: **Audit**.
- `GET /users` — user roster (operator+; non-admins see their own
  tenant). Navbar: **Users**. Admins get a create-user form (username,
  password ≥8, role, groups, tenant) and per-user revoke buttons
  (self-revoke refused, mirroring the API).
- `GET /profile` — your own profile plus a change-password form
  (current + new + confirm). Navbar shows your username linking here.
- `POST /profile/password` — form password change; wrong current
  password and mismatched confirmation are reported inline.

The navbar hides every app link for anonymous visitors — only
**Login** (plus the theme switcher) is shown. Once logged in, the menu
shows Workflows, Designer, Clients, Credentials, Results, Audit, Users,
your username (→ `/profile`), and Logout. On a fresh install with no
users yet (open-bootstrap mode) the full menu stays visible so the
first admin can be created from the **Users** page; afterwards login
is required.

Once any user exists, anonymous browsers are redirected to `/login`
for every HTML page except `/login` itself. API, assets, health, docs,
and websocket stay open under their own auth. Web form posts enforce
roles too: designer save, workflow enable/clone/delete, client revoke,
token mint/revoke, and credential store/delete require operator+;
user create/revoke require admin; anonymous posts redirect to `/login`.

## Audit trail

`GET /api/audit?limit=100` requires any authenticated role (viewer+);
non-admins see their own tenant only. New actions in this slice:

- `auth.login`, `auth.logout`
- `user.create`, `user.revoke`, `user.password_change`
- `workflow.import`, `workflow.clone`, `workflow.enable`,
  `workflow.delete`

Existing actions keep their actor attribution, now set to the session
user id instead of `"api"`: `enrollment.create`, `enrollment.revoke`,
`client.revoke`, `credential.create`, `credential.delete`,
`credential.fetch`, `assignment.enqueue`, `assignment.complete`.

## Groups

Groups are lightweight membership strings on the user (`groups: ["ops"]`),
not a separate entity yet. They feed credential group-scope resolution
today; a dedicated group-membership API is a later slice.
