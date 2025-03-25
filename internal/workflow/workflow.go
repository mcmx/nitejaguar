package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

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
	Nodes map[string]Node `json:"nodes"`
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
	resultChan       chan common.ResultData
}

func NewWorkflowManager() *WorkflowManager {
	return &WorkflowManager{
		Workflows:        make(map[string]WorkflowInt),
		Actions2Workflow: make(map[string]string),
		TriggerManager:   *actions.NewTriggerManager(),
		ActionManager:    *actions.NewActionManager(),
		resultChan:       make(chan common.ResultData),
	}
}

// Starts the WorkflowManager and other managers
func (wm *WorkflowManager) Run() {
	log.Println("WorkflowManager running...")
	go wm.TriggerManager.Run(wm.resultChan)
	var value common.ResultData
	for {
		select {
		case value = <-wm.resultChan:
			value.WorkflowID = wm.Actions2Workflow[value.ActionID]
			// TODO here use the Condition and validate
			// Not all results are a trigger or are they?

			// Either way then pass the result to an action
			wm.saveResult(value)

		case <-time.After(10 * time.Millisecond):
			// do nothing
		}
	}
}

func (wm *WorkflowManager) saveResult(result common.ResultData) {
	jsonResult, _ := json.MarshalIndent(result, "", "  ")
	jsonFileName := "./results/" + result.ResultID + ".json"
	_ = os.WriteFile(jsonFileName, jsonResult, 0644)
	fmt.Println("Trigger Result JSON file saved:", jsonFileName)
}

// type Node
type Node struct {
	Id           string               `json:"id"`
	Name         string               `json:"name"`
	Description  string               `json:"description"`
	ActionType   string               `json:"action_type"` // trigger or action
	ActionName   string               `json:"action_name"` // the type could be infered from this, it's to make it faster
	Conditions   *ConditionDictionary `json:"conditions"`  // next Node's id... TODO I'm not happy with this I need a list with conditions or no condition at all
	Arguments    map[string]string    `json:"arguments"`
	Dependencies []string             `json:"dependencies"`
}

func (n *Node) GetNextNodes() []string {
	next_nodes := []string{}
	for _, c := range n.Conditions.Entries {
		next_nodes = append(next_nodes, c.Nexts...)
	}
	return next_nodes
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
