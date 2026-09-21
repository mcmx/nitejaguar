// Package wait implements an action that pauses a workflow for a human-friendly
// duration before allowing the next node to run.
package wait

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mcmx/nitejaguar/common"
)

var durationPart = regexp.MustCompile(`(?i)([+]?(?:\d+(?:\.\d*)?|\.\d+))\s*([a-zµμ]+)`)

var units = map[string]time.Duration{
	"ns": time.Nanosecond, "nanosecond": time.Nanosecond, "nanoseconds": time.Nanosecond,
	"us": time.Microsecond, "µs": time.Microsecond, "μs": time.Microsecond,
	"microsecond": time.Microsecond, "microseconds": time.Microsecond,
	"ms": time.Millisecond, "millisecond": time.Millisecond, "milliseconds": time.Millisecond,
	"s": time.Second, "sec": time.Second, "secs": time.Second, "second": time.Second, "seconds": time.Second,
	"m": time.Minute, "min": time.Minute, "mins": time.Minute, "minute": time.Minute, "minutes": time.Minute,
	"h": time.Hour, "hr": time.Hour, "hrs": time.Hour, "hour": time.Hour, "hours": time.Hour,
	"d": 24 * time.Hour, "day": 24 * time.Hour, "days": 24 * time.Hour,
	"w": 7 * 24 * time.Hour, "wk": 7 * 24 * time.Hour, "wks": 7 * 24 * time.Hour, "week": 7 * 24 * time.Hour, "weeks": 7 * 24 * time.Hour,
	"fn": 14 * 24 * time.Hour, "fortnight": 14 * 24 * time.Hour, "fortnights": 14 * 24 * time.Hour,
	"mo": 30 * 24 * time.Hour, "month": 30 * 24 * time.Hour, "months": 30 * 24 * time.Hour,
	"q": 90 * 24 * time.Hour, "quarter": 90 * 24 * time.Hour, "quarters": 90 * 24 * time.Hour,
	"y": 365 * 24 * time.Hour, "yr": 365 * 24 * time.Hour, "year": 365 * 24 * time.Hour, "years": 365 * 24 * time.Hour,
}

type waitAction struct {
	data   common.ActionArgs
	events chan common.ResultData
}

// New creates a waitAction. The duration is read when Execute is called, so
// values such as "$input.delay" can be supplied by an upstream action.
func New(events chan common.ResultData, data common.ActionArgs) (common.Action, error) {
	data.ActionType = "action"
	return &waitAction{events: events, data: data}, nil
}

func (w *waitAction) Execute(executionID string, inputs []any) {
	args, err := common.ArgsToStringMap(w.data.Args)
	if err != nil {
		w.send(executionID, map[string]any{"type": "error", "result": err.Error()})
		return
	}
	raw := first(args, "duration", "wait", "value", "time")
	raw, err = resolveInput(raw, inputs)
	if err != nil {
		w.send(executionID, map[string]any{"type": "error", "result": err.Error()})
		return
	}
	d, err := ParseDuration(raw)
	if err != nil {
		w.send(executionID, map[string]any{"type": "error", "duration": raw, "result": err.Error()})
		return
	}
	time.Sleep(d)
	w.send(executionID, map[string]any{
		"type": "success", "duration": raw, "duration_ms": float64(d) / float64(time.Millisecond),
	})
}

func (w *waitAction) Stop() error { return nil }

func (w *waitAction) GetArgs() common.ActionArgs { return w.data }

func (w *waitAction) send(executionID string, payload map[string]any) {
	w.events <- common.ResultData{ExecutionID: executionID, ActionID: w.data.Id, ActionType: w.data.ActionType, ActionName: w.data.ActionName, Payload: payload}
}

func first(args map[string]string, names ...string) string {
	for _, name := range names {
		if strings.TrimSpace(args[name]) != "" {
			return args[name]
		}
	}
	return ""
}

// ParseDuration accepts Go-like units plus days, weeks, fortnights, months,
// quarters, and years. Months/quarters/years intentionally use fixed lengths
// (30/90/365 days), since a wait has no calendar anchor. Parts may be combined:
// "1.5h", "1h 20m", and "2weeks, 3days" are all valid.
func ParseDuration(input string) (time.Duration, error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return 0, fmt.Errorf("missing duration (example: 1.5 minutes)")
	}
	var total float64
	position := 0
	parts := durationPart.FindAllStringSubmatchIndex(s, -1)
	if len(parts) == 0 {
		if n, err := strconv.ParseFloat(s, 64); err == nil && n >= 0 {
			return durationFromFloat(n, time.Second)
		}
		return 0, fmt.Errorf("invalid duration %q (use a number and unit, e.g. 1.5s or 2 days)", input)
	}
	for _, p := range parts {
		separator := strings.TrimFunc(s[position:p[0]], func(r rune) bool { return unicode.IsSpace(r) || r == ',' })
		if separator != "" {
			return 0, fmt.Errorf("invalid duration %q near %q", input, separator)
		}
		n, err := strconv.ParseFloat(s[p[2]:p[3]], 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid duration number in %q", input)
		}
		unit, ok := units[strings.ToLower(s[p[4]:p[5]])]
		if !ok {
			return 0, fmt.Errorf("unknown duration unit %q", s[p[4]:p[5]])
		}
		total += n * float64(unit)
		if math.IsInf(total, 0) || total > float64(math.MaxInt64) {
			return 0, fmt.Errorf("duration %q is too large", input)
		}
		position = p[1]
	}
	if strings.TrimFunc(s[position:], func(r rune) bool { return unicode.IsSpace(r) || r == ',' }) != "" {
		return 0, fmt.Errorf("invalid duration %q near %q", input, s[position:])
	}
	return time.Duration(total), nil
}

func durationFromFloat(value float64, unit time.Duration) (time.Duration, error) {
	ns := value * float64(unit)
	if math.IsInf(ns, 0) || ns > float64(math.MaxInt64) {
		return 0, fmt.Errorf("duration is too large")
	}
	return time.Duration(ns), nil
}

func resolveInput(raw string, inputs []any) (string, error) {
	if !strings.HasPrefix(raw, "$input.") {
		return raw, nil
	}
	path := strings.Split(strings.TrimPrefix(raw, "$input."), ".")
	for _, in := range inputs {
		result, ok := in.(common.ResultData)
		if !ok {
			if ptr, ok := in.(*common.ResultData); ok && ptr != nil {
				result = *ptr
			} else {
				continue
			}
		}
		value := result.Payload
		for _, key := range path {
			m, ok := value.(map[string]any)
			if !ok {
				return "", fmt.Errorf("cannot resolve %q from input", raw)
			}
			value, ok = m[key]
			if !ok {
				return "", fmt.Errorf("input field %q was not found", raw)
			}
		}
		return common.StringifyArgValue(value), nil
	}
	return "", fmt.Errorf("cannot resolve %q: no input result", raw)
}
