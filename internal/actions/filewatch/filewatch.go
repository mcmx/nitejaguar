// Package filewatch implements the file-arrival watchdog trigger: it
// watches a directory for files matching a pattern and enforces an
// expectation window around their arrival.
//
// Two modes share one loop:
//
//   - Watch mode (no schedule): the window opens when the trigger starts.
//     Every matching arrival emits a success result and re-opens the
//     window; a window that expires with no arrival emits a missing result
//     and re-opens. This answers "alert me if no file shows up for X".
//   - Schedule mode (`interval` or `cron` set): each scheduled tick opens
//     a fresh window of `expect_within`. An arrival inside an open window
//     emits success and closes it; an expiry emits missing and closes it.
//     This answers "a file is due on this schedule, plus a grace period".
//     A tick restarts a still-open window, so the schedule period should
//     exceed `expect_within`, or missing results never fire.
//
// Downstream nodes route on the payload: `$result.type == "missing"`
// feeds the alert action (email, slack, webhook), `$result.type ==
// "success"` continues the normal path.
package filewatch

import (
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mcmx/nitejaguar/common"

	"github.com/fsnotify/fsnotify"
	"github.com/robfig/cron/v3"
)

// parser accepts the same schedules as the cron trigger: standard 5-field
// expressions, an optional leading seconds field, and @descriptors.
var parser = cron.NewParser(
	cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

type watchdog struct {
	data   common.ActionArgs
	events chan common.ResultData

	watcher *fsnotify.Watcher

	dir             string
	pattern         string
	eventMask       fsnotify.Op
	expectWithin    time.Duration
	expectWithinRaw string

	// schedule is nil in watch mode; otherwise each tick opens a window.
	schedule     cron.Schedule
	scheduleDesc string

	debounceMs int
	mu         sync.Mutex
	last       map[string]time.Time

	stopOnce sync.Once
	stop     chan struct{}
}

// New validates the trigger arguments so a bad definition fails while the
// workflow loads instead of silently never firing.
func New(events chan common.ResultData, data common.ActionArgs) (common.Action, error) {
	args, err := argsToMap(data.Args)
	if err != nil {
		return nil, err
	}

	rawPath := strings.TrimSpace(args["path"])
	if rawPath == "" {
		return nil, fmt.Errorf("path is required (directory to watch)")
	}
	dir, err := common.ExpandPath(rawPath)
	if err != nil {
		return nil, fmt.Errorf("invalid path %q: %w", rawPath, err)
	}

	rawWithin := strings.TrimSpace(args["expect_within"])
	if rawWithin == "" {
		return nil, fmt.Errorf("expect_within is required (e.g. \"30m\")")
	}
	within, err := time.ParseDuration(rawWithin)
	if err != nil {
		return nil, fmt.Errorf("invalid expect_within %q: %w (use a Go duration such as \"30m\" or \"1h\")", rawWithin, err)
	}
	if within <= 0 {
		return nil, fmt.Errorf("expect_within must be greater than zero, got %q", rawWithin)
	}

	pattern := strings.TrimSpace(args["pattern"])
	if pattern == "" {
		pattern = "*"
	}
	if _, err := path.Match(pattern, ""); err != nil {
		return nil, fmt.Errorf("invalid pattern %q: %w", pattern, err)
	}

	mask, err := parseEventTypes(args["event_type"])
	if err != nil {
		return nil, err
	}

	debounceMs := 500
	if raw, ok := args["debounce_ms"]; ok && strings.TrimSpace(raw) != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("invalid debounce_ms %q: %w", raw, err)
		}
		if parsed < 0 {
			return nil, fmt.Errorf("debounce_ms must not be negative, got %q", raw)
		}
		debounceMs = parsed
	}

	schedule, desc, err := buildSchedule(args)
	if err != nil {
		return nil, err
	}

	// Timezone only drives cron field matching, but a typo must fail at
	// load even in watch mode, where no schedule consumes it.
	if tzRaw := strings.TrimSpace(args["timezone"]); tzRaw != "" {
		if _, err := time.LoadLocation(tzRaw); err != nil {
			return nil, fmt.Errorf("invalid timezone %q: %w", tzRaw, err)
		}
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}

	t := &watchdog{
		data:            data,
		events:          events,
		watcher:         watcher,
		dir:             dir,
		pattern:         pattern,
		eventMask:       mask,
		expectWithin:    within,
		expectWithinRaw: rawWithin,
		schedule:        schedule,
		scheduleDesc:    desc,
		debounceMs:      debounceMs,
		last:            make(map[string]time.Time),
		stop:            make(chan struct{}),
	}
	t.data.ActionType = "trigger"
	log.Println("Initializing File Watchdog Trigger with id:", t.data.Id, "dir:", dir, "expect_within:", rawWithin)

	return t, nil
}

// Execute blocks for the lifetime of the trigger: it watches the directory,
// opens expectation windows, and emits success/missing results until Stop.
func (t *watchdog) Execute(executionId string, inputs []any) {
	log.Println("Executing File Watchdog Trigger with id:", t.data.Id)
	info, err := os.Stat(t.dir)
	if err != nil {
		log.Printf("[filewatch] Cannot watch %q: %v", t.dir, err)
		return
	}
	if !info.IsDir() {
		log.Printf("[filewatch] Cannot watch %q: not a directory", t.dir)
		return
	}
	if err := t.watcher.Add(t.dir); err != nil {
		log.Printf("[filewatch] Error adding watcher for path %q: %v", t.dir, err)
		return
	}

	var deadline *time.Timer
	var deadlineCh <-chan time.Time
	windowOpen := false
	openWindow := func() {
		windowOpen = true
		if deadline == nil {
			deadline = time.NewTimer(t.expectWithin)
		} else {
			if !deadline.Stop() {
				select {
				case <-deadline.C:
				default:
				}
			}
			deadline.Reset(t.expectWithin)
		}
		deadlineCh = deadline.C
	}
	closeWindow := func() {
		windowOpen = false
		deadlineCh = nil
		if deadline != nil {
			if !deadline.Stop() {
				select {
				case <-deadline.C:
				default:
				}
			}
		}
	}

	// The first window opens at start in both modes.
	openWindow()

	var schedTimer *time.Timer
	var schedCh <-chan time.Time
	armSchedule := func() {
		if t.schedule == nil {
			return
		}
		next := t.schedule.Next(time.Now())
		if next.IsZero() {
			log.Printf("[filewatch] schedule %q has no future fire time", t.scheduleDesc)
			return
		}
		delay := time.Until(next)
		if delay < 0 {
			delay = 0
		}
		if schedTimer == nil {
			schedTimer = time.NewTimer(delay)
		} else {
			if !schedTimer.Stop() {
				select {
				case <-schedTimer.C:
				default:
				}
			}
			schedTimer.Reset(delay)
		}
		schedCh = schedTimer.C
	}
	armSchedule()
	defer func() {
		if deadline != nil {
			deadline.Stop()
		}
		if schedTimer != nil {
			schedTimer.Stop()
		}
	}()

	for {
		select {
		case <-t.stop:
			return
		case event, ok := <-t.watcher.Events:
			if !ok {
				return
			}
			if event.Op&t.eventMask == 0 {
				continue
			}
			matched, err := path.Match(t.pattern, filepath.Base(event.Name))
			if err != nil || !matched {
				continue
			}
			if t.debounced(event.Name) {
				continue
			}
			t.events <- t.successResult(executionId, strings.ToLower(event.Op.String()), event.Name)
			if t.schedule == nil {
				openWindow() // watch mode: the arrival restarts the clock
			} else {
				closeWindow() // schedule mode: the arrival satisfies the window
			}
		case err, ok := <-t.watcher.Errors:
			if !ok {
				return
			}
			log.Println("[filewatch] watcher error:", err)
		case <-deadlineCh:
			if !windowOpen {
				continue
			}
			t.events <- t.missingResult(executionId)
			if t.schedule == nil {
				openWindow() // watch mode: keep watching after a miss
			} else {
				closeWindow() // schedule mode: wait for the next tick
			}
		case <-schedCh:
			schedCh = nil
			openWindow() // a tick opens (or restarts) the window
			armSchedule()
		}
	}
}

// Stop ends the Execute loop. It is idempotent and safe to call while the
// trigger is mid-wait, or before Execute ever ran.
func (t *watchdog) Stop() error {
	log.Println("Stopping the filewatch trigger", t.data.Id)
	t.stopOnce.Do(func() { close(t.stop) })
	return t.watcher.Close()
}

// GetArgs returns the ActionArgs associated with the filewatch trigger.
func (t *watchdog) GetArgs() common.ActionArgs {
	return t.data
}

func (t *watchdog) debounced(file string) bool {
	if t.debounceMs <= 0 {
		return false
	}
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	if previous, ok := t.last[file]; ok && now.Sub(previous) < time.Duration(t.debounceMs)*time.Millisecond {
		t.last[file] = now
		return true
	}
	t.last[file] = now
	return false
}

func (t *watchdog) basePayload() map[string]any {
	return map[string]any{
		"trigger":       "filewatch",
		"path":          t.dir,
		"pattern":       t.pattern,
		"expect_within": t.expectWithinRaw,
		"schedule":      t.scheduleDesc,
	}
}

func (t *watchdog) successResult(executionId, eventType, file string) common.ResultData {
	payload := t.basePayload()
	payload["type"] = "success"
	payload["event"] = eventType
	payload["file"] = file
	return common.ResultData{
		ExecutionID: executionId,
		ActionID:    t.data.Id,
		ActionType:  t.data.ActionType,
		ActionName:  t.data.ActionName,
		Payload:     payload,
	}
}

func (t *watchdog) missingResult(executionId string) common.ResultData {
	payload := t.basePayload()
	payload["type"] = "missing"
	return common.ResultData{
		ExecutionID: executionId,
		ActionID:    t.data.Id,
		ActionType:  t.data.ActionType,
		ActionName:  t.data.ActionName,
		Payload:     payload,
	}
}

// buildSchedule resolves `interval` (which wins when set) or `cron` into a
// schedule, or nil for watch mode when neither is set.
func buildSchedule(args map[string]string) (cron.Schedule, string, error) {
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
		return nil, "", nil
	}
	schedule, err := parser.Parse(expr)
	if err != nil {
		return nil, "", fmt.Errorf("invalid cron expression %q: %w", expr, err)
	}
	if tzRaw := strings.TrimSpace(args["timezone"]); tzRaw != "" {
		loc, err := time.LoadLocation(tzRaw)
		if err != nil {
			return nil, "", fmt.Errorf("invalid timezone %q: %w", tzRaw, err)
		}
		if spec, ok := schedule.(*cron.SpecSchedule); ok {
			spec.Location = loc
		}
	}
	return schedule, expr, nil
}

// parseEventTypes reads the optional event_type filter (default: create).
// Unknown event names are rejected so typos fail at load, not as silence.
func parseEventTypes(raw string) (fsnotify.Op, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fsnotify.Create, nil
	}
	var mask fsnotify.Op
	for _, part := range strings.Split(raw, ",") {
		switch strings.TrimSpace(strings.ToLower(part)) {
		case "create":
			mask |= fsnotify.Create
		case "write":
			mask |= fsnotify.Write
		case "rename":
			mask |= fsnotify.Rename
		case "remove":
			mask |= fsnotify.Remove
		case "chmod":
			mask |= fsnotify.Chmod
		case "":
		default:
			return 0, fmt.Errorf("invalid event_type %q (use one or more of create, write, rename, remove, chmod)", strings.TrimSpace(part))
		}
	}
	if mask == 0 {
		return fsnotify.Create, nil
	}
	return mask, nil
}

// argsToMap normalizes node args, treating a nil Args as "no arguments".
func argsToMap(raw any) (map[string]string, error) {
	if raw == nil {
		return map[string]string{}, nil
	}
	return common.ArgsToStringMap(raw)
}
