# PagerDuty Action (`pagerduty`)

The `pagerduty` action manages incidents through the PagerDuty Events API v2. It is the "incident management" failure-alert channel: route a node's error result to a `pagerduty` node via a `$result.type == error` condition (see [Alerting on failure](../workflows.md#alerting-on-failure)). For Opsgenie or other incident platforms without a dedicated action, use the generic [`http` action](./http-action.md).

Create an Events API v2 integration in PagerDuty and store the routing key as a `token` (or `generic`) credential. The framework injects it just-in-time at execution — workflow JSON then carries no secret. A literal `routing_key` arg works for local dev.

## Arguments

| Argument | Type | Default | Description |
|---|---|---|---|
| `routing_key` | string | `""` | Events API v2 integration key. Required unless `credential_ref` holds it. Supports templates (prefer the credential). |
| `event_action` | string | `"trigger"` | `trigger` opens an incident; `acknowledge`/`resolve` update one and require `dedup_key`. |
| `summary` | string | `""` | Incident summary (required for `trigger`). Supports `$input.` refs and `{{...}}` templates. |
| `source` | string | `"nitejaguar"` | Event source shown in the incident. Supports templates. |
| `severity` | string | `"error"` | `critical`, `error`, `warning`, or `info`. |
| `dedup_key` | string | `""` | Stable per-failure identity. Repeats collapse into one incident instead of paging on every retry — set it (e.g. `nitejaguar-{{ $input.workflow }}-archive`) so a failing loop does not spam. Required for `acknowledge`/`resolve`. Supports templates. |
| `api_url` | string | Events API v2 endpoint | Override for tests/proxies (`http`/`https` only). |
| `timeout` | string/integer | `"10"` | HTTP timeout in seconds (1–60). |

Auth is not inline: set `credential_ref` on the node to a `token`/`generic` credential holding the routing key. Any other credential type, or an unresolvable `credential_ref`, fails closed with an error result (no event sent).

## Templating

All fields support bare `$input.<path>` refs and `{{ ... }}` templates via the shared engine (`common/template.go`), with the same filter chain as the [`set` action](./set-action.md). `$result.`/`$args.` are rejected.

## Payload (conditions API)

Accepted events: `{type: "success", event_action, status, dedup_key, result: "PagerDuty event <action> accepted"}`. `dedup_key` prefers the server's response, falling back to the request value — feed it back into later `acknowledge`/`resolve` nodes via `$input.dedup_key`. Route on `$result.type`, `$result.event_action`, `$result.dedup_key`. Rejected events and transport errors: `{type: "error", event_action, status?, result: message}` carrying the upstream message.

## Example

Deduped page on failure (see `examples/workflow-pagerduty.json`):

```json
{
  "id": "action_01hpduty00000000000002",
  "action_type": "action",
  "action_name": "pagerduty",
  "credential_ref": "pagerduty-deploy-key",
  "arguments": {
    "event_action": "trigger",
    "summary": "Archive failed for $input.file: $input.result",
    "severity": "error",
    "source": "nitejaguar",
    "dedup_key": "nitejaguar-archive-$input.name"
  },
  "conditions": {"entries": {}},
  "dependencies": ["action_01hpduty00000000000001"]
}
```

Create the credential once (operator+): `POST /api/credentials` with `{name: "pagerduty-deploy-key", type: "token", secret_fields: {token: "<integration-key>"}}`.

Resolve the incident when a later step recovers, reusing the key:

```json
{
  "action_type": "action",
  "action_name": "pagerduty",
  "credential_ref": "pagerduty-deploy-key",
  "arguments": {
    "event_action": "resolve",
    "dedup_key": "nitejaguar-archive-$input.name"
  }
}
```

## Edge cases

- Missing `routing_key` (and no credential), or `trigger` without `summary` → error result, nothing sent.
- `acknowledge`/`resolve` without `dedup_key` → error result (without a key PagerDuty would open a *new* incident instead of updating one — the action refuses rather than mis-firing).
- Unknown `event_action`/`severity`, bad `api_url`, out-of-range `timeout` → error at load (`New`) for literals, error result for templated values.
- Rejected events (e.g. 400 for a bad key) → error result with the upstream message; the incident was *not* created — route on it.
- Transport failure / timeout → error result.
- Credential type other than `token`/`generic`, or unresolvable `credential_ref` → error result (fail closed, no event sent).
