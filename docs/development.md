# Development Guide

This guide covers building, testing, code generation, and extending Nitejaguar.

## Codegen Prerequisites

- **Templ (`v0.3.1020`)**:
  - `*.templ` files are the source of truth; `*_templ.go` and `*_templ.txt` are generated.
  - After editing any `.templ` file, run:
    ```bash
    templ generate -path .
    ```
  - **Important**: Always use the pinned version (`v0.3.1020`). Never use `templ@latest`.
- **Ent**:
  - Entity schemas reside in `ent/schema/workflow.go` and `ent/schema/client.go`.
  - Regenerate ORM code after schema changes:
    ```bash
    make ent # go generate ./ent
    ```
- **Tailwind CSS**:
  - CSS source: `cmd/web/assets/css/input.css` → `output.css`.
  - Fetched via `make tailwind`.

## Common Make Commands

- `make all` — Build + Test
- `make build` — Ent codegen, Tailwind CSS compilation, Templ generation, and binary build (`main`)
- `make test` — Templ generation, build check, and running `go test ./... -v`
- `make lint` — Templ generation and `golangci-lint run ./...`
- `make run` — Run server with actions enabled (`server -e`)

## Adding a New Action or Trigger

1. Create a new package under `internal/actions/myaction/`.
2. Implement the `common.Action` interface.
3. Register the action in `internal/actions/actions.go` (`AddAction`) or trigger in `internal/actions/trigger.go` (`AddTrigger`), keyed by `action_name`.
4. Document the new action in `/docs/actions/myaction.md`.

### Naming actions and triggers

Workflow `action_name` values are concise, lowercase, category-free identifiers. Use names such as `file`, `datetime`, `wait`, and `filechange`; do not add `Action` or `Trigger` suffixes because `action_type` already identifies whether a node is an action or trigger. Each feature should expose one canonical identifier in both the server and client dispatch switches. Internal Go package/type names may use longer names when useful for clarity.
