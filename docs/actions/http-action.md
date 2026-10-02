# HTTP Action (`http`)

The `http` action performs one outbound HTTP request. It is the "outbound webhook call" failure-alert channel — route a node's error result to an `http` node via a `$result.type == error` condition (see [Alerting on failure](../workflows.md#alerting-on-failure)) — and the generic escape hatch for any JSON API, including incident-management platforms (PagerDuty Events API v2, Opsgenie) without a dedicated action.

## Arguments

| Argument | Type | Default | Description |
|---|---|---|---|
| `url` | string | `""` (required) | Target URL (`http`/`https` only). Supports `$input.` refs and `{{...}}` templates. |
| `method` | string | `"POST"` | One of `GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `HEAD`. `GET`/`HEAD` never send a body. |
| `headers` | string/map | `{}` | Extra headers as a JSON object string or native map. Names and values support templates. |
| `body` | string | `""` | Request body. After template expansion, JSON bodies send as-is (default `Content-Type: application/json` when unset); anything else sends as `text/plain`. Ignored for `GET`/`HEAD`. Supports `$input.` refs and `{{...}}` templates. |
| `auth` | string | `""` | `""` (anonymous) or `"bearer"`: injects the node's `credential_ref` (`token`/`generic`) as `Authorization: Bearer <secret>`. |
| `timeout` | string/integer | `"10"` | HTTP timeout in seconds (1–60). |

Auth is not inline: with `auth: bearer`, set `credential_ref` on the node to a `token`/`generic` credential. The framework fetches it just-in-time; any other credential type, or a missing/unresolvable binding, fails closed with an error result.

## Templating

All fields support bare `$input.<path>` refs and `{{ ... }}` templates via the shared engine (`common/template.go`), with the same filter chain as the [`set` action](./set-action.md). `$result.`/`$args.` are rejected. Header names and values are templated individually.

## Payload (conditions API)

2xx: `{type: "success", method, url, status, body, result}`. The response body is JSON-decoded when possible and kept as a string otherwise (mirroring the [`webhook` trigger](./webhook-trigger.md)), so downstream conditions route on `$result.body.<path>`. Non-2xx and transport errors: `{type: "error", method, url, status?, body?, result: message}` — a failed delivery is itself routable as a failure. Responses are capped at 1 MiB.

## Examples

Failure webhook (see `examples/workflow-http.json`):

```json
{
  "id": "action_01hhttp00000000000002",
  "action_type": "action",
  "action_name": "http",
  "credential_ref": "ops-webhook-token",
  "arguments": {
    "url": "https://ops.example.com/hooks/deploy-failures",
    "method": "POST",
    "auth": "bearer",
    "headers": "{\"X-Alert-Source\": \"nitejaguar\"}",
    "body": "{\"severity\": \"error\", \"file\": \"$input.file\", \"error\": \"$input.result\"}"
  },
  "conditions": {"entries": {}},
  "dependencies": ["action_01hhttp00000000000001"]
}
```

PagerDuty Events API v2 (trigger an incident from a failure — the dedicated [`pagerduty` action](./pagerduty-action.md) wraps this with `dedup_key` support; the raw shape is):

```json
{
  "action_type": "action",
  "action_name": "http",
  "arguments": {
    "url": "https://events.pagerduty.com/v2/enqueue",
    "method": "POST",
    "body": "{\"routing_key\": \"$input.routing_key\", \"event_action\": \"trigger\", \"payload\": {\"summary\": \"Archive failed: $input.result\", \"source\": \"nitejaguar\", \"severity\": \"error\"}}"
  }
}
```

Prefer `credential_ref` + `auth: bearer` (or the `pagerduty` action) over embedding a routing key in `body`: secrets in args land in workflow JSON, logs, and results.

Opsgenie (create an alert):

```json
{
  "action_type": "action",
  "action_name": "http",
  "credential_ref": "opsgenie-api-key",
  "arguments": {
    "url": "https://api.opsgenie.com/v2/alerts",
    "method": "POST",
    "auth": "bearer",
    "body": "{\"message\": \"Archive failed: $input.result\", \"priority\": \"P2\", \"source\": \"nitejaguar\"}"
  }
}
```

## Edge cases

- Missing/invalid `url`, unknown `method`, bad `headers` JSON, out-of-range `timeout` → error at load (`New`) for literals, error result for templated values.
- `auth: bearer` without a usable credential binding → error result (fail closed, no request attempted).
- Non-2xx responses → error result carrying the decoded upstream body (a 4xx from PagerDuty/Opsgenie means the incident was *not* created — route on it).
- Transport failure / timeout → error result.
- Response bodies over 1 MiB are truncated to the cap.
