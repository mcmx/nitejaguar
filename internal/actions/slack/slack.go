// Package slack implements the `slack` workflow action: it posts a message
// to Slack through an incoming webhook URL.
//
// Arguments (all templatable via the shared engine):
//   - webhook_url: Slack incoming webhook URL (https://hooks.slack.com/...).
//     Optional when the node's credential_ref resolves to it (see below);
//     one of the two is required.
//   - text: message text (required). Supports $input refs and {{...}}
//     templates, including the shared filter chain.
//   - channel: channel override (optional, e.g. "#alerts"). Incoming
//     webhooks created after 2021 ignore it; it is sent when set.
//   - username: bot display-name override (optional).
//   - timeout: HTTP timeout in seconds (default 10, max 60).
//
// Auth: the webhook URL itself is the secret. Prefer the node's
// credential_ref (a `token` or `generic` credential holding the URL),
// injected just-in-time by the framework as a common.CredentialBinding —
// workflow JSON then carries no secret. A literal `webhook_url` arg is
// accepted for local dev. Any other credential type fails closed, as does
// an unresolvable credential_ref (no anonymous post is possible).
//
// Templating uses the shared engine only (common.SplitTemplates /
// common.EvalTemplate / common.ResolveRefPath): every string field supports
// bare $input.<path> refs and {{...}} templates. $result./$args. refs are
// rejected (own result does not exist yet).
//
// Payload (conditions API): success is
// {type: "success", status: <http-status>, result: "message posted"} —
// route on $result.type / $result.status. Non-2xx responses and transport
// errors emit {type: "error", status?, result: message} with the upstream
// body truncated, so a failing alert never blows up the result store.
package slack

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

// defaultTimeout is the HTTP timeout in seconds when the `timeout` arg is
// absent; maxTimeout caps an explicit value.
const defaultTimeout = 10
const maxTimeout = 60

// maxErrorBody caps how much of a non-2xx response body is kept in the
// error result.
const maxErrorBody = 2048

type slackAction struct {
	data   common.ActionArgs
	events chan common.ResultData
}

// New creates the action; ActionType is forced to "action". A literal
// (non-templated) webhook_url is validated here so a bad definition fails
// at load; templated values defer to Execute.
func New(events chan common.ResultData, data common.ActionArgs) (common.Action, error) {
	s := &slackAction{events: events, data: data}
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

func (s *slackAction) Stop() error { return nil }

func (s *slackAction) GetArgs() common.ActionArgs { return s.data }

func (s *slackAction) send(executionID string, payload map[string]any) {
	s.events <- common.ResultData{
		ExecutionID: executionID,
		ActionID:    s.data.Id,
		ActionType:  s.data.ActionType,
		ActionName:  s.data.ActionName,
		Payload:     payload,
	}
}

// Execute resolves templates, builds the webhook payload, and posts it.
func (s *slackAction) Execute(executionID string, inputs []any) {
	rawArgs, err := rawArgsMap(s.data.Args)
	if err != nil {
		s.send(executionID, errorPayload(0, err.Error()))
		return
	}
	input := findInputResult(inputs)
	cred := common.FindCredential(inputs)

	resolved, err := resolveFields(rawArgs, input)
	if err != nil {
		s.send(executionID, errorPayload(0, err.Error()))
		return
	}
	webhookURL := resolved.webhookURL
	if webhookURL == "" {
		webhookURL, err = webhookURLFromCredential(cred)
		if err != nil {
			s.send(executionID, errorPayload(0, err.Error()))
			return
		}
	}
	if err := validateResolved(webhookURL, resolved.text); err != nil {
		s.send(executionID, errorPayload(0, err.Error()))
		return
	}
	if err := validateURL(webhookURL); err != nil {
		s.send(executionID, errorPayload(0, err.Error()))
		return
	}
	timeout, err := parseTimeout(resolved.timeout)
	if err != nil {
		s.send(executionID, errorPayload(0, err.Error()))
		return
	}
	body := map[string]any{"text": resolved.text}
	if resolved.channel != "" {
		body["channel"] = resolved.channel
	}
	if resolved.username != "" {
		body["username"] = resolved.username
	}
	status, err := postJSON(webhookURL, body, timeout)
	if err != nil {
		s.send(executionID, errorPayload(status, err.Error()))
		return
	}
	s.send(executionID, map[string]any{
		"type": "success", "status": status,
		"result": "Slack message posted",
	})
}

func errorPayload(status int, msg string) map[string]any {
	payload := map[string]any{"type": "error", "result": msg}
	if status != 0 {
		payload["status"] = status
	}
	return payload
}

// resolvedFields holds the templated string fields after resolution.
type resolvedFields struct {
	webhookURL string
	text       string
	channel    string
	username   string
	timeout    string
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
	if r.webhookURL, err = get("webhook_url", "webhookUrl", "url"); err != nil {
		return r, err
	}
	if r.text, err = get("text", "message"); err != nil {
		return r, err
	}
	if r.channel, err = get("channel"); err != nil {
		return r, err
	}
	if r.username, err = get("username", "user"); err != nil {
		return r, err
	}
	if r.timeout, err = get("timeout", "timeout_seconds", "timeoutSeconds"); err != nil {
		return r, err
	}
	return r, nil
}

// validateStatic rejects a bad literal webhook_url at load while letting
// templated values ($input./{{...}}) through to Execute-time resolution.
func validateStatic(rawArgs map[string]any) error {
	v, ok := firstPresent(rawArgs, "webhook_url", "webhookUrl", "url")
	if !ok {
		return nil
	}
	s, ok := stringifyScalar(v)
	if !ok {
		if str, isStr := v.(string); isStr {
			s = str
		} else {
			return nil
		}
	}
	if looksTemplated(s) {
		return nil
	}
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return validateURL(s)
}

func validateResolved(webhookURL, text string) error {
	if strings.TrimSpace(webhookURL) == "" {
		return fmt.Errorf("missing webhook_url argument (or credential_ref holding the Slack webhook URL)")
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("missing text argument (message text)")
	}
	return nil
}

// validateURL accepts only http/https URLs: the webhook target must be an
// explicit web endpoint, never a file path or other scheme.
func validateURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid webhook_url %q: %v", raw, err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("invalid webhook_url %q: scheme must be https (http allowed for local dev)", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("invalid webhook_url %q: missing host", raw)
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

// webhookURLFromCredential resolves the Slack webhook URL from the
// framework-injected credential binding. Only single-secret types (token,
// generic) are accepted; anything else fails closed so a structured secret
// (e.g. username_password) is never misused as a URL.
func webhookURLFromCredential(cred *common.CredentialBinding) (string, error) {
	if cred != nil && strings.TrimSpace(cred.Err) != "" {
		return "", fmt.Errorf("cannot resolve credential %q: %s", cred.Ref, cred.Err)
	}
	if cred == nil || strings.TrimSpace(cred.Secret) == "" {
		return "", nil
	}
	if cred.Type != "" && cred.Type != "token" && cred.Type != "generic" {
		return "", fmt.Errorf("credential type %q does not match slack action (requires token or generic holding the webhook URL)", cred.Type)
	}
	fields, err := common.DecodeCredentialSecret(credTypeOrGeneric(cred.Type), cred.Secret)
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

func credTypeOrGeneric(ctype string) string {
	if strings.TrimSpace(ctype) == "" {
		return "generic"
	}
	return ctype
}

// postFunc is the HTTP transport (overridable in tests).
var postFunc = defaultPost

func postJSON(webhookURL string, body map[string]any, timeout time.Duration) (int, error) {
	return postFunc(webhookURL, body, timeout)
}

// defaultPost delivers the message and maps non-2xx responses to errors
// carrying the (truncated) upstream body for diagnostics.
func defaultPost(webhookURL string, body map[string]any, timeout time.Duration) (int, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, fmt.Errorf("cannot encode slack payload: %v", err)
	}
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodPost, webhookURL, bytes.NewReader(raw))
	if err != nil {
		return 0, fmt.Errorf("cannot build slack request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("slack post failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		msg := strings.TrimSpace(string(snippet))
		if msg == "" {
			msg = resp.Status
		}
		return resp.StatusCode, fmt.Errorf("slack post failed with status %d: %s", resp.StatusCode, msg)
	}
	return resp.StatusCode, nil
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
