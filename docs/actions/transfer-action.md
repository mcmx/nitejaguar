# Transfer Action (`transfer`)

The `transfer` action copies a file byte-for-byte to a destination path with cross-OS safe semantics. A node with no destination (or addressed to self) copies locally; a node addressed to another client (`destination_client` / `destination_client_tags`) delivers over WebRTC P2P first with the server relay as fallback.

## What It Can Do

- **Copy**: reads `file` (source) and writes `destination_file` verbatim (no line-ending conversion), reporting `bytes` + `sha256`.
- **Automatic Directory Creation**: missing destination parents are created (`os.MkdirAll` `0755`).
- **Dynamic Argument Templating**: literal strings, `$input.<path>` (upstream payload), and `{{file}}` / `{{base}}` / `{{ext}}` / `{{stem}}` derived from the source file. `~` expands on the executing side.
- **Collision Safety**: existing destination → `Type: "error"`, source untouched, no partial file.
- **Permissions**: optional Unix octal `permissions` (`"0644"`, `"0755"`); omitted = preserve source mode (fallback `0644`). Best-effort on Windows (warn, still succeed).
- **Cross-OS guard**: destination basenames with Windows-illegal `<>:"|?*` or reserved names (`CON`, `NUL`, `COM1-9`, `LPT1-9`, ...) are rejected on every OS.

## Arguments

| Argument | Type | Default | Description |
|---|---|---|---|
| `file` | string | `""` | Source file path (required) |
| `destination_file` | string | `""` | Destination file path, single full path (required; alias `new_file`) |
| `destination_client` | string | `""` | Receiver client id (optional, reserved for distributed routing; echoed) |
| `destination_client_tags` | string | `""` | Receiver tags alternative (optional, reserved; echoed) |
| `permissions` | string | `""` | Octal mode `000`-`777` (e.g. `"0644"`); empty preserves source |

## How to Use

```json
{
  "id": "action_01h...",
  "action_type": "action",
  "action_name": "transfer",
  "arguments": {
    "file": "$input.file",
    "destination_file": "~/received/{{base}}",
    "destination_client": "client_01h...",
    "permissions": "0644"
  },
  "conditions": {"nexts": []},
  "dependencies": ["trigger_01h..."]
}
```

Use `~`-relative or templated paths for Linux↔Windows workflows; absolute `/tmp/...` vs `C:\...` paths do not port.

## Distributed delivery (P2P + relay)

The sender runner intercepts transfer nodes addressed elsewhere: it reads the source, opens a `transfer_` session (`POST /api/transfers/init`, audited as `transfer.init`), attempts a WebRTC DataChannel delivery via server signaling (`POST/GET /api/transfers/{id}/signal`, audited as `transfer.signal`), and falls back to chunked relay upload (`POST /api/transfers/{id}/chunks`, 60KiB chunks, 32MiB cap). The sender's result (with `transfer_id`, `bytes`, `sha256`, `via: p2p|relay`) advances the workflow — downstream `nexts` run on the sender.

Receivers poll `GET /api/clients/{id}/transfers/pending`, answer P2P offers first, otherwise download the relay (`GET /api/transfers/{id}/chunks`), write with the same safety rules (no overwrite, `MkdirAll`, basename guard, permissions), and complete (`POST /api/transfers/{id}/complete`, audited as `transfer.complete`, sha256-verified). Receivers post no workflow result.

- Control plane always goes through the server; sessions, signals, and relay are strictly tenant-isolated (sender-only upload, receiver-only complete).
- Clients advertise P2P capability via heartbeat `dial_info` (visible in `GET /api/clients`); host-candidate WebRTC connects mutually reachable peers, everyone else uses the relay.
- `permissions` applies on the receiver (explicit octal honored, best-effort on Windows); empty means the receiver default (`0644`) for remote delivery (local copies preserve the source mode instead).
