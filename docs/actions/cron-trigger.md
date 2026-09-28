# Cron Trigger (`cron`)

The `cron` trigger is the cronjob analogue: it fires on a schedule — a standard cron expression or a fixed interval — and emits a result carrying `now()` at the moment it triggered, so downstream nodes always start from the fire time.

## What It Can Do
- **Cron Schedules**: standard 5-field expressions (`minute hour day-of-month month day-of-week`), an optional leading seconds field, and descriptors (`@every 30s`, `@hourly`, `@daily`, `@midnight`, `@weekly`).
- **Fixed Intervals**: Go durations (`30s`, `5m`, `1h30m`) when a crontab is more ceremony than needed.
- **Timezone Awareness**: an IANA timezone drives both cron field matching (so `0 9 * * *` means 9am *there*) and the emitted timestamp; defaults to local time.
- **Flexible Formatting**: ISO-8601 (`RFC3339`) by default, custom Go time layouts, or Unix epoch timestamps (`unix` / `unix_ms`) — the same specs as the [`datetime` action](./datetime-action.md).
- **Start-up Fire**: optional `run_on_start` emits one result immediately when the trigger starts, before waiting for the first scheduled fire.

## Arguments

| Argument | Type | Default | Description |
|---|---|---|---|
| `cron` | string | `* * * * *` | 5-field cron expression (`m h dom mon dow`), optionally 6 fields with a leading seconds field, or a descriptor such as `@every 30s` / `@daily`. Ignored when `interval` is set |
| `interval` | string | `""` | Fixed Go duration (`30s`, `5m`, `1h30m`); takes precedence over `cron`. Durations below `1s` round up to `1s` |
| `timezone` | string | local time | IANA timezone name (e.g., `Europe/Madrid`, `UTC`). Matches cron fields and renders the payload; an explicit value wins over a `TZ=`/`CRON_TZ=` prefix inside `cron` |
| `format` | string | `RFC3339` | Go time layout for `datetime`, or `unix` / `unix_ms` |
| `run_on_start` | boolean | `false` | Emit one result as soon as the trigger starts, before the first scheduled fire (`true`/`1`/`yes`/`on`) |

## Result Payload

Every fire emits `now()` (rendered in the trigger's timezone):

```json
{
  "type": "success",
  "trigger": "cron",
  "datetime": "2026-09-28T09:00:00Z",
  "timestamp": 1790607600,
  "timestamp_ms": 1790607600000,
  "format": "RFC3339",
  "timezone": "UTC",
  "schedule": "0 9 * * *"
}
```

| Field | Description |
|---|---|
| `datetime` | `now()` formatted with `format` |
| `timestamp` / `timestamp_ms` | epoch seconds / milliseconds of the same instant |
| `format` | the effective format spec (empty → `RFC3339`) |
| `timezone` | the timezone the payload was rendered in |
| `schedule` | the resolved schedule (`cron` expression or `interval` value) |

The shape mirrors the `datetime` action, so downstream nodes can read `$input.datetime` / `$input.timestamp` from either source interchangeably.

## Behavioral Edge Cases

- **Validation happens at load time**: an invalid expression, interval, timezone, or `run_on_start` value fails while the workflow is added (`AddTrigger` logs `Cannot create new trigger: …`) instead of silently never firing.
- **The schedule is evaluated in the trigger's timezone**, not the host's: `0 9 * * *` with `timezone: UTC` fires at 09:00 UTC regardless of where the server runs.
- **`interval` wins over `cron`** when both are set; the payload's `schedule` field reports whichever one is active.
- **Sub-second intervals** round up to `1s` (the scheduler's resolution).
- **`run_on_start: false`** means the trigger waits for the first scheduled moment — it never fires immediately at startup.
- **Stopping is idempotent**: `Stop()` ends the wait loop and may be called more than once (workflow reload/removal paths).
- **Actions are gated separately**: the trigger emits results even without `-e`, but downstream actions only execute when the server runs with actions enabled.

## How to Use

Add a `cron` node to a workflow JSON definition:

```json
{
  "id": "trigger_01h...",
  "action_type": "trigger",
  "action_name": "cron",
  "arguments": {
    "cron": "0 9 * * *",
    "timezone": "UTC",
    "format": "2006-01-02",
    "run_on_start": "true"
  },
  "conditions": {
    "entries": {
      "entry1": {
        "condition": { "leftOperand": true, "operator": "", "rightOperand": null },
        "nexts": ["action_01h..."]
      }
    }
  },
  "dependencies": []
}
```

Or drive it with an interval instead of a crontab:

```json
{
  "id": "trigger_01h...",
  "action_type": "trigger",
  "action_name": "cron",
  "arguments": {
    "interval": "15m",
    "format": "unix_ms"
  },
  "conditions": {
    "entries": {
      "entry1": {
        "condition": { "leftOperand": true, "operator": "", "rightOperand": null },
        "nexts": ["action_01h..."]
      }
    }
  },
  "dependencies": []
}
```

### Complete workflow

`examples/workflow-cron.json` fires every 5 minutes and creates a stamp file named after the fire time:

```bash
./nitejaguar-server -i examples/workflow-cron.json -e
```

```json
{
  "action_type": "trigger",
  "action_name": "cron",
  "arguments": {
    "cron": "*/5 * * * *",
    "format": "20060102-150405",
    "timezone": "UTC"
  },
  "conditions": {
    "entries": {
      "entry1": {
        "condition": { "leftOperand": true, "operator": "", "rightOperand": null },
        "nexts": ["action_01cron01example00000001"]
      }
    }
  }
}
```

with the downstream `file` action:

```json
{
  "action_type": "action",
  "action_name": "file",
  "arguments": {
    "action": "create",
    "file": "/tmp/nitejaguar-cron-{{ $input.datetime }}.txt"
  }
}
```

The trigger is also available in the visual designer picker (**Cron**) and runs on remote clients like any other trigger.
