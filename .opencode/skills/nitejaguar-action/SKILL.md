---
name: nitejaguar-action
description: Use when adding a new Nitejaguar action or trigger (action_name, AddAction, AddTrigger, internal/actions, newClientTrigger). Covers naming, scaffolding, template rules, registration, docs, and verification.
---

# Add a Nitejaguar action or trigger

Conventions for `github.com/mcmx/nitejaguar`. Follow AGENTS.md; this skill is the condensed checklist so a new node ships complete without re-discovering the rules.

## 1. Naming (`action_name`)

- One canonical identifier: concise, lowercase, category-free (`file`, `datetime`, `wait`, `filechange`, `cron`, `webhook`). Never append `Action`/`Trigger` — `action_type` already distinguishes.
- Exactly one name per implementation; no aliases unless a migration plan requires them.
- Go package/type names may be descriptive (`fileaction/`, `filechange/`); the JSON `action_name` stays short.
- IDs are typeid-based with prefixes (`workflow_`, `result_`, `action_`, `trigger_`, `execution_`, `client_`, `assign_`, `transfer_`). Generate via `go.jetify.com/typeid`, never ints.

## 2. Scaffold the package

Create `internal/actions/<pkg>/<pkg>.go` (+ `<pkg>_test.go`) implementing `common.Action`:

```go
func New(events chan common.ResultData, data common.ActionArgs) (common.Action, error)
func (t *x) Execute(executionId string, inputs []any)  // blocks for triggers; runs once for actions
func (t *x) Stop() error                               // idempotent (sync.Once), ends Execute
func (t *x) GetArgs() common.ActionArgs
```

Rules:

- **Validate in `New`, fail at load.** Bad args (bad cron expr, bad method, bad timezone) must return an error so `AddWorkflow` logs `Cannot create new trigger: …` instead of silently never firing. Treat nil `Args` as defaults (see `cron.argsToMap`), since `common.ArgsToStringMap` rejects nil.
- **Normalize args with `common.ArgsToStringMap`** (accepts `map[string]string`/`map[string]any`/any string-keyed map). Stringify values with `common.StringifyArgValue`.
- **Triggers emit `common.ResultData{ActionID, ActionType, ActionName, Payload}`** on the `events` chan. `TriggerManager`/`ActionManager.Run` stamp `CreatedAt`/`ResultID`. Keep `Execute` blocking for triggers (`<-stop`, timer loops); never busy-loop (see cron's `time.NewTimer` pattern).
- **Payload design is the conditions API.** Routing evaluates `$result.<path>` (own payload) and `$args.<path>`; `$input.` (upstream) is **rejected** in conditions. Shape payloads as plain maps (`type`, `trigger`, domain fields) so `$result.body.event`-style routing works. Document every field.
- **ExecutorID semantics:** leave empty for server-local results (`Run` stamps `wm.executorID` → downstream runs in-process under `-e`). A client id means remote handoff: owned nexts return to the reporter, foreign nexts become pending assignments — a broadcast node "owned" by a pseudo-client stalls forever, so server ingress must stay empty.

## 3. Templates and `$input` (mandatory, shared engine only)

- Resolve `$input.<path>` via `common.ResolveRefPath` / `common.InputLookup` (against `inputs []any`, usually a parent `ResultData`); expand `{{ … }}` via `common.EvalTemplate` / `ExpandTemplates` / `ExpandJSONTemplates` / `SplitTemplates`. **Never copy-paste a private resolver.**
- Split `{{…}}` regions from bare `$input.` refs (`common.SplitTemplates`) so adjacent forms (`$input.now{{ext}}`) don't consume each other. Reject `$result.`/`$args.` in args (own result doesn't exist at resolution time).
- New filters/expression syntax belong in `common/template.go` (`applyTemplateFilter`) **only**, covered by `common/template_test.go`, and the user-facing table in `docs/actions/set-action.md` must be updated with them. Current: `upper`, `lower`, `trim`, `trimPrefix`, `trimSuffix`, `replace`, `default`.

## 4. Register everywhere (or it doesn't exist)

1. `internal/actions/actions.go` `AddAction` (actions) or `internal/actions/trigger.go` `AddTrigger` (triggers) — switch on `action_name`.
2. Client dispatch in `internal/client/client.go`: `newClientAction` / `newClientTrigger`. The client binary **must never link** `internal/server`, `internal/database`, `ent`, `echo`/`huma`, or `templ` — keep new code to `common` + `internal/actions`.
3. `common/providers.go` `core` list (credential-free built-ins).
4. `cmd/web/dashboard.templ` `DESIGNER_CATALOG` picker entry. templ gotchas (all bitten before): never a literal `{{`/`}}` inside `<script>` (build brace strings via `String.fromCharCode(123, …)`); never `<template x-for>` inside `<svg>` (render edges via `x-html`); canvas drag listeners detach on `pointerup` **and** `pointercancel` with a dead-zone.

## 5. Tenant isolation & RBAC (every change)

- New ent objects holding per-tenant data **must** carry `tenant_id`; children without it are only OK if every access path re-checks the parent's tenant.
- Derive tenants from the session/client token (`scopedTenant`, `normalizeTenantID` — empty == `default`, never `!= "default"` carve-outs); ignore/override caller-supplied `tenant_id`. `default` is ordinary, never a wildcard.
- Reads: session required (viewer+ min); collections omit foreign rows; single foreign objects → `404` (never `403`); anonymous → `401` once users exist. Public-by-design endpoints (like webhooks) are the exception — document them and bind results to the workflow tenant.
- Writes: verify ownership **before** mutating; global namespaces (workflow ids) need a cross-tenant hijack guard (403); suspended tenants fail closed everywhere.
- Audit every mutation with the session user as actor. Extend `internal/server/tenant_isolation_test.go` and update `docs/tenants.md` + `docs/rbac.md` concurrently.

## 6. Docs, examples, tests (part of the change)

- `docs/actions/<name>.md`: operations, full argument table (key/type/default/description), edge cases, JSON workflow examples. Index it in `docs/README.md`; touch `docs/workflows.md`, `providers.md`, `architecture.md`, `client-mode.md` / `server-mode.md`, `credentials.md`, `development.md` where they enumerate nodes.
- `examples/workflow-<name>.json` using realistic ids.
- Tests: package unit tests (args validation, payload shape, method/event filtering); `AddTrigger`/`AddAction` dispatch + invalid-config rejection in `internal/actions/*_test.go`; example-load test reading the example JSON; client dispatch case in `internal/client/client_test.go`; server handler tests (payload, routing, 4xx paths, disabled/suspended) + isolation extension if it adds an endpoint or entity.

## 7. Verify (order matters — codegen first)

- `templ` generator must equal the runtime in `go.mod` (`v0.3.1020`, see Makefile); never `templ@latest`. After any `.templ` edit: `templ generate -path .`, then regenerate `output.css` (`make build`, or `./tailwindcss -i cmd/web/assets/css/input.css -o cmd/web/assets/css/output.css`) and commit it — `output.css` **is** committed, `*_templ.go` is gitignored.
- `make lint` (mirrors CI) then `make test` (generate → `go build ./...` → `go test ./...`). `ent/` regenerates via `make ent` after schema changes.
- Git: fresh `main` + `git pull`, one `feat/`-`fix/`-`chore/` branch per task, PR via `gh pr create` (use `Fixes #N`), merge only on green CI, never push to `main`.
