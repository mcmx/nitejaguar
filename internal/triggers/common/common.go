package common

type TriggerArgs struct {
	Id          string
	Name        string
	TriggerType string
	Args        []string
}

type Trigger interface {
	Execute()
}
