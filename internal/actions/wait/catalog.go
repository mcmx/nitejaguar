package wait

import "github.com/mcmx/nitejaguar/common"

// CatalogEntry describes the wait action for the visual designer picker
// and typed inspector form. Args are the new-node defaults.
func CatalogEntry() common.DesignerCatalogEntry {
	return common.DesignerCatalogEntry{
		ActionType: "action", ActionName: "wait",
		Icon: "⏳", Label: "Wait",
		Desc: "Pause the execution before continuing.",
		Args: map[string]any{"duration": "1.5 minutes"},
		Fields: []common.DesignerField{
			{Key: "duration", Label: "Duration", Type: "string", Required: true, Placeholder: "1.5 minutes", Help: "Bare numbers mean seconds. Combines parts: 1h 20m, 2 weeks. Supports $input.<field>."},
		},
	}
}
