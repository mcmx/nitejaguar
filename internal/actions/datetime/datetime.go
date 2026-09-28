// Package datetime implements a workflow action that returns the current
// date and time, mirroring the n8n Date & Time node "Get Current Date"
// operation in minimal form.
//
// Supported args (all optional, all resolve `$input.<path>` against the
// upstream result payload):
//   - operation:    operation to run. v1 implements only "getCurrentDate"
//     (aliases: "", "current", "now"). Unknown values emit an error result.
//   - format:       Go time layout for the output string. Empty defaults to
//     time.RFC3339. "unix"/"unix_ms" emit epoch seconds/millis instead.
//   - timezone:     IANA timezone name (e.g. "Europe/Madrid", "UTC").
//     Empty means local time.
//   - output_field: field name echoing the formatted value in the payload
//     (n8n parity). Defaults to "datetime".
//
// Extensibility: operations dispatch through the `operations` registry.
// To add a future n8n operation (addToDate, subtractFromDate, formatDate,
// extractPart, roundDate, getTimeBetweenDates), define its args struct,
// implement `operationFunc`, and register it in `operations` — no changes
// to Execute/sendResult plumbing required.
package datetime

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/mcmx/nitejaguar/common"
)

// Operation identifies a datetime computation (n8n operation parity).
type Operation string

const (
	// OpGetCurrentDate returns the current date/time (v1, implemented).
	OpGetCurrentDate Operation = "getCurrentDate"
	// Future n8n operations (registered when implemented).
	OpAddToDate        Operation = "addToDate"
	OpSubtractFromDate Operation = "subtractFromDate"
	OpFormatDate       Operation = "formatDate"
	OpExtractPart      Operation = "extractPart"
	OpRoundDate        Operation = "roundDate"
	OpGetTimeBetween   Operation = "getTimeBetweenDates"
)

// SupportedOperations lists operations the registry can run.
func SupportedOperations() []Operation {
	ops := make([]Operation, 0, len(operations))
	for op := range operations {
		ops = append(ops, op)
	}
	return ops
}

// operationFunc computes a datetime result for the given moment.
type operationFunc func(now time.Time, args map[string]string) (formatted string, extra map[string]any, err error)

var operations = map[Operation]operationFunc{
	OpGetCurrentDate: opGetCurrentDate,
	// Future: OpAddToDate: opAddToDate, ...
}

// nowFunc is overridable in tests for deterministic output.
var nowFunc = time.Now

type datetimeAction struct {
	data   common.ActionArgs
	events chan common.ResultData
}

// Execute resolves args, runs the operation, and emits a flat result payload:
//
//	{"type":"success","operation":"getCurrentDate","datetime":"...",
//	 "timestamp":..., "timestamp_ms":...,
//	 "format":"...","timezone":"...","output_field":"...",
//	 "<output_field>":"..." (when different from "datetime")}
func (d *datetimeAction) Execute(executionID string, inputs []any) {
	rawArgs, err := argsToStringMap(d.data.Args)
	if err != nil {
		d.sendResult(executionID, errorPayload("", err.Error()))
		return
	}
	trigger := findInputResult(inputs)

	opRaw, err := resolveArgValue(rawArgs["operation"], trigger)
	if err != nil {
		d.sendResult(executionID, errorPayload("", err.Error()))
		return
	}
	op := normalizeOperation(opRaw)

	format, err := resolveArgValue(firstNonEmpty(rawArgs["format"], rawArgs["output_format"]), trigger)
	if err != nil {
		d.sendResult(executionID, errorPayload(string(op), err.Error()))
		return
	}
	tz, err := resolveArgValue(firstNonEmpty(rawArgs["timezone"], rawArgs["tz"], rawArgs["time_zone"]), trigger)
	if err != nil {
		d.sendResult(executionID, errorPayload(string(op), err.Error()))
		return
	}
	outField, err := resolveArgValue(firstNonEmpty(rawArgs["output_field"], rawArgs["outputField"], rawArgs["field"]), trigger)
	if err != nil {
		d.sendResult(executionID, errorPayload(string(op), err.Error()))
		return
	}
	if outField == "" {
		outField = "datetime"
	}

	fn, ok := operations[op]
	if !ok {
		d.sendResult(executionID, errorPayload(string(op),
			fmt.Sprintf("unknown operation %q (supported: %s)", rawArgs["operation"], supportedList())))
		return
	}

	loc := time.Local
	tzName := "Local"
	if tz != "" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			d.sendResult(executionID, errorPayload(string(op), fmt.Sprintf("invalid timezone %q: %v", tz, err)))
			return
		}
		loc = l
		tzName = tz
	}

	now := nowFunc().In(loc)
	// Hand the resolved format/timezone downstream via well-known keys so
	// operation funcs stay decoupled from arg naming.
	opArgs := map[string]string{"format": format, "timezone": tzName}
	formatted, extra, err := fn(now, opArgs)
	if err != nil {
		d.sendResult(executionID, errorPayload(string(op), err.Error()))
		return
	}

	payload := map[string]any{
		"type":         "success",
		"operation":    string(op),
		"datetime":     formatted,
		"timestamp":    now.Unix(),
		"timestamp_ms": now.UnixMilli(),
		"format":       common.EffectiveTimestampFormat(format),
		"timezone":     tzName,
		"output_field": outField,
	}
	for k, v := range extra {
		payload[k] = v
	}
	if outField != "" && outField != "datetime" {
		payload[outField] = formatted
	}
	d.sendResult(executionID, payload)
}

func (d *datetimeAction) Stop() error {
	return nil
}

func (d *datetimeAction) GetArgs() common.ActionArgs {
	return d.data
}

// New creates the action; ActionType is forced to "action".
func New(events chan common.ResultData, data common.ActionArgs) (common.Action, error) {
	s := &datetimeAction{events: events, data: data}
	s.data.ActionType = "action"
	return s, nil
}

func (d *datetimeAction) sendResult(executionID string, payload map[string]any) {
	d.events <- common.ResultData{
		ExecutionID: executionID,
		ActionID:    d.data.Id,
		ActionType:  d.data.ActionType,
		ActionName:  d.data.ActionName,
		Payload:     payload,
	}
}

func errorPayload(operation, msg string) map[string]any {
	return map[string]any{"type": "error", "operation": operation, "result": msg}
}

// opGetCurrentDate formats now using format (Go layout, "" => RFC3339,
// "unix"/"unix_ms" => epoch strings) through the shared timestamp formatter,
// so the cron trigger produces byte-identical values for the same spec.
func opGetCurrentDate(now time.Time, args map[string]string) (string, map[string]any, error) {
	return common.FormatTimestamp(now, args["format"]), nil, nil
}

func supportedList() string {
	ops := SupportedOperations()
	names := make([]string, len(ops))
	for i, op := range ops {
		names[i] = string(op)
	}
	return strings.Join(names, ", ")
}

// normalizeOperation maps empty/aliases to the canonical operation.
func normalizeOperation(raw string) Operation {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "getcurrentdate", "current", "now", "get_current_date", "get-current-date":
		return OpGetCurrentDate
	case "addtodate", "add_to_date", "add-to-date", "add":
		return OpAddToDate
	case "subtractfromdate", "subtract_from_date", "subtract":
		return OpSubtractFromDate
	case "formatdate", "format_date", "format":
		return OpFormatDate
	case "extractpart", "extract_part", "extract":
		return OpExtractPart
	case "rounddate", "round_date", "round":
		return OpRoundDate
	case "gettimebetweendates", "get_time_between_dates", "timebetween", "diff":
		return OpGetTimeBetween
	default:
		return Operation(raw)
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// argsToStringMap normalizes action args without panicking. Accepts
// map[string]string, map[string]any, and any other map with string keys.
// Values are stringified. A nil map means "no args" (defaults apply);
// non-map args return an explicit error.
func argsToStringMap(args any) (map[string]string, error) {
	if args == nil {
		return map[string]string{}, nil
	}
	if m, ok := args.(map[string]string); ok {
		out := make(map[string]string, len(m))
		for k, v := range m {
			out[k] = v
		}
		return out, nil
	}
	if m, ok := args.(map[string]any); ok {
		out := make(map[string]string, len(m))
		for k, v := range m {
			out[k] = stringifyArgValue(v)
		}
		return out, nil
	}
	v := reflect.ValueOf(args)
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return map[string]string{}, nil
		}
		v = v.Elem()
	}
	if v.Kind() == reflect.Map {
		if v.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("invalid arguments type %T: map key must be string", args)
		}
		out := make(map[string]string, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out[iter.Key().String()] = stringifyArgValue(iter.Value().Interface())
		}
		return out, nil
	}
	return nil, fmt.Errorf("invalid arguments type %T: must be a map", args)
}

func stringifyArgValue(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Interface || rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return ""
		}
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.String {
		return rv.String()
	}
	return fmt.Sprint(v)
}

// findInputResult returns the first ResultData found in inputs, if any.
func findInputResult(inputs []any) *common.ResultData {
	for _, in := range inputs {
		switch v := in.(type) {
		case common.ResultData:
			c := v
			return &c
		case *common.ResultData:
			if v != nil {
				return v
			}
		}
	}
	return nil
}

var resultRefPattern = regexp.MustCompile(`\$input\.[A-Za-z0-9_.\[\]]+`)

// resolveArgValue resolves inline `$input.<path>` references against the
// upstream result payload. Literals pass through. `$result.`/`$args.` are
// rejected: the node's own result does not exist yet at resolution time.
func resolveArgValue(raw string, input *common.ResultData) (string, error) {
	if raw == "" {
		return "", nil
	}
	if strings.Contains(raw, "$result.") || strings.Contains(raw, "$args.") {
		return "", fmt.Errorf("unsupported reference in %q: only $input. resolves against upstream payload in action args", raw)
	}
	out := raw
	if !strings.Contains(out, "$input.") {
		return out, nil
	}
	if input == nil {
		return "", fmt.Errorf("no input result available to resolve %q", raw)
	}
	var resolveErr error
	out = resultRefPattern.ReplaceAllStringFunc(out, func(m string) string {
		if resolveErr != nil {
			return m
		}
		val, err := common.ResolveRefPath(input.Payload, m)
		if err != nil {
			resolveErr = err
			return m
		}
		return stringifyArgValue(val)
	})
	if resolveErr != nil {
		return "", resolveErr
	}
	return out, nil
}
