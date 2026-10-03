// Package pagerduty implements the `pagerduty` workflow action: it manages
// incidents through the PagerDuty Events API v2 (the "incident management"
// alerting channel — Opsgenie-style flows without a dedicated action go
// through the `http` action, see docs/actions/http-action.md).
//
// Arguments (all templatable via the shared engine):
//   - routing_key: Events API v2 integration key (required, unless the
//     node's credential_ref resolves to it — see below).
//   - event_action: trigger (default), acknowledge, or resolve.
//   - summary: incident summary (required for trigger, e.g. "Archive
//     failed: $input.result").
//   - source: event source (default "nitejaguar", e.g. hostname/client).
//   - severity: critical, error (default), warning, or info.
//   - dedup_key: dedup key (optional). PagerDuty collapses repeated events
//     with the same key into one incident instead of paging on every
//     retry — this is the answer to "a failing loop must not spam": set it
//     to a stable per-failure identity such as
//     "nitejaguar-{{ $input.workflow }}-archive".
//   - api_url: Events API endpoint override (default
//     https://events.pagerduty.com/v2/enqueue; tests point it at a local
//     server).
//   - timeout: HTTP timeout in seconds (default 10, max 60).
//
// Auth: the routing key is the secret. Prefer the node's credential_ref
// (a `token` or `generic` credential holding the key), injected just-in-time
// by the framework as a common.CredentialBinding — workflow JSON then
// carries no secret. A literal `routing_key` arg is accepted for local dev.
// Any other credential type fails closed, as does an unresolvable
// credential_ref (no event is sent).
//
// Templating uses the shared engine only (common.SplitTemplates /
// common.EvalTemplate / common.ResolveRefPath): every string field supports
// bare $input.<path> refs and {{...}} templates. $result./$args. refs are
// rejected (own result does not exist yet).
//
// Payload (conditions API): accepted events emit
// {type: "success", event_action, status, dedup_key, result} — route on
// $result.type / $result.event_action / $result.dedup_key. Rejected events
// and transport errors emit {type: "error", event_action, status?,
// result: message} with the upstream error carried in result.
package pagerduty

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mcmx/nitejaguar/common"
)

// defaultAPIURL is the PagerDuty Events API v2 endpoint.
const defaultAPIURL = "https://events.pagerduty.com/v2/enqueue"

// defaultTimeout is the HTTP timeout in seconds when the `timeout` arg is
// absent; maxTimeout caps an explicit value.
const defaultTimeout = 10
const maxTimeout = 60

// maxErrorBody caps how much of a rejected-event response body is kept in
// the error result.
const maxErrorBody = 2048

type pagerdutyAction struct {
	data   common.ActionArgs
	events chan common.ResultData
}

// New creates the action; ActionType is forced to "action". Literal
// (non-templated) values are validated here so a bad definition fails at
// load; templated values defer to Execute.
func New(events chan common.ResultData, data common.ActionArgs) (common.Action, error) {
	s := &pagerdutyAction{events: events, data: data}
	s.data.ActionType = "action"
	rawArgs, err := rawArgsMap(data.Args)
	if err != nil {
		return nil, err
	}
	if err := validateStatic(rawArgs); err != nil {
		return nil, err
	}
	return s, nil
}

func (p *pagerdutyAction) Stop() error { return nil }

func (p *pagerdutyAction) GetArgs() common.ActionArgs { return p.data }

func (p *pagerdutyAction) send(executionID string, payload map[string]any) {
	p.events <- common.ResultData{
		ExecutionID: executionID,
		ActionID:    p.data.Id,
		ActionType:  p.data.ActionType,
		ActionName:  p.data.ActionName,
		Payload:     payload,
	}
}

// Execute resolves templates, builds the Events API v2 event, and sends it.
func (p *pagerdutyAction) Execute(executionID string, inputs []any) {
	rawArgs, err := rawArgsMap(p.data.Args)
	if err != nil {
		p.send(executionID, errorPayload("", 0, err.Error()))
		return
	}
	input := findInputResult(inputs)
	cred := common.FindCredential(inputs)

	resolved, err := resolveFields(rawArgs, input)
	if err != nil {
		p.send(executionID, errorPayload("", 0, err.Error()))
		return
	}
	routingKey := resolved.routingKey
	if routingKey == "" {
		routingKey, err = routingKeyFromCredential(cred)
		if err != nil {
			p.send(executionID, errorPayload("", 0, err.Error()))
			return
		}
	}
	eventAction, err := normalizeEventAction(resolved.eventAction)
	if err != nil {
		p.send(executionID, errorPayload("", 0, err.Error()))
		return
	}
	severity, err := normalizeSeverity(resolved.severity)
	if err != nil {
		p.send(executionID, errorPayload(eventAction, 0, err.Error()))
		return
	}
	if err := validateResolved(routingKey, eventAction, resolved.summary, resolved.dedupKey); err != nil {
		p.send(executionID, errorPayload(eventAction, 0, err.Error()))
		return
	}
	apiURL := strings.TrimSpace(resolved.apiURL)
	if apiURL == "" {
		apiURL = defaultAPIURL
	}
	if err := validateURL(apiURL); err != nil {
		p.send(executionID, errorPayload(eventAction, 0, err.Error()))
		return
	}
	timeout, err := parseTimeout(resolved.timeout)
	if err != nil {
		p.send(executionID, errorPayload(eventAction, 0, err.Error()))
		return
	}
	event := map[string]any{
		"routing_key":  routingKey,
		"event_action": eventAction,
	}
	if resolved.dedupKey != "" {
		event["dedup_key"] = resolved.dedupKey
	}
	if eventAction == "trigger" {
		source := resolved.source
		if source == "" {
			source = "nitejaguar"
		}
		event["payload"] = map[string]any{
			"summary":  resolved.summary,
			"source":   source,
			"severity": severity,
		}
	}
	status, respDedup, err := postEvent(apiURL, event, timeout)
	if err != nil {
		p.send(executionID, errorPayload(eventAction, status, err.Error()))
		return
	}
	dedup := resolved.dedupKey
	if respDedup != "" {
		dedup = respDedup
	}
	p.send(executionID, map[string]any{
		"type": "success", "event_action": eventAction,
		"status": status, "dedup_key": dedup,
		"result": fmt.Sprintf("PagerDuty event %s accepted", eventAction),
	})
}

func errorPayload(eventAction string, status int, msg string) map[string]any {
	payload := map[string]any{"type": "error", "result": msg}
	if eventAction != "" {
		payload["event_action"] = eventAction
	}
	if status != 0 {
		payload["status"] = status
	}
	return payload
}

// resolvedFields holds the templated string fields after resolution.
type resolvedFields struct {
	routingKey  string
	eventAction string
	summary     string
	source      string
	severity    string
	dedupKey    string
	apiURL      string
	timeout     string
}

func resolveFields(rawArgs map[string]any, input *common.ResultData) (resolvedFields, error) {
	get := func(names ...string) (string, error) {
		v, ok := firstPresent(rawArgs, names...)
		if !ok {
			return "", nil
		}
		s, ok := stringifyScalar(v)
		if !ok {
			str, isStr := v.(string)
			if !isStr {
				return "", fmt.Errorf("invalid value for %q: must be a string", names[0])
			}
			s = str
		}
		return resolveStringTemplate(s, input)
	}
	var r resolvedFields
	var err error
	if r.routingKey, err = get("routing_key", "routingKey", "integration_key", "service_key"); err != nil {
		return r, err
	}
	if r.eventAction, err = get("event_action", "eventAction", "action"); err != nil {
		return r, err
	}
	if r.summary, err = get("summary", "description", "title"); err != nil {
		return r, err
	}
	if r.source, err = get("source", "origin"); err != nil {
		return r, err
	}
	if r.severity, err = get("severity", "priority"); err != nil {
		return r, err
	}
	if r.dedupKey, err = get("dedup_key", "dedupKey", "dedup"); err != nil {
		return r, err
	}
	if r.apiURL, err = get("api_url", "apiUrl", "endpoint", "url"); err != nil {
		return r, err
	}
	if r.timeout, err = get("timeout", "timeout_seconds", "timeoutSeconds"); err != nil {
		return r, err
	}
	return r, nil
}

// validateStatic rejects bad literal values at load while letting templated
// values ($input./{{...}}/$result./$args.) through to Execute-time
// resolution.
func validateStatic(rawArgs map[string]any) error {
	strOf := func(names ...string) string {
		v, ok := firstPresent(rawArgs, names...)
		if !ok {
			return ""
		}
		if s, ok := stringifyScalar(v); ok {
			return s
		}
		if str, isStr := v.(string); isStr {
			return str
		}
		return ""
	}
	if a := strOf("event_action", "eventAction", "action"); a != "" && !looksTemplated(a) {
		if _, err := normalizeEventAction(a); err != nil {
			return err
		}
	}
	if s := strOf("severity", "priority"); s != "" && !looksTemplated(s) {
		if _, err := normalizeSeverity(s); err != nil {
			return err
		}
	}
	if u := strOf("api_url", "apiUrl", "endpoint", "url"); u != "" && !looksTemplated(u) {
		if err := validateURL(u); err != nil {
			return err
		}
	}
	if t := strOf("timeout", "timeout_seconds", "timeoutSeconds"); t != "" && !looksTemplated(t) {
		if _, err := parseTimeout(t); err != nil {
			return err
		}
	}
	return nil
}

func validateResolved(routingKey, eventAction, summary, dedupKey string) error {
	if strings.TrimSpace(routingKey) == "" {
		return fmt.Errorf("missing routing_key argument (or credential_ref holding the integration key)")
	}
	if eventAction == "trigger" && strings.TrimSpace(summary) == "" {
		return fmt.Errorf("missing summary argument (required for trigger)")
	}
	if eventAction != "trigger" && strings.TrimSpace(dedupKey) == "" {
		// Acknowledge/resolve without an explicit dedup_key cannot address
		// an incident: PagerDuty would open a new one instead.
		return fmt.Errorf("event_action %q needs dedup_key (otherwise PagerDuty opens a new incident instead of updating one)", eventAction)
	}
	return nil
}

// normalizeEventAction lowercases and validates the Events API v2 action.
func normalizeEventAction(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "trigger", "fire":
		return "trigger", nil
	case "acknowledge", "ack":
		return "acknowledge", nil
	case "resolve", "resolved":
		return "resolve", nil
	default:
		return "", fmt.Errorf("invalid event_action %q: use trigger, acknowledge, or resolve", raw)
	}
}

// normalizeSeverity lowercases and validates the payload severity.
func normalizeSeverity(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "error":
		return "error", nil
	case "critical", "crit":
		return "critical", nil
	case "warning", "warn":
		return "warning", nil
	case "info":
		return "info", nil
	default:
		return "", fmt.Errorf("invalid severity %q: use critical, error, warning, or info", raw)
	}
}

// validateURL accepts only http/https URLs.
func validateURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid api_url %q: %v", raw, err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("invalid api_url %q: scheme must be http or https", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("invalid api_url %q: missing host", raw)
	}
	return nil
}

func looksTemplated(s string) bool {
	// $result./$args. refs are unsupported in args, but they defer to
	// Execute-time resolution so the failure carries the explicit
	// "unsupported reference" error instead of a misleading shape error.
	return strings.Contains(s, "$input.") || strings.Contains(s, "{{") ||
		strings.Contains(s, "$result.") || strings.Contains(s, "$args.")
}

func parseTimeout(raw string) (time.Duration, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultTimeout * time.Second, nil
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil || n < 1 || n > maxTimeout {
		return 0, fmt.Errorf("invalid timeout %q: must be 1-%d seconds", raw, maxTimeout)
	}
	return time.Duration(n) * time.Second, nil
}

// routingKeyFromCredential resolves the integration key from the
// framework-injected credential binding. Only single-secret types (token,
// generic) are accepted; anything else fails closed so a structured secret
// is never misused as a key.
func routingKeyFromCredential(cred *common.CredentialBinding) (string, error) {
	if cred != nil && strings.TrimSpace(cred.Err) != "" {
		return "", fmt.Errorf("cannot resolve credential %q: %s", cred.Ref, cred.Err)
	}
	if cred == nil || strings.TrimSpace(cred.Secret) == "" {
		return "", nil
	}
	if cred.Type != "" && cred.Type != "token" && cred.Type != "generic" {
		return "", fmt.Errorf("credential type %q does not match pagerduty action (requires token or generic holding the routing key)", cred.Type)
	}
	ctype := cred.Type
	if strings.TrimSpace(ctype) == "" {
		ctype = "generic"
	}
	fields, err := common.DecodeCredentialSecret(ctype, cred.Secret)
	if err != nil {
		return "", fmt.Errorf("cannot decode credential %q: %v", cred.Ref, err)
	}
	for _, v := range fields {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v), nil
		}
	}
	return "", fmt.Errorf("credential %q holds an empty secret", cred.Ref)
}

// postFunc is the HTTP transport (overridable in tests).
var postFunc = defaultPost

func postEvent(apiURL string, event map[string]any, timeout time.Duration) (status int, dedupKey string, err error) {
	return postFunc(apiURL, event, timeout)
}

// eventResponse is the accepted-event shape of Events API v2
// ({"status":"success","message":"Event processed","dedup_key":"..."}).
type eventResponse struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	DedupKey string `json:"dedup_key"`
}

// defaultPost delivers the event. Non-2xx responses are errors carrying the
// upstream message, so a rejected event routes as a failure.
func defaultPost(apiURL string, event map[string]any, timeout time.Duration) (int, string, error) {
	raw, err := json.Marshal(event)
	if err != nil {
		return 0, "", fmt.Errorf("cannot encode pagerduty event: %v", err)
	}
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewReader(raw))
	if err != nil {
		return 0, "", fmt.Errorf("cannot build pagerduty request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("pagerduty post failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		return resp.StatusCode, "", fmt.Errorf("pagerduty event rejected with status %d: %s", resp.StatusCode, msg)
	}
	var parsed eventResponse
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.DedupKey != "" {
		return resp.StatusCode, parsed.DedupKey, nil
	}
	return resp.StatusCode, "", nil
}

// --- templating helpers (shared engine only) ---

var resultRefPattern = regexp.MustCompile(`\$input\.[A-Za-z0-9_.\[\]]+`)

// resolveStringTemplate expands {{...}} templates then bare $input. refs.
func resolveStringTemplate(raw string, input *common.ResultData) (string, error) {
	if raw == "" {
		return "", nil
	}
	if strings.Contains(raw, "$result.") || strings.Contains(raw, "$args.") {
		return "", fmt.Errorf("unsupported reference in %q: only $input. resolves against upstream payload in action args", raw)
	}
	if strings.Contains(raw, "{{") && !common.HasTemplate(raw) {
		return "", fmt.Errorf("invalid template in %q: unclosed \"{{\"", raw)
	}
	segs := common.SplitTemplates(raw)
	if len(segs) == 3 && segs[0] == "" && segs[2] == "" {
		v, err := common.EvalTemplate(segs[1], common.InputLookup(input))
		if err != nil {
			return "", err
		}
		return common.TemplateString(v), nil
	}
	var sb strings.Builder
	for i, seg := range segs {
		if i%2 == 1 {
			v, err := common.EvalTemplate(seg, common.InputLookup(input))
			if err != nil {
				return "", err
			}
			sb.WriteString(common.TemplateString(v))
			continue
		}
		if !strings.Contains(seg, "$input.") {
			sb.WriteString(seg)
			continue
		}
		if input == nil {
			return "", fmt.Errorf("no input result available to resolve %q", raw)
		}
		if len(segs) == 1 {
			if trimmed := strings.TrimSpace(seg); resultRefPattern.FindString(trimmed) == trimmed {
				val, err := common.ResolveRefPath(input.Payload, trimmed)
				if err != nil {
					return "", err
				}
				return common.TemplateString(val), nil
			}
		}
		var resolveErr error
		out := resultRefPattern.ReplaceAllStringFunc(seg, func(m string) string {
			if resolveErr != nil {
				return m
			}
			val, err := common.ResolveRefPath(input.Payload, m)
			if err != nil {
				resolveErr = err
				return m
			}
			return common.TemplateString(val)
		})
		if resolveErr != nil {
			return "", resolveErr
		}
		sb.WriteString(out)
	}
	return sb.String(), nil
}

// rawArgsMap normalizes action args while preserving value types.
// Nil means no args.
func rawArgsMap(args any) (map[string]any, error) {
	if args == nil {
		return map[string]any{}, nil
	}
	if m, ok := args.(map[string]any); ok {
		out := make(map[string]any, len(m))
		for k, v := range m {
			out[k] = v
		}
		return out, nil
	}
	if m, ok := args.(map[string]string); ok {
		out := make(map[string]any, len(m))
		for k, v := range m {
			out[k] = v
		}
		return out, nil
	}
	v := reflect.ValueOf(args)
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return map[string]any{}, nil
		}
		v = v.Elem()
	}
	if v.Kind() == reflect.Map {
		if v.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("invalid arguments type %T: map key must be string", args)
		}
		out := make(map[string]any, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out[iter.Key().String()] = iter.Value().Interface()
		}
		return out, nil
	}
	return nil, fmt.Errorf("invalid arguments type %T: must be a map", args)
}

func firstPresent(args map[string]any, names ...string) (any, bool) {
	for _, n := range names {
		if v, ok := args[n]; ok && v != nil && strings.TrimSpace(common.StringifyArgValue(v)) != "" {
			return v, true
		}
	}
	return nil, false
}

func stringifyScalar(v any) (string, bool) {
	if v == nil {
		return "", false
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Interface || rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return "", false
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return fmt.Sprint(rv.Interface()), true
	default:
		return "", false
	}
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
