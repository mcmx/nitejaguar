# Set Action (`set`)

The `set` action builds a new data payload from fixed values and upstream references. It can set new fields as well as overwrite existing ones, making it the shaping step before nodes that expect specific input data (file paths, notifications, downstream actions).

## Modes (`mode`)

| Mode | Description |
|---|---|
| `manual` (default) | Assign individual fields via the `fields` object and/or `field.<name>` args. |
| `json` | Parse the `json` template string (after reference resolution) as a JSON object. |

`manual_mapping` is accepted as an alias of `manual`; `json_output` as an alias of `json`.

## Arguments

| Argument | Type | Default | Description |
|---|---|---|---|
| `mode` | string | `manual` | `manual` or `json` (aliases above) |
| `fields` | object or JSON string | `""` | Field assignments for `manual` mode (`values` / `assignments` are aliases). String values may embed `$input.<path>` references |
| `field.<name>` | string or value | — | Single field assignment for `manual` mode; merged over `fields` on conflicts |
| `json` | JSON string | `""` | Object template for `json` mode (`json_output` / `output` are aliases) |
| `keep_only_set` | bool string | `false` | `true` outputs only the assigned fields; `false` merges the upstream payload underneath (assigned fields win). `keep_only` / `keep` are aliases |
| `include` | string | — | Overrides `keep_only_set`: `all` merges upstream input, `none` keeps only assigned fields |
| `dot_notation` | bool string | `true` | A field name with dots (`address.city`) builds nested objects; `false` keeps the literal key. `support_dot_notation` is an alias |
| `ignore_type_errors` | bool string | `false` | `true` skips assignments that fail to resolve instead of emitting an error result. `ignore_errors` is an alias |

## Value resolution

- `$input.<path>` resolves against the upstream result payload (dot-separated keys, `json`-tag-aware struct fields, `[index]` suffixes — same as the other core actions). `$result.` / `$args.` are rejected.
- A value that is exactly one reference keeps its native type (number, boolean, object, array, null). References embedded in a larger string interpolate as strings.
- In `json` mode, bare references keep native types (`"array": [$input.id]`) while quoted references become strings (`"name": "$input.name"`).
- A reference with no upstream input available is an error result.

## Templates (`{{ ... }}`)

Every value additionally accepts `{{ ... }}` placeholders holding a `$input.<path>` reference (or a quoted literal) plus an optional `|` filter chain. Templates expand before bare `$input.` references, so both can mix in one value. All template syntax lives in plain workflow JSON args, so it is fully user-editable.

| Filter | Example | Result |
|---|---|---|
| `upper` (`uppercase`) | `{{ $input.name \| upper }}` | `ADA` |
| `lower` (`lowercase`) | `{{ $input.name \| lower }}` | `ada` |
| `trim` | `{{ $input.name \| trim }}` | surrounding whitespace removed (`trim:" ."` trims a custom cutset) |
| `trimPrefix` | `{{ $input.p \| trimPrefix:/tmp/ }}` | leading prefix stripped |
| `trimSuffix` | `{{ $input.p \| trimSuffix:.bkp }}` | trailing suffix stripped |
| `replace` | `{{ $input.s \| replace:a:b }}` | `ReplaceAll` (`old:new`; quote args containing `:` or `\|`) |
| `default` | `{{ $input.missing \| default:n/a }}` | fallback for missing/null values (later filters still apply) |

Concat works by placing templates next to literal text:

```json
{"arguments": {"field.backup": "{{ $input.file }}.bkp"}}
```

`/tmp/report.pdf` → `/tmp/report.pdf.bkp`.

Notes:

- A value that is exactly one filter-free template keeps the referenced native type (`"{{ $input.count }}"` with `count: 7` stays a number); filtered or embedded templates always produce strings.
- Filters chain left to right (`{{ $input.name | trim | upper }}`) and operate Unicode-aware (`upper`/`lower`).
- Unknown filters, empty templates, and unclosed `{{` are error results (or skipped assignments with `ignore_type_errors`).

## Result payload

Success payloads are flat: `{"type":"success","mode":"manual","keep_only_set":false, ...fields}`. Assigned fields overlay the merged input (nested objects merge recursively). `type` is reserved and always `success` on success; error payloads look like `{"type":"error","mode":"...","result":"..."}`.

## Edge cases

- At least one assigned field is required; an empty assignment set is an error result.
- `field.` entries win over `fields` keys on name conflicts.
- Dot-notation collisions with a non-object value (e.g. setting both `a` and `a.b`) are error results unless `ignore_type_errors` is set.
- `json` templates must parse to a top-level JSON object after resolution.
- Non-object upstream payloads (or no upstream input) with `keep_only_set=false` behave like `keep_only_set=true`.

## Examples

Manual mapping with upstream references:

```json
{
  "id": "action_set_01h...",
  "action_type": "action",
  "action_name": "set",
  "arguments": {
    "mode": "manual",
    "fields": "{\"full_name\": \"$input.name\", \"country\": \"$input.country\"}",
    "field.kind": "contact",
    "keep_only_set": "false"
  },
  "conditions": {"nexts": []},
  "dependencies": ["trigger_01h..."]
}
```

Dot notation builds nested objects (`number.one: 20` → `{"number": {"one": 20}}`):

```json
{
  "id": "action_set_01h...",
  "action_type": "action",
  "action_name": "set",
  "arguments": {
    "field.number.one": "20",
    "keep_only_set": "true"
  },
  "conditions": {"nexts": []},
  "dependencies": ["trigger_01h..."]
}
```

JSON mode combining literals with typed references:

```json
{
  "id": "action_set_01h...",
  "action_type": "action",
  "action_name": "set",
  "arguments": {
    "mode": "json",
    "json": "{\"newKey\": \"new value\", \"array\": [$input.id, \"$input.name\"], \"object\": {\"inner\": \"$input.name\"}}",
    "keep_only_set": "true"
  },
  "conditions": {"nexts": []},
  "dependencies": ["trigger_01h..."]
}
```
