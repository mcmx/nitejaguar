# Workflows

Workflows in Nitejaguar are defined as JSON structures containing metadata and a directed graph of nodes (triggers and actions).

## Node naming

The `action_name` field uses one concise, lowercase, category-free identifier for each node implementation. For example, use `file`, `datetime`, and `wait` for actions and `filechange` for a trigger. Do not append `Action` or `Trigger`; the `action_type` field already distinguishes those categories. New implementations must use the same canonical identifier in server dispatch, client dispatch, examples, tests, and their dedicated reference document.

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

- **Import (`server -i file.json`)**:
  - Imports the workflow verbatim.
  - Existing IDs and names are preserved (upsert behavior).
- **Clone (`server -c file.json`)**:
  - Creates a brand new workflow.
  - Mints fresh TypeIDs (`workflow_`, `trigger_`, `action_`).
  - Rewrites all dependency edges (`conditions.nexts` and `dependencies`) through the old-to-new ID map.
  - Prefixes the workflow name with `"Clone of: "`.
