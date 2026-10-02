package datetime

import "github.com/mcmx/nitejaguar/common"

// CatalogEntry describes the datetime action for the visual designer
// picker and typed inspector form. Args are the new-node defaults.
func CatalogEntry() common.DesignerCatalogEntry {
	return common.DesignerCatalogEntry{
		ActionType: "action", ActionName: "datetime",
		Icon: "🕒", Label: "Datetime",
		Desc: "Current timestamp, formatted for downstream use.",
		Args: map[string]any{"operation": "getCurrentDate", "format": "2006-01-02", "timezone": "UTC", "output_field": "current_date"},
		Fields: []common.DesignerField{
			{Key: "operation", Label: "Operation", Type: "select", Default: "getCurrentDate", Options: common.FieldOptions("getCurrentDate")},
			{Key: "format", Label: "Format", Type: "string", Placeholder: "RFC3339", Help: "Go time layout, or unix / unix_ms. Empty means RFC3339."},
			{Key: "timezone", Label: "Timezone", Type: "string", Placeholder: "UTC", Help: "IANA name (Europe/Madrid, UTC). Empty means local time."},
			{Key: "output_field", Label: "Output field", Type: "string", Default: "datetime", Placeholder: "datetime", Help: "Payload field echoing the value for $input.<field> downstream."},
		},
	}
}
