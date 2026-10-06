package execaction

import "github.com/mcmx/nitejaguar/common"

// CatalogEntry describes the exec action for the visual designer picker
// and typed inspector form. Args are the new-node defaults (dispatchable:
// the placeholder command and timeout pass New validation).
func CatalogEntry() common.DesignerCatalogEntry {
	return common.DesignerCatalogEntry{
		ActionType: "action", ActionName: "exec",
		Icon: "▶️", Label: "Exec",
		Desc: "Run a local program or script; capture exit code and output.",
		Args: map[string]any{
			"command": "/usr/local/bin/job.sh",
			"timeout": "5m",
		},
		Fields: []common.DesignerField{
			{Key: "command", Label: "Command", Type: "string", Required: true, Placeholder: "/usr/local/bin/job.sh", Help: "Binary or script path. Runs directly, no shell — pipes/globs need sh -c. Supports $input refs and {{...}} templates; ~ expands."},
			{Key: "args", Label: "Args", Type: "json", Rows: 3, Placeholder: `["--date", "$input.day"]`, Help: "One argv element each as a JSON array; a single string is one element. Each element supports $input refs and {{...}} templates."},
			{Key: "workdir", Label: "Working directory", Type: "string", Placeholder: "/tmp", Help: "Working directory; ~ expands. Empty means the process default."},
			{Key: "timeout", Label: "Timeout", Type: "string", Default: "5m", Placeholder: "5m", Help: "Go duration (e.g. 30s, 5m). The process group is killed on timeout."},
			{Key: "env", Label: "Environment", Type: "json", Rows: 3, Placeholder: `{"API_KEY": "$input.key"}`, Help: "Extra environment as a JSON object, merged on top of the process env. Values support $input refs and {{...}} templates."},
		},
	}
}
