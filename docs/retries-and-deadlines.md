# Retries & deadlines

A node can declare an engine-level **retry policy** and a per-attempt
**deadline**. The framework applies both around the action (server-local
and on remote clients alike) — action implementations stay unaware.

## Node fields

All four fields live on the node (alongside `client`, `credential_ref`,
`merge_input`) and are only honoured on `action_type: "action"` nodes;
triggers ignore them.

| Field | Type | Default | Meaning |
|---|---|---|---|
| `retry_max` | integer | `0` | Retries **after** the first attempt. Total attempts = `1 + retry_max`. `0` disables retrying. Must be `>= 0`. |
| `retry_backoff` | duration string | `"1s"` | Base delay between attempts. Doubles each retry (exponential). Must be `> 0` when set. |
| `retry_backoff_max` | duration string | `"30s"` | Cap on the exponential delay. Must be `> 0` when set. |
| `timeout` | duration string | unset | Deadline for **each attempt**. Unset means no deadline. Must be `> 0` when set. |

Durations are Go duration strings: `"500ms"`, `"1s"`, `"2m"`, `"1h30m"`.
Invalid values fail workflow load (`Cannot create new action`), never at
run time.

```json
{
  "id": "action_01h...",
  "action_type": "action",
  "action_name": "http",
  "arguments": { "method": "POST", "url": "https://example.invalid/upload" },
  "retry_max": 3,
  "retry_backoff": "1s",
  "retry_backoff_max": "30s",
  "timeout": "10s",
  "conditions": { "entries": {} },
  "dependencies": ["trigger_01h..."]
}
```

## Semantics

- **Attempt = one execution of the action.** The framework builds a fresh
  action for every attempt and runs it on a private channel, so a partial
  emission cannot leak across retries.
- **Failure detection** follows the engine convention: a result whose
  `type` is `error` (map payloads and struct payloads like the `file`
  action's are both recognised) is a failure; anything else is a success.
- **Intermediate failures are marked `retrying: true`** on the result.
  The engine **persists every attempt** (one result row each, with a
  1-based `attempt` number) for observability, but does **not route** a
  retrying result — downstream nodes do not run until the node finally
  succeeds, exhausts its retries, or times out.
- **Backoff**: attempt 1 uses `retry_backoff`; every later attempt
  doubles it, capped at `retry_backoff_max`.
- **Deadline** bounds each attempt independently. When it fires the
  attempt is stopped and a terminal error result is emitted
  (`{type: "error", timeout: true, result: "node timed out after ..."}`).
  A deadline is terminal: it does **not** retry.
- **Final result** (success, exhausted retries, or timeout) has
  `retrying: false` and routes normally, so existing failure routing
  (`$result.type == error`) keeps working — the alert fires once the
  node gives up, not on every attempt.

| Outcome | Attempts | `retrying` (all but last) | Routes downstream |
|---|---|---|---|
| Success on attempt 1 | 1 | — | success path |
| Success after N failures | N+1 | first N | success path |
| Failed `1 + retry_max` times | `1 + retry_max` | first `retry_max` | error path (final attempt) |
| Timeout | 1 | — | error path (`timeout: true`) |

## Honest limits

Go cannot force-kill a goroutine. The deadline **stops waiting** and
emits the timeout result, and calls the action's `Stop`; an action that
spawns blocking work must honour `Stop` for a hard kill. The `exec` and
`http` actions bound their own work (process group / `SIGKILL` and
request timeout respectively), so they are killed promptly.

## Scope

- Per-node retries and per-attempt deadlines are implemented for both
  server-local and remote-client execution.
- Not yet implemented: a workflow-level (whole-execution) deadline,
  `retry_on` predicates (retry only some error types), jitter, and retry
  for `transfer` nodes addressed to a remote client (their sender path
  bypasses the action decorator).

## Example

See [`examples/workflow-retry.json`](../examples/workflow-retry.json):
an `http` upload retried three times with exponential backoff and a 10s
per-attempt deadline, alerting to Slack only after the retries are
exhausted. The designer exposes these fields in the node inspector under
**Retries & deadline**.
