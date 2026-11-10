package workflow

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mcmx/nitejaguar/common"
)

// ResolveBusinessCalendarArgs replaces a Cron trigger's calendar reference
// with its immutable rule snapshot for the current workflow load/assignment.
func ResolveBusinessCalendarArgs(args map[string]string, tenantID string, store common.WorkflowStore) (map[string]string, error) {
	out := make(map[string]string, len(args)+2)
	for key, value := range args {
		out[key] = value
	}
	name := strings.TrimSpace(out["calendar"])
	delete(out, "calendar_weekdays")
	delete(out, "calendar_dates")
	if name == "" {
		return out, nil
	}
	lookup, ok := store.(common.BusinessCalendarLookup)
	if !ok {
		return nil, fmt.Errorf("calendar %q cannot be resolved: business calendar store unavailable", name)
	}
	def, err := lookup.LookupBusinessCalendar(tenantID, name)
	if err != nil {
		return nil, err
	}
	weekdays, err := json.Marshal(def.Weekdays)
	if err != nil {
		return nil, fmt.Errorf("failed to encode calendar weekdays: %w", err)
	}
	dates, err := json.Marshal(def.ExcludedDates)
	if err != nil {
		return nil, fmt.Errorf("failed to encode calendar dates: %w", err)
	}
	out["calendar_weekdays"] = string(weekdays)
	out["calendar_dates"] = string(dates)
	return out, nil
}
