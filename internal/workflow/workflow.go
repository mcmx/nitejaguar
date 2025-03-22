package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/google/uuid"
	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/actions"
)

type Workflow struct {
	Id          string                       `json:"id"`
	Name        string                       `json:"name"`
	TriggerList map[string]common.ActionArgs `json:"triggers"`
	ActionList  map[string]common.ActionArgs `json:"actions"`
	// TODO change this to a list of Nodes
}

type WorkflowInt struct {
	Id          string
	Name        string
	Definition  Workflow
	TriggerList map[string]common.Action
	ActionList  map[string]common.Action
}

type WorkflowManager struct {
	Workflows        map[string]WorkflowInt
	Actions2Workflow map[string]string
	TriggerManager   actions.TriggerManager
	ActionManager    actions.ActionManager
}

func NewWorkflowManager() *WorkflowManager {
	return &WorkflowManager{
		Workflows:        make(map[string]WorkflowInt),
		Actions2Workflow: make(map[string]string),
		TriggerManager:   *actions.NewTriggerManager(),
		ActionManager:    *actions.NewActionManager(),
	}
}

// Starts the WorkflowManager and other managers
func (wm *WorkflowManager) Run() {
	log.Println("WorkflowManager running...")
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
	log.Println("Adding workflow", data.Name)
	if data.Id == "" {
		data.Id = uuid.New().String()
	}
	if data.Id == "" {
		return errors.New("incorrect workflow input data")
	}
	wm.Workflows[data.Id] = WorkflowInt{
		Id:          data.Id,
		Name:        data.Name,
		Definition:  data,
		TriggerList: make(map[string]common.Action),
	}
	for _, t := range data.TriggerList {
		nt, id, err := wm.TriggerManager.AddTrigger(t)
		if err != nil {
			log.Printf("Cannot create new trigger: %s", err)
		}
		wm.Workflows[data.Id].TriggerList[id] = nt
		wm.Actions2Workflow[id] = data.Id
	}
	// Do the same for actions
	for _, a := range data.ActionList {
		na, id, err := wm.ActionManager.AddAction(a)
		if err != nil {
			log.Printf("Cannot create new action: %s", err)
		}
		wm.Workflows[data.Id].ActionList[id] = na
		wm.Actions2Workflow[id] = data.Id
	}

	jsonDef, err := json.MarshalIndent(wm.Workflows[data.Id].Definition, "", "  ")
	if err != nil {
		log.Printf("Cannot marshal workflow: %s", err)
	}
	err = os.WriteFile(fmt.Sprintf("workflows/%s.json", data.Id), jsonDef, 0644)
	if err != nil {
		log.Printf("Cannot write workflow file: %s", err)
	}
	return nil
}
