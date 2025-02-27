package actions

import (
	"fmt"
	"time"

	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/actions/filechange"

	"github.com/google/uuid"
)

type Action struct {
	action common.Action
}

type TriggerService struct {
	Events      chan string
	triggerList map[string]*Action
}

// New creates a new Action instance and adds it to the TriggerList.
// It takes a common.ActionArgs object as input, which contains the action name and other relevant data.
// The function returns a pointer to the newly created Action instance and an error if any occurs.
func (ts *TriggerService) New(data common.ActionArgs) (*Action, error) {
	if ts.triggerList == nil {
		ts.triggerList = make(map[string]*Action)
	}
	if ts.Events == nil {
		ts.Events = make(chan string)
	}
	var err error
	id, _ := uuid.NewV7()
	data.Id = id.String()
	t := &Action{}

	switch data.ActionName {
	case "filechangeTrigger":
		t.action, err = filechange.New(ts.Events, data)
		if err != nil {
			return nil, err
		}
		ts.triggerList[data.Id] = t
		go t.action.Execute()
		return t, nil
	}

	return nil, nil
}

func (ts *TriggerService) Stop(id string) {
	err := ts.triggerList[id].action.Stop()
	if err != nil {
		fmt.Println("Error while stopping action:", err)
	}
}

func (ts *TriggerService) Run() {
	var value string
	for {
		select {
		case value = <-ts.Events:
			fmt.Println("Trigger Result", value)
		case <-time.After(200 * time.Millisecond):
			// do nothing
		}
	}
}
