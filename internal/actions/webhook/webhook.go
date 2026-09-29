// Package webhook implements a workflow trigger fired by an inbound HTTP
// request instead of a schedule or filesystem event.
//
// A webhook node exposes one endpoint:
//
//	/<prefix>/<trigger-id>   (server: /webhook/{id}, client: /webhook/{id})
//
// The `method` argument selects which HTTP methods fire the trigger:
// empty, "ALL" or "*" accepts every method; otherwise a comma-separated
// list such as "POST" or "GET,POST" is matched case-insensitively.
// Requests with any other method are rejected with 405 and never produce
// a result.
//
// Methods that carry a payload (POST, PUT, PATCH, DELETE, ...) post it in
// the result: the body is JSON-decoded when possible and kept as a string
// otherwise, so downstream conditions can route on it via $result.body,
// $result.query, $result.method and friends:
//
//	{"leftOperand": "$result.method", "operator": "==", "rightOperand": "POST"}
//	{"leftOperand": "$result.body.event", "operator": "==", "rightOperand": "push"}
package webhook

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/mcmx/nitejaguar/common"
)

// SupportedMethods is the set of HTTP methods a webhook trigger accepts
// in its `method` argument. "ALL" (or "*" / empty) expands to all of them.
var SupportedMethods = []string{
	"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE", "CONNECT",
}

var supportedSet = func() map[string]bool {
	m := make(map[string]bool, len(SupportedMethods))
	for _, method := range SupportedMethods {
		m[method] = true
	}
	return m
}()

// MaxBodyBytes caps how much request body the server/client webhook
// handlers buffer for one delivery. Larger bodies are rejected with 413.
const MaxBodyBytes = 1 << 20 // 1 MiB

// ParseAllowedMethods normalizes the trigger's `method` argument into the
// set of accepted uppercase methods plus a short description for logs and
// payloads. Empty, "ALL" and "*" all mean every supported method. A
// comma-separated list (e.g. "POST" or "get, post") is matched
// case-insensitively; anything else is an error.
func ParseAllowedMethods(raw string) (map[string]bool, string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.EqualFold(trimmed, "ALL") || trimmed == "*" {
		out := make(map[string]bool, len(SupportedMethods))
		for _, m := range SupportedMethods {
			out[m] = true
		}
		return out, "ALL", nil
	}
	out := make(map[string]bool)
	var names []string
	for _, part := range strings.Split(trimmed, ",") {
		name := strings.ToUpper(strings.TrimSpace(part))
		if name == "" {
			continue
		}
		if name == "ALL" || name == "*" {
			for _, m := range SupportedMethods {
				out[m] = true
			}
			return out, "ALL", nil
		}
		if !supportedSet[name] {
			return nil, "", fmt.Errorf("invalid method %q (use a comma-separated combination of %s, or ALL)", strings.TrimSpace(part), strings.Join(SupportedMethods, ", "))
		}
		if !out[name] {
			out[name] = true
			names = append(names, name)
		}
	}
	if len(out) == 0 {
		return nil, "", fmt.Errorf("invalid method %q (use a comma-separated combination of %s, or ALL)", raw, strings.Join(SupportedMethods, ", "))
	}
	return out, strings.Join(names, ","), nil
}

// MethodAllowed reports whether method fires a trigger configured with the
// given allowed set. Comparison is case-insensitive; an empty method never
// matches.
func MethodAllowed(allowed map[string]bool, method string) bool {
	if len(allowed) == 0 {
		return false
	}
	m := strings.ToUpper(strings.TrimSpace(method))
	if m == "" {
		return false
	}
	return allowed[m]
}

// ParseBody decodes one webhook request body. Empty input yields nil (no
// payload to route on). Valid JSON decodes to its native value (object,
// array, number, bool); anything else is kept as a plain string so form
// posts and opaque bytes still reach conditions via $result.body.
func ParseBody(raw []byte) any {
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

// BuildPayload assembles the trigger result payload shared by the server
// and client webhook handlers. query and headers use plain string maps so
// conditions address them as $result.query.<name> / $result.headers.<name>;
// body holds the decoded payload (or nil for bodiless methods) and
// body_raw keeps the exact bytes received as text.
func BuildPayload(method, path string, query map[string]string, queryString string, headers map[string]string, body any, raw string) map[string]any {
	if query == nil {
		query = map[string]string{}
	}
	if headers == nil {
		headers = map[string]string{}
	}
	return map[string]any{
		"type":         "success",
		"trigger":      "webhook",
		"method":       strings.ToUpper(strings.TrimSpace(method)),
		"path":         path,
		"query":        query,
		"query_string": queryString,
		"headers":      headers,
		"body":         body,
		"body_raw":     raw,
	}
}

type webhookTrigger struct {
	data    common.ActionArgs
	events  chan common.ResultData
	allowed map[string]bool
	methods string

	stopOnce sync.Once
	stop     chan struct{}
}

// New creates the webhook trigger. The `method` arg is validated here so a
// bad method fails while the workflow loads instead of silently never
// firing. Execute blocks for the trigger lifetime; HTTP deliveries arrive
// via Deliver.
func New(events chan common.ResultData, data common.ActionArgs) (common.Action, error) {
	args, err := argsToMap(data.Args)
	if err != nil {
		return nil, err
	}
	allowed, desc, err := ParseAllowedMethods(args["method"])
	if err != nil {
		return nil, err
	}
	t := &webhookTrigger{
		data:    data,
		events:  events,
		allowed: allowed,
		methods: desc,
		stop:    make(chan struct{}),
	}
	t.data.ActionType = "trigger"
	log.Println("Initializing Webhook Trigger with id:", t.data.Id, "methods:", desc)
	return t, nil
}

// Execute blocks until Stop: unlike cron/filechange there is nothing to
// poll — the HTTP handler calls Deliver for every accepted request.
func (t *webhookTrigger) Execute(executionId string, inputs []any) {
	log.Printf("Executing Webhook Trigger with id: %s (methods %q)", t.data.Id, t.methods)
	<-t.stop
}

// Deliver emits one trigger result for an accepted HTTP request. It returns
// false when the method is not allowed (the caller should answer 405) and
// true once the result is queued. Payload follows BuildPayload so
// conditions route on $result.method / $result.query / $result.body.
func (t *webhookTrigger) Deliver(executionID, method, path string, query map[string]string, queryString string, headers map[string]string, body any, raw string) bool {
	if !MethodAllowed(t.allowed, method) {
		return false
	}
	result := common.ResultData{
		ExecutionID: executionID,
		ActionID:    t.data.Id,
		ActionType:  t.data.ActionType,
		ActionName:  t.data.ActionName,
		Payload:     BuildPayload(method, path, query, queryString, headers, body, raw),
	}
	select {
	case t.events <- result:
		return true
	case <-t.stop:
		return false
	}
}

// AllowedMethods returns the normalized method set description
// (e.g. "ALL" or "GET,POST").
func (t *webhookTrigger) AllowedMethods() string {
	return t.methods
}

// Stop ends the Execute loop. It is idempotent.
func (t *webhookTrigger) Stop() error {
	log.Println("Stopping the webhook trigger", t.data.Id)
	t.stopOnce.Do(func() { close(t.stop) })
	return nil
}

// GetArgs returns the ActionArgs associated with the webhook trigger.
func (t *webhookTrigger) GetArgs() common.ActionArgs {
	return t.data
}

// argsToMap normalizes node args, treating nil as defaults (ALL methods) —
// unlike common.ArgsToStringMap, which rejects nil.
func argsToMap(raw any) (map[string]string, error) {
	if raw == nil {
		return map[string]string{}, nil
	}
	return common.ArgsToStringMap(raw)
}
