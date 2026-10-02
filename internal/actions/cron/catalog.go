package cron

import "github.com/mcmx/nitejaguar/common"

// CatalogEntry describes the cron trigger for the visual designer picker
// and typed inspector form. Args are the new-node defaults.
func CatalogEntry() common.DesignerCatalogEntry {
	return common.DesignerCatalogEntry{
		ActionType: "trigger", ActionName: "cron",
		Icon: "⏰", Label: "Cron",
		Desc: "Run on a cron schedule or interval, emitting now() each time it fires.",
		Args: map[string]any{"cron": "*/5 * * * *", "format": "2006-01-02 15:04:05", "run_on_start": false},
		Fields: []common.DesignerField{
			{Key: "cron", Label: "Cron expression", Type: "string", Default: "* * * * *", Placeholder: "0 9 * * *", Help: "5 fields (m h dom mon dow), optional leading seconds, or @every/@daily/@hourly. Ignored when interval is set."},
			{Key: "interval", Label: "Interval", Type: "string", Placeholder: "30s", Help: "Fixed Go duration (30s, 5m, 1h30m). Wins over cron when set."},
			{Key: "timezone", Label: "Timezone", Type: "string", Placeholder: "UTC", Help: "IANA name (Europe/Madrid, UTC). Empty means local time."},
			{Key: "format", Label: "Format", Type: "string", Placeholder: "RFC3339", Help: "Go time layout for datetime, or unix / unix_ms."},
			{Key: "run_on_start", Label: "Run on start", Type: "boolean", Default: false, Help: "Emit one result immediately when the trigger starts."},
		},
	}
}
