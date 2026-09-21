# Workflows

Workflows in Nitejaguar are defined as JSON structures containing metadata and a directed graph of nodes (triggers and actions).

## Workflow JSON Structure

```json
{
  "id": "workflow_01h... ",
  "name": "PDF Renamer",
  "nodes": [
    {
      "id": "trigger_01h...",
      "action_type": "trigger",
      "action_name": "filechangeTrigger",
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
      "action_name": "fileAction",
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
