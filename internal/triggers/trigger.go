package triggers

import (
	"fmt"
	"nitejaguar/internal/triggers/common"
	"nitejaguar/internal/triggers/filechange"

	"github.com/google/uuid"
)

type TriggerV struct {
	Events  chan string
	trigger Trigger
}

type TriggerService struct {
	TriggerList map[string]*TriggerV
}

type Trigger interface {
	Execute() error
}

// This data is not a pointer, this is intentional
// to create a copy
func (ts *TriggerService) New(data common.TriggerArgs) (*TriggerV, error) {
	if ts.TriggerList == nil {
		ts.TriggerList = make(map[string]*TriggerV)
	}
	var err error
	id, _ := uuid.NewV7()
	data.Id = id.String()
	t := &TriggerV{
		Events: make(chan string),
	}

	switch data.TriggerType {
	case "filechange":
		t.trigger, err = filechange.New(t.Events, data)
		if err != nil {
			return nil, err
		}
		ts.TriggerList[data.Id] = t
		go t.trigger.Execute()
		return t, nil
	}

	return nil, nil
}

func (ts *TriggerService) Run() {
	go func() {
		var value string
		for {
			for _, t := range ts.TriggerList {
				value = <-t.Events
				fmt.Println("Trigger Result", value)
			}
		}
	}()
}
