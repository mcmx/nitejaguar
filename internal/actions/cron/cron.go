// Package cron implements a workflow trigger that fires on a schedule — the
// cronjob analogue — and emits a result carrying now() at the moment it
// triggered.
//
// Supported args (all optional; resolved in New so a bad schedule fails while
// the workflow loads instead of silently never firing):
//   - cron:         standard 5-field cron expression ("m h dom mon dow"),
//     optionally 6 fields with leading seconds ("s m h dom mon dow"),
//     plus descriptors (@every 30s, @daily, @hourly, @midnight, @weekly).
//     Defaults to "* * * * *" (every minute).
//   - interval:     fixed Go duration ("30s", "5m", "1h30m"). When set it
//     takes precedence over `cron`; durations below one second round up to
//     one second.
//   - timezone:     IANA name ("Europe/Madrid", "UTC"). Used both to match
//     cron fields and to render the payload. Empty means local time. An
//     explicit timezone wins over a TZ=/CRON_TZ= prefix inside `cron`.
//   - format:       Go time layout for the payload's "datetime" string.
//     Empty defaults to time.RFC3339; "unix"/"unix_ms" emit epoch
//     seconds/millis (shared spec with the datetime action).
//   - run_on_start: "true" emits one result immediately when the trigger
//     starts, before waiting for the first scheduled fire.
//
// The payload mirrors the datetime action so downstream nodes can read
// $input.datetime / $input.timestamp from either source interchangeably.
package cron

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/mcmx/nitejaguar/common"

	"github.com/robfig/cron/v3"
)

// defaultSpec is the classic bare crontab entry: every minute.
const defaultSpec = "* * * * *"

// parser accepts the standard 5-field expression, an optional leading seconds
// field, and @descriptor schedules.
var parser = cron.NewParser(
	cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

type calendarRule struct {
	allowedWeekdays map[time.Weekday]bool
	excludedDates   map[string]struct{}
}

func (c *calendarRule) Allow(t time.Time) bool {
	if c == nil {
		return true
	}
	if len(c.allowedWeekdays) > 0 {
		if !c.allowedWeekdays[t.Weekday()] {
			return false
		}
	}
	if _, blocked := c.excludedDates[dateKey(t)]; blocked {
		return false
	}
	return true
}

type cronTrigger struct {
	data   common.ActionArgs
	events chan common.ResultData

	schedule     cron.Schedule
	scheduleDesc string
	calendar     *calendarRule
	calendarName string

	loc    *time.Location
	tzName string
	format string

	runOnStart bool

	stopOnce sync.Once
	stop     chan struct{}
}

// New creates the cron trigger. Args are validated here (schedule, timezone,
// run_on_start) so an invalid definition surfaces as an AddTrigger error.
func New(events chan common.ResultData, data common.ActionArgs) (common.Action, error) {
	args, err := argsToMap(data.Args)
	if err != nil {
		return nil, err
	}

	tzRaw := strings.TrimSpace(args["timezone"])
	loc := time.Local
	tzName := "Local"
	if tzRaw != "" {
		l, err := time.LoadLocation(tzRaw)
		if err != nil {
			return nil, fmt.Errorf("invalid timezone %q: %w", tzRaw, err)
		}
		loc, tzName = l, tzRaw
	}

	calendar, err := buildCalendar(args)
	if err != nil {
		return nil, err
	}

	schedule, desc, err := buildSchedule(args, tzRaw != "", loc, calendar)
	if err != nil {
		return nil, err
	}

	runOnStart, err := parseBool(args["run_on_start"])
	if err != nil {
		return nil, err
	}

	t := &cronTrigger{
		data:         data,
		events:       events,
		schedule:     schedule,
		scheduleDesc: desc,
		calendar:     calendar,
		calendarName: strings.TrimSpace(args["calendar"]),
		loc:          loc,
		tzName:       tzName,
		format:       strings.TrimSpace(args["format"]),
		runOnStart:   runOnStart,
		stop:         make(chan struct{}),
	}
	t.data.ActionType = "trigger"
	log.Println("Initializing Cron Trigger with id:", t.data.Id, "schedule:", desc)

	return t, nil
}

// Execute blocks for the lifetime of the trigger: it optionally fires once
// immediately, then sleeps until the next scheduled moment and emits a result
// payload holding now() for every fire, until Stop is called.
func (t *cronTrigger) Execute(executionId string, inputs []any) {
	log.Printf("Executing Cron Trigger with id: %s (schedule %q)", t.data.Id, t.scheduleDesc)
	if t.runOnStart {
		t.sendResult(executionId, t.now())
	}
	for {
		next := t.schedule.Next(t.now())
		if next.IsZero() {
			log.Printf("[cron] schedule %q has no future fire time; stopping trigger %s", t.scheduleDesc, t.data.Id)
			return
		}
		delay := time.Until(next)
		if delay < 0 {
			delay = 0
		}
		timer := time.NewTimer(delay)
		select {
		case <-t.stop:
			timer.Stop()
			return
		case <-timer.C:
			t.sendResult(executionId, t.now())
		}
	}
}

// Stop ends the Execute loop. It is idempotent and safe to call while the
// trigger is mid-wait.
func (t *cronTrigger) Stop() error {
	log.Println("Stopping the cron trigger", t.data.Id)
	t.stopOnce.Do(func() { close(t.stop) })
	return nil
}

// GetArgs returns the ActionArgs associated with the cron trigger.
func (t *cronTrigger) GetArgs() common.ActionArgs {
	return t.data
}

// now is the moment the trigger evaluates: the local clock rendered in the
// trigger's timezone.
func (t *cronTrigger) now() time.Time {
	return time.Now().In(t.loc)
}

func (t *cronTrigger) sendResult(executionId string, now time.Time) {
	payload := map[string]any{
		"type":         "success",
		"trigger":      "cron",
		"datetime":     common.FormatTimestamp(now, t.format),
		"timestamp":    now.Unix(),
		"timestamp_ms": now.UnixMilli(),
		"format":       common.EffectiveTimestampFormat(t.format),
		"timezone":     t.tzName,
		"schedule":     t.scheduleDesc,
	}
	if t.calendar != nil && t.calendarName != "" {
		payload["calendar"] = t.calendarName
		payload["calendar_allowed"] = true
	}
	t.events <- common.ResultData{
		ExecutionID: executionId,
		ActionID:    t.data.Id,
		ActionType:  t.data.ActionType,
		ActionName:  t.data.ActionName,
		CreatedAt:   now,
		Payload:     payload,
	}
}

type calendarSchedule struct {
	base     cron.Schedule
	calendar *calendarRule
}

func (s *calendarSchedule) Next(t time.Time) time.Time {
	if s == nil || s.base == nil || s.calendar == nil {
		if s == nil || s.base == nil {
			return time.Time{}
		}
		return s.base.Next(t)
	}
	for {
		next := s.base.Next(t)
		if next.IsZero() {
			return time.Time{}
		}
		if s.calendar.Allow(next) {
			return next
		}
		t = time.Date(next.Year(), next.Month(), next.Day()+1, 0, 0, 0, 0, next.Location()).Add(-time.Nanosecond)
	}
}

// buildSchedule resolves `interval` (which wins when set) or the `cron`
// expression into a schedule. tzExplicit forces the schedule to match fields
// in loc, overriding any TZ=/CRON_TZ= prefix in the expression.
func buildSchedule(args map[string]string, tzExplicit bool, loc *time.Location, calendar *calendarRule) (cron.Schedule, string, error) {
	var schedule cron.Schedule
	var desc string
	if raw := strings.TrimSpace(args["interval"]); raw != "" {
		delay, err := time.ParseDuration(raw)
		if err != nil {
			return nil, "", fmt.Errorf("invalid interval %q: %w (use a Go duration such as \"30s\" or \"5m\")", raw, err)
		}
		if delay <= 0 {
			return nil, "", fmt.Errorf("interval must be greater than zero, got %q", raw)
		}
		schedule = cron.Every(delay)
		desc = raw
	} else {
		expr := strings.TrimSpace(args["cron"])
		if expr == "" {
			expr = defaultSpec
		}
		parsed, err := parser.Parse(expr)
		if err != nil {
			return nil, "", fmt.Errorf("invalid cron expression %q: %w", expr, err)
		}
		schedule = parsed
		desc = expr
		if tzExplicit {
			if spec, ok := schedule.(*cron.SpecSchedule); ok {
				spec.Location = loc
			}
		}
	}
	if calendar != nil {
		schedule = &calendarSchedule{base: schedule, calendar: calendar}
	}
	return schedule, desc, nil
}

func buildCalendar(args map[string]string) (*calendarRule, error) {
	name := strings.TrimSpace(args["calendar"])
	if name == "" {
		return nil, nil
	}
	out := &calendarRule{
		allowedWeekdays: map[time.Weekday]bool{},
		excludedDates:   map[string]struct{}{},
	}
	if args["calendar_weekdays"] == "" || args["calendar_dates"] == "" {
		return nil, fmt.Errorf("business calendar %q has not been resolved", name)
	}
	var weekdays, dates []string
	if err := json.Unmarshal([]byte(args["calendar_weekdays"]), &weekdays); err != nil {
		return nil, fmt.Errorf("invalid resolved calendar weekdays: %w", err)
	}
	if err := json.Unmarshal([]byte(args["calendar_dates"]), &dates); err != nil {
		return nil, fmt.Errorf("invalid resolved calendar dates: %w", err)
	}
	for _, weekday := range weekdays {
		day, ok := parseWeekdayToken(weekday)
		if !ok {
			return nil, fmt.Errorf("invalid calendar weekday %q", weekday)
		}
		out.allowedWeekdays[day] = true
	}
	for _, rawDate := range dates {
		date, err := time.Parse("2006-01-02", rawDate)
		if err != nil || date.Format("2006-01-02") != rawDate {
			return nil, fmt.Errorf("invalid calendar date %q", rawDate)
		}
		out.excludedDates[dateKey(date)] = struct{}{}
	}
	return out, nil
}

func parseWeekdayToken(raw string) (time.Weekday, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "mon", "monday":
		return time.Monday, true
	case "tue", "tues", "tuesday":
		return time.Tuesday, true
	case "wed", "weds", "wednesday":
		return time.Wednesday, true
	case "thu", "thur", "thurs", "thursday":
		return time.Thursday, true
	case "fri", "friday":
		return time.Friday, true
	case "sat", "saturday":
		return time.Saturday, true
	case "sun", "sunday":
		return time.Sunday, true
	default:
		return 0, false
	}
}

func dateKey(t time.Time) string {
	return t.Format("2006-01-02")
}

// argsToMap normalizes node args, treating a nil Args as "no arguments" (the
// defaults apply) — unlike common.ArgsToStringMap, which rejects nil.
func argsToMap(raw any) (map[string]string, error) {
	if raw == nil {
		return map[string]string{}, nil
	}
	return common.ArgsToStringMap(raw)
}

// parseBool reads run_on_start, accepting the usual spellings.
func parseBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "0", "false", "no", "off":
		return false, nil
	case "1", "true", "yes", "on":
		return true, nil
	default:
		return false, fmt.Errorf("invalid run_on_start %q (use true or false)", raw)
	}
}
