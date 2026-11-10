package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/mcmx/nitejaguar/internal/database"
	"github.com/mcmx/nitejaguar/internal/workflow"
)

func TestBusinessCalendarWebCRUD(t *testing.T) {
	t.Setenv("DB_URL", "file:ent.db?mode=memory&cache=shared&_fk=1")
	db, err := database.New()
	if err != nil {
		t.Fatalf("initialize database: %v", err)
	}
	s := &Server{db: db, wm: workflow.NewWorkflowManager(false, db)}
	tenantID := fmt.Sprintf("calendarweb%d", testAdminSeq.Add(1))
	operatorAuth := ensureRoleToken(t, db, tenantID, database.RoleOperator)
	session := webSession(t, db, operatorAuth)

	newFormContext := func(target string, values url.Values) (echo.Context, *httptest.ResponseRecorder) {
		req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
		rec := httptest.NewRecorder()
		return echo.New().NewContext(req, rec), rec
	}

	createValues := url.Values{
		"name":           {"web-calendar"},
		"weekdays":       {"Mon", "Tue", "Wed", "Thu", "Fri"},
		"excluded_dates": {"2026-12-25\n2027-01-01"},
	}
	createContext, createRecorder := newFormContext("/calendars", createValues)
	if err := s.createBusinessCalendarWeb(createContext); err != nil {
		t.Fatalf("createBusinessCalendarWeb: %v", err)
	}
	if createRecorder.Code != http.StatusOK || !strings.Contains(createRecorder.Body.String(), "Calendar created.") {
		t.Fatalf("create response status/body = %v/%s", createRecorder.Code, createRecorder.Body.String())
	}
	rows, err := db.ListBusinessCalendars(tenantID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("created rows = %v, %v", rows, err)
	}
	id := rows[0].ID

	updateValues := url.Values{
		"name":           {"updated-calendar"},
		"weekdays":       {"Mon"},
		"excluded_dates": {"2026-12-26"},
	}
	updateContext, updateRecorder := newFormContext("/calendars/"+id+"/update", updateValues)
	updateContext.SetParamNames("id")
	updateContext.SetParamValues(id)
	if err := s.updateBusinessCalendarWeb(updateContext); err != nil {
		t.Fatalf("updateBusinessCalendarWeb: %v", err)
	}
	if updateRecorder.Code != http.StatusOK || !strings.Contains(updateRecorder.Body.String(), "Calendar updated.") {
		t.Fatalf("update response status/body = %v/%s", updateRecorder.Code, updateRecorder.Body.String())
	}
	row, err := db.GetBusinessCalendar(id)
	if err != nil || row.Name != "updated-calendar" {
		t.Fatalf("updated row = %+v, %v", row, err)
	}

	pageRequest := httptest.NewRequest(http.MethodGet, "/calendars", nil)
	pageRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	pageRecorder := httptest.NewRecorder()
	pageContext := echo.New().NewContext(pageRequest, pageRecorder)
	if err := s.businessCalendarsPage(pageContext); err != nil {
		t.Fatalf("businessCalendarsPage: %v", err)
	}
	for _, want := range []string{"Business calendars", "updated-calendar", "/calendars/" + id + "/update", "2026-12-26"} {
		if !strings.Contains(pageRecorder.Body.String(), want) {
			t.Errorf("calendar page missing %q: %s", want, pageRecorder.Body.String())
		}
	}

	deleteContext, deleteRecorder := newFormContext("/calendars/"+id+"/delete", nil)
	deleteContext.SetParamNames("id")
	deleteContext.SetParamValues(id)
	if err := s.deleteBusinessCalendarWeb(deleteContext); err != nil {
		t.Fatalf("deleteBusinessCalendarWeb: %v", err)
	}
	if deleteRecorder.Code != http.StatusOK || !strings.Contains(deleteRecorder.Body.String(), "Calendar deleted.") {
		t.Fatalf("delete response status/body = %v/%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}
	if _, err := db.GetBusinessCalendar(id); err == nil {
		t.Fatal("calendar still exists after web delete")
	}
}
