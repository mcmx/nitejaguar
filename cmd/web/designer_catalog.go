package web

import "encoding/json"

// DesignerFieldOption is one choice in a select/multiselect field.
type DesignerFieldOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// DesignerField describes one typed argument in the designer inspector.
// Type is one of: string, number, integer, boolean, select,
// multiselect, textarea, json.
type DesignerField struct {
	Key         string                `json:"key"`
	Label       string                `json:"label"`
	Type        string                `json:"type"`
	Default     any                   `json:"default,omitempty"`
	Placeholder string                `json:"placeholder,omitempty"`
	Help        string                `json:"help,omitempty"`
	Required    bool                  `json:"required,omitempty"`
	Options     []DesignerFieldOption `json:"options,omitempty"`
	Rows        int                   `json:"rows,omitempty"`
}

// DesignerCatalogEntry is one picker card plus its typed arg schema.
type DesignerCatalogEntry struct {
	ActionType string          `json:"action_type"`
	ActionName string          `json:"action_name"`
	Icon       string          `json:"icon"`
	Label      string          `json:"label"`
	Desc       string          `json:"desc"`
	Args       map[string]any  `json:"args"`
	Fields     []DesignerField `json:"fields"`
}

func opt(v string) DesignerFieldOption { return DesignerFieldOption{Value: v, Label: v} }

func opts(vals ...string) []DesignerFieldOption {
	out := make([]DesignerFieldOption, 0, len(vals))
	for _, v := range vals {
		out = append(out, opt(v))
	}
	return out
}

// DesignerCatalog is the single visual source of truth for the picker
// and the typed inspector form. Args hold the defaults for a new node;
// Fields drive the per-type HTML controls (number input for integers,
// checkbox for booleans, combo for finite option lists, textareas for
// JSON blobs). The JSON textarea in the inspector remains as the
// advanced fallback (extra keys, field.* dynamics, $input templates).
func DesignerCatalog() []DesignerCatalogEntry {
	return []DesignerCatalogEntry{
		{
			ActionType: "trigger", ActionName: "filechange",
			Icon: "📁", Label: "File Change",
			Desc: "Watch a directory for creates, writes, renames…",
			Args: map[string]any{"path": "/tmp", "event_type": "create"},
			Fields: []DesignerField{
				{Key: "path", Label: "Path", Type: "string", Required: true, Placeholder: "~/Downloads", Help: "Directory to watch. Supports ~ expansion."},
				{Key: "event_type", Label: "Event types", Type: "multiselect", Default: "create,write,rename,remove,chmod", Help: "One or more of create, write, rename, remove, chmod. Empty means all.", Options: opts("create", "write", "rename", "remove", "chmod")},
				{Key: "debounce_ms", Label: "Debounce (ms)", Type: "integer", Default: 500, Placeholder: "500", Help: "Groups rapid bursts. 0 disables debouncing."},
			},
		},
		{
			ActionType: "trigger", ActionName: "cron",
			Icon: "⏰", Label: "Cron",
			Desc: "Run on a cron schedule or interval, emitting now() each time it fires.",
			Args: map[string]any{"cron": "*/5 * * * *", "format": "2006-01-02 15:04:05", "run_on_start": false},
			Fields: []DesignerField{
				{Key: "cron", Label: "Cron expression", Type: "string", Default: "* * * * *", Placeholder: "0 9 * * *", Help: "5 fields (m h dom mon dow), optional leading seconds, or @every/@daily/@hourly. Ignored when interval is set."},
				{Key: "interval", Label: "Interval", Type: "string", Placeholder: "30s", Help: "Fixed Go duration (30s, 5m, 1h30m). Wins over cron when set."},
				{Key: "timezone", Label: "Timezone", Type: "string", Placeholder: "UTC", Help: "IANA name (Europe/Madrid, UTC). Empty means local time."},
				{Key: "format", Label: "Format", Type: "string", Placeholder: "RFC3339", Help: "Go time layout for datetime, or unix / unix_ms."},
				{Key: "run_on_start", Label: "Run on start", Type: "boolean", Default: false, Help: "Emit one result immediately when the trigger starts."},
			},
		},
		{
			ActionType: "trigger", ActionName: "webhook",
			Icon: "🪝", Label: "Webhook",
			Desc: "Fire on inbound HTTP at /webhook/{id}; method selects which verbs trigger.",
			Args: map[string]any{"method": "POST"},
			Fields: []DesignerField{
				{Key: "method", Label: "Method", Type: "select", Default: "ALL", Help: "ALL accepts every method. Combine several with commas in JSON (GET,POST).", Options: opts("ALL", "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE", "CONNECT")},
			},
		},
		{
			ActionType: "action", ActionName: "file",
			Icon: "📄", Label: "File",
			Desc: "Create, remove or rename files.",
			Args: map[string]any{"action": "create", "file": "/tmp/out.txt"},
			Fields: []DesignerField{
				{Key: "action", Label: "Operation", Type: "select", Required: true, Default: "create", Options: opts("create", "remove", "rename")},
				{Key: "file", Label: "File", Type: "string", Required: true, Placeholder: "/tmp/out.txt", Help: "Target path (create/remove) or source (rename). Supports $input refs and {{...}} templates."},
				{Key: "new_file", Label: "New file", Type: "string", Placeholder: "/tmp/renamed.txt", Help: "Destination path. Required for rename. Supports $input refs and {{...}} templates."},
			},
		},
		{
			ActionType: "action", ActionName: "datetime",
			Icon: "🕒", Label: "Datetime",
			Desc: "Current timestamp, formatted for downstream use.",
			Args: map[string]any{"operation": "getCurrentDate", "format": "2006-01-02", "timezone": "UTC", "output_field": "current_date"},
			Fields: []DesignerField{
				{Key: "operation", Label: "Operation", Type: "select", Default: "getCurrentDate", Options: opts("getCurrentDate")},
				{Key: "format", Label: "Format", Type: "string", Placeholder: "RFC3339", Help: "Go time layout, or unix / unix_ms. Empty means RFC3339."},
				{Key: "timezone", Label: "Timezone", Type: "string", Placeholder: "UTC", Help: "IANA name (Europe/Madrid, UTC). Empty means local time."},
				{Key: "output_field", Label: "Output field", Type: "string", Default: "datetime", Placeholder: "datetime", Help: "Payload field echoing the value for $input.<field> downstream."},
			},
		},
		{
			ActionType: "action", ActionName: "wait",
			Icon: "⏳", Label: "Wait",
			Desc: "Pause the execution before continuing.",
			Args: map[string]any{"duration": "1.5 minutes"},
			Fields: []DesignerField{
				{Key: "duration", Label: "Duration", Type: "string", Required: true, Placeholder: "1.5 minutes", Help: "Bare numbers mean seconds. Combines parts: 1h 20m, 2 weeks. Supports $input.<field>."},
			},
		},
		{
			ActionType: "action", ActionName: "transfer",
			Icon: "📦", Label: "Transfer",
			Desc: "Copy a file, locally or to another client.",
			Args: map[string]any{"file": "$input.file", "destination_file": "~/received/{{base}}"},
			Fields: []DesignerField{
				{Key: "file", Label: "Source file", Type: "string", Required: true, Placeholder: "$input.file", Help: "Source path. Supports $input refs and {{...}} templates."},
				{Key: "destination_file", Label: "Destination file", Type: "string", Required: true, Placeholder: "~/received/{{base}}", Help: "Single full destination path (alias new_file). Supports $input refs and {{...}} templates."},
				{Key: "destination_client", Label: "Destination client", Type: "string", Placeholder: "empty = local copy", Help: "Receiver client id. Empty copies locally."},
				{Key: "destination_client_tags", Label: "Destination tags", Type: "string", Placeholder: "gpu, edge", Help: "Receiver tags alternative, comma separated."},
				{Key: "permissions", Label: "Permissions", Type: "string", Placeholder: "0644", Help: "Octal 000-777. Empty preserves the source mode."},
			},
		},
		{
			ActionType: "action", ActionName: "set",
			Icon: "🧩", Label: "Set",
			Desc: "Shape a payload from literals and $input refs.",
			Args: map[string]any{"mode": "manual", "field.kind": "contact", "keep_only_set": false},
			Fields: []DesignerField{
				{Key: "mode", Label: "Mode", Type: "select", Default: "manual", Options: opts("manual", "json"), Help: "manual assigns fields / field.<name>; json parses the json template object."},
				{Key: "fields", Label: "Fields (JSON object)", Type: "json", Rows: 4, Placeholder: "{\"full_name\": \"$input.name\"}", Help: "Manual mode: object of assignments. field.<name> args (below in JSON) win on conflicts."},
				{Key: "json", Label: "JSON template", Type: "json", Rows: 4, Placeholder: "{\"newKey\": \"$input.name\"}", Help: "Json mode: object template parsed after $input / {{...}} resolution."},
				{Key: "keep_only_set", Label: "Keep only set fields", Type: "boolean", Default: false, Help: "True outputs only assigned fields; false merges upstream underneath."},
				{Key: "include", Label: "Include", Type: "select", Help: "Overrides keep_only_set: all merges input, none keeps only assigned.", Options: []DesignerFieldOption{{Value: "", Label: "(unset)"}, {Value: "all", Label: "all"}, {Value: "none", Label: "none"}}},
				{Key: "dot_notation", Label: "Dot notation", Type: "boolean", Default: true, Help: "Dotted names (address.city) build nested objects."},
				{Key: "ignore_type_errors", Label: "Ignore type errors", Type: "boolean", Default: false, Help: "Skip assignments that fail to resolve instead of erroring."},
			},
		},
	}
}

// DesignerCatalogJSON renders the catalog for the designer <script> tag.
// encoding/json escapes <, > and & so the payload cannot break out.
func DesignerCatalogJSON() string {
	data, err := json.Marshal(DesignerCatalog())
	if err != nil {
		return "[]"
	}
	return string(data)
}
