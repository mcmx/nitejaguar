# Providers (= Ansible Collections)

A **provider is a collection**: a named group of actions that share
exactly one credential type. Strict rule — every action in a collection
uses the collection's type, no per-action overrides.

## Registry

The source of truth is `Providers()` in `common/providers.go`:

| Collection | Credential type | Actions |
|---|---|---|
| `core` | — (none) | `file`, `datetime`, `wait`, `filechange` trigger |
| `aws` | `aws` | `ec2`, `s3` (placeholders — executables ship later) |

Helpers: `ProviderForAction`, `CredentialTypeForAction`,
`KnownCredentialTypes` (generic families `generic`, `token`,
`username_password`, `ssh_key` plus each collection's declared type).
`SupportedCredentialTypes` / `RequiredCredentialTypes` in
`internal/actions/actions.go` delegate to the registry.

## Rules

- One canonical `action_name` per action (category-free, per AGENTS.md).
- Credential types are provider-declared, never hardcoded.
- The legacy `s3` credential type is rejected at creation; S3 uses `aws`.
- Fetch-time enforcement: a collection-typed node only receives
  credentials of its collection's type (fail closed, `403`); `core`
  nodes and unknown/node-less fetches impose no constraint. See
  [Credentials](./credentials.md#type-enforcement).

## Adding a collection

1. Add the entry to `Providers()` with its single credential type and
   action names (executables can follow in later slices).
2. Creation validation and fetch enforcement pick it up automatically.
3. Document the actions under `docs/actions/` once implemented.
