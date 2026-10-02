package fileaction

import "github.com/mcmx/nitejaguar/common"

// CatalogEntry describes the file action for the visual designer picker
// and typed inspector form. Args are the new-node defaults.
func CatalogEntry() common.DesignerCatalogEntry {
	return common.DesignerCatalogEntry{
		ActionType: "action", ActionName: "file",
		Icon: "📄", Label: "File",
		Desc: "Create, remove or rename files.",
		Args: map[string]any{"action": "create", "file": "/tmp/out.txt"},
		Fields: []common.DesignerField{
			{Key: "action", Label: "Operation", Type: "select", Required: true, Default: "create", Options: common.FieldOptions("create", "remove", "rename")},
			{Key: "file", Label: "File", Type: "string", Required: true, Placeholder: "/tmp/out.txt", Help: "Target path (create/remove) or source (rename). Supports $input refs and {{...}} templates."},
			{Key: "new_file", Label: "New file", Type: "string", Placeholder: "/tmp/renamed.txt", Help: "Destination path. Required for rename. Supports $input refs and {{...}} templates."},
		},
	}
}
