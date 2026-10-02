# Email Action (`email`)

The `email` action sends an SMTP message with templated headers, plain and/or HTML bodies, and file or inline attachments. SMTP host/port come from node args; SMTP auth comes only from the node's `credential_ref` (a `username_password` credential) injected just-in-time at execution — never from inline args or workflow JSON. Nodes without a credential send anonymously (local MailHog / open-relay dev).

## Arguments

| Argument | Type | Default | Description |
|---|---|---|---|
| `host` | string | `""` (required) | SMTP host. Supports `$input.` refs and `{{...}}` templates. |
| `port` | string/integer | `"587"` | SMTP port. `465` implicit TLS, `587` STARTTLS, else plaintext with opportunistic STARTTLS. Supports templates. |
| `from` | string | `""` (required) | Sender address. Supports templates. |
| `to` | string | `""` (required with cc/bcc) | Comma-separated recipients. Supports templates. |
| `cc` | string | `""` | Comma-separated recipients. Supports templates. |
| `bcc` | string | `""` | Comma-separated recipients (envelope only, never in headers). Supports templates. |
| `subject` | string | `""` | Subject line. Supports templates. At least one of subject/body/html must be non-empty. |
| `body` | string | `""` | Plain-text body. Supports templates. |
| `html` | string | `""` | HTML body. With `body` sends multipart/alternative; alone sends html-only. Supports templates. |
| `attachments` | string/list | `""` | Comma-separated paths or JSON array of path strings / `{path}` / `{name, content_base64}`. Names and paths support templates; content bytes never do. 10MB total cap. |

Auth is not an argument: set `credential_ref` on the node to a `username_password` credential (`{username, password}`). The framework fetches it just-in-time (server-local via the credential store, remote via `GET /api/credentials/{ref}/fetch`) and injects it in-memory for the send. Any other credential type fails closed; an unresolvable `credential_ref` fails closed instead of falling back to anonymous.

## Templating

All fields except attachment content bytes support bare `$input.<path>` refs and `{{ ... }}` templates via the shared engine (`common/template.go`), with the same filter chain as the [`set` action](./set-action.md) (`upper`, `lower`, `trim`, `trimPrefix`, `trimSuffix`, `replace`, `default`). `$result.`/`$args.` are rejected. Attachment names and paths are templated; inline `content_base64` is decoded verbatim (base64, raw-string fallback) and never templated.

## Message shape

- No attachments, one body part: single `text/plain` or `text/html`.
- Both bodies, no attachments: `multipart/alternative`.
- Any attachments: outer `multipart/mixed`; the body block nests inside (alternative when both set), then one base64 part per attachment (`application/octet-stream`, `Content-Disposition: attachment`).

## Payload (conditions API)

Success: `{type: "success", host, port, from, to[], cc[], bcc[], subject, attachments: [names], bytes, result}`. Error: `{type: "error", host, result: message}`. Secrets never appear in payloads, logs, or results. Route on `$result.type`, `$result.to`, `$result.subject`.

## Examples

Anonymous dev send (MailHog on port 1025):

```json
{
  "id": "action_01hemail000001",
  "action_type": "action",
  "action_name": "email",
  "arguments": {
    "host": "127.0.0.1",
    "port": "1025",
    "from": "noreply@example.com",
    "to": "ops@example.com",
    "subject": "Build {{ $input.build }} finished",
    "body": "Hi $input.name, see attached."
  },
  "conditions": {"entries": {}},
  "dependencies": ["trigger_01hemail000001"]
}
```

Authenticated send with HTML + attachments (credential holds the SMTP login):

```json
{
  "id": "action_01hemail000002",
  "action_type": "action",
  "action_name": "email",
  "credential_ref": "smtp-prod",
  "arguments": {
    "host": "smtp.example.com",
    "port": "587",
    "from": "noreply@example.com",
    "to": "ops@example.com, oncall@example.com",
    "cc": "{{ $input.manager }}",
    "subject": "Deploy {{ $input.version | upper }} done",
    "body": "Hi $input.name, deploy log attached.",
    "html": "<b>Hi {{ $input.name }}</b><p>Deploy log attached.</p>",
    "attachments": "[\"/tmp/deploy.log\", {\"name\": \"summary-{{ $input.version }}.txt\", \"content_base64\": \"aGVsbG8=\"}]"
  },
  "conditions": {"entries": {}},
  "dependencies": ["trigger_01hemail000001"]
}
```

Create the credential once (operator+): `POST /api/credentials` with `{name: "smtp-prod", type: "username_password", secret_fields: {username: "svc", password: "…"}}`.

## Edge cases

- Missing host/from/recipients or empty subject+body+html → error result, nothing sent.
- Bad port (non-numeric, out of 1–65535) → error at load (`New`) for literals, error result for templated values.
- Invalid address (unparseable by `net/mail`) → error result.
- Unreadable attachment path, malformed attachments JSON, inline entry without name/content → error result.
- Attachments over 10MB total (decoded) → error result; bytes never enter the result payload (only names).
- Port 587 without server STARTTLS + auth present → error (refuses cleartext credentials); without auth it proceeds plaintext.
- Credential type other than `username_password`, or unresolvable `credential_ref` → error result (fail closed, no anonymous fallback, no send attempted).
