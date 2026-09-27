# Client Mode (Remote Runner)

The `nitejaguar` binary is the client-only build. Nitejaguar supports distributed execution where remote **Clients** (runners) register with a central server, poll for assigned task nodes (`filechange`, `file`), execute them locally, and report execution results back.

## Starting a Client

```bash
./nitejaguar --server http://127.0.0.1:8080 --name worker-node-1 --enrollment-token <join-token>
```

### CLI Flags & Environment Variables

| Flag | Env Variable | Default | Description |
|---|---|---|---|
| `--server` | `NITEJAGUAR_SERVER` | state file, then `http://127.0.0.1:8080` | URL of the Nitejaguar server |
| `--name` | `NITEJAGUAR_CLIENT_NAME` | state file, then `nitejaguar-client` | Human-readable client name |
| `--client-id` | `NITEJAGUAR_CLIENT_ID` | (auto-generated) | Persistent client ID for reconnection |
| `--token` | `NITEJAGUAR_TOKEN` | `""` | Authentication token for secured servers |
| `--enrollment-token` | `NITEJAGUAR_ENROLLMENT_TOKEN` | `""` | Tenant enrollment/join token for self-registration (required on first register; tenant comes from the token) |
| `--state-file` | `NITEJAGUAR_STATE_FILE` | `client_state.json` | Path to the client identity state file (use one file per client when running several clients from the same directory) |

## Client State File

After the first registration the client stores its identity in the state file
(`client_state.json` in the working directory by default, `0600` permissions):

```json
{ "client_id": "client_...", "token": "...", "server": "http://127.0.0.1:8080", "name": "worker-node-1" }
```

- `server` and `name` record the registration they belong to. When the
  corresponding flag/env is absent, the saved values are reused silently — a
  bare `./nitejaguar` restart reconnects with the same identity. Only an
  explicitly passed flag/env that differs from the saved value counts as
  drift.
- Restarting with a different `--server` discards the saved identity (the old
  token cannot work against the new server) and re-registers. This needs a
  valid `--enrollment-token`, otherwise registration retries with backoff.
- Restarting with a different `--name` keeps the existing registration and
  logs a warning — the name is only sent on first registration. To register
  as a new named client, stop the client, delete (or point `--state-file` at
  a fresh path), and restart with an enrollment token. The old server-side
  client entry remains until revoked.
- Passing explicit `--client-id` reuses the saved token only when the id
  matches; a `--server` mismatch is warned about and the saved token is not
  reused.

## Execution Lifecycle

1. **Registration**: On startup, the client registers itself with the server using its enrollment token (or reconnects using an existing `--client-id`). Without a valid join token the server returns `401`.
2. **Polling**: The client periodically polls the server for assigned workflow nodes (`workflows`) plus owned pending cross-client handoffs (`pending`). Node filtering applies per-node overrides over workflow defaults; untargeted nodes broadcast.
3. **Local Execution**: Assigned nodes (such as file watches or file transformations) execute locally against the client's file system (e.g., expanding `~` paths locally). Pending handoffs execute at most once per process with the parent payload as `$input` (seeded for `merge_input`); completion is confirmed when the node's result is posted.
4. **Result Reporting**: Success or error results are transmitted back to the server and logged in `./results/`. The server returns only caller-owned `nexts`; foreign nexts arrive later as `pending` for their owners.
5. **Transfer inbox**: each tick also polls inbound file deliveries. `transfer` nodes addressed elsewhere are sent WebRTC-P2P-first (server-signaled) with relay fallback; inbound sessions are received, written with the transfer safety rules, and completed (no workflow result posted by the receiver). See [Transfer Action](./actions/transfer-action.md).
6. **Resilience**: The client implements exponential backoff on connection/polling failures and stops cleanly on SIGINT / context cancellation.

## Workflow Import / Clone via the Server

The client command can import workflows through a running server's HTTP API
(no direct database access; the server must be running):

```bash
./nitejaguar workflow import <file.json> [--server http://127.0.0.1:8080]
./nitejaguar workflow clone <file.json> [--server http://127.0.0.1:8080]
```

- `workflow import` POSTs the JSON to `POST /api/workflows/import`; the server saves it verbatim (upsert; ids and name untouched) and returns the workflow id.
- `workflow clone` POSTs the JSON to `POST /api/workflows/clone`; the server saves an independent copy with fresh `workflow_`/`trigger_`/`action_` ids, rewritten edges, and a `"Clone of: "` name prefix, returning the new workflow id.
- `--server` (or `NITEJAGUAR_SERVER`) selects the server; `--token` (or `NITEJAGUAR_TOKEN`) is forwarded as the API token when the server requires auth.
