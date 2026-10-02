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
- Adding a new action/trigger means adding a package under `internal/actions/` (e.g. `fileaction/`, `filechange/`) **and** a `case` in the switch in `internal/actions/actions.go` (`AddAction`) or `internal/actions/trigger.go` (`AddTrigger`), keyed by the `action_name` string used in workflow JSON. The package must also ship its designer schema — there is no central schema file:
  - `catalog.go` with `CatalogEntry() common.DesignerCatalogEntry`: picker card (icon/label/desc), new-node defaults (`Args`), and typed inspector fields (`common.DesignerField` — `string`, `integer`/`number` → number input, `boolean` → checkbox, `select` → combo, `multiselect` → checkboxes, `textarea`/`json` → textarea; build options with `common.FieldOptions`). Schema types live in `common/designer_catalog.go`, so `CatalogEntry` adds no forbidden deps to the client binary.
  - One `CatalogEntry()` line in `ActionCatalog()` (`actions.go`) / `TriggerCatalog()` (`trigger.go`), next to the dispatch switch — that is the only central edit.
  - `internal/actions/catalog_test.go` dispatches every catalog entry with its own designer defaults, so a switch case without a working entry fails the build; `cmd/web/designer_catalog_test.go` asserts the browser payload mirrors the assembled catalog.
- External workflow identifiers are category-free: use a concise lowercase name such as `file`, `datetime`, `wait`, or `filechange`; do not append `Action` or `Trigger`. `action_type` already distinguishes actions from triggers. Keep Go package and type names descriptive as needed, but expose exactly one canonical `action_name` and do not add aliases unless a migration plan explicitly requires them.
- All entity IDs are typeid-based with prefixes: `workflow_`, `result_`, `action_`, `trigger_`. New code should keep generating IDs via `go.jetify.com/typeid` with the matching prefix rather than ints or unknown formats.
- Workflow JSON structure: top-level `id`/`name`/`nodes`; each node has `action_type` (`trigger`|`action`), `action_name`, `arguments`, `conditions`, `dependencies`. See `examples/workflow2.json` / `workflows/workflow3.json`.
- Client mode is stubbed (root command runs `client`, which exits with "not implemented").
- Templates & `$input` references (shared engine in `common/template.go` + `common/refpath.go` — mandatory for new implementations):
  - New actions/triggers MUST resolve `$input.<path>` via `common.ResolveRefPath` and `{{ ... }}` expressions via the shared template engine (`common.EvalTemplate` / `common.ExpandTemplates` / `common.ExpandJSONTemplates` / `common.SplitTemplates` / `common.InputLookup`); never copy-paste a private resolver.
  - New filters and expression syntax belong in `common/template.go` only (registered in `applyTemplateFilter`, covered by `common/template_test.go`), so every action benefits. Supported today: `upper`, `lower`, `trim`, `trimPrefix`, `trimSuffix`, `replace`, `default` — see `docs/actions/set-action.md` (filter table is the user-facing contract; update it when adding filters).
  - Splitting rule: expand `{{...}}` regions separately from bare `$input.` refs (see `common.SplitTemplates`) so adjacent forms (`$input.now{{ext}}`, `{{ $input.tag | upper }}`) never consume each other. Reject `$result.`/`$args.` in args (own result doesn't exist at resolution time).

## Web UI (sidebar shell + graph designer)

- App shell: `Base` (`cmd/web/base.templ`) renders a left `Sidebar` + sticky `Topbar` (`cmd/web/modules/navbar.templ`) around page content (`.nj-content`, max 1400px). `Navbar` is kept as a thin wrapper over `Sidebar` for `TestNavbarGating` — don't remove it. Active-link highlighting is client-side (data-navlink vs `location.pathname`) so `Base` needs no route param.
- Shell/graph styles live in `cmd/web/assets/css/input.css` (`nj-shell`, `nj-canvas`, `nj-node`, …); `output.css` is committed, so regenerate + commit it after any `.templ`/CSS change (`make build`).
- Designer (`DesignerPage` in `cmd/web/dashboard.templ`) is a visual graph editor: draggable cards, bezier edges with arrowheads, per-node **＋ Add action** picker backed by the designer catalog (each action/trigger package defines its own `CatalogEntry` in `internal/actions/*`, assembled next to dispatch in `internal/actions`, rendered as the `DESIGNER_CATALOG` JS table), node inspector, live JSON preview. Canvas edges (`nexts`) are the source of truth — `getJSON()` translates them into workflow JSON (`conditions.entries` forward, `dependencies` recomputed from incoming edges) for `POST /designer/save`; the backend save path is unchanged.
- templ + Alpine gotchas (all bitten before, all covered by `TestDesignerGraphClientLogic`):
  - Never write a literal `{{`/`}}` inside a `<script>` block in `.templ` (the compiler parses it as template code → `undefined: base` build failure). Build brace strings via `String.fromCharCode(123, …)`.
  - Never use `<template x-for>` inside `<svg>` (content parses in the HTML namespace, so `<path>` never paints). Render edges via `x-html` on a `<g>`.
  - Canvas drag listeners must detach on `pointerup` **and** `pointercancel` (a missing detach leaves the card stuck to the cursor), with a small dead-zone so plain clicks don't jitter cards.
- Designer JS is headless-testable: extract the `<script>` from `dashboard.templ`, stub `window` listener registry, and drive `designerGraph()` under `node` (edges, drag attach/detach, connect).

## Tenant isolation & RBAC — mandatory for every change

- Model: every tenant-scoped row carries a `tenant_id` slug (`Tenant.Slug`; `default` always exists). Admins of `default` are **superusers**; tenant-local admins manage only their own tenant. Helpers: `database.IsSuperUser` / `database.CanCrossTenant` (server: `normalizeTenantID`, `scopedTenant`, `workflowTenant`). Roles rank `viewer < operator < admin` — gate with `requireRole` (API) / `webActor` (web forms).
- New entities: any new ent object holding per-tenant data **must** carry `tenant_id`. Child objects without it (like `transfer_chunks`/`transfer_signals` today) are only acceptable if **every** access path re-checks the parent session's tenant first.
- Never trust caller-supplied tenants: derive the tenant from the authenticated session (`scopedTenant`) or client token, and ignore/override any `tenant_id` in the body/JSON (see `PostResult`, `designerSaveWorkflow`). `default` is an ordinary tenant, **never** a wildcard — compare tenants with `normalizeTenantID`/`normalizeTenant` (empty == `default`), never with `!= "default"` carve-outs.
- Reads: every endpoint needs a session (viewer+ minimum; open-bootstrap `nil`-caller only for fresh-install flows). Collections silently omit foreign rows; single foreign objects return `404` (never `403`, no existence oracle); anonymous calls get `401` once users exist.
- Writes: verify ownership **before** mutating. Global namespaces (workflow ids are upsert-by-primary-key) need an explicit cross-tenant hijack guard — only superusers may overwrite another tenant's id (see `ImportWorkflow`, `designerSaveWorkflow`). Cross-tenant writes are `403`.
- Suspension fails closed: suspended tenants must be refused in `VerifyUser`, `CreateSession`, `AuthenticateSession`, enrollment mint/consume, and `RegisterClient`.
- Audit every tenant/user/token/workflow/credential mutation with the session user as actor (`tenant.create/suspend/...` for registry ops).
- Tests + docs are part of the change: extend `internal/server/tenant_isolation_test.go` (anonymous, cross-tenant read, cross-tenant write, web-denial cases) for any new endpoint or entity, and update `docs/tenants.md` + `docs/rbac.md` concurrently.

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
