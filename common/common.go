package common

import "time"

// ActionArgs holds the arguments for an action
type ActionArgs struct {
	Id         string
	Name       string
	ActionType string
	ActionName string
	Args       []string
}

// Action interface for actions
type Action interface {
	Execute() error
	Stop() error
	GetArgs() ActionArgs
}

// Generic ResultData struct for various actions
type ResultData struct {
	ResultID   string      `json:"result_id"`
	ActionID   string      `json:"action_id"`
	ActionType string      `json:"action_type"`
	ActionName string      `json:"name"`
	CreatedAt  time.Time   `json:"created_at"`
	Payload    interface{} `json:"payload"` // Generic payload for additional data
}
