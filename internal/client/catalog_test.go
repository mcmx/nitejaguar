package client

import (
	"testing"

	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/actions"
)

// TestClientDispatchCoversCatalog guards the client's parallel dispatch:
// every node the designer catalog advertises must construct on the
// client with its designer defaults, so a workflow saved from the
// designer never hits "unknown action_name" on a remote runner.
func TestClientDispatchCoversCatalog(t *testing.T) {
	events := make(chan common.ResultData, len(actions.DesignerCatalog()))
	for _, e := range actions.DesignerCatalog() {
		args := common.ActionArgs{Name: "catalog probe", ActionName: e.ActionName, Args: e.Args}
		var (
			a   common.Action
			err error
		)
		if e.ActionType == "trigger" {
			a, err = newClientTrigger(events, args)
		} else {
			a, err = newClientAction(events, args)
		}
		if err != nil {
			t.Errorf("client dispatch(%s:%s with designer defaults) error: %v", e.ActionType, e.ActionName, err)
			continue
		}
		if a == nil {
			t.Errorf("client dispatch(%s:%s) = nil action", e.ActionType, e.ActionName)
			continue
		}
		if got := a.GetArgs().ActionType; got != e.ActionType {
			t.Errorf("client dispatch(%s) runs as %q", e.ActionName, got)
		}
		if err := a.Stop(); err != nil {
			t.Errorf("Stop(%s:%s) error: %v", e.ActionType, e.ActionName, err)
		}
	}
}
