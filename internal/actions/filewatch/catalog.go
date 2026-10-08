package filewatch

import "github.com/mcmx/nitejaguar/common"

// CatalogEntry describes the filewatch trigger for the visual designer
// picker and typed inspector form. Args are the new-node defaults.
func CatalogEntry() common.DesignerCatalogEntry {
	return common.DesignerCatalogEntry{
		ActionType: "trigger", ActionName: "filewatch",
		Icon: "👀", Label: "File Watchdog",
		Desc: "Expect a file on a schedule; alert when it never arrives.",
		Args: map[string]any{"path": "/tmp", "pattern": "*", "expect_within": "30m", "event_type": "create"},
		Fields: []common.DesignerField{
			{Key: "path", Label: "Path", Type: "string", Required: true, Placeholder: "~/inbox", Help: "Directory to watch. Supports ~ expansion."},
			{Key: "pattern", Label: "Filename pattern", Type: "string", Default: "*", Placeholder: "statement-*.pdf", Help: "Glob matched against the file name. * means every file."},
			{Key: "expect_within", Label: "Expect within", Type: "string", Required: true, Default: "30m", Placeholder: "30m", Help: "Grace window (Go duration: 30s, 5m, 1h). No matching file inside it emits a missing result."},
			{Key: "interval", Label: "Interval", Type: "string", Placeholder: "24h", Help: "Fixed Go duration opening a fresh window per tick. Wins over cron when set. Empty means watch mode: windows repeat back-to-back."},
			{Key: "cron", Label: "Cron expression", Type: "string", Placeholder: "0 9 * * *", Help: "5 fields (m h dom mon dow), optional leading seconds, or @descriptors. Each fire opens a fresh window. Ignored when interval is set."},
			{Key: "timezone", Label: "Timezone", Type: "string", Placeholder: "UTC", Help: "IANA name driving cron field matching. Empty means local time."},
			{Key: "event_type", Label: "Event types", Type: "multiselect", Default: "create", Help: "One or more of create, write, rename, remove, chmod. Empty means create.", Options: common.FieldOptions("create", "write", "rename", "remove", "chmod")},
			{Key: "debounce_ms", Label: "Debounce (ms)", Type: "integer", Default: 500, Placeholder: "500", Help: "Groups rapid bursts for the same file. 0 disables debouncing."},
		},
	}
}
