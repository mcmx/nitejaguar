package webhook

import (
	"testing"

	"github.com/mcmx/nitejaguar/common"
)

func TestParseAllowedMethods(t *testing.T) {
	for _, raw := range []string{"", "ALL", "all", "*", " all "} {
		allowed, desc, err := ParseAllowedMethods(raw)
		if err != nil {
			t.Fatalf("ParseAllowedMethods(%q) error: %v", raw, err)
		}
		if desc != "ALL" || len(allowed) != len(SupportedMethods) {
			t.Fatalf("ParseAllowedMethods(%q) = %v, %q; want all methods", raw, allowed, desc)
		}
		for _, m := range SupportedMethods {
			if !MethodAllowed(allowed, m) {
				t.Fatalf("ParseAllowedMethods(%q) should allow %s", raw, m)
			}
		}
	}

	allowed, desc, err := ParseAllowedMethods("post")
	if err != nil {
		t.Fatalf("ParseAllowedMethods(post) error: %v", err)
	}
	if desc != "POST" || !MethodAllowed(allowed, "POST") || !MethodAllowed(allowed, "post") {
		t.Fatalf("single method not normalized: %v %q", allowed, desc)
	}
	if MethodAllowed(allowed, "GET") {
		t.Fatalf("POST-only set should not allow GET")
	}

	allowed, desc, err = ParseAllowedMethods("GET, post")
	if err != nil {
		t.Fatalf("ParseAllowedMethods(GET, post) error: %v", err)
	}
	if desc != "GET,POST" || !MethodAllowed(allowed, "GET") || !MethodAllowed(allowed, "POST") {
		t.Fatalf("multi method not parsed: %v %q", allowed, desc)
	}
	if MethodAllowed(allowed, "DELETE") {
		t.Fatalf("GET,POST set should not allow DELETE")
	}

	for _, raw := range []string{"BREW", "GET,NOPE", "   "} {
		if raw == "   " {
			continue // whitespace-only means ALL, tested above via ""
		}
		if _, _, err := ParseAllowedMethods(raw); err == nil {
			t.Errorf("ParseAllowedMethods(%q) succeeded, want error", raw)
		}
	}
}

func TestParseBody(t *testing.T) {
	if got := ParseBody(nil); got != nil {
		t.Errorf("ParseBody(nil) = %v, want nil", got)
	}
	if got := ParseBody([]byte("   ")); got != nil {
		t.Errorf("ParseBody(blank) = %v, want nil", got)
	}
	got := ParseBody([]byte(`{"event":"push","n":3}`))
	m, ok := got.(map[string]any)
	if !ok || m["event"] != "push" {
		t.Fatalf("ParseBody(json object) = %#v, want decoded map", got)
	}
	if got := ParseBody([]byte(`[1,2]`)); got == nil {
		t.Fatalf("ParseBody(json array) = nil, want slice")
	}
	if got := ParseBody([]byte(`hello world`)); got != "hello world" {
		t.Fatalf("ParseBody(text) = %#v, want raw string", got)
	}
}

func TestBuildPayloadExposesMethodAndBody(t *testing.T) {
	p := BuildPayload("post", "/webhook/trigger_1",
		map[string]string{"branch": "main"}, "branch=main",
		map[string]string{"content-type": "application/json"},
		map[string]any{"event": "push"}, `{"event":"push"}`)
	if p["method"] != "POST" {
		t.Errorf("method = %v, want POST", p["method"])
	}
	if p["trigger"] != "webhook" || p["type"] != "success" {
		t.Errorf("payload missing trigger markers: %#v", p)
	}
	q, ok := p["query"].(map[string]string)
	if !ok || q["branch"] != "main" {
		t.Errorf("query = %#v, want branch=main", p["query"])
	}
	body, ok := p["body"].(map[string]any)
	if !ok || body["event"] != "push" {
		t.Errorf("body = %#v, want decoded event", p["body"])
	}
}

func TestTriggerDeliverEnforcesMethod(t *testing.T) {
	events := make(chan common.ResultData, 2)
	tr, err := New(events, common.ActionArgs{
		Id: "trigger_webhook1", Name: "Hook", ActionType: "trigger",
		ActionName: "webhook", Args: map[string]string{"method": "POST"},
	})
	if err != nil {
		t.Fatalf("New error: %v", err)
	}
	w, ok := tr.(*webhookTrigger)
	if !ok {
		t.Fatalf("New returned %T, want *webhookTrigger", tr)
	}
	if w.AllowedMethods() != "POST" {
		t.Fatalf("AllowedMethods = %q, want POST", w.AllowedMethods())
	}
	if w.Deliver("", "GET", "/webhook/trigger_webhook1", nil, "", nil, nil, "") {
		t.Fatalf("Deliver(GET) on POST-only trigger should return false")
	}
	if len(events) != 0 {
		t.Fatalf("rejected method must not emit a result")
	}
	if !w.Deliver("", "POST", "/webhook/trigger_webhook1",
		map[string]string{}, "", map[string]string{}, map[string]any{"a": 1}, `{"a":1}`) {
		t.Fatalf("Deliver(POST) should return true")
	}
	select {
	case got := <-events:
		if got.ActionID != "trigger_webhook1" {
			t.Errorf("ActionID = %q, want trigger_webhook1", got.ActionID)
		}
		p, ok := got.Payload.(map[string]any)
		if !ok {
			t.Fatalf("payload = %#v, want map", got.Payload)
		}
		if p["method"] != "POST" {
			t.Errorf("payload method = %v, want POST", p["method"])
		}
	default:
		t.Fatalf("accepted method must emit a result")
	}
	if err := tr.Stop(); err != nil {
		t.Fatalf("Stop error: %v", err)
	}
	if err := tr.Stop(); err != nil {
		t.Fatalf("second Stop error: %v", err)
	}
}

func TestNewRejectsInvalidMethod(t *testing.T) {
	events := make(chan common.ResultData, 1)
	if _, err := New(events, common.ActionArgs{
		ActionName: "webhook", Args: map[string]string{"method": "BREW"},
	}); err == nil {
		t.Fatalf("New(BREW) succeeded, want error")
	}
}

func TestNewDefaultsToAllMethods(t *testing.T) {
	events := make(chan common.ResultData, 1)
	tr, err := New(events, common.ActionArgs{ActionName: "webhook"})
	if err != nil {
		t.Fatalf("New(nil args) error: %v", err)
	}
	defer func() { _ = tr.Stop() }()
	if got := tr.GetArgs().ActionType; got != "trigger" {
		t.Errorf("ActionType = %q, want trigger", got)
	}
	w := tr.(*webhookTrigger)
	for _, m := range SupportedMethods {
		if !MethodAllowed(w.allowed, m) {
			t.Errorf("default trigger should allow %s", m)
		}
	}
}
