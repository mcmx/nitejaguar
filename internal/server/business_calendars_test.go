package server

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/mcmx/nitejaguar/internal/database"
	"github.com/mcmx/nitejaguar/internal/workflow"
)

func TestBusinessCalendarAPICRUDAndAssignments(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("initialize database: %v", err)
	}
	s := &Server{db: db, wm: workflow.NewWorkflowManager(false, db)}
	_, api := newTestAPI(t)
	addApiRoutes(api, s)

	tenantID := fmt.Sprintf("calendarcrud%d", testAdminSeq.Add(1))
	operatorAuth := ensureRoleToken(t, db, tenantID, database.RoleOperator)
	viewerAuth := ensureRoleToken(t, db, tenantID, database.RoleViewer)

	resp := api.Post("/api/calendars", viewerAuth, map[string]any{
		"name": "weekdays",
	})
	if resp.Code != http.StatusForbidden {
		t.Fatalf("viewer create status = %v, want 403", resp.Code)
	}
	resp = api.Post("/api/calendars", operatorAuth, map[string]any{
		"name": "weekdays", "weekdays": []string{"Mon", "Tue", "Wed", "Thu", "Fri"},
		"excluded_dates": []string{"2026-12-25"},
	})
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("create calendar status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var created struct {
		ID       string   `json:"id"`
		TenantID string   `json:"tenant_id"`
		Name     string   `json:"name"`
		Weekdays []string `json:"weekdays"`
		Dates    []string `json:"excluded_dates"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &created)
	if created.ID == "" || created.TenantID != tenantID || created.Name != "weekdays" {
		t.Fatalf("create response = %+v", created)
	}
	if len(created.Weekdays) != 5 || len(created.Dates) != 1 || created.Dates[0] != "2026-12-25" {
		t.Fatalf("calendar rules = weekdays %v, dates %v", created.Weekdays, created.Dates)
	}

	resp = api.Get("/api/calendars/"+created.ID, viewerAuth)
	if resp.Code != http.StatusOK {
		t.Fatalf("get calendar status = %v, body = %s", resp.Code, resp.Body.String())
	}
	resp = api.Get("/api/calendars", viewerAuth)
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), created.ID) {
		t.Fatalf("list calendars status/body = %v/%s", resp.Code, resp.Body.String())
	}
	resp = api.Do(http.MethodPut, "/api/calendars/"+created.ID, operatorAuth, map[string]any{
		"name": "revised", "weekdays": []string{"Mon"}, "excluded_dates": []string{"2026-12-26"},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("update calendar status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var updated struct {
		Name  string   `json:"name"`
		Dates []string `json:"excluded_dates"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &updated)
	if updated.Name != "revised" || len(updated.Dates) != 1 || updated.Dates[0] != "2026-12-26" {
		t.Fatalf("updated calendar = %+v", updated)
	}

	resp = api.Post("/api/calendars", operatorAuth, map[string]any{
		"name": "invalid", "excluded_dates": []string{"not-a-date"},
	})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("invalid date status = %v, want 400 (body %s)", resp.Code, resp.Body.String())
	}

	// A Cron assignment carries an immutable rule snapshot, not only the
	// registry name, so remote clients can run it without DB access.
	joinToken := mintEnrollmentToken(t, db, tenantID, 0)
	resp = api.Post("/api/clients/register", map[string]any{
		"name": "calendar-runner", "enrollment_token": joinToken,
	})
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("register client status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var client struct {
		ClientID string `json:"client_id"`
		Token    string `json:"token"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &client)
	wfID := fmt.Sprintf("workflow_01calendar%013d", testAdminSeq.Load())
	triggerID := fmt.Sprintf("trigger_01calendar%013d", testAdminSeq.Load())
	resp = api.Post("/api/workflows/import", operatorAuth, map[string]any{
		"id": wfID, "name": "Calendar test",
		"nodes": map[string]any{
			triggerID: map[string]any{
				"id": triggerID, "name": "Daily schedule", "description": "Calendar-filtered schedule",
				"action_type": "trigger", "action_name": "cron",
				"arguments":    map[string]any{"cron": "0 9 * * *", "timezone": "UTC", "calendar": "revised"},
				"conditions":   map[string]any{"entries": map[string]any{}},
				"dependencies": []string{},
			},
		},
	})
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("import workflow status = %v, body = %s", resp.Code, resp.Body.String())
	}
	resp = api.Get("/api/clients/"+client.ClientID+"/assignments", "Authorization: Bearer "+client.Token)
	if resp.Code != http.StatusOK {
		t.Fatalf("get assignments status = %v, body = %s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `"calendar_weekdays":"[\"Mon\"]"`) ||
		!strings.Contains(resp.Body.String(), `"calendar_dates":"[\"2026-12-26\"]"`) {
		t.Fatalf("assignment omitted resolved calendar rules: %s", resp.Body.String())
	}
	var initialAssignments struct {
		Workflows []WorkflowDefinition `json:"workflows"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &initialAssignments)
	if len(initialAssignments.Workflows) != 1 {
		t.Fatalf("assignment workflows = %d, want 1", len(initialAssignments.Workflows))
	}
	initialRevision := initialAssignments.Workflows[0].Revision

	resp = api.Do(http.MethodPut, "/api/calendars/"+created.ID, operatorAuth, map[string]any{
		"name": "revised", "weekdays": []string{"Tue"},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("update assigned calendar status = %v, body = %s", resp.Code, resp.Body.String())
	}
	resp = api.Get("/api/clients/"+client.ClientID+"/assignments", "Authorization: Bearer "+client.Token)
	if resp.Code != http.StatusOK {
		t.Fatalf("get updated assignments status = %v, body = %s", resp.Code, resp.Body.String())
	}
	var updatedAssignments struct {
		Workflows []WorkflowDefinition `json:"workflows"`
	}
	decodeBody(t, strings.NewReader(resp.Body.String()), &updatedAssignments)
	if len(updatedAssignments.Workflows) != 1 {
		t.Fatalf("updated assignment workflows = %d, want 1", len(updatedAssignments.Workflows))
	}
	if updatedAssignments.Workflows[0].Revision == initialRevision {
		t.Fatal("calendar rule update did not change assignment revision")
	}
	if !strings.Contains(resp.Body.String(), `"calendar_weekdays":"[\"Tue\"]"`) {
		t.Fatalf("updated assignment omitted new calendar rules: %s", resp.Body.String())
	}

	resp = api.Delete("/api/calendars/"+created.ID, operatorAuth)
	if resp.Code != http.StatusOK {
		t.Fatalf("delete calendar status = %v, body = %s", resp.Code, resp.Body.String())
	}
	resp = api.Get("/api/calendars/"+created.ID, operatorAuth)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("get deleted calendar status = %v, want 404", resp.Code)
	}
	auditRows, err := db.ListAuditLogs(100)
	if err != nil {
		t.Fatalf("list calendar audit entries: %v", err)
	}
	audited := map[string]bool{}
	for _, entry := range auditRows {
		if entry.Target == created.ID {
			audited[entry.Action] = true
		}
	}
	for _, action := range []string{"calendar.create", "calendar.update", "calendar.delete"} {
		if !audited[action] {
			t.Errorf("missing %s audit event for %s", action, created.ID)
		}
	}
}
