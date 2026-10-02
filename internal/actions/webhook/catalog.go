package webhook

import "github.com/mcmx/nitejaguar/common"

// CatalogEntry describes the webhook trigger for the visual designer
// picker and typed inspector form. Args are the new-node defaults.
func CatalogEntry() common.DesignerCatalogEntry {
	return common.DesignerCatalogEntry{
		ActionType: "trigger", ActionName: "webhook",
		Icon: "🪝", Label: "Webhook",
		Desc: "Fire on inbound HTTP at /webhook/{id}; method selects which verbs trigger.",
		Args: map[string]any{"method": "POST"},
		Fields: []common.DesignerField{
			{Key: "method", Label: "Method", Type: "select", Default: "ALL", Help: "ALL accepts every method. Combine several with commas in JSON (GET,POST).", Options: common.FieldOptions("ALL", "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE", "CONNECT")},
		},
	}
}
