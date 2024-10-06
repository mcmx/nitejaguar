package triggers

import (
	"nitejaguar/internal/triggers/common"
	"nitejaguar/internal/triggers/filechange"

	"github.com/google/uuid"
)

type TriggerV struct {
	Events  chan string
	trigger Trigger
}

type Trigger interface {
	Execute() error
}

var TriggerList = map[string]*TriggerV{}

func New(data *common.TriggerArgs) (*TriggerV, error) {
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
		TriggerList[data.Id] = t
		go t.trigger.Execute()
		return t, nil
	}

	return nil, nil
}
