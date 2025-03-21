package workflow

import (
	"errors"
	"log"
	"github.com/google/uuid"
	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/actions"
)

type Workflow struct {
	Id          string                   `json:"id"`
	Name        string                   `json:"name"`
	TriggerList map[string]common.ActionArgs `json:"triggers"`
}

type WorkflowInt struct {
	Id          string
	Name        string
	Definition Workflow
	TriggerList map[string]common.Action
}

type WorkflowManager struct {
	Workflows      map[string]WorkflowInt
	TriggerManager actions.TriggerManager
	ActionManager  actions.ActionManager
}

func NewWorkflowManager() *WorkflowManager {
	return &WorkflowManager{
		Workflows:      make(map[string]WorkflowInt),
		TriggerManager: *actions.NewTriggerManager(),
		ActionManager:  *actions.NewActionManager(),
	}
}

// Starts the WorkflowManager and other managers
func (wm *WorkflowManager) Run() {
	wm.TriggerManager.Run()
}

// type Node
type Node struct {
	Id          string              `json:"id"`
	Description string              `json:"description"`
	Type        string              `json:"type"`       // trigger or action
	Action      string              `json:"action"`     // the type could be infered from this, it's to make it faster
	Conditions  ConditionDictionary `json:"conditions"` // next Node's id... TODO I'm not happy with this I need a list with conditions or no condition at all
}

func (wm *WorkflowManager) AddWorkflow(data Workflow) error {
	if data.Id == "" {
		data.Id = uuid.New().String()
	}
	if data.Id == "" {
		return errors.New("Incorrect workflow input dataks")
	}
	wm.Workflows[data.Id] = WorkflowInt{
		Id: data.Id,
		Name: data.Name,
		Definition: data,
		TriggerList: make(map[string]common.Action),
	}
	for _, t := range data.TriggerList {
		nt, err := wm.TriggerManager.AddTrigger(t)
		if err != nil {
			log.Fatalf("Cannot create new trigger: %s", err)
		}
		wm.Workflows[data.Id].TriggerList[t.Id] = nt
	}
	return nil
}
