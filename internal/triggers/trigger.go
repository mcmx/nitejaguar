package triggers

import (
	"fmt"
	"nitejaguar/internal/triggers/common"
	"nitejaguar/internal/triggers/filechange"
	"time"

	"github.com/google/uuid"
)

type TriggerV struct {
	action common.Action
}

type TriggerService struct {
	Events      chan string
	TriggerList map[string]*TriggerV
}

// This data is not a pointer, this is intentional
// to create a copy
func (ts *TriggerService) New(data common.ActionArgs) (*TriggerV, error) {
	if ts.TriggerList == nil {
		ts.TriggerList = make(map[string]*TriggerV)
	}
	if ts.Events == nil {
		ts.Events = make(chan string)
	}
	var err error
	id, _ := uuid.NewV7()
	data.Id = id.String()
	t := &TriggerV{}

	switch data.ActionName {
	case "filechangeTrigger":
		t.action, err = filechange.New(ts.Events, data)
		if err != nil {
			return nil, err
		}
		ts.TriggerList[data.Id] = t
		go t.action.Execute()
		return t, nil
	}

	return nil, nil
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
