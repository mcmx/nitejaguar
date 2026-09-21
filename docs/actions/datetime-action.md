# Datetime Action (`datetimeAction`)

The `datetimeAction` provides time-based processing and formatting capabilities within workflows, mirroring n8n's Date & Time node "Get Current Date" operation in minimal form.

## What It Can Do
- **Get Current Date / Time**: Retrieve the current timestamp during workflow execution.
- **Timezone Conversion**: Support IANA timezones (e.g. `Europe/Madrid`, `UTC`, defaulting to local time).
- **Flexible Formatting**: Output ISO-8601 (`RFC3339`), custom Go time layouts, or Unix epoch timestamps (`unix` / `unix_ms`).
- **Field Echoing**: Echo the resulting timestamp under a configurable output field name (`output_field`, defaults to `datetime`), which can be referenced upstream by subsequent actions via `$input.<field>`.

## Arguments

| Argument | Type | Default | Description |
|---|---|---|---|
| `operation` | string | `getCurrentDate` | Operation to run (`getCurrentDate`, or aliases `""`, `current`, `now`) |
| `format` | string | `RFC3339` | Go time layout string, or `unix` / `unix_ms` |
| `timezone` | string | local time | IANA timezone name (e.g., `America/New_York`, `UTC`) |
| `output_field` | string | `datetime` | Field name in the result payload for referencing via `$input.<output_field>` |

## How to Use

Add a `datetimeAction` node to generate timestamps and feed them into file actions or notifications:

```json
{
  "id": "action_date_01h...",
  "action_type": "action",
  "action_name": "datetimeAction",
  "arguments": {
    "operation": "getCurrentDate",
    "format": "2006-01-02",
    "timezone": "UTC",
    "output_field": "current_date"
  },
  "conditions": {
    "nexts": ["action_file_01h..."]
  },
  "dependencies": ["trigger_01h..."]
}
```
Then reference it in downstream actions as `$input.current_date`.
