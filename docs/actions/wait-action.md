# Wait Action (`wait`)

`wait` pauses the current workflow execution before emitting a successful result, allowing downstream nodes to run after a delay.

## Arguments

| Argument | Type | Default | Description |
|---|---|---|---|
| `duration` | string or number | required | Delay to wait. A bare number means seconds. `wait`, `value`, and `time` are accepted aliases. |

Durations may be fractional (`1.5s`, `1.2hours`) and may combine parts (`1h 20m`, `2 weeks, 3 days`). Supported units include `ns`, `us`/`µs`, `ms`, `s`/`sec`, `m`/`min`, `h`/`hr`, `d`/`day`, `w`/`week`, `fn`/`fortnight`, `mo`/`month`, `q`/`quarter`, and `y`/`year`, including plural forms. Calendar units are fixed lengths: month = 30 days, quarter = 90 days, year = 365 days.

The duration can be resolved from an upstream result using `$input.<field>`, for example `$input.delay`.

## Example

```json
{
  "id": "action_wait_01h...",
  "action_type": "action",
  "action_name": "wait",
  "arguments": {
    "duration": "1.5 minutes"
  },
  "conditions": {
    "nexts": ["action_after_wait_01h..."]
  },
  "dependencies": ["trigger_01h..."]
}
```

The result payload is shaped like `{"type":"success","duration":"1.5 minutes","duration_ms":90000}`. Invalid or missing durations emit an error result and do not wait.
