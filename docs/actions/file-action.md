# File Action (`fileAction`)

The `fileAction` is a workflow action that performs file system operations (`create`, `remove`, `rename`) with support for dynamic argument templates, upstream input threading, automatic parent directory creation, and collision safety.

## What It Can Do
- **File Operations (`action`)**:
  - `create`: Creates a new empty file at the specified path.
  - `remove`: Deletes/removes the specified file.
  - `rename`: Renames or moves a file from source (`file`) to destination (`new_file`).
- **Automatic Directory Creation**: If the target directory (or parent directories for `create` and `rename`) does not exist, `fileAction` automatically creates them (`os.MkdirAll`), preventing missing-directory errors.
- **Dynamic Argument Templating (`issue #28`)**:
  - Supports literal strings, `$input.<path>` references (threaded from upstream dependencies like `datetimeAction`), and `{{...}}` placeholders.
  - Template placeholders:
    - `{{file}}`, `{{base}}`, `{{ext}}`, `{{stem}}` derived from the source file.
    - `{{date}}` (defaults to local `YYYYMMDD`, e.g., `20060102`) or `{{date:<layout>}}` (e.g. `{{date:2006-01-02}}`).
  - Home directory `~` expansion on both source and destination paths.
- **Collision Safety**: If the destination file already exists during a `create` or `rename` operation, `fileAction` refuses to overwrite it, emits an error result (`Type: "error"`), and leaves the source untouched.

## Arguments

| Argument | Type | Default | Description |
|---|---|---|---|
| `action` | string | `""` | Operation type: `create`, `remove`, or `rename` |
| `file` | string | `""` | Target file path (for `create` / `remove`) or source file path (for `rename`) |
| `new_file` | string | `""` | Destination file path (required for `rename`) |

## How to Use

### Example 1: Renaming a File (PDF Renamer POC)
```json
{
  "id": "action_01h...",
  "action_type": "action",
  "action_name": "fileAction",
  "arguments": {
    "action": "rename",
    "file": "$input.file",
    "new_file": "$input.file/../{{stem}}-{{date}}{{ext}}"
  },
  "conditions": {
    "nexts": []
  },
  "dependencies": ["trigger_01h..."]
}
```

### Example 2: Creating a File in a Subdirectory
If the target directory (`~/Documents/reports`) does not exist, `fileAction` automatically creates it before creating the file:
```json
{
  "id": "action_create_01h...",
  "action_type": "action",
  "action_name": "fileAction",
  "arguments": {
    "action": "create",
    "file": "~/Documents/reports/output-{{date}}.txt"
  },
  "conditions": {
    "nexts": []
  },
  "dependencies": []
}
```
