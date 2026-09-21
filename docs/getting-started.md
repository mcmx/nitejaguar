# Getting Started

This guide walks you through setting up Nitejaguar for development and running the included proof-of-concept (POC) workflow.

## Prerequisites

- **Go 1.26+** (matching `go.mod`)
- **Templ** (`v0.3.1020`, pinned runtime version)
- **Golangci-lint** (for linting)

## Building the Project

Nitejaguar uses a `Makefile` to automate code generation (`ent`, `templ`, `tailwindcss`) and building:

```bash
make build
```

To run tests and linting:

```bash
make test
make lint
```

---

## Running in Server Mode

1. Create a `.env` file in the project root:
   ```env
   PORT=8080
   DB_URL=file:./test.db?_fk=1&cache=shared
   ```
2. Start the server (with `-e` to enable local action execution):
   ```bash
   make run
   # Or directly: ./nitejaguar server -e
   ```
3. Open your browser at `http://localhost:8080` for the Web Dashboard, or `http://localhost:8080/docs` for the OpenAPI documentation.

---

## Running the Downloads PDF Rename POC

Nitejaguar includes an end-to-end workflow example (`examples/workflow-poc-downloads.json`) that watches `~/Downloads` for new PDFs, debounces them, and renames them to `stem-YYYYMMDD.pdf`.

### 1. Server-Side Execution (Local Actions)
Import and run the workflow on the server:
```bash
./nitejaguar server -i examples/workflow-poc-downloads.json -e
```

### 2. Client-Side Execution (Remote Runner)
Alternatively, start the server without `-e`, and run a separate client instance:

**Terminal 1 (Server):**
```bash
./nitejaguar server -i examples/workflow-poc-downloads.json
```

**Terminal 2 (Client / Runner):**
```bash
./nitejaguar client --server http://127.0.0.1:8080 --name downloads-client
```

Copy a PDF into `~/Downloads` to see it processed and renamed automatically. Check `./results/` for execution outputs.
