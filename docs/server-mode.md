# Server Mode

The Nitejaguar server acts as the central orchestrator, managing workflows, serving the Web UI, exposing the OpenAPI REST API, and coordinating remote clients.

## Configuration

Server configuration relies on environment variables (loaded via `godotenv` from `.env`):

- `PORT` — HTTP server port (defaults to `8080`).
- `DB_URL` — Database connection string (e.g., `file:./test.db?_fk=1&cache=shared`). If unset, falls back to in-memory SQLite.
- `CREDENTIALS_KEY` — AES-256 key for credential secrets at rest (64-char hex, base64 32-byte key, or any passphrase hashed with SHA-256). If unset, an ephemeral process-local key is generated (stored secrets do not survive restarts).
- `ADMIN_PASSWORD` — Pins the bootstrap `admin` password (min 8 chars). If unset, a random one is generated and printed once to stdout on first run.

## Starting the Server

```bash
./nitejaguar server [flags]
```

### Key Flags
- `-e, --enable-actions` — Enable local execution of actions and triggers on the server.
- `-i, --import string` — Import a workflow JSON file verbatim (upsert; IDs and names untouched).
- `-c, --clone string` — Clone a workflow JSON file with fresh TypeIDs (`workflow_`, `trigger_`, `action_`), rewriting dependency edges.

## Web Dashboard & API

- **Web Dashboard**: Built with Go `templ`, HTMX, and Tailwind CSS (`cmd/web/`). Accessible at `http://localhost:8080`.
- **OpenAPI Docs**: Built on Huma v2. Interactive Swagger / Redoc documentation available at `http://localhost:8080/docs`.
- **WebSocket**: Real-time event streaming at `/websocket`.
- **Workflow upsert API** (operator+, used by `client workflow import/clone`):
  - `POST /api/workflows/import` — Save a workflow definition verbatim (upsert); returns `{ok, workflow_id}`; audited as `workflow.import`.
  - `POST /api/workflows/clone` — Save an independent copy with fresh IDs and a `"Clone of: "` name prefix; returns `{ok, workflow_id}`; audited as `workflow.clone`.
- **Artifacts**:
  - Logs: `./log/server.log`
  - Results: `./results/`

## RBAC + website auth & management

Users, roles (`admin > operator > viewer`), and login sessions gate
every management endpoint. See [RBAC & Auth](./rbac.md) for the full
reference.

- **Bootstrap**: first server run prints a one-time `admin` password to
  stdout (or set `ADMIN_PASSWORD`). Use it to log in at `/login` or
  `POST /api/auth/login`.
- **Website**: `/login`, `/logout`, `/profile` (own profile +
  password change), `/credentials` (metadata list + store/delete
  forms), `/clients` (client roster with revoke buttons, enrollment
  token list with revoke, and an enroll-a-client form that mints a
  token and prints the ready-to-run enrollment command once),
  `/users` (roster + admin create/revoke forms), `/audit` (audit
  trail). Anonymous browsers redirect to `/login` once users exist,
  and the navbar hides every app link until you log in (only Login
  stays visible).
- **Gated API** (operator+ unless noted): enrollment tokens
  (`POST/GET /api/enrollment/tokens`, revoke), client revoke,
  credential create/delete, workflow import/clone/delete
  (`DELETE /api/workflows/{id}`, audited as `workflow.delete`);
  password change is any role (`POST /api/auth/password` — own
  password with current required; admins may reset others via
  `user_id`); user management is admin-only (`POST/GET
  /api/users`, revoke); audit read is viewer+. Sessions go in
  `Authorization: Bearer` / `X-Auth-Token` (cookie on web).
- **Audit**: `auth.login/logout`, `user.create/revoke/password_change`,
  `workflow.import/clone/enable/delete`, plus the existing enrollment, client,
  credential, and assignment actions; `GET /api/audit` is tenant-scoped
  for non-admins.

## Tenant Enrollment & Client Lifecycle

Open client registration is closed. Clients self-register with a
**tenant enrollment (join) token**; the tenant is derived from the token
and never from client-supplied input (a `tenant_id` in the register body
is ignored).

- **Bootstrap**: the first server run creates the `default` tenant's
  one-time join token and prints its plaintext once to stdout
  (`BOOTSTRAP enrollment token ...`). The plaintext is never stored —
  only its hash is persisted.
- **Register**: `POST /api/clients/register` requires
  `enrollment_token` (alias `join_token`); missing/invalid/expired/
  revoked/exhausted tokens return `401`.
- **Token management** (operator+; cross-tenant requires admin):
  - `POST /api/enrollment/tokens` — mint a token
    (`{tenant_id, label, expires_in_hours, max_uses}`; `max_uses: 0`
    means unlimited). Returns the plaintext `token` once.
  - `GET /api/enrollment/tokens` — list tokens (hashes never exposed;
    non-admins see their own tenant).
  - `POST /api/enrollment/tokens/{id}/revoke` — revoke a token to
    block new joins.
- **Client revoke** (operator+): `POST /api/clients/{id}/revoke` — the client's
  token stops authenticating (heartbeat, assignments, results all
  return `401`); revoked clients show as `revoked`/stale in
  `GET /api/clients` and on the `/clients` page (revoke button per
  client).
- **Enrolling from the UI**: on `/clients`, fill in the client name
  (plus optional label, expiry hours, max uses — empty max uses means
  a single enrollment) and submit. The page mints a token and shows
  the copy-paste command **once** (the plaintext is stored hashed and
  cannot be recovered later):

  ```bash
  nitejaguar client --server https://server:8080 --name edge-worker-1 --enrollment-token <TOKEN>
  ```

  Names with spaces are shell-quoted automatically. Existing tokens
  are listed with use counts and revoke buttons; revoking blocks new
  joins with that token.
- **Audit**: every enrollment use, token op, and client revoke is
  recorded; `GET /api/audit?limit=100` (viewer+, tenant-scoped for
  non-admins) returns the trail
  (`enrollment.use`, `enrollment.create`, `enrollment.revoke`,
  `client.revoke`).

## Client targeting & distributed dispatch

- **Targeting**: workflows carry `default_client`/`default_client_tags`;
  nodes override with `client`/`client_tags`. Empty means broadcast.
  `GET /api/clients/{id}/assignments` filters definitions with the same
  override-or-default resolution and tenant isolation.
- **Pending handoffs**: every ingested result enqueues one pending
  assignment per downstream `next` (idempotent per
  workflow/execution/node, `assign_` ids) and marks the reporting node
  done. `GET .../assignments` returns owned pending items in `pending`
  with the parent payload as `$input`.
- **Handoff fix**: `POST /api/results` returns only caller-owned `nexts`;
  foreign edges are never executed locally — they are routed via pending
  assignments. Audited as `assignment.enqueue` and `assignment.complete`.
- **Client-to-client transfer**: `transfer` sessions (`transfer_` ids)
  with chunked relay (`POST/GET /api/transfers/{id}/chunks`) and WebRTC
  signaling (`POST/GET /api/transfers/{id}/signal`); receivers poll
  `GET /api/clients/{id}/transfers/pending` and complete with sha
  verification (`POST /api/transfers/{id}/complete`). Tenant-isolated,
  audited as `transfer.init/signal/complete`. Heartbeat accepts
  `dial_info`; `GET /api/clients` shows it next to online status.

## Credentials model

Nodes hold a `credential_ref` (credential id or name) — never the
secret. See [Credentials](./credentials.md) for the full reference.

- **Management** (operator+; cross-tenant requires admin): `POST /api/credentials` (store encrypted at rest),
  `GET /api/credentials`, `GET /api/credentials/{id}`,
  `DELETE /api/credentials/{id}` — secrets are never exposed here.
- **Just-in-time fetch**: `GET /api/credentials/{ref}/fetch`
  authenticates with the executing client's own token, is
  tenant-isolated, resolves names `user > group > tenant`, and returns
  `{credential_id, name, type, secret, ttl_seconds}` (60s, memory-only).
- **Audit**: `credential.create`, `credential.delete`, and every
  `credential.fetch` are recorded; `GET /api/audit?limit=100` returns
  the trail.
