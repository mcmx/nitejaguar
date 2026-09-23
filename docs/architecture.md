# Architecture & Design

Nitejaguar follows a clean, modular Go architecture designed for standalone deployment or distributed client/server execution.

## Package Layering

- **`cmd/`** — CLI entrypoints using Cobra (`cmd/api`, `cmd/server.go`, `cmd/client.go`, `cmd/web/`).
- **`internal/server/`** — HTTP routing, Huma OpenAPI integration, Echo middleware, and REST/WebSocket endpoints.
- **`internal/database/`** — Ent ORM integration and SQLite database management (`ent/`).
- **`internal/workflow/`** — Workflow engine handling parsing, graph dependencies, conditions, import, and clone operations.
- **`internal/actions/`** — Action and trigger execution managers, hosting individual plugin packages (`filechange/`, `fileaction/`, `datetime/`).
- **`internal/client/`** — Remote client registration, polling loop, and task execution runner.
- **`common/`** — Shared data structures (`Action`, `ActionArgs`, `ResultData`).

## Data Flow

1. **Workflows Definition**: Stored as JSON defining trigger and action nodes connected via dependencies (`conditions.nexts`). Each workflow carries `default_client`/`default_client_tags`; each node may override with `client`/`client_tags`.
2. **Execution**:
   - **Standalone / Server Mode**: Trigger nodes emit events; the workflow engine evaluates dependencies and triggers dependent action nodes.
   - **Client/Runner Mode**: The server exposes task definitions plus persisted pending handoffs; remote clients poll the server, execute assigned definitions and owned pending nodes locally, and report results back.
3. **Distributed dispatch**: `IngestResult` persists one pending assignment per downstream `next` (idempotent per workflow/execution/node) and marks the reporting node done. `POST /api/results` returns only caller-owned nexts; foreign nexts are picked up via the `pending` array in `GET /api/clients/{id}/assignments` with tenant isolation and workflow-default resolution.
