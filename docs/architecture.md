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

1. **Workflows Definition**: Stored as JSON defining trigger and action nodes connected via dependencies (`conditions.nexts`).
2. **Execution**:
   - **Standalone / Server Mode**: Trigger nodes emit events; the workflow engine evaluates dependencies and triggers dependent action nodes.
   - **Client/Runner Mode**: The server exposes tasks/assignments; remote clients poll the server, execute assigned tasks locally, and report results back.
