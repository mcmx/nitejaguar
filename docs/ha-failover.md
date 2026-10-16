# HA & Failover

Supported topology today: **one active server + one SQLite/Turso database
file**. There is no leader election and no shared-write clustering. High
availability means **active/passive**: a standby host holds a copy of the
database and starts its own server only after the primary is confirmed
dead. Hot standby with replication and external-DB support are explicitly
deferred (see Limits).

## What survives a failover

Durable (lives in the DB file, survives kill -9 + restart on the same file):

- Workflow definitions (`SaveWorkflow` / `GetWorkflows`).
- Pending node assignments (`EnqueueNodeAssignment` /
  `ListPendingAssignments`) — clients repoll and pick up where they left
  off. Enqueue is idempotent per `(workflow, execution, node)`.
- Results (`SaveResult`, idempotent per `result_id`) — replayed results
  are safe.
- Transfer sessions, chunks, and signals — a mid-upload transfer stalls
  in `uploading` until the sender retries; completed chunks are kept.
- Tenants, users, sessions, credentials, audit log.

Volatile (in-memory, lost on restart):

- The workflow registry (`Workflows` map), trigger subscriptions, and the
  trigger event channel.
- Cron and filechange schedules restart from boot time. Webhook
  subscriptions must re-register.
- `./results/` JSON files are a human-readable mirror only and can
  diverge from the DB; the DB is the source of truth.

Covered by `TestFailoverReopenSurvivesRestart` (`internal/database/`):
close the handle, reopen the same file, and the workflow, its pending
assignment, and its result are all still there.

## Backup & restore

Back up the live server without stopping it (online snapshot via
`VACUUM INTO`):

```sh
nitejaguar-server backup -o /backups/nite-$(date +%F).db
```

The command verifies the snapshot by reopening it and counting workflows.
Snapshots are full replacements — backing up twice overwrites the
previous file. Backing up an in-memory database (`DB_URL` unset) fails
closed: persist to disk first (`DB_URL=./nite.db`).

Restore (server **stopped** on both hosts):

```sh
# on the standby (or repaired primary), with no server running:
cp /backups/nite-2026-10-13.db ./nite.db
# preserve the secret that decrypts credentials (see below), then start:
nitejaguar-server -e
```

RPO/RTO expectations:

- RPO = age of the newest snapshot (schedule `backup` from cron at the
  granularity you can afford to lose — e.g. hourly).
- RTO = minutes: copy the file, confirm the old primary is dead (see
  below), start the standby.
- Always keep `CREDENTIALS_KEY` identical across primary and standby.
  Credential secrets are encrypted at rest; a standby booted with a
  different (or missing) key sees the rows but cannot decrypt them, and
  an ephemeral unset key loses secrets on every restart.

## Failover procedure

1. Confirm the primary is dead (`/health`, process check). Never start
   the standby while the primary can still write — see Double-fire.
2. Restore the newest snapshot onto the standby host (or attach the
   shared disk holding the live DB file).
3. Copy the environment (`DB_URL` path, `CREDENTIALS_KEY`, `PORT`).
4. Start the standby: `nitejaguar-server -e`.
5. Verify: workflows listed, pending assignments repolled by clients,
   triggers firing on their next schedule.

## Limits (honest)

- **No active/active.** Two servers writing the same DB file, or two
  servers on divergent copies, will double-fire triggers: `ResultID` and
  `execution` ids are minted fresh on every trigger fire, so the
  idempotency keys (`result_id`, `(workflow, execution, node)`) cannot
  collapse duplicates from two writers. Run exactly one writer; keep the
  standby stopped until failover.
- **Triggers refire from boot.** A cron schedule missed during the
  outage does not backfill; it fires on its next occurrence after the
  standby starts. Deduplicate side effects downstream if the outage
  window matters.
- **Replication is file copies.** There is no streaming replica or
  point-in-time recovery — only full snapshots. External-DB support
  (Postgres/libsql server) and hot standby remain future work.
