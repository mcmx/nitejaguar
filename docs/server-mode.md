# Server Mode

The Nitejaguar server acts as the central orchestrator, managing workflows, serving the Web UI, exposing the OpenAPI REST API, and coordinating remote clients.

## Configuration

Server configuration relies on environment variables (loaded via `godotenv` from `.env`):

- `PORT` — HTTP server port (defaults to `8080`).
- `DB_URL` — Database connection string (e.g., `file:./test.db?_fk=1&cache=shared`). If unset, falls back to in-memory SQLite.

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
- **Workflow upsert API** (used by `client workflow import/clone`):
  - `POST /api/workflows/import` — Save a workflow definition verbatim (upsert); returns `{ok, workflow_id}`.
  - `POST /api/workflows/clone` — Save an independent copy with fresh IDs and a `"Clone of: "` name prefix; returns `{ok, workflow_id}`.
- **Artifacts**:
  - Logs: `./log/server.log`
  - Results: `./results/`

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
- **Token management** (currently unauthenticated; role-gating arrives
  with the RBAC slice):
  - `POST /api/enrollment/tokens` — mint a token
    (`{tenant_id, label, expires_in_hours, max_uses}`; `max_uses: 0`
    means unlimited). Returns the plaintext `token` once.
  - `GET /api/enrollment/tokens` — list tokens (hashes never exposed).
  - `POST /api/enrollment/tokens/{id}/revoke` — revoke a token to
    block new joins.
- **Client revoke**: `POST /api/clients/{id}/revoke` — the client's
  token stops authenticating (heartbeat, assignments, results all
  return `401`); revoked clients show as `revoked`/stale in
  `GET /api/clients` and on the `/clients` page.
- **Audit**: every enrollment use, token op, and client revoke is
  recorded; `GET /api/audit?limit=100` returns the trail
  (`enrollment.use`, `enrollment.create`, `enrollment.revoke`,
  `client.revoke`).
