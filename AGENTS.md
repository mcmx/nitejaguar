# AGENTS.md

Workflow automation app (Go). Module `github.com/mcmx/nitejaguar`. Entrypoint `cmd/api.RunServer` (cobra CLI in `cmd/`).

## Build prerequisites — codegen first

- `*.templ` files are the source of truth; `*_templ.go` and `*_templ.txt` are **gitignored and generated**. After editing a `.templ` file you must run `templ generate -path .` before build/test/lint (CI does this explicitly). The generated files exist in the working tree but are not committed.
- `ent/` is checked in, but schema lives in `ent/schema/workflow.go` — regenerate with `make ent` (`go generate ./ent`) after schema changes.
- `tailwindcss` is a downloaded standalone binary (gitignored), fetched by `make tailwind`. CSS pipeline: `cmd/web/assets/css/input.css` → `output.css`.
- `make build` calls ent, tailwind, and templ generation, then builds binary `main`.

## Commands

- `make all` — build + test
- `make test` — `go test ./... -v`
- `make run` — `go run main.go server -e` (`-e` enables action execution, required for triggers/actions to run)
- `make watch` — runs air + templ-watch + tailwind-watch in parallel; requires `templ` and the `tailwindcss` binary
- `make clean` — removes the `main` binary
- `server -i file.json` imports a workflow verbatim (upsert; ids/name untouched)
- `server -c file.json` clones a workflow with fresh ids (mints new `workflow_`/`trigger_`/`action_` ids and rewrites every `conditions.nexts`/`dependencies` edge through the old→new map; name gets `"Clone of: "`). Literal-import vs clone are separate paths (`ImportWorkflowJSON` ≠ `CloneWorkflowJSON`), so don't conflate them.

## Runtime / env

- `.env` is loaded via `godotenv` autoload and is gitignored. Server mode needs `DB_URL` (e.g. `file:./test.db?_fk=1&cache=shared`); without it the app falls back to in-memory SQLite. `PORT` defaults to 8080.
- `log/server.log`, `results/`, `*.db`, and the `main` binary are gitignored runtime artifacts.
- API is OpenAPI via huma v2 (docs at `/docs`) on top of Echo; websocket at `/websocket`; static assets from `cmd/web/assets`.
- Edge-case note: the `routes_test.go` / `ent/db_test.go` tests use in-memory SQLite and set `DB_URL` themselves; running the full `go test ./...` needs templ generated first (see above).

## Architecture / conventions

- Layering: `cmd/api` (server bootstrap) → `internal/server` (HTTP/routes) → `internal/database` (ent + SQLite), `internal/workflow` (workflow engine), `internal/actions` (triggers/actions). Shared types (`Action`, `ActionArgs`, `ResultData`) live in `common/`.
- Adding a new action/trigger means adding a package under `internal/actions/` (e.g. `fileaction/`, `filechange/`) **and** a `case` in the switch in `internal/actions/actions.go` (`AddAction`) or `internal/actions/trigger.go` (`AddTrigger`), keyed by the `action_name` string used in workflow JSON.
- All entity IDs are typeid-based with prefixes: `workflow_`, `result_`, `action_`, `trigger_`. New code should keep generating IDs via `go.jetify.com/typeid` with the matching prefix rather than ints or unknown formats.
- Workflow JSON structure: top-level `id`/`name`/`nodes`; each node has `action_type` (`trigger`|`action`), `action_name`, `arguments`, `conditions`, `dependencies`. See `examples/workflow2.json` / `workflows/workflow3.json`.
- Client mode is stubbed (root command runs `client`, which exits with "not implemented").

## Lint

- Local linting is via trunk (`trunk check`) using golangci-lint v1.64.8 and gofmt, configured in `.trunk/trunk.yaml`. CI runs the same golangci-lint version (v1.64) after `templ generate`. Run `golangci-lint run` (or `trunk check`) after changes; remember to regenerate templ first so generated `_templ.go` files are linted/typed too.