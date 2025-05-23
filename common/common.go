package common

import "time"

// ActionArgs holds the arguments for an action
type ActionArgs struct {
	Id         string
	Name       string
	ActionType string
	ActionName string
	Args       any
}

// Action interface for actions
type Action interface {
	Execute(executionId string, inputs []any)
	Stop() error
	GetArgs() ActionArgs
}

// Generic ResultData struct for various actions
type ResultData struct {
	ResultID    string    `json:"result_id"`
	WorkflowID  string    `json:"workflow_id"`
	ExecutionID string    `json:"execution_id"`
	ActionID    string    `json:"action_id"`
	ActionType  string    `json:"action_type"`
	ActionName  string    `json:"action_name"`
	CreatedAt   time.Time `json:"created_at"`
	Payload     any       `json:"payload"` // Generic payload for additional data
}
