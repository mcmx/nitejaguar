// Package httpcall implements the `http` workflow action: it performs one
// outbound HTTP request (the "outbound webhook call" alerting channel, and
// the generic escape hatch for incident-management APIs such as PagerDuty
// Events API v2 or Opsgenie — see docs/actions/http-action.md).
//
// Arguments (all templatable via the shared engine):
//   - url: target URL (required). Only http/https.
//   - method: HTTP method (default POST). One of GET, POST, PUT, PATCH,
//     DELETE, HEAD.
//   - headers: extra headers as a JSON object string or native map
//     (optional). Names and values support $input refs and {{...}}
//     templates.
//   - body: request body (optional, templated). After expansion, a body
//     that parses as JSON is sent as-is (with application/json as the
//     default Content-Type when none is set); anything else is sent as
//     text/plain. GET/HEAD never send a body.
//   - auth: "" (none) or "bearer" (optional). With "bearer" the node's
//     credential_ref (a `token` or `generic` credential) is injected
//     just-in-time as `Authorization: Bearer <secret>`.
//   - timeout: HTTP timeout in seconds (default 10, max 60).
//
// Templating uses the shared engine only (common.SplitTemplates /
// common.EvalTemplate / common.ResolveRefPath): every string field supports
// bare $input.<path> refs and {{...}} templates. $result./$args. refs are
// rejected (own result does not exist yet).
//
// Payload (conditions API): 2xx responses emit
// {type: "success", method, url, status, body, result}; the body is
// JSON-decoded when possible and kept as a string otherwise, so downstream
// conditions route on it via $result.body.<path>. Non-2xx responses and
// transport errors emit {type: "error", method, url, status?, body?,
// result: message} — a failed delivery is itself routable as a failure.
package httpcall

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

// maxResponseBody caps how much response body is kept in the result.
const maxResponseBody = 1 << 20 // 1 MiB

// allowedMethods is the set of HTTP methods the action supports.
var allowedMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"}

type httpAction struct {
	data   common.ActionArgs
	events chan common.ResultData
}

// New creates the action; ActionType is forced to "action". Literal
// (non-templated) url/method/timeout values are validated here so a bad
// definition fails at load; templated values defer to Execute.
func New(events chan common.ResultData, data common.ActionArgs) (common.Action, error) {
	s := &httpAction{events: events, data: data}
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

func (h *httpAction) Stop() error { return nil }

func (h *httpAction) GetArgs() common.ActionArgs { return h.data }

func (h *httpAction) send(executionID string, payload map[string]any) {
	h.events <- common.ResultData{
		ExecutionID: executionID,
		ActionID:    h.data.Id,
		ActionType:  h.data.ActionType,
		ActionName:  h.data.ActionName,
		Payload:     payload,
	}
}

// Execute resolves templates, performs the request, and emits the result.
func (h *httpAction) Execute(executionID string, inputs []any) {
	rawArgs, err := rawArgsMap(h.data.Args)
	if err != nil {
		h.send(executionID, errorPayload("", "", 0, nil, err.Error()))
		return
	}
	input := findInputResult(inputs)
	cred := common.FindCredential(inputs)

	resolved, err := resolveFields(rawArgs, input)
	if err != nil {
		h.send(executionID, errorPayload("", "", 0, nil, err.Error()))
		return
	}
	method, err := normalizeMethod(resolved.method)
	if err != nil {
		h.send(executionID, errorPayload("", resolved.url, 0, nil, err.Error()))
		return
	}
	if strings.TrimSpace(resolved.url) == "" {
		h.send(executionID, errorPayload(method, "", 0, nil, "missing url argument (target URL)"))
		return
	}
	if err := validateURL(resolved.url); err != nil {
		h.send(executionID, errorPayload(method, resolved.url, 0, nil, err.Error()))
		return
	}
	headers, err := resolveHeaders(rawArgs, input)
	if err != nil {
		h.send(executionID, errorPayload(method, resolved.url, 0, nil, err.Error()))
		return
	}
	auth, err := resolveAuth(resolved.auth, cred)
	if err != nil {
		h.send(executionID, errorPayload(method, resolved.url, 0, nil, err.Error()))
		return
	}
	if auth != "" {
		headers["Authorization"] = "Bearer " + auth
	}
	timeout, err := parseTimeout(resolved.timeout)
	if err != nil {
		h.send(executionID, errorPayload(method, resolved.url, 0, nil, err.Error()))
		return
	}
	var bodyReader io.Reader
	contentType := headers["Content-Type"]
	if methodAllowsBody(method) && strings.TrimSpace(resolved.body) != "" {
		raw := []byte(resolved.body)
		if contentType == "" {
			if isJSON(raw) {
				contentType = "application/json"
			} else {
				contentType = "text/plain; charset=utf-8"
			}
		}
		bodyReader = bytes.NewReader(raw)
	}
	status, respBody, err := doRequest(method, resolved.url, headers, contentType, bodyReader, timeout)
	if err != nil {
		h.send(executionID, errorPayload(method, resolved.url, status, respBody, err.Error()))
		return
	}
	h.send(executionID, map[string]any{
		"type": "success", "method": method, "url": resolved.url,
		"status": status, "body": respBody,
		"result": fmt.Sprintf("HTTP %s %s -> %d", method, resolved.url, status),
	})
}

func errorPayload(method, rawURL string, status int, body any, msg string) map[string]any {
	payload := map[string]any{"type": "error", "result": msg}
	if method != "" {
		payload["method"] = method
	}
	if rawURL != "" {
		payload["url"] = rawURL
	}
	if status != 0 {
		payload["status"] = status
	}
	if body != nil {
		payload["body"] = body
	}
	return payload
}

// resolvedFields holds the templated scalar fields after resolution.
// Headers resolve separately (resolveHeaders) to preserve their shape.
type resolvedFields struct {
	url     string
	method  string
	body    string
	auth    string
	timeout string
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
	if r.url, err = get("url", "uri", "endpoint"); err != nil {
		return r, err
	}
	if r.method, err = get("method", "verb"); err != nil {
		return r, err
	}
	if r.method == "" {
		r.method = "POST"
	}
	if r.body, err = get("body", "data", "payload"); err != nil {
		return r, err
	}
	if r.auth, err = get("auth", "auth_type", "authType"); err != nil {
		return r, err
	}
	if r.timeout, err = get("timeout", "timeout_seconds", "timeoutSeconds"); err != nil {
		return r, err
	}
	return r, nil
}

// resolveHeaders normalizes the `headers` arg (JSON object string or native
// map) and templates each name and value.
func resolveHeaders(rawArgs map[string]any, input *common.ResultData) (map[string]string, error) {
	out := map[string]string{}
	v, ok := firstPresent(rawArgs, "headers", "header")
	if !ok {
		return out, nil
	}
	var obj map[string]any
	switch t := v.(type) {
	case map[string]any:
		obj = t
	case map[string]string:
		obj = make(map[string]any, len(t))
		for k, val := range t {
			obj[k] = val
		}
	case string:
		expanded := t
		if strings.Contains(t, "{{") {
			var err error
			expanded, err = common.ExpandJSONTemplates(t, common.InputLookup(input))
			if err != nil {
				return nil, err
			}
		} else if strings.Contains(t, "$input.") {
			var err error
			expanded, err = common.ExpandJSONRefs(t, common.InputLookup(input))
			if err != nil {
				return nil, err
			}
		}
		if err := json.Unmarshal([]byte(expanded), &obj); err != nil {
			return nil, fmt.Errorf("invalid headers JSON object: %v", err)
		}
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("invalid headers value: %v", err)
		}
		if err := json.Unmarshal(raw, &obj); err != nil {
			return nil, fmt.Errorf("invalid headers value: must be an object")
		}
	}
	for k, val := range obj {
		name, err := resolveStringTemplate(k, input)
		if err != nil {
			return nil, fmt.Errorf("header name %q: %w", k, err)
		}
		s, ok := stringifyScalar(val)
		if !ok {
			str, isStr := val.(string)
			if !isStr {
				return nil, fmt.Errorf("header %q: value must be a string", k)
			}
			s = str
		}
		value, err := resolveStringTemplate(s, input)
		if err != nil {
			return nil, fmt.Errorf("header %q: %w", k, err)
		}
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("header name must not be empty")
		}
		out[http.CanonicalHeaderKey(strings.TrimSpace(name))] = value
	}
	return out, nil
}

// resolveAuth maps the `auth` arg plus the injected credential binding to
// a bearer secret ("" = anonymous). Only single-secret types (token,
// generic) are accepted; anything else fails closed.
func resolveAuth(rawAuth string, cred *common.CredentialBinding) (string, error) {
	switch strings.ToLower(strings.TrimSpace(rawAuth)) {
	case "", "none", "anonymous":
		return "", nil
	case "bearer", "token":
		if cred != nil && strings.TrimSpace(cred.Err) != "" {
			return "", fmt.Errorf("cannot resolve credential %q: %s", cred.Ref, cred.Err)
		}
		if cred == nil || strings.TrimSpace(cred.Secret) == "" {
			return "", fmt.Errorf("auth %q needs a credential_ref holding the bearer token", rawAuth)
		}
		if cred.Type != "" && cred.Type != "token" && cred.Type != "generic" {
			return "", fmt.Errorf("credential type %q does not match http bearer auth (requires token or generic)", cred.Type)
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
	default:
		return "", fmt.Errorf("unknown auth %q: use \"\" (none) or \"bearer\"", rawAuth)
	}
}

// validateStatic rejects bad literal url/method/timeout values at load while
// letting templated values ($input./{{...}}) through to Execute-time
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
	if u := strOf("url", "uri", "endpoint"); u != "" && !looksTemplated(u) {
		if err := validateURL(u); err != nil {
			return err
		}
	}
	if m := strOf("method", "verb"); m != "" && !looksTemplated(m) {
		if _, err := normalizeMethod(m); err != nil {
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

// normalizeMethod uppercases and validates the HTTP method.
func normalizeMethod(raw string) (string, error) {
	m := strings.ToUpper(strings.TrimSpace(raw))
	if m == "" {
		return "POST", nil
	}
	for _, a := range allowedMethods {
		if m == a {
			return m, nil
		}
	}
	return "", fmt.Errorf("invalid method %q (supported: %s)", raw, strings.Join(allowedMethods, ", "))
}

// validateURL accepts only http/https URLs.
func validateURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid url %q: %v", raw, err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("invalid url %q: scheme must be http or https", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("invalid url %q: missing host", raw)
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

func methodAllowsBody(method string) bool {
	switch method {
	case "POST", "PUT", "PATCH", "DELETE":
		return true
	default:
		return false
	}
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

// isJSON reports whether raw parses as a JSON value.
func isJSON(raw []byte) bool {
	var v any
	return json.Unmarshal(raw, &v) == nil
}

// decodeBody mirrors the webhook trigger's ParseBody: valid JSON decodes to
// its native value (object, array, number, bool); anything else is kept as
// a plain string so conditions still route on it via $result.body.
func decodeBody(raw []byte) any {
	if len(bytesTrimSpace(raw)) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	return v
}

func bytesTrimSpace(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}

// doFunc is the HTTP transport (overridable in tests).
var doFunc = defaultDo

func doRequest(method, rawURL string, headers map[string]string, contentType string, body io.Reader, timeout time.Duration) (int, any, error) {
	return doFunc(method, rawURL, headers, contentType, body, timeout)
}

// defaultDo performs the request. Non-2xx responses are errors carrying the
// decoded body, so failed deliveries route as failures.
func defaultDo(method, rawURL string, headers map[string]string, contentType string, body io.Reader, timeout time.Duration) (int, any, error) {
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(method, rawURL, body)
	if err != nil {
		return 0, nil, fmt.Errorf("cannot build request: %v", err)
	}
	for k, v := range headers {
		if strings.EqualFold(k, "Content-Type") {
			continue
		}
		req.Header.Set(k, v)
	}
	if body != nil && contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("http request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("cannot read response: %v", err)
	}
	decoded := decodeBody(raw)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, decoded, fmt.Errorf("http %s %s failed with status %d", method, rawURL, resp.StatusCode)
	}
	return resp.StatusCode, decoded, nil
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
