# Project Nitejaguar

Workflow automation written in Go

A standalone or client/server workflow automation application.


## Getting Started

These instructions will get you a copy of the project up and running on your local machine for development and testing purposes. See deployment for notes on how to deploy the project on a live system.

### Server mode
To start the application in server mode, you need to define some environment variables in a `.env` file:

```txt
PORT=8081  # if not set, 8080 will be used by default
DB_URL=file:./test.db?_fk=1&cache=shared
```

The only required value is DB_URL for server mode, if not defined you will receive the following message:
`
DB_URL is empty, you could set it to: file:ent.db?mode=memory&cache=shared&_fk=1, to start in memory only
`

Note that if using the recommended value any change will be lost after the application is shut.

To start the server:

`./nitejaguar server`

Now you can access the web page at the port.

Or the API at /api

Logs are stored at `./log/server.log`
Results are stored at `./results/`

At the moment, only workflows JSON definitions are stored in the database in the workflows table.
With time also results will be stored in the database in a separate table, with a respective job to clear old results.

## API

Check out the API documentation at:

http://127.0.0.1:8081/docs

### Client mode

The client registers with a NiteJaguar server, polls for assignments, executes assigned
`filechange` and `file` nodes locally, and reports each result back to the
server. The default server is `http://127.0.0.1:8080`:

```bash
./nitejaguar client --server http://127.0.0.1:8080 --name downloads
```

Use `--client-id` to reconnect with an existing registration, and `--token` for servers
that require authentication. These options also accept `NITEJAGUAR_SERVER`,
`NITEJAGUAR_CLIENT_ID`, `NITEJAGUAR_CLIENT_NAME`, and `NITEJAGUAR_TOKEN`. The client
retries registration and polling with exponential backoff and stops cleanly on context
cancellation (for example, SIGINT).

## Downloads PDF rename POC

Import and run the example on the server (the `-e` flag enables local actions):

```bash
./nitejaguar server -i examples/workflow-poc-downloads.json -e
```

The workflow watches `~/Downloads` for create and write events, debounces bursts from
common download programs, and renames each PDF to `stem-YYYYMMDD.pdf` using the local
date. Files already ending in `-YYYYMMDD.pdf` are ignored, so the rename cannot loop.
The destination is never overwritten; a collision is reported as an error result.

For client mode, start the server with the imported workflow and run this in another
terminal (the same JSON is assigned without changes; the `~` path is expanded on the
client):

```bash
./nitejaguar client --server http://127.0.0.1:8080 --name downloads
```

To test either mode, remove any old dated destination, copy a PDF into `~/Downloads`,
and verify that it becomes `name-YYYYMMDD.pdf`. Check `./results/` for the trigger and
action result JSON. Run the focused tests with:

```bash
go test ./internal/actions/filechange ./internal/workflow
```

## Workflows

Workflows are stored in the workflows table in the database, currently we have the following fields:
id|enabled|json_definition|created_at|updated_at

The workflow json_definition is as follows:
id: string
name:string
nodes: hash

node: {
    id: string
    name: string
    description: string
    action_type: string [trigger|action]
    action_name: string
    arguments: hash (check each action or trigger for the arguments)
    conditions: hash
    dependencies: list
}

### Reference syntax

- Conditions (routing on the node's own output): `$result.<path>` (own `Payload`), `$args.<path>` (static node `arguments`). `$input.` (upstream) is rejected here — triggers have no upstream, so file/webhook filters use `$result.file`.
- Action arguments (templates resolved against upstream): `$input.<path>` (dependency payloads threaded as `inputs`). `$result.`/`$args.` are rejected here.
- Merging upstream into the result: set `"merge_input": true` on an action node to deep-merge the upstream `$input` payload into the node's `$result` payload (`$result` keys win on conflict; non-object payloads pass through unchanged). The flag is part of the workflow JSON (export/import/clone preserve it) and is forwarded to remote clients via assignments. The merge is applied by the framework — the server workflow manager for local results, the polling client runner before reporting, and again server-side on result ingest — so action implementations stay unaware of it and every action is compliant by default.

Example trigger condition: `{"leftOperand": "$result.file", "operator": "=~", "rightOperand": "^statement-.*\\.pdf$"}`. Example action args: `{"action": "rename", "file": "$input.file", "new_file": "{{stem}}-$input.now{{ext}}"}` (date stamping comes from an upstream `datetime` with `"output_field": "now"`; `$input` refs are bare, never wrapped in `{{...}}`).

### Condition operators

Conditions support `==`, `!=`, `>`, `>=`, `<`, `<=`, bare booleans, plus pattern matching:

- `=~`: regex match. Left is the value, right is the pattern (Go `regexp` RE2 syntax, unanchored unless wrapped in `^...$`). Example: `{"leftOperand": "$result.file", "operator": "=~", "rightOperand": "^statement-.*\\.pdf$"}`.
- `glob`: glob match using `path.Match` semantics (`*` matches any sequence of non-`/` characters, `?` matches any single non-`/` character, `[...]` character classes, `\\` escapes; pattern must match the entire value, `*` does not cross `/`). Example: `{"leftOperand": "$result.file", "operator": "glob", "rightOperand": "statement-*.pdf"}`.

Both operands are resolved first (so `$result.file` / `$args.*` work), then must both be strings; non-string operands and invalid patterns return an error.

json schema (auto generated by copilot haven't been verified):
```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "Workflow",
  "type": "object",
  "required": ["id", "name", "nodes"],
  "properties": {
    "id": { "type": "string" },
    "name": { "type": "string" },
    "nodes": {
      "type": "object",
      "patternProperties": {
        "^.*$": {
          "type": "object",
          "required": ["id", "name", "description", "action_type", "action_name", "arguments", "conditions"],
          "properties": {
            "id": { "type": "string" },
            "name": { "type": "string" },
            "description": { "type": "string" },
            "action_type": { "type": "string", "enum": ["action", "trigger"] },
            "action_name": { "type": "string" },
            "arguments": {
              "type": "object",
              "additionalProperties": true
            },
            "conditions": {
              "type": "object",
              "properties": {
                "entries": {
                  "type": "object",
                  "patternProperties": {
                    "^.*$": {
                      "type": "object",
                      "required": ["condition", "nexts"],
                      "properties": {
                        "condition": {
                          "type": "object",
                          "required": ["leftOperand", "operator", "rightOperand"],
                          "properties": {
                            "leftOperand": {},
                            "operator": { "type": "string" },
                            "rightOperand": {}
                          }
                        },
                        "nexts": {
                          "type": "array",
                          "items": { "type": "string" }
                        }
                      }
                    }
                  }
                }
              }
            },
            "dependencies": {
              "anyOf": [
                { "type": "array", "items": { "type": "string" } },
                { "type": "null" }
              ]
            }
          }
        }
      }
    }
  }
}
```



## Triggers and Actions

## Inner architecture

All ID's are typeid based on uuid, it provides a sortable time based id with the following prefixes used:
- workflow, indicates a workflow id
- result, indicates this is a result id
- action, indicates this is an action id
- trigger, indicates this is a trigger id

Having these ID's allows us to export/import a complete workflow from one instance to another without errors.
Also it allows the operator to very easily identify the entities, and for the results it makes it very easy to
sort by creation creation date (you can't get the date and time from the id) but you know it's in a chronological order.

## MakeFile

Run build make command with tests
```bash
make all
```

Build the application
```bash
make build
```

Run the application
```bash
make run
```

Live reload the application:
```bash
make watch
```

Run the test suite:
```bash
make test
```

Clean up binary from the last build:
```bash
make clean
```
