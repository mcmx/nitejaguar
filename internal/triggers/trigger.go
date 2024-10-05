package triggers

import (
	"github.com/google/uuid"
)

type ITrigger interface {
	Execute() error
}

type TriggerArgs struct {
	Id          string
	TriggerType string
	Name        string
	Args        []string
}

var triggerList = map[string]ITrigger{}

func New(data *TriggerArgs) (ITrigger, error) {
	var trigger ITrigger
	var err error
	id, _ := uuid.NewV7()
	data.Id = id.String()

	switch data.TriggerType {
	case "filechange":
		trigger, err = newFileChange(data)
		if err != nil {
			return nil, err
		}
		triggerList[data.Id] = trigger
		go trigger.Execute()
		return trigger, nil
	}

	return nil, nil
}
