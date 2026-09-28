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

type cronTrigger struct {
	data   common.ActionArgs
	events chan common.ResultData

	schedule     cron.Schedule
	scheduleDesc string

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

	schedule, desc, err := buildSchedule(args, tzRaw != "", loc)
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
	t.events <- common.ResultData{
		ExecutionID: executionId,
		ActionID:    t.data.Id,
		ActionType:  t.data.ActionType,
		ActionName:  t.data.ActionName,
		CreatedAt:   now,
		Payload: map[string]any{
			"type":         "success",
			"trigger":      "cron",
			"datetime":     common.FormatTimestamp(now, t.format),
			"timestamp":    now.Unix(),
			"timestamp_ms": now.UnixMilli(),
			"format":       common.EffectiveTimestampFormat(t.format),
			"timezone":     t.tzName,
			"schedule":     t.scheduleDesc,
		},
	}
}

// buildSchedule resolves `interval` (which wins when set) or the `cron`
// expression into a schedule. tzExplicit forces the schedule to match fields
// in loc, overriding any TZ=/CRON_TZ= prefix in the expression.
func buildSchedule(args map[string]string, tzExplicit bool, loc *time.Location) (cron.Schedule, string, error) {
	if raw := strings.TrimSpace(args["interval"]); raw != "" {
		delay, err := time.ParseDuration(raw)
		if err != nil {
			return nil, "", fmt.Errorf("invalid interval %q: %w (use a Go duration such as \"30s\" or \"5m\")", raw, err)
		}
		if delay <= 0 {
			return nil, "", fmt.Errorf("interval must be greater than zero, got %q", raw)
		}
		return cron.Every(delay), raw, nil
	}

	expr := strings.TrimSpace(args["cron"])
	if expr == "" {
		expr = defaultSpec
	}
	schedule, err := parser.Parse(expr)
	if err != nil {
		return nil, "", fmt.Errorf("invalid cron expression %q: %w", expr, err)
	}
	if tzExplicit {
		if spec, ok := schedule.(*cron.SpecSchedule); ok {
			spec.Location = loc
		}
	}
	return schedule, expr, nil
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
