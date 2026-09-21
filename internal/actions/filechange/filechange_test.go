package filechange

import (
	"os"
	"testing"

	"github.com/mcmx/nitejaguar/common"
)

func TestFilechangeTriggerTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home dir available")
	}

	events := make(chan common.ResultData, 10)
	trigger, err := New(events, common.ActionArgs{
		Id:         "trigger_1",
		ActionName: "filechange",
		Args:       map[string]any{"path": "~/Downloads"},
	})
	if err != nil {
		t.Fatalf("failed to create filechange trigger: %v", err)
	}
	if trigger.GetArgs().ActionName != "filechange" {
		t.Fatalf("unexpected action name: %s", trigger.GetArgs().ActionName)
	}
}
