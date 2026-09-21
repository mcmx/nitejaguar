# Nitejaguar Documentation

Welcome to the official documentation for **Nitejaguar**, a lightweight and powerful workflow automation system written in Go.

## Table of Contents

### Core Guides
- [Getting Started](./getting-started.md) — Quick installation, building, and running your first workflow POC.
- [Architecture](./architecture.md) — System layering, package organization, and design principles.
- [Server Mode](./server-mode.md) — Running the server orchestrator, Huma OpenAPI documentation, and Echo web dashboard.
- [Client Mode](./client-mode.md) — Running remote clients/runners, task polling, and local execution.
- [Workflows](./workflows.md) — Workflow JSON schemas, triggers, actions, conditions, dependencies, import (`-i`), and clone (`-c`) operations.
- [Development Guide](./development.md) — Building, codegen (`templ` & `ent`), testing (`make test`), and linting (`make lint`).

### Actions & Triggers Reference
- [File Change Trigger (`filechange`)](./actions/filechange-trigger.md)
- [File Action (`file`)](./actions/file-action.md)
- [Datetime Action (`datetime`)](./actions/datetime-action.md)
- [Wait Action (`wait`)](./actions/wait-action.md)
