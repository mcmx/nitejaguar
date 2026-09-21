# Client Mode (Remote Runner)

Nitejaguar supports distributed execution where remote **Clients** (runners) register with a central server, poll for assigned task nodes (`filechange`, `file`), execute them locally, and report execution results back.

## Starting a Client

```bash
./nitejaguar client --server http://127.0.0.1:8080 --name worker-node-1
```

### CLI Flags & Environment Variables

| Flag | Env Variable | Default | Description |
|---|---|---|---|
| `--server` | `NITEJAGUAR_SERVER` | `http://127.0.0.1:8080` | URL of the Nitejaguar server |
| `--name` | `NITEJAGUAR_CLIENT_NAME` | `default` | Human-readable client name |
| `--client-id` | `NITEJAGUAR_CLIENT_ID` | (auto-generated) | Persistent client ID for reconnection |
| `--token` | `NITEJAGUAR_TOKEN` | `""` | Authentication token for secured servers |

## Execution Lifecycle

1. **Registration**: On startup, the client registers itself with the server (or reconnects using an existing `--client-id`).
2. **Polling**: The client periodically polls the server for assigned workflow nodes.
3. **Local Execution**: Assigned nodes (such as file watches or file transformations) execute locally against the client's file system (e.g., expanding `~` paths locally).
4. **Result Reporting**: Success or error results are transmitted back to the server and logged in `./results/`.
5. **Resilience**: The client implements exponential backoff on connection/polling failures and stops cleanly on SIGINT / context cancellation.
