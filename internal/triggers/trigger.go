package triggers

type TriggerArgs struct {
	Id   string
	Name string
	Args []string
}

type Trigger interface {
	Execute() error
}
