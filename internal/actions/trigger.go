package actions

import (
	"fmt"
	"time"

	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/actions/filechange"

	"github.com/google/uuid"
)

// TriggerService manages triggers and their events
type TriggerService struct {
	events   chan string
	triggers map[string]common.Action
}

// New creates a new Action instance and adds it to the TriggerList.
// It takes a common.ActionArgs object as input, which contains the action name and other relevant data.
// The function returns a pointer to the newly created Action instance and an error if any occurs.
func (ts *TriggerService) New(data common.ActionArgs) (common.Action, error) {
	if ts.triggers == nil {
		ts.triggers = make(map[string]common.Action)
	}
	if ts.events == nil {
		ts.events = make(chan string)
	}

	if data.Id == "" {
		data.Id = uuid.New().String()
	}

	switch data.ActionName {
	case "filechangeTrigger":
		trigger, err := filechange.New(ts.events, data)
		if err != nil {
			return nil, err
		}
		ts.triggers[data.Id] = trigger
		go trigger.Execute()
		return trigger, nil
	}

	return nil, nil
}

func (ts *TriggerService) Stop(id string) {
	err := ts.triggers[id].Stop()
	if err != nil {
		fmt.Println("Error while stopping action:", err)
	}
}

func (ts *TriggerService) Run() {
	fmt.Println("Starting Trigger Service")
	var value string
	for {
		select {
		case value = <-ts.events:
			fmt.Println("Trigger Result", value)
		case <-time.After(200 * time.Millisecond):
			// do nothing
		}
	}
}

func (ts *TriggerService) ListTriggers() {
	for k, v := range ts.triggers {
		fmt.Println("Trigger:", k, v)
	}
}
