# File Watchdog Trigger (`filewatch`)

The `filewatch` trigger is the file-arrival watchdog: it watches a directory for files matching a pattern and enforces an expectation window around their arrival. When a matching file lands in time it emits a `success` result with the file info; when the window expires with no file it emits a `missing` result. Downstream conditions route the `missing` branch to an alert action (email, slack, outbound webhook), so a late or absent file drop pages someone instead of stalling silently.

## What It Can Do

- **Arrival detection**: watches a local directory (with home directory `~` expansion) via `fsnotify` and matches file names against a glob `pattern` (e.g. `statement-*.pdf`).
- **Expectation windows**: every window lasts `expect_within` (e.g. `30m`).
- **Watch mode** (no schedule): windows repeat back-to-back from trigger start. Each arrival emits `success` and restarts the clock; each expiry emits `missing` and restarts it. Answers "alert me if no file shows up for X".
- **Schedule mode** (`interval` or `cron` set): each scheduled fire opens a fresh window. An arrival inside an open window emits `success` and closes it; an expiry emits `missing` and closes it. Answers "a file is due on this schedule, plus a grace period".
- **Event filtering and debouncing**: same `event_type` filter and `debounce_ms` burst grouping as the [`filechange` trigger](./filechange-trigger.md).

## Arguments

| Argument | Type | Default | Description |
|---|---|---|---|
| `path` | string | `""` (required) | Directory to watch (supports `~` expansion) |
| `pattern` | string | `*` | Glob matched against the file name (`statement-*.pdf`); `*` means every file |
| `expect_within` | string | required | Grace window as a Go duration (`30s`, `5m`, `1h`); must be greater than zero |
| `interval` | string | `""` | Fixed Go duration opening a fresh window per tick; takes precedence over `cron`; empty means watch mode |
| `cron` | string | `""` | 5-field cron expression (optional leading seconds, `@descriptors`); each fire opens a fresh window; ignored when `interval` is set; empty means watch mode |
| `timezone` | string | local time | IANA timezone name driving cron field matching; empty means local time |
| `event_type` | string | `create` | Comma-separated list of `create`, `write`, `rename`, `remove`, `chmod`; empty means `create` |
| `debounce_ms` | integer | `500` | Groups rapid bursts for the same file; `0` disables debouncing |

## Result Payload

Arrivals emit `success`; expiries emit `missing`. Both carry the same envelope so one condition pair routes them:

```json
{
  "type": "success",
  "trigger": "filewatch",
  "file": "/tmp/nitejaguar-inbox/statement-20260101.pdf",
  "event": "create",
  "path": "/tmp/nitejaguar-inbox",
  "pattern": "statement-*.pdf",
  "expect_within": "30m",
  "schedule": "0 9 * * *"
}
```

| Field | Description |
|---|---|
| `type` | `success` (file arrived) or `missing` (window expired) — route on this with `$result.type` |
| `file` | Full path of the arrived file (`success` only) |
| `event` | fsnotify event that fired (`create`, `write`, …; `success` only) |
| `path` / `pattern` | The watched directory and filename glob, echoed back |
| `expect_within` | The configured grace window |
| `schedule` | The active schedule (`interval` value or `cron` expression; empty in watch mode) |

## Behavioral Edge Cases

- **Validation happens at load time**: a missing `path`/`expect_within`, an unparseable duration, a bad glob, an unknown `event_type`, or an invalid schedule/timezone fails while the workflow is added (`AddTrigger` logs `Cannot create new trigger: …`) instead of silently never firing.
- **Watch mode never stops after a miss**: a `missing` result re-opens the window, so a second quiet period emits a second `missing`. Throttle downstream if one page per outage is wanted.
- **Schedule mode closes the window on arrival**: after a `success`, no `missing` can fire for that window; the next tick opens the next one.
- **A tick restarts an open window**: if the schedule period is shorter than `expect_within`, the window never expires and `missing` never fires. Keep the period longer than the grace window (e.g. daily schedule, `30m` grace).
- **An arrival with no open window** (schedule mode, file lands between windows) still emits `success`; it satisfies nothing and changes nothing.
- **Pattern matches the file name only**, not the full path: `statement-*.pdf` matches `/tmp/inbox/statement-1.pdf`.
- **`event_type` typos fail at load**, unlike `filechange`, which ignores unknown tokens. The default watches `create` only — add `write` when deliveries stream into place.
- **Missing directories fail at start, not at load**: the watched directory may be created after the workflow is imported; `Execute` logs and the trigger stays idle until the workflow is reloaded with the directory present.
- **Stopping is idempotent**: `Stop()` ends the wait loop and may be called more than once (workflow reload/removal paths).

## How to Use

Expect `statement-*.pdf` daily by 09:30 UTC; page ops on a miss, stamp arrivals otherwise:

```json
{
  "id": "trigger_01h...",
  "action_type": "trigger",
  "action_name": "filewatch",
  "arguments": {
    "path": "/tmp/nitejaguar-inbox",
    "pattern": "statement-*.pdf",
    "cron": "0 9 * * *",
    "timezone": "UTC",
    "expect_within": "30m"
  },
  "conditions": {
    "entries": {
      "entry_missing": {
        "condition": { "leftOperand": "$result.type", "operator": "==", "rightOperand": "missing" },
        "nexts": ["action_01h..."]
      },
      "entry_arrived": {
        "condition": { "leftOperand": "$result.type", "operator": "==", "rightOperand": "success" },
        "nexts": ["action_01h..."]
      }
    }
  },
  "dependencies": []
}
```

Or skip the schedule for a plain "no file for X" watchdog:

```json
{
  "id": "trigger_01h...",
  "action_type": "trigger",
  "action_name": "filewatch",
  "arguments": {
    "path": "~/inbox",
    "pattern": "*",
    "expect_within": "1h"
  },
  "conditions": {
    "entries": {
      "entry_missing": {
        "condition": { "leftOperand": "$result.type", "operator": "==", "rightOperand": "missing" },
        "nexts": ["action_01h..."]
      }
    }
  },
  "dependencies": []
}
```

### Complete workflow

`examples/workflow-filewatch.json` wires the daily-statement watchdog to an email alert on `missing` and a stamp file on `success`:

```bash
./nitejaguar-server -i examples/workflow-filewatch.json -e
```

The trigger is also available in the visual designer picker (**File Watchdog**) and runs on remote clients like any other trigger.
