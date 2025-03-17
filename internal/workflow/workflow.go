package workflow

import (
	"github.com/google/uuid"
	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/actions"
)

type Workflow struct {
	Id          string                   `json:"id"`
	Name        string                   `json:"name"`
	TriggerList map[string]common.Action `json:"triggers"`
}

type WorkflowManager struct {
	Workflows      map[string]Workflow
	TriggerManager actions.TriggerManager
	ActionManager  actions.ActionManager
}

func NewWorkflowManager() *WorkflowManager {
	return &WorkflowManager{
		Workflows:      make(map[string]Workflow),
		TriggerManager: *actions.NewTriggerManager(),
		ActionManager:  *actions.NewActionManager(),
	}
}


// type Node
type Node struct {
	Id          string              `json:"id"`
	Description string              `json:"description"`
	Type        string              `json:"type"`       // trigger or action
	Action      string              `json:"action"`     // the type could be infered from this, it's to make it faster
	Conditions  ConditionDictionary `json:"conditions"` // next Node's id... TODO I'm not happy with this I need a list with conditions or no condition at all
}

func (w *WorkflowManager) AddWorkflow(data Workflow) {
	if data.Id == "" {
		data.Id = uuid.New().String()
	}
	w.Workflows[data.Id] = data
}
