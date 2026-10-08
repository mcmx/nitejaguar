package actions

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/mcmx/nitejaguar/common"
)

func TestAddTriggerDispatch(t *testing.T) {
	ts := NewTriggerManager()
	a, id, err := ts.AddTrigger(common.ActionArgs{
		Name:       "Every hour",
		ActionName: "cron",
		Args:       map[string]string{"interval": "1h"},
	})
	if err != nil {
		t.Fatalf("AddTrigger(cron) error: %v", err)
	}
	if a == nil || id == "" {
		t.Fatalf("AddTrigger(cron) = %v, %q; want a trigger and an id", a, id)
	}
	if got := a.GetArgs().ActionType; got != "trigger" {
		t.Errorf("ActionType = %q, want trigger", got)
	}
	if _, ok := ts.triggers[id]; !ok {
		t.Fatalf("cron trigger not registered under %q", id)
	}
	if err := ts.RemoveTrigger(id); err != nil {
		t.Fatalf("RemoveTrigger error: %v", err)
	}
	if _, ok := ts.triggers[id]; ok {
		t.Errorf("trigger still registered after RemoveTrigger")
	}
}

func TestAddTriggerWebhookDispatch(t *testing.T) {
	ts := NewTriggerManager()
	a, id, err := ts.AddTrigger(common.ActionArgs{
		Name:       "Inbound hook",
		ActionName: "webhook",
		Args:       map[string]string{"method": "POST"},
	})
	if err != nil {
		t.Fatalf("AddTrigger(webhook) error: %v", err)
	}
	if a == nil || id == "" {
		t.Fatalf("AddTrigger(webhook) = %v, %q; want a trigger and an id", a, id)
	}
	if got := a.GetArgs().ActionType; got != "trigger" {
		t.Errorf("ActionType = %q, want trigger", got)
	}
	if _, ok := ts.triggers[id]; !ok {
		t.Fatalf("webhook trigger not registered under %q", id)
	}
	if err := ts.RemoveTrigger(id); err != nil {
		t.Fatalf("RemoveTrigger error: %v", err)
	}
}

func TestAddTriggerRejectsInvalidWebhookMethod(t *testing.T) {
	ts := NewTriggerManager()
	if _, id, err := ts.AddTrigger(common.ActionArgs{ActionName: "webhook", Args: map[string]string{"method": "BREW"}}); err == nil {
		t.Errorf("AddTrigger(invalid webhook method) = nil error, id %q; want an error", id)
	}
}

func TestAddTriggerRejectsInvalidCron(t *testing.T) {
	ts := NewTriggerManager()
	if _, id, err := ts.AddTrigger(common.ActionArgs{ActionName: "cron", Args: map[string]string{"cron": "nope"}}); err == nil {
		t.Errorf("AddTrigger(invalid cron) = nil error, id %q; want an error", id)
	}
}

func TestAddTriggerFilewatchDispatch(t *testing.T) {
	ts := NewTriggerManager()
	a, id, err := ts.AddTrigger(common.ActionArgs{
		Name:       "Daily statement drop",
		ActionName: "filewatch",
		Args:       map[string]string{"path": "/tmp", "pattern": "statement-*.pdf", "expect_within": "30m"},
	})
	if err != nil {
		t.Fatalf("AddTrigger(filewatch) error: %v", err)
	}
	if a == nil || id == "" {
		t.Fatalf("AddTrigger(filewatch) = %v, %q; want a trigger and an id", a, id)
	}
	if got := a.GetArgs().ActionType; got != "trigger" {
		t.Errorf("ActionType = %q, want trigger", got)
	}
	if _, ok := ts.triggers[id]; !ok {
		t.Fatalf("filewatch trigger not registered under %q", id)
	}
	if err := ts.RemoveTrigger(id); err != nil {
		t.Fatalf("RemoveTrigger error: %v", err)
	}
	if _, ok := ts.triggers[id]; ok {
		t.Errorf("trigger still registered after RemoveTrigger")
	}
}

func TestAddTriggerRejectsInvalidFilewatch(t *testing.T) {
	ts := NewTriggerManager()
	cases := []map[string]string{
		{"expect_within": "30m"},                                 // missing path
		{"path": "/tmp"},                                         // missing expect_within
		{"path": "/tmp", "expect_within": "sometime"},            // bad duration
		{"path": "/tmp", "expect_within": "30m", "pattern": "["}, // bad glob
		{"path": "/tmp", "expect_within": "30m", "cron": "nope"}, // bad schedule
	}
	for _, args := range cases {
		if _, id, err := ts.AddTrigger(common.ActionArgs{ActionName: "filewatch", Args: args}); err == nil {
			t.Errorf("AddTrigger(invalid filewatch %v) = nil error, id %q; want an error", args, id)
		}
	}
}

func TestAddTriggerUnknownActionName(t *testing.T) {
	ts := NewTriggerManager()
	if _, _, err := ts.AddTrigger(common.ActionArgs{ActionName: "nope"}); err == nil {
		t.Errorf("AddTrigger(nope) succeeded, want unknown action_name error")
	}
}

// TestExampleCronWorkflowLoads guards examples/workflow-cron.json: the cron
// arguments shipped as documentation must stay constructible.
func TestExampleCronWorkflowLoads(t *testing.T) {
	raw, err := os.ReadFile("../../examples/workflow-cron.json")
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	var wf struct {
		Nodes map[string]struct {
			ID         string            `json:"id"`
			ActionType string            `json:"action_type"`
			ActionName string            `json:"action_name"`
			Arguments  map[string]string `json:"arguments"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("parse example: %v", err)
	}

	ts := NewTriggerManager()
	triggers := 0
	for _, n := range wf.Nodes {
		if n.ActionType != "trigger" {
			continue
		}
		a, id, err := ts.AddTrigger(common.ActionArgs{
			Id: n.ID, Name: n.ID, ActionType: n.ActionType,
			ActionName: n.ActionName, Args: n.Arguments,
		})
		if err != nil {
			t.Fatalf("AddTrigger(%s=%s) error: %v", n.ActionType, n.ActionName, err)
		}
		if a.GetArgs().ActionName != "cron" {
			t.Errorf("example trigger action_name = %q, want cron", a.GetArgs().ActionName)
		}
		triggers++
		if err := ts.RemoveTrigger(id); err != nil {
			t.Errorf("RemoveTrigger error: %v", err)
		}
	}
	if triggers == 0 {
		t.Fatal("example workflow has no trigger nodes")
	}
}

// TestExampleFilewatchWorkflowLoads guards examples/workflow-filewatch.json:
// the filewatch arguments shipped as documentation must stay constructible.
func TestExampleFilewatchWorkflowLoads(t *testing.T) {
	raw, err := os.ReadFile("../../examples/workflow-filewatch.json")
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	var wf struct {
		Nodes map[string]struct {
			ID         string            `json:"id"`
			ActionType string            `json:"action_type"`
			ActionName string            `json:"action_name"`
			Arguments  map[string]string `json:"arguments"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("parse example: %v", err)
	}

	ts := NewTriggerManager()
	triggers := 0
	for _, n := range wf.Nodes {
		if n.ActionType != "trigger" {
			continue
		}
		a, id, err := ts.AddTrigger(common.ActionArgs{
			Id: n.ID, Name: n.ID, ActionType: n.ActionType,
			ActionName: n.ActionName, Args: n.Arguments,
		})
		if err != nil {
			t.Fatalf("AddTrigger(%s=%s) error: %v", n.ActionType, n.ActionName, err)
		}
		if a.GetArgs().ActionName != "filewatch" {
			t.Errorf("example trigger action_name = %q, want filewatch", a.GetArgs().ActionName)
		}
		triggers++
		if err := ts.RemoveTrigger(id); err != nil {
			t.Errorf("RemoveTrigger error: %v", err)
		}
	}
	if triggers == 0 {
		t.Fatal("example workflow has no trigger nodes")
	}
}

// TestExampleWebhookWorkflowLoads guards examples/workflow-webhook.json:
// the webhook arguments shipped as documentation must stay constructible.
func TestExampleWebhookWorkflowLoads(t *testing.T) {
	raw, err := os.ReadFile("../../examples/workflow-webhook.json")
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	var wf struct {
		Nodes map[string]struct {
			ID         string            `json:"id"`
			ActionType string            `json:"action_type"`
			ActionName string            `json:"action_name"`
			Arguments  map[string]string `json:"arguments"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("parse example: %v", err)
	}

	ts := NewTriggerManager()
	triggers := 0
	for _, n := range wf.Nodes {
		if n.ActionType != "trigger" {
			continue
		}
		a, id, err := ts.AddTrigger(common.ActionArgs{
			Id: n.ID, Name: n.ID, ActionType: n.ActionType,
			ActionName: n.ActionName, Args: n.Arguments,
		})
		if err != nil {
			t.Fatalf("AddTrigger(%s=%s) error: %v", n.ActionType, n.ActionName, err)
		}
		if a.GetArgs().ActionName != "webhook" {
			t.Errorf("example trigger action_name = %q, want webhook", a.GetArgs().ActionName)
		}
		triggers++
		if err := ts.RemoveTrigger(id); err != nil {
			t.Errorf("RemoveTrigger error: %v", err)
		}
	}
	if triggers == 0 {
		t.Fatal("example workflow has no trigger nodes")
	}
}
