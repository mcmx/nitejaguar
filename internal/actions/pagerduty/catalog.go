package pagerduty

import "github.com/mcmx/nitejaguar/common"

// CatalogEntry describes the pagerduty action for the visual designer picker
// and typed inspector form. Args are the new-node defaults (dispatchable:
// trigger with a placeholder routing key passes New validation). The
// routing key is a secret — production nodes should leave it empty and set
// credential_ref to a token/generic credential holding the key instead.
func CatalogEntry() common.DesignerCatalogEntry {
	return common.DesignerCatalogEntry{
		ActionType: "action", ActionName: "pagerduty",
		Icon: "🚨", Label: "PagerDuty",
		Desc: "Trigger, acknowledge, or resolve a PagerDuty incident (Events API v2).",
		Args: map[string]any{
			"routing_key":  "00000000000000000000000000000000",
			"event_action": "trigger",
			"summary":      "Workflow {{ $input.workflow }} failed: $input.result",
			"severity":     "error",
			"source":       "nitejaguar",
		},
		Fields: []common.DesignerField{
			{Key: "routing_key", Label: "Routing key", Type: "string", Placeholder: "Events API v2 integration key", Help: "Integration routing key. Secret: prefer credential_ref (token/generic) holding the key and leave this empty. Supports templates."},
			{Key: "event_action", Label: "Event action", Type: "select", Default: "trigger", Options: common.FieldOptions("trigger", "acknowledge", "resolve"), Help: "trigger opens an incident; acknowledge/resolve need the incident dedup_key."},
			{Key: "summary", Label: "Summary", Type: "textarea", Rows: 2, Placeholder: "Archive failed: $input.result", Help: "Incident summary (required for trigger). Supports $input refs and {{...}} templates."},
			{Key: "severity", Label: "Severity", Type: "select", Default: "error", Options: common.FieldOptions("critical", "error", "warning", "info"), Help: "Payload severity for trigger events."},
			{Key: "source", Label: "Source", Type: "string", Default: "nitejaguar", Placeholder: "nitejaguar", Help: "Event source shown in the incident. Supports templates."},
			{Key: "dedup_key", Label: "Dedup key", Type: "string", Placeholder: "nitejaguar-{{ $input.workflow }}-archive", Help: "Stable per-failure identity: repeats collapse into one incident instead of paging every retry. Required for acknowledge/resolve."},
		},
	}
}
