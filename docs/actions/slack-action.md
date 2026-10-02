# Slack Action (`slack`)

The `slack` action posts a message to Slack through an incoming webhook URL. It is one of the selectable failure-alert channels: route a node's error result to a `slack` node via a `$result.type == error` condition (see [Alerting on failure](../workflows.md#alerting-on-failure)).

Create the webhook in Slack (Incoming Webhooks app) and store the URL as a `token` (or `generic`) credential. The framework injects it just-in-time at execution — workflow JSON then carries no secret. A literal `webhook_url` arg works for local dev.

## Arguments

| Argument | Type | Default | Description |
|---|---|---|---|
| `webhook_url` | string | `""` | Slack incoming webhook URL (`https://hooks.slack.com/…`). Optional when `credential_ref` holds it. Supports `$input.` refs and `{{...}}` templates. |
| `text` | string | `""` (required) | Message text. Supports `$input.` refs and `{{...}}` templates with the shared filter chain. |
| `channel` | string | `""` | Channel override (e.g. `#alerts`). Recent incoming webhooks ignore it; sent when set. Supports templates. |
| `username` | string | `""` | Bot display-name override. Supports templates. |
| `timeout` | string/integer | `"10"` | HTTP timeout in seconds (1–60). |

Auth is not an argument: set `credential_ref` on the node to a `token`/`generic` credential holding the webhook URL. Any other credential type, or an unresolvable `credential_ref`, fails closed with an error result (no post attempted).

## Templating

All fields support bare `$input.<path>` refs and `{{ ... }}` templates via the shared engine (`common/template.go`), with the same filter chain as the [`set` action](./set-action.md) (`upper`, `lower`, `trim`, `trimPrefix`, `trimSuffix`, `replace`, `default`). `$result.`/`$args.` are rejected.

## Payload (conditions API)

Success: `{type: "success", status: <http-status>, result: "Slack message posted"}`. Error: `{type: "error", status?, result: message}` (non-2xx responses carry the truncated upstream body). Route on `$result.type`, `$result.status`.

## Example

Failure alert (see `examples/workflow-slack.json`): the `file` node routes its error result to the alert node; the alert text interpolates the failed node's payload via `$input.` (the alert's input is the failed node's result):

```json
{
  "id": "action_01hslack000000000002",
  "action_type": "action",
  "action_name": "slack",
  "credential_ref": "slack-alerts-webhook",
  "arguments": {
    "text": ":rotating_light: Archive failed for $input.file: $input.result",
    "channel": "#alerts",
    "username": "nitejaguar"
  },
  "conditions": {"entries": {}},
  "dependencies": ["action_01hslack000000000001"]
}
```

Create the credential once (operator+): `POST /api/credentials` with `{name: "slack-alerts-webhook", type: "token", secret_fields: {token: "https://hooks.slack.com/services/…"}}`.

## Edge cases

- Missing `text`, or neither `webhook_url` nor credential URL → error result, nothing posted.
- Non-http(s) `webhook_url` (literal at load, templated at execution) → error at load (`New`) for literals, error result for templated values.
- Non-2xx Slack response (e.g. `channel_not_found`) → error result with the upstream body (truncated to 2KB); nothing is retried — retries belong in the workflow (re-route or loop), not in the action.
- Transport failure / timeout → error result.
- Credential type other than `token`/`generic`, or unresolvable `credential_ref` → error result (fail closed, no post attempted).
