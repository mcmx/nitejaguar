# Nitejaguar Documentation

Welcome to the official documentation for **Nitejaguar**, a lightweight and powerful workflow automation system written in Go.

## Table of Contents

### Core Guides
- [Getting Started](./getting-started.md) — Quick installation, building, and running your first workflow POC.
- [Architecture](./architecture.md) — System layering, package organization, and design principles.
- [Server Mode](./server-mode.md) — Running the server orchestrator, Huma OpenAPI documentation, and Echo web dashboard.
- [Client Mode](./client-mode.md) — Running remote clients/runners, task polling, and local execution.
- [Workflows](./workflows.md) — Workflow JSON schemas, triggers, actions, conditions, dependencies, import (`server -i`, `client workflow import`), and clone (`server -c`, `client workflow clone`) operations.
- [Development Guide](./development.md) — Building, codegen (`templ` & `ent`), testing (`make test`), and linting (`make lint`).
- [Credentials](./credentials.md) — Credential storage (encrypted at rest), scopes & resolution (`user > group > tenant`), just-in-time fetch, and `credential_ref` workflow usage.
- [Providers](./providers.md) — Provider collections, the shared-credential-type rule, and the registry.
- [RBAC & Auth](./rbac.md) — Users, roles (`admin > operator > viewer`), login sessions, gated management endpoints, website login, and the audit trail.

### Actions & Triggers Reference
- [File Change Trigger (`filechange`)](./actions/filechange-trigger.md)
- [File Action (`file`)](./actions/file-action.md)
- [Datetime Action (`datetime`)](./actions/datetime-action.md)
- [Wait Action (`wait`)](./actions/wait-action.md)
- [Transfer Action (`transfer`)](./actions/transfer-action.md)
