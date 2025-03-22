package actions

import (
	"fmt"
	"time"

	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/actions/filechange"

	"github.com/google/uuid"
)

// TriggerManager manages triggers and their events
type TriggerManager struct {
	events   chan common.ResultData
	triggers map[string]common.Action
}

// NewTriggerManager creates a new TriggerManager instance
func NewTriggerManager() *TriggerManager {
	return &TriggerManager{
		triggers: make(map[string]common.Action),
		events:   make(chan common.ResultData),
	}
}

// New creates a new Action instance and adds it to the TriggerList.
// It takes a common.ActionArgs object as input, which contains the action name and other relevant data.
// The function returns a pointer to the newly created Action instance and an error if any occurs.
func (ts *TriggerManager) AddTrigger(data common.ActionArgs) (common.Action, string, error) {
	if data.Id == "" {
		data.Id = uuid.New().String()
	}

	switch data.ActionName {
	case "filechangeTrigger":
		trigger, err := filechange.New(ts.events, data)
		if err != nil {
			return nil, "", err
		}
		ts.triggers[data.Id] = trigger
		// TODO: Add an error handler to the trigger execution
		go trigger.Execute()
		return trigger, data.Id, nil
	}

	return nil, "", nil
}

func (ts *TriggerManager) RemoveTrigger(id string) {
	err := ts.triggers[id].Stop()
	if err != nil {
		fmt.Println("Error while stopping action:", err)
	}
}

func (ts *TriggerManager) Run(wmEvents chan common.ResultData) {
	fmt.Println("Starting Trigger Service")
	var value common.ResultData
	for {
		select {
		case value = <-ts.events:
			if value.CreatedAt.IsZero() {
				value.CreatedAt = time.Now()
			}
			if value.ResultID == "" {
				value.ResultID = uuid.New().String()
			}
			wmEvents <- value
		case <-time.After(50 * time.Millisecond):
			// do nothing
		}
	}
}

func (ts *TriggerManager) ListTriggers() {
	for k, v := range ts.triggers {
		fmt.Println("Trigger:", k, v)
	}
}
