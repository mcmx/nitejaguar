# Workflows

Workflows in Nitejaguar are defined as JSON structures containing metadata and a directed graph of nodes (triggers and actions).

## Node naming

The `action_name` field uses one concise, lowercase, category-free identifier for each node implementation. For example, use `file`, `datetime`, `wait`, and `transfer` for actions and `filechange` for a trigger. Do not append `Action` or `Trigger`; the `action_type` field already distinguishes those categories. New implementations must use the same canonical identifier in server dispatch, client dispatch, examples, tests, and their dedicated reference document.

## Workflow JSON Structure

```json
{
  "id": "workflow_01h... ",
  "name": "PDF Renamer",
  "nodes": [
    {
      "id": "trigger_01h...",
      "action_type": "trigger",
      "action_name": "filechange",
      "arguments": {
        "path": "~/Downloads"
      },
      "conditions": {
        "nexts": ["action_01h..."]
      },
      "dependencies": []
    },
    {
      "id": "action_01h...",
      "action_type": "action",
      "action_name": "file",
      "arguments": {
        "operation": "rename_pdf"
      },
      "conditions": {
        "nexts": []
      },
      "dependencies": ["trigger_01h..."]
    }
  ]
}
```

## Import vs. Clone

- **Import (`server -i file.json` or `client workflow import file.json`)**:
  - Imports the workflow verbatim.
  - Existing IDs and names are preserved (upsert behavior).
  - `server -i` imports at server startup; `client workflow import` POSTs the file to `POST /api/workflows/import` on a running server and prints the workflow id.
- **Clone (`server -c file.json` or `client workflow clone file.json`)**:
  - Creates a brand new workflow.
  - Mints fresh TypeIDs (`workflow_`, `trigger_`, `action_`).
  - Rewrites all dependency edges (`conditions.nexts` and `dependencies`) through the old-to-new ID map.
  - Prefixes the workflow name with `"Clone of: "`.
  - `server -c` clones at server startup; `client workflow clone` POSTs the file to `POST /api/workflows/clone` on a running server and prints the new workflow id.
- **Delete (`DELETE /api/workflows/{id}`, operator+, audited as
  `workflow.delete`)**: removes a workflow definition. Cross-tenant
  requires admin.

## Website management

The workflow list (`/`) shows **View**, **Edit** (designer), **Clone**,
and **Delete** (confirm-guarded) per workflow; the detail page
(`/workflows/:id`) adds enable/disable plus the same clone/delete
actions. Clone lands on the new workflow; delete returns to the list
with a confirmation notice. Mutating buttons require operator+ and
are hidden otherwise.

## Client targeting & distributed execution

Each workflow selects a default client target; each node can override it.

- **Workflow defaults**: `default_client` (exact client ID) and
  `default_client_tags` (tag list). Empty means broadcast to every client.
- **Per-node overrides**: `client` and `client_tags` on a node win over
  the workflow default. A node with neither per-node nor workflow-default
  targeting is broadcast.
- **Resolution**: `NodeAssignedTo` applies the override-or-default rule;
  the designer, `GET /api/clients/{id}/assignments`, and
  `POST /api/results` filtering all use the same resolution.
- **Cross-client handoff**: `POST /api/results` returns only the `nexts`
  owned by the reporting client. Foreign downstream nodes are persisted
  as pending assignments (`assign_` rows) and delivered via the
  `pending` array in assignment polling — never via direct local
  execution. The owner executes the pending node with the parent payload
  as `$input` and reports back; the pending row is marked `done`.

```json
{
  "id": "workflow_01h...",
  "name": "Cross-client demo",
  "default_client_tags": ["gpu"],
  "nodes": {
    "trigger_01h...": {
      "id": "trigger_01h...",
      "action_type": "trigger",
      "action_name": "filechange",
      "arguments": {"path": "/tmp"},
      "conditions": {"entries": {"entry1": {"condition": {"leftOperand": true, "operator": "", "rightOperand": null}, "nexts": ["action_01h..."]}}},
      "dependencies": []
    },
    "action_01h...": {
      "id": "action_01h...",
      "action_type": "action",
      "action_name": "file",
      "arguments": {"action": "create", "file": "/tmp/out.txt"},
      "conditions": {"entries": {}},
      "dependencies": ["trigger_01h..."],
      "client_tags": ["cpu"]
    }
  }
}
```

In the example the trigger inherits the workflow `gpu` default while the
action overrides it to `cpu`. Import/clone preserve the defaults; the
designer edits them as workflow-level fields.

## Conditions & routing decisions

Each node routes via its `conditions.entries`: every entry holds a
`condition` (`leftOperand` / `operator` / `rightOperand`, with
`$result.<path>` addressing the node's own payload and `$args.<path>`
its static arguments) plus the downstream `nexts` taken when it matches.
An omitted or empty condition is an unconditional route. Evaluation
errors are logged server-side and recorded — they never fail the
execution; routing continues with the healthy entries.

The decision is persisted with the result that produced it:

- `condition_results` — per-entry outcome (`entry id -> matched`).
- `nexts` — flattened downstream node IDs whose entry matched.
- `condition_error` — first evaluation error, if any.

## Results

Every node result is dual-written: one row in the `workflow_results` DB
table (queryable via `SaveResult`/`GetResult`/`ListResults`, filterable
by workflow/execution, idempotent per `result_id`) and one JSON mirror
at `./results/<result_id>.json`. The `/results` page reads the DB first
(files cover legacy rows), and `POST /api/results` returns
`{workflow_id, execution_id, nexts, condition_results,
condition_error}` so callers see the routing decision immediately.

## Credential references

Every node (actions AND triggers) may set `credential_ref` to a
credential id (`credential_...`) or a credential name (resolved
`user > group > tenant`). The secret itself never lives in workflow
JSON — import/clone preserve the reference verbatim, assignment
polling ships only the reference, and the executing client fetches the
secret just-in-time via `GET /api/credentials/{ref}/fetch`. See
[Credentials](./credentials.md) for scopes, types, and the fetch API.

```json
{
  "id": "action_01h...",
  "action_type": "action",
  "action_name": "s3",
  "credential_ref": "prod-aws",
  "arguments": {"bucket": "backups"},
  "conditions": {"entries": {}},
  "dependencies": ["trigger_01h..."]
}
```

`s3` is an AWS-collection action, so its credential must have type
`aws` — fetches for it are strictly enforced (see
[Credentials](./credentials.md); `ec2`/`s3` executables ship in a later
slice, the registry entries exist today).
