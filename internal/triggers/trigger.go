package triggers

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
	data.Id = "MyFakeID+1"

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
