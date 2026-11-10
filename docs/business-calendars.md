# Business Calendars

Business calendars are reusable, tenant-scoped filters for the `cron`
trigger. Manage them from **Calendars** in the web sidebar or through the
calendar API. Operators can create, edit, and delete calendars; viewers can
list and inspect calendars in their own tenant. Superusers may manage any
tenant's calendars. Mutations are audit logged.

Each calendar has:

- A tenant-unique `name`.
- An optional `weekdays` allowlist using `Mon`, `Tue`, `Wed`, `Thu`, `Fri`,
  `Sat`, and `Sun`. An empty list allows every weekday.
- An `excluded_dates` list of exact `YYYY-MM-DD` dates. These are excluded
  regardless of the weekday allowlist.

## API

All endpoints require a session once users exist. Create, update, and delete
require operator or admin. Foreign calendar reads return `404`; cross-tenant
writes are denied unless performed by a superuser.

```http
POST /api/calendars
Content-Type: application/json

{
  "name": "us-business-days",
  "weekdays": ["Mon", "Tue", "Wed", "Thu", "Fri"],
  "excluded_dates": ["2026-12-25"]
}
```

`tenant_id` is optional and is derived from the caller for non-superusers.
Superusers may provide it to create a calendar in another tenant.

- `GET /api/calendars` — list tenant-visible calendars; superusers may pass
  `tenant_id`.
- `GET /api/calendars/{id}` — read one calendar.
- `PUT /api/calendars/{id}` — replace its `name`, `weekdays`, and
  `excluded_dates`.
- `DELETE /api/calendars/{id}` — delete it.

## Cron trigger reference

Set the Cron trigger's `calendar` argument to a calendar name in the same
tenant as its workflow:

```json
{
  "action_type": "trigger",
  "action_name": "cron",
  "arguments": {
    "cron": "0 9 * * *",
    "timezone": "UTC",
    "calendar": "us-business-days"
  }
}
```

Cron skips scheduled fires on dates excluded by the calendar or weekdays not
in its allowlist. Successful payloads include `calendar` and
`calendar_allowed: true` for auditability. The trigger receives a snapshot of
the calendar rules when the workflow is loaded. A running local trigger keeps
that snapshot until the workflow is reloaded. Remote assignments include the
resolved rules in their revision, so a calendar edit rotates the trigger on
the client's next assignment poll.
