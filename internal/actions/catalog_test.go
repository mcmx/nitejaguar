package actions

import (
	"testing"

	"github.com/mcmx/nitejaguar/common"
)

// TestDesignerCatalogDispatches guards the per-package designer schemas:
// every catalog entry must dispatch through its manager with its own
// designer defaults, and the entry's action_type must match the runtime
// type. A new action/trigger that ships a switch case but no working
// CatalogEntry fails here.
func TestDesignerCatalogDispatches(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range DesignerCatalog() {
		key := e.ActionType + ":" + e.ActionName
		if seen[key] {
			t.Errorf("duplicate catalog entry %q", key)
		}
		seen[key] = true
		if len(e.Fields) == 0 {
			t.Errorf("catalog entry %q has no fields", key)
		}
	}

	am := NewActionManager(true)
	for _, e := range ActionCatalog() {
		a, id, err := am.AddAction(common.ActionArgs{
			Name: "catalog probe", ActionName: e.ActionName, Args: e.Args,
		})
		if err != nil {
			t.Errorf("AddAction(%q with designer defaults) error: %v", e.ActionName, err)
			continue
		}
		if a == nil || id == "" {
			t.Errorf("AddAction(%q) = %v, %q; want an action and an id", e.ActionName, a, id)
			continue
		}
		if got := a.GetArgs().ActionType; got != "action" || e.ActionType != "action" {
			t.Errorf("catalog entry action:%s dispatches as %q", e.ActionName, got)
		}
		am.RemoveAction(id)
	}

	ts := NewTriggerManager()
	for _, e := range TriggerCatalog() {
		a, id, err := ts.AddTrigger(common.ActionArgs{
			Name: "catalog probe", ActionName: e.ActionName, Args: e.Args,
		})
		if err != nil {
			t.Errorf("AddTrigger(%q with designer defaults) error: %v", e.ActionName, err)
			continue
		}
		if a == nil || id == "" {
			t.Errorf("AddTrigger(%q) = %v, %q; want a trigger and an id", e.ActionName, a, id)
			continue
		}
		if got := a.GetArgs().ActionType; got != "trigger" || e.ActionType != "trigger" {
			t.Errorf("catalog entry trigger:%s dispatches as %q", e.ActionName, got)
		}
		if err := ts.RemoveTrigger(id); err != nil {
			t.Errorf("RemoveTrigger(%q) error: %v", e.ActionName, err)
		}
	}
}
