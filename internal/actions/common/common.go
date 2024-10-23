package common

type ActionArgs struct {
	Id         string
	Name       string
	ActionType string
	ActionName string
	Args       []string
}

type Action interface {
	Execute() error
	Stop() error
}
