# AGENTS.md

Workflow automation app (Go). Module `github.com/mcmx/nitejaguar`. Entrypoint `cmd/api.RunServer` (cobra CLI in `cmd/`).

## Build prerequisites — codegen first

- `*.templ` files are the source of truth; `*_templ.go` and `*_templ.txt` are **gitignored and generated**. After editing a `.templ` file you must run `templ generate -path .` before build/test/lint (CI does this explicitly). The generated files exist in the working tree but are not committed.
- The templ **generator** version must match the templ **runtime** in `go.mod` (`v0.3.1020`, pinned in the Makefile and all CI workflows). A newer generator emits code (e.g. `templ.ResolveAttributeValue`) the pinned runtime lacks, which breaks `go build` and lint. Never install/generate with `templ@latest`.
- `ent/` is checked in, but schema lives in `ent/schema/workflow.go` — regenerate with `make ent` (`go generate ./ent`) after schema changes.
- `tailwindcss` is a downloaded standalone binary (gitignored), fetched by `make tailwind`. CSS pipeline: `cmd/web/assets/css/input.css` → `output.css`. Unlike the `_templ.go` files, `output.css` **is committed**, so after any `.templ` markup change you must regenerate it (`make build`, or `./tailwindcss -i cmd/web/assets/css/input.css -o cmd/web/assets/css/output.css`) and commit the updated `output.css` — otherwise the PR ships classes with no CSS.
- `make build` calls ent, tailwind, and templ generation, then builds binary `main`. Run it before opening a PR so `output.css` and all other codegen are fresh.

## Commands

- `make all` — build + test
- `make test` — mirrors CI (`templ generate -path .`, then `go build -v ./...`, then `go test ./... -v`)
- `make lint` — mirrors CI (`templ generate -path .`, then `golangci-lint run ./...`)
- `make run` — `go run main.go server -e` (`-e` enables action execution, required for triggers/actions to run)
- `make watch` — runs air + templ-watch + tailwind-watch in parallel; requires `templ` and the `tailwindcss` binary
- `make clean` — removes the `main` binary
- `server -i file.json` imports a workflow verbatim (upsert; ids/name untouched)
- `server -c file.json` clones a workflow with fresh ids (mints new `workflow_`/`trigger_`/`action_` ids and rewrites every `conditions.nexts`/`dependencies` edge through the old→new map; name gets `"Clone of: "`). Literal-import vs clone are separate paths (`ImportWorkflowJSON` ≠ `CloneWorkflowJSON`), so don't conflate them.

## Git workflow

- Start every task from fresh `main`: `git checkout main && git pull`.
- One feature branch per task: `git checkout -b <type>/short-desc` (`fix/`, `feat/`, `chore/`).
- Verify with `make lint` and `make test` before pushing.
- Run `make build` before opening a PR so committed codegen (`output.css`, ent, templ) is fresh — in particular, any `.templ` markup change requires a regenerated + committed `output.css` (see above).
- Push and open a PR (`gh pr create`); reference issues as `Fixes #N` so they auto-close on merge.
- Merge only with CI (lint + test jobs) green. Never push directly to `main`.

## Runtime / env

- `.env` is loaded via `godotenv` autoload and is gitignored. Server mode needs `DB_URL` (e.g. `file:./test.db?_fk=1&cache=shared`); without it the app falls back to in-memory SQLite. `PORT` defaults to 8080.
- `log/server.log`, `results/`, `*.db`, and the `main` binary are gitignored runtime artifacts.
- API is OpenAPI via huma v2 (docs at `/docs`) on top of Echo; websocket at `/websocket`; static assets from `cmd/web/assets`.
- Edge-case note: the `routes_test.go` / `ent/db_test.go` tests use in-memory SQLite and set `DB_URL` themselves; running the full `go test ./...` needs templ generated first (see above).

## Architecture / conventions

- Layering: `cmd/api` (server bootstrap) → `internal/server` (HTTP/routes) → `internal/database` (ent + SQLite), `internal/workflow` (workflow engine), `internal/actions` (triggers/actions). Shared types (`Action`, `ActionArgs`, `ResultData`) live in `common/`.
- Adding a new action/trigger means adding a package under `internal/actions/` (e.g. `fileaction/`, `filechange/`) **and** a `case` in the switch in `internal/actions/actions.go` (`AddAction`) or `internal/actions/trigger.go` (`AddTrigger`), keyed by the `action_name` string used in workflow JSON.
- External workflow identifiers are category-free: use a concise lowercase name such as `file`, `datetime`, `wait`, or `filechange`; do not append `Action` or `Trigger`. `action_type` already distinguishes actions from triggers. Keep Go package and type names descriptive as needed, but expose exactly one canonical `action_name` and do not add aliases unless a migration plan explicitly requires them.
- All entity IDs are typeid-based with prefixes: `workflow_`, `result_`, `action_`, `trigger_`. New code should keep generating IDs via `go.jetify.com/typeid` with the matching prefix rather than ints or unknown formats.
- Workflow JSON structure: top-level `id`/`name`/`nodes`; each node has `action_type` (`trigger`|`action`), `action_name`, `arguments`, `conditions`, `dependencies`. See `examples/workflow2.json` / `workflows/workflow3.json`.
- Client mode is stubbed (root command runs `client`, which exits with "not implemented").

## Lint

- Local linting is via `make lint`, which mirrors CI (`templ generate -path .`, then `golangci-lint run ./...` with golangci-lint v2.13.2). Run it after changes; remember to regenerate templ first so generated `_templ.go` files are linted/typed too.
- The repo targets Go 1.26 (`go`/`toolchain` in `go.mod`); golangci-lint binaries built with older Go refuse to analyze the module, so the local binary must be built with Go ≥ 1.26 (`make lint` installs the pinned version if missing).

## Documentation Guidelines

- **Maintain `/docs`**: The AI agent is responsible for maintaining and updating all documentation in the `/docs` directory.
- **Sync with Code**: Whenever new features, CLI flags, configuration options, server/client modes, workflow JSON schemas, or actions/triggers are modified or added, the corresponding documentation files under `/docs/` must be updated concurrently.
- **Detailed Action & Trigger Reference**: Each action and trigger must have its own dedicated document under `docs/actions/` detailing:
  - All supported operations / action types (e.g., `create`, `remove`, `rename`).
  - Complete argument tables (key, type, default, description).
  - Behavioral edge cases (e.g., automatic parent directory creation via `os.MkdirAll` vs. collision safety policies refusing to overwrite existing files).
  - Concrete JSON workflow usage examples.
- **Structure**:
  - `/docs/README.md` — Documentation index.
  - Core guides: `getting-started.md`, `architecture.md`, `server-mode.md`, `client-mode.md`, `workflows.md`, `development.md`.
  - Actions & Triggers: One document per action/trigger under `/docs/actions/`.
