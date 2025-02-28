package common

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
	ActionID   string      `json:"action_id"`
	ActionType string      `json:"action_type"`
	ActionName string      `json:"name"`
	EventType  string      `json:"event_type"` // New field for event type
	Payload    interface{} `json:"payload"`    // Generic payload for additional data
}

// Event struct for event handling
type Event struct {
	Type    string      `json:"type"`    // Event type
	Payload interface{} `json:"payload"` // Generic payload for event-specific data
}
