package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/actions"
	"go.jetify.com/typeid"
)

type Workflow struct {
	Id    string          `json:"id"`
	Name  string          `json:"name"`
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
	Arguments    map[string]string    `json:"arguments"`
	Conditions   *ConditionDictionary `json:"conditions"` // Dictionary of conditions, it has the next nodes id according to each condition
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
	log.Println("Adding workflow:", data.Name)
	if data.Id == "" {
		dId, _ := typeid.WithPrefix("workflow")
		data.Id = dId.String()
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
	for _, n := range data.Nodes {
		args := []string{}
		for _, v := range n.Arguments {
			args = append(args, v)
		}

		if n.ActionType == "trigger" {
			cArgs := common.ActionArgs{
				Id:         n.Id,
				Name:       n.Name,
				ActionType: n.ActionType,
				ActionName: n.ActionName,
				Args:       args,
			}
			nt, id, err := wm.TriggerManager.AddTrigger(cArgs)
			if err != nil {
				log.Printf("Cannot create new trigger: %s", err)
			}
			wm.Workflows[data.Id].TriggerList[id] = nt
			wm.Actions2Workflow[id] = data.Id
		}
	}

	return nil
}

func (wm *WorkflowManager) ExportWorkflowJSON(workflowId string) ([]byte, error) {
	if _, ok := wm.Workflows[workflowId]; !ok {
		return nil, errors.New("workflow not found")
	}
	jsonDef, err := json.MarshalIndent(wm.Workflows[workflowId].Definition, "", "  ")
	if err != nil {
		log.Printf("Cannot marshal workflow: %s", err)
		return nil, err
	}

	return jsonDef, nil
}

func (wm *WorkflowManager) ExportWorkflowJSONFile(workflowId string) error {
	jsonDef, err := wm.ExportWorkflowJSON(workflowId)
	if err != nil {
		return err
	}
	err = os.WriteFile(fmt.Sprintf("workflows/%s.json", workflowId), jsonDef, 0644)
	if err != nil {
		log.Printf("Cannot write workflow file: %s", err)
		return err
	}
	return nil
}
