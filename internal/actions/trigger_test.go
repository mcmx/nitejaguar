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

func TestAddTriggerRejectsInvalidCron(t *testing.T) {
	ts := NewTriggerManager()
	if _, id, err := ts.AddTrigger(common.ActionArgs{ActionName: "cron", Args: map[string]string{"cron": "nope"}}); err == nil {
		t.Errorf("AddTrigger(invalid cron) = nil error, id %q; want an error", id)
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
