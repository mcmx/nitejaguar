# Webhook Trigger (`webhook`)

The `webhook` trigger fires a workflow from an inbound HTTP request instead of a schedule or filesystem event. Each webhook node exposes one endpoint:

```
POST /webhook/{trigger_id}
```

on the server, and — when the client listener is enabled — the same path on every client that owns the trigger.

## What It Can Do

- **All HTTP methods**: `GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `HEAD`, `OPTIONS`, `TRACE`, `CONNECT`. The `method` argument selects which ones fire; anything else is rejected with `405` and never produces a result.
- **Payload in the result**: methods that carry a body post it in the trigger result. JSON bodies decode to native values, anything else stays a string, so downstream conditions can route on `$result.body`, `$result.query`, `$result.method` and friends.
- **Server ingress**: `e.Any("/webhook/:id")` on the server webserver. No session required so external systems (GitHub, Stripe, CI, `curl`) can deliver. The ack answers `{"ok":true,"workflow_id":...,"execution_id":...,"trigger_id":...}`.
- **Client ingress**: the client runs its own webserver (`--webhook-addr 127.0.0.1:8081`, or `NITEJAGUAR_WEBHOOK_ADDR`) answering the same `/webhook/{id}` path. A client only fires triggers assigned to it — workflow `default_client` / `default_client_tags` first, per-node `client` / `client_tags` overrides, broadcast otherwise — because the server already filters assignments that way. On install the client logs every served trigger (`webhook trigger installed; serving deliveries`, trigger id + path); unknown ids answer `404`, disallowed methods `405`.

## Arguments

| Argument | Type | Default | Description |
|---|---|---|---|
| `method` | string | `ALL` | Which HTTP methods fire. Empty, `ALL` or `*` accepts every method; otherwise a comma-separated list such as `POST` or `GET,POST` (case-insensitive) |

## Result Payload

Every accepted request emits:

```json
{
  "type": "success",
  "trigger": "webhook",
  "method": "POST",
  "path": "/webhook/trigger_01h...",
  "query": {"branch": "main"},
  "query_string": "branch=main",
  "headers": {"content-type": "application/json"},
  "body": {"event": "push"},
  "body_raw": "{\"event\":\"push\"}"
}
```

| Field | Description |
|---|---|
| `method` | Uppercase HTTP method (`POST`, `GET`, …) |
| `path` | Request path as received |
| `query` | Query params (last value wins per key); route with `$result.query.branch` |
| `query_string` | Raw query string (useful for signatures) |
| `headers` | Lowercased header names, multi-values joined with `", "` |
| `body` | Decoded payload: JSON becomes native values, anything else stays a string, empty stays `null` |
| `body_raw` | Exact body bytes as text |

## Behavioral Edge Cases

- **Validation happens at load time**: an invalid `method` fails while the workflow is added (`AddTrigger` logs `Cannot create new trigger: …`) instead of silently never firing.
- **Method mismatch is a 405**: the request never creates a result, execution, or audit noise beyond the access log.
- **Unknown or disabled workflows are 404**: the endpoint reveals nothing about other tenants' triggers.
- **Suspended tenants fail closed**: webhooks for a suspended tenant answer `403` and never fire.
- **Bodies are capped at 1 MiB** (`413` above that) so one delivery cannot exhaust the server.
- **Server dispatch is distributed**: `IngestResult` routes downstream nodes — nodes the reporting path owns run locally, the rest become pending assignments for their owners, exactly like client-posted results.
- **Client dispatch is pull-based**: a client webhook queues into the runner event stream and posts to the server as the trigger result; downstream routing then follows the normal assignment flow.

## How to Use

Add a `webhook` node to a workflow JSON definition:

```json
{
  "id": "trigger_01h...",
  "action_type": "trigger",
  "action_name": "webhook",
  "arguments": {
    "method": "POST"
  },
  "conditions": {
    "entries": {
      "push": {
        "condition": { "leftOperand": "$result.body.event", "operator": "==", "rightOperand": "push" },
        "nexts": ["action_01h..."]
      }
    }
  },
  "dependencies": []
}
```

Fire it against the server:

```bash
curl -X POST http://127.0.0.1:8080/webhook/trigger_01h... \
  -H 'Content-Type: application/json' \
  -d '{"event":"push","branch":"main"}'
```

Or bind it to a client and fire it there (client owns the trigger via targeting):

```json
{
  "id": "trigger_01h...",
  "action_type": "trigger",
  "action_name": "webhook",
  "arguments": {"method": "GET,POST"},
  "client_tags": ["edge"],
  "conditions": {"entries": {"entry1": {"condition": {"leftOperand": true, "operator": "", "rightOperand": null}, "nexts": ["action_01h..."]}}}
}
```

```bash
nitejaguar --server http://127.0.0.1:8080 --webhook-addr 127.0.0.1:8081
curl http://127.0.0.1:8081/webhook/trigger_01h...?branch=main
```

A complete workflow is in `examples/workflow-webhook.json`:

```bash
./nitejaguar-server -i examples/workflow-webhook.json -e
curl -X POST http://127.0.0.1:8080/webhook/trigger_01webhook example0000001 \
  -H 'Content-Type: application/json' -d '{"event":"push"}'
```

The trigger is also available in the visual designer picker (**Webhook**) and runs on remote clients like any other trigger. Selecting a webhook node in the designer shows its endpoint (`/webhook/{trigger_id}`) with a copy button, and the workflow detail page lists the same endpoint per webhook node.
