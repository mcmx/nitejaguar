# File Change Trigger (`filechangeTrigger`)

The `filechangeTrigger` is a core trigger in Nitejaguar that monitors a specified directory for file system events (such as creations, modifications, renames, deletions, and permission changes) using `fsnotify`. When an event occurs, it debounces rapid bursts and emits a result to trigger downstream workflow execution.

## What It Can Do
- **Directory Watching**: Recursively or directly watch any local directory path (with home directory `~` expansion).
- **Event Filtering**: Filter by specific file system event types (`create`, `write`, `rename`, `remove`, `chmod`).
- **Debouncing**: Automatically debounce rapid bursts of events (e.g., streaming writes or file downloads) using a configurable millisecond window (`debounce_ms`).
- **Loop Prevention**: Automatically ignores files matching certain backup or destination patterns (such as `.pdf` files ending in `-YYYYMMDD.pdf`) to prevent infinite workflow loops.

## Arguments

| Argument | Type | Default | Description |
|---|---|---|---|
| `path` | string | `""` | Directory path to watch (supports `~` expansion) |
| `debounce_ms` | integer | `500` | Debounce window in milliseconds to group rapid file write events |
| `event_type` | string | all (`create,write,rename,remove,chmod`) | Comma-separated list of event types to listen for |

## How to Use

Add a `filechangeTrigger` node to your workflow JSON definition:

```json
{
  "id": "trigger_01h...",
  "action_type": "trigger",
  "action_name": "filechangeTrigger",
  "arguments": {
    "path": "~/Downloads",
    "debounce_ms": "1000"
  },
  "conditions": {
    "nexts": ["action_01h..."]
  },
  "dependencies": []
}
```

Start the workflow on the server:
```bash
./nitejaguar server -i examples/workflow-poc-downloads.json -e
```
