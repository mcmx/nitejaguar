# Server Mode

The Nitejaguar server acts as the central orchestrator, managing workflows, serving the Web UI, exposing the OpenAPI REST API, and coordinating remote clients.

## Configuration

Server configuration relies on environment variables (loaded via `godotenv` from `.env`):

- `PORT` — HTTP server port (defaults to `8080`).
- `DB_URL` — Database connection string (e.g., `file:./test.db?_fk=1&cache=shared`). If unset, falls back to in-memory SQLite.

## Starting the Server

```bash
./nitejaguar server [flags]
```

### Key Flags
- `-e, --enable-actions` — Enable local execution of actions and triggers on the server.
- `-i, --import string` — Import a workflow JSON file verbatim (upsert; IDs and names untouched).
- `-c, --clone string` — Clone a workflow JSON file with fresh TypeIDs (`workflow_`, `trigger_`, `action_`), rewriting dependency edges.

## Web Dashboard & API

- **Web Dashboard**: Built with Go `templ`, HTMX, and Tailwind CSS (`cmd/web/`). Accessible at `http://localhost:8080`.
- **OpenAPI Docs**: Built on Huma v2. Interactive Swagger / Redoc documentation available at `http://localhost:8080/docs`.
- **WebSocket**: Real-time event streaming at `/websocket`.
- **Artifacts**:
  - Logs: `./log/server.log`
  - Results: `./results/`
