package triggers

import (
	"nitejaguar/internal/triggers/common"
	"nitejaguar/internal/triggers/filechange"

	"github.com/google/uuid"
)

type Trigger interface {
	Execute() error
}

var triggerList = map[string]Trigger{}

func New(data *common.TriggerArgs) (Trigger, error) {
	var trigger Trigger
	var err error
	id, _ := uuid.NewV7()
	data.Id = id.String()

	switch data.TriggerType {
	case "filechange":
		trigger, err = filechange.New(data)
		if err != nil {
			return nil, err
		}
		triggerList[data.Id] = trigger
		go trigger.Execute()
		return trigger, nil
	}

	return nil, nil
}
