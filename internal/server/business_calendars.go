package server

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/mcmx/nitejaguar/internal/database"
)

type BusinessCalendarView struct {
	ID            string   `json:"id"`
	TenantID      string   `json:"tenant_id"`
	Name          string   `json:"name"`
	Weekdays      []string `json:"weekdays"`
	ExcludedDates []string `json:"excluded_dates"`
	CreatedAt     string   `json:"created_at"`
	UpdatedAt     string   `json:"updated_at"`
}

func (s *Server) businessCalendarView(id string) (BusinessCalendarView, error) {
	row, err := s.db.GetBusinessCalendar(id)
	if err != nil {
		return BusinessCalendarView{}, err
	}
	rules, err := database.BusinessCalendarRules(row)
	if err != nil {
		return BusinessCalendarView{}, err
	}
	return BusinessCalendarView{
		ID: row.ID, TenantID: row.TenantID, Name: row.Name,
		Weekdays: rules.Weekdays, ExcludedDates: rules.ExcludedDates,
		CreatedAt: row.CreatedAt.Format(time.RFC3339), UpdatedAt: row.UpdatedAt.Format(time.RFC3339),
	}, nil
}

type CreateBusinessCalendarInput struct {
	Authorization string `header:"Authorization"`
	SessionToken  string `header:"X-Auth-Token"`
	Body          struct {
		TenantID      string   `json:"tenant_id,omitempty"`
		Name          string   `json:"name"`
		Weekdays      []string `json:"weekdays,omitempty"`
		ExcludedDates []string `json:"excluded_dates,omitempty"`
	}
}

type BusinessCalendarOutput struct {
	Body BusinessCalendarView
}

func (s *Server) CreateBusinessCalendar(_ context.Context, input *CreateBusinessCalendarInput) (*BusinessCalendarOutput, error) {
	caller, actor, err := s.requireRole(input.Authorization, input.SessionToken, database.RoleOperator)
	if err != nil {
		return nil, err
	}
	tenantID, err := scopedTenant(caller, input.Body.TenantID)
	if err != nil {
		return nil, err
	}
	row, err := s.db.CreateBusinessCalendar(tenantID, input.Body.Name, input.Body.Weekdays, input.Body.ExcludedDates)
	if err != nil {
		if strings.HasPrefix(err.Error(), "failed to ") {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		return nil, huma.Error400BadRequest(err.Error())
	}
	_ = s.db.LogAudit("calendar.create", row.TenantID, actor, row.ID, "name="+row.Name)
	log.Printf("business calendar created: id=%s tenant=%s name=%q actor=%s", row.ID, row.TenantID, row.Name, actor)
	out, err := s.businessCalendarView(row.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to read created calendar")
	}
	return &BusinessCalendarOutput{Body: out}, nil
}

type ListBusinessCalendarsInput struct {
	Authorization string `header:"Authorization"`
	SessionToken  string `header:"X-Auth-Token"`
	TenantID      string `query:"tenant_id"`
}

type ListBusinessCalendarsOutput struct {
	Body struct {
		Calendars []BusinessCalendarView `json:"calendars"`
	}
}

func (s *Server) ListBusinessCalendars(_ context.Context, input *ListBusinessCalendarsInput) (*ListBusinessCalendarsOutput, error) {
	caller, _, err := s.requireRole(input.Authorization, input.SessionToken, database.RoleViewer)
	if err != nil {
		return nil, err
	}
	tenantID := input.TenantID
	if caller != nil && !database.CanCrossTenant(caller) {
		if tenantID != "" && normalizeTenantID(tenantID) != normalizeTenantID(caller.TenantID) {
			return nil, huma.Error403Forbidden("cross-tenant operation requires a superuser")
		}
		tenantID = caller.TenantID
	}
	rows, err := s.db.ListBusinessCalendars(tenantID)
	if err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}
	out := &ListBusinessCalendarsOutput{}
	for _, row := range rows {
		rules, err := database.BusinessCalendarRules(row)
		if err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		out.Body.Calendars = append(out.Body.Calendars, BusinessCalendarView{
			ID: row.ID, TenantID: row.TenantID, Name: row.Name,
			Weekdays: rules.Weekdays, ExcludedDates: rules.ExcludedDates,
			CreatedAt: row.CreatedAt.Format(time.RFC3339), UpdatedAt: row.UpdatedAt.Format(time.RFC3339),
		})
	}
	if out.Body.Calendars == nil {
		out.Body.Calendars = []BusinessCalendarView{}
	}
	return out, nil
}

type GetBusinessCalendarInput struct {
	Authorization string `header:"Authorization"`
	SessionToken  string `header:"X-Auth-Token"`
	ID            string `path:"id"`
}

func (s *Server) GetBusinessCalendar(_ context.Context, input *GetBusinessCalendarInput) (*BusinessCalendarOutput, error) {
	caller, _, err := s.requireRole(input.Authorization, input.SessionToken, database.RoleViewer)
	if err != nil {
		return nil, err
	}
	row, err := s.db.GetBusinessCalendar(input.ID)
	if err != nil {
		return nil, huma.Error404NotFound("business calendar not found")
	}
	if caller != nil && !database.CanCrossTenant(caller) && normalizeTenantID(row.TenantID) != normalizeTenantID(caller.TenantID) {
		return nil, huma.Error404NotFound("business calendar not found")
	}
	view, err := s.businessCalendarView(row.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}
	return &BusinessCalendarOutput{Body: view}, nil
}

type UpdateBusinessCalendarInput struct {
	Authorization string `header:"Authorization"`
	SessionToken  string `header:"X-Auth-Token"`
	ID            string `path:"id"`
	Body          struct {
		Name          string   `json:"name"`
		Weekdays      []string `json:"weekdays,omitempty"`
		ExcludedDates []string `json:"excluded_dates,omitempty"`
	}
}

func (s *Server) UpdateBusinessCalendar(_ context.Context, input *UpdateBusinessCalendarInput) (*BusinessCalendarOutput, error) {
	caller, actor, err := s.requireRole(input.Authorization, input.SessionToken, database.RoleOperator)
	if err != nil {
		return nil, err
	}
	row, err := s.db.GetBusinessCalendar(input.ID)
	if err != nil {
		return nil, huma.Error404NotFound("business calendar not found")
	}
	if caller != nil && !database.CanCrossTenant(caller) && normalizeTenantID(row.TenantID) != normalizeTenantID(caller.TenantID) {
		return nil, huma.Error403Forbidden("cross-tenant operation requires a superuser")
	}
	updated, err := s.db.UpdateBusinessCalendar(input.ID, input.Body.Name, input.Body.Weekdays, input.Body.ExcludedDates)
	if err != nil {
		if strings.HasPrefix(err.Error(), "failed to ") {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		return nil, huma.Error400BadRequest(err.Error())
	}
	_ = s.db.LogAudit("calendar.update", updated.TenantID, actor, updated.ID, "name="+updated.Name)
	log.Printf("business calendar updated: id=%s tenant=%s name=%q actor=%s", updated.ID, updated.TenantID, updated.Name, actor)
	view, err := s.businessCalendarView(updated.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to read updated calendar")
	}
	return &BusinessCalendarOutput{Body: view}, nil
}

type DeleteBusinessCalendarInput struct {
	Authorization string `header:"Authorization"`
	SessionToken  string `header:"X-Auth-Token"`
	ID            string `path:"id"`
}

type DeleteBusinessCalendarOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

func (s *Server) DeleteBusinessCalendar(_ context.Context, input *DeleteBusinessCalendarInput) (*DeleteBusinessCalendarOutput, error) {
	caller, actor, err := s.requireRole(input.Authorization, input.SessionToken, database.RoleOperator)
	if err != nil {
		return nil, err
	}
	row, err := s.db.GetBusinessCalendar(input.ID)
	if err != nil {
		return nil, huma.Error404NotFound("business calendar not found")
	}
	if caller != nil && !database.CanCrossTenant(caller) && normalizeTenantID(row.TenantID) != normalizeTenantID(caller.TenantID) {
		return nil, huma.Error403Forbidden("cross-tenant operation requires a superuser")
	}
	if err := s.db.DeleteBusinessCalendar(input.ID); err != nil {
		return nil, huma.Error500InternalServerError(fmt.Sprintf("failed to delete business calendar: %v", err))
	}
	_ = s.db.LogAudit("calendar.delete", row.TenantID, actor, row.ID, "name="+row.Name)
	log.Printf("business calendar deleted: id=%s tenant=%s actor=%s", row.ID, row.TenantID, actor)
	out := &DeleteBusinessCalendarOutput{}
	out.Body.Ok = true
	return out, nil
}
