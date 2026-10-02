package filechange

import "github.com/mcmx/nitejaguar/common"

// CatalogEntry describes the filechange trigger for the visual designer
// picker and typed inspector form. Args are the new-node defaults.
func CatalogEntry() common.DesignerCatalogEntry {
	return common.DesignerCatalogEntry{
		ActionType: "trigger", ActionName: "filechange",
		Icon: "📁", Label: "File Change",
		Desc: "Watch a directory for creates, writes, renames…",
		Args: map[string]any{"path": "/tmp", "event_type": "create"},
		Fields: []common.DesignerField{
			{Key: "path", Label: "Path", Type: "string", Required: true, Placeholder: "~/Downloads", Help: "Directory to watch. Supports ~ expansion."},
			{Key: "event_type", Label: "Event types", Type: "multiselect", Default: "create,write,rename,remove,chmod", Help: "One or more of create, write, rename, remove, chmod. Empty means all.", Options: common.FieldOptions("create", "write", "rename", "remove", "chmod")},
			{Key: "debounce_ms", Label: "Debounce (ms)", Type: "integer", Default: 500, Placeholder: "500", Help: "Groups rapid bursts. 0 disables debouncing."},
		},
	}
}
