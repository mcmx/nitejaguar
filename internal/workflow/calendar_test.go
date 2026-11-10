package workflow

import (
	"fmt"
	"testing"

	"github.com/mcmx/nitejaguar/common"
)

type calendarTestStore struct {
	baseStore
}

func (calendarTestStore) LookupBusinessCalendar(tenantID, name string) (*common.BusinessCalendarDefinition, error) {
	if tenantID != "calendar-tenant" || name != "weekdays" {
		return nil, fmt.Errorf("calendar not found")
	}
	return &common.BusinessCalendarDefinition{
		Name: "weekdays", Weekdays: []string{"Mon", "Tue", "Wed", "Thu", "Fri"},
		ExcludedDates: []string{"2026-12-25"},
	}, nil
}

func TestAddWorkflowResolvesBusinessCalendar(t *testing.T) {
	wmmInstance = nil
	defer func() { wmmInstance = nil }()
	wm := NewWorkflowManager(false, calendarTestStore{}).(*workflowManager)
	def := Workflow{
		Id:       "workflow_calendar",
		TenantID: "calendar-tenant",
		Nodes: map[string]Node{
			"trigger_calendar": {
				Id: "trigger_calendar", Name: "daily", ActionType: "trigger", ActionName: "cron",
				Arguments: map[string]string{"cron": "0 9 * * *", "calendar": "weekdays"},
			},
		},
	}
	if err := wm.AddWorkflow(def); err != nil {
		t.Fatalf("AddWorkflow: %v", err)
	}
	trigger := wm.Workflows[def.Id].TriggerList["trigger_calendar"]
	if trigger == nil {
		t.Fatal("calendar trigger was not registered")
	}
	args, ok := trigger.GetArgs().Args.(map[string]string)
	if !ok {
		t.Fatalf("trigger args type = %T, want map[string]string", trigger.GetArgs().Args)
	}
	if args["calendar_weekdays"] != `["Mon","Tue","Wed","Thu","Fri"]` ||
		args["calendar_dates"] != `["2026-12-25"]` {
		t.Errorf("resolved args = %v", args)
	}
	if err := trigger.Stop(); err != nil {
		t.Errorf("Stop: %v", err)
	}
}
