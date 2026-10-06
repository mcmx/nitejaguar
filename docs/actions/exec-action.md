# Exec Action (`exec`)

`exec` runs a local program or script on the executing node (server or client) and captures the outcome — the Tidal-style job primitive. Use it to run ETL scripts, batch jobs, and system utilities, then route on the exit code and output.

## Arguments

| Argument | Type | Default | Description |
|---|---|---|---|
| `command` | string | required | Binary or script path, e.g. `/usr/local/bin/etl.sh`. `~` expansion applies. |
| `args` | array of strings | `[]` | One argv element each, individually template-resolved. Accepts a real JSON array; a single string is treated as one element. |
| `workdir` | string | `""` | Working directory, `~` expansion applies. Empty means the process default. |
| `timeout` | string | `"5m"` | Go duration (`time.ParseDuration`: `30s`, `5m`, `1h30m`). Invalid or non-positive values fail load (`New`); templated values are validated at execution. |
| `env` | object string→string | `{}` | Extra environment merged on top of the process env. Values are template-resolved. |

All string fields resolve `$input.<path>` refs and `{{ ... }}` templates via the shared engine. `$result.`/`$args.` refs are rejected (the node's own result does not exist at resolution time).

## No shell

The command runs **directly, with no shell**: pipes (`|`), globs (`*`), redirects (`>`), and `&&` chains do not work. To use shell features, invoke the shell explicitly:

```json
{"command": "sh", "args": ["-c", "ls /data/*.csv | head -5"]}
```

Shell mode (a `shell: true` convenience flag) is a follow-up, not part of this action.

## Kill on timeout

The process runs in its own process group. When `timeout` elapses, the whole group is killed (`SIGKILL`), so spawned children die with the parent. The timeout result carries the partial stdout/stderr captured before the kill.

## Output truncation

`stdout` and `stderr` each truncate at **64KiB** (`maxOutputBytes = 64 << 10`), keeping the first 64KiB, so results cannot blow up the DB. If output is routinely larger, have the script write artifacts to disk (or a transfer node target) and print only a summary.

## Result payload

Success (exit code 0):

```json
{"type": "success", "command": "/usr/local/bin/etl.sh", "exit_code": 0, "stdout": "...", "stderr": "...", "duration_ms": 1234.5}
```

Failures emit `{"type": "error", ...}` with a `result` message, routable to alerting with a `$result.type == error` condition entry (cf. issue #85):

| Case | Payload |
|---|---|
| Missing/empty command | `{"type": "error", "result": "missing command argument ..."}` |
| Timeout | `{"type": "error", "command": ..., "exit_code": -1, "stdout": "<partial>", "stderr": "<partial>", "duration_ms": ..., "result": "command \"...\" timed out after 5m"}` |
| Non-zero exit | `{"type": "error", "command": ..., "exit_code": 1, "stdout": ..., "stderr": ..., "duration_ms": ..., "result": "command \"...\" exited with code 1"}` |
| Invalid workdir | `{"type": "error", "command": ..., "result": "invalid workdir ...: ..."}` |
| Start failure (not found, not executable) | `{"type": "error", "command": ..., "result": "fork/exec ..."}` |

Note: `exec` never needs a credential; secrets reach scripts via `env` templating from upstream results or via the node's `credential_ref` just-in-time binding — never hardcode secrets in workflow JSON.

## Examples

Run a script on schedule:

```json
{
  "id": "action_01h...",
  "action_type": "action",
  "action_name": "exec",
  "arguments": {
    "command": "/usr/local/bin/etl.sh",
    "args": "[\"--date\", \"$input.datetime\"]",
    "workdir": "/var/etl",
    "timeout": "5m",
    "env": "{\"RUN_DATE\": \"$input.datetime\"}"
  },
  "conditions": {"entries": {}},
  "dependencies": ["trigger_01h..."]
}
```

Alert on failure:

```json
"conditions": {"entries": {"on_error": {
  "condition": {"leftOperand": "$result.type", "operator": "==", "rightOperand": "error"},
  "nexts": ["action_01h...alert"]
}}}
```

Full workflow: `examples/workflow-exec.json` (cron trigger → `exec` → Slack alert on `$result.type == error`).
