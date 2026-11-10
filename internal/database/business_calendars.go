package database

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/ent"
	"github.com/mcmx/nitejaguar/ent/businesscalendar"
	"go.jetify.com/typeid"
)

var validCalendarWeekdays = map[string]struct{}{
	"Mon": {}, "Tue": {}, "Wed": {}, "Thu": {}, "Fri": {}, "Sat": {}, "Sun": {},
}

func normalizeCalendarRules(weekdays, excludedDates []string) ([]string, []string, error) {
	cleanWeekdays := make([]string, 0, len(weekdays))
	seenWeekdays := make(map[string]struct{}, len(weekdays))
	for _, day := range weekdays {
		day = strings.TrimSpace(day)
		if _, ok := validCalendarWeekdays[day]; !ok {
			return nil, nil, fmt.Errorf("invalid weekday %q (use Mon, Tue, Wed, Thu, Fri, Sat, or Sun)", day)
		}
		if _, seen := seenWeekdays[day]; !seen {
			cleanWeekdays = append(cleanWeekdays, day)
			seenWeekdays[day] = struct{}{}
		}
	}
	cleanDates := make([]string, 0, len(excludedDates))
	seenDates := make(map[string]struct{}, len(excludedDates))
	for _, rawDate := range excludedDates {
		rawDate = strings.TrimSpace(rawDate)
		if rawDate == "" {
			continue
		}
		date, err := time.Parse("2006-01-02", rawDate)
		if err != nil || date.Format("2006-01-02") != rawDate {
			return nil, nil, fmt.Errorf("invalid excluded date %q (use YYYY-MM-DD)", rawDate)
		}
		if _, seen := seenDates[rawDate]; !seen {
			cleanDates = append(cleanDates, rawDate)
			seenDates[rawDate] = struct{}{}
		}
	}
	return cleanWeekdays, cleanDates, nil
}

func decodeCalendarJSON(weekdaysJSON, datesJSON string) ([]string, []string, error) {
	var weekdays, dates []string
	if err := json.Unmarshal([]byte(weekdaysJSON), &weekdays); err != nil {
		return nil, nil, fmt.Errorf("invalid stored calendar weekdays: %w", err)
	}
	if err := json.Unmarshal([]byte(datesJSON), &dates); err != nil {
		return nil, nil, fmt.Errorf("invalid stored calendar dates: %w", err)
	}
	return weekdays, dates, nil
}

func (s *service) CreateBusinessCalendar(tenantID, name string, weekdays, excludedDates []string) (*ent.BusinessCalendar, error) {
	tenantID = normalizeTenant(tenantID)
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("calendar name is required")
	}
	weekdays, excludedDates, err := normalizeCalendarRules(weekdays, excludedDates)
	if err != nil {
		return nil, err
	}
	weekdaysJSON, _ := json.Marshal(weekdays)
	datesJSON, _ := json.Marshal(excludedDates)
	id, _ := typeid.WithPrefix("calendar")
	row, err := s.client.BusinessCalendar.Create().
		SetID(id.String()).
		SetTenantID(tenantID).
		SetName(name).
		SetWeekdaysJSON(string(weekdaysJSON)).
		SetExcludedDatesJSON(string(datesJSON)).
		Save(context.Background())
	if err != nil {
		if ent.IsConstraintError(err) {
			return nil, fmt.Errorf("calendar %q already exists in tenant %q", name, tenantID)
		}
		return nil, fmt.Errorf("failed to create business calendar: %w", err)
	}
	return row, nil
}

func (s *service) ListBusinessCalendars(tenantID string) ([]*ent.BusinessCalendar, error) {
	q := s.client.BusinessCalendar.Query()
	if tenantID != "" {
		q.Where(businesscalendar.TenantID(normalizeTenant(tenantID)))
	}
	rows, err := q.Order(ent.Asc(businesscalendar.FieldName)).All(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to list business calendars: %w", err)
	}
	return rows, nil
}

func (s *service) GetBusinessCalendar(id string) (*ent.BusinessCalendar, error) {
	row, err := s.client.BusinessCalendar.Get(context.Background(), id)
	if err != nil {
		return nil, fmt.Errorf("business calendar not found: %w", err)
	}
	return row, nil
}

func (s *service) UpdateBusinessCalendar(id, name string, weekdays, excludedDates []string) (*ent.BusinessCalendar, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("calendar name is required")
	}
	weekdays, excludedDates, err := normalizeCalendarRules(weekdays, excludedDates)
	if err != nil {
		return nil, err
	}
	weekdaysJSON, _ := json.Marshal(weekdays)
	datesJSON, _ := json.Marshal(excludedDates)
	row, err := s.client.BusinessCalendar.UpdateOneID(id).
		SetName(name).
		SetWeekdaysJSON(string(weekdaysJSON)).
		SetExcludedDatesJSON(string(datesJSON)).
		Save(context.Background())
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("business calendar not found: %w", err)
		}
		if ent.IsConstraintError(err) {
			return nil, fmt.Errorf("calendar %q already exists in this tenant", name)
		}
		return nil, fmt.Errorf("failed to update business calendar: %w", err)
	}
	return row, nil
}

func (s *service) DeleteBusinessCalendar(id string) error {
	if err := s.client.BusinessCalendar.DeleteOneID(id).Exec(context.Background()); err != nil {
		return fmt.Errorf("failed to delete business calendar: %w", err)
	}
	return nil
}

func (s *service) LookupBusinessCalendar(tenantID, name string) (*common.BusinessCalendarDefinition, error) {
	tenantID = normalizeTenant(tenantID)
	row, err := s.client.BusinessCalendar.Query().
		Where(businesscalendar.TenantID(tenantID), businesscalendar.Name(strings.TrimSpace(name))).
		Only(context.Background())
	if err != nil {
		return nil, fmt.Errorf("business calendar %q not found in tenant %q", name, tenantID)
	}
	weekdays, dates, err := decodeCalendarJSON(row.WeekdaysJSON, row.ExcludedDatesJSON)
	if err != nil {
		return nil, err
	}
	return &common.BusinessCalendarDefinition{Name: row.Name, Weekdays: weekdays, ExcludedDates: dates}, nil
}

func BusinessCalendarRules(row *ent.BusinessCalendar) (*common.BusinessCalendarDefinition, error) {
	weekdays, dates, err := decodeCalendarJSON(row.WeekdaysJSON, row.ExcludedDatesJSON)
	if err != nil {
		return nil, err
	}
	return &common.BusinessCalendarDefinition{Name: row.Name, Weekdays: weekdays, ExcludedDates: dates}, nil
}
