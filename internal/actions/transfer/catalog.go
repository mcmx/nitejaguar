package transfer

import "github.com/mcmx/nitejaguar/common"

// CatalogEntry describes the transfer action for the visual designer
// picker and typed inspector form. Args are the new-node defaults.
func CatalogEntry() common.DesignerCatalogEntry {
	return common.DesignerCatalogEntry{
		ActionType: "action", ActionName: "transfer",
		Icon: "📦", Label: "Transfer",
		Desc: "Copy a file, locally or to another client.",
		Args: map[string]any{"file": "$input.file", "destination_file": "~/received/{{base}}"},
		Fields: []common.DesignerField{
			{Key: "file", Label: "Source file", Type: "string", Required: true, Placeholder: "$input.file", Help: "Source path. Supports $input refs and {{...}} templates."},
			{Key: "destination_file", Label: "Destination file", Type: "string", Required: true, Placeholder: "~/received/{{base}}", Help: "Single full destination path (alias new_file). Supports $input refs and {{...}} templates."},
			{Key: "destination_client", Label: "Destination client", Type: "string", Placeholder: "empty = local copy", Help: "Receiver client id. Empty copies locally."},
			{Key: "destination_client_tags", Label: "Destination tags", Type: "string", Placeholder: "gpu, edge", Help: "Receiver tags alternative, comma separated."},
			{Key: "permissions", Label: "Permissions", Type: "string", Placeholder: "0644", Help: "Octal 000-777. Empty preserves the source mode."},
		},
	}
}
