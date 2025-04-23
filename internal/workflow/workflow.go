package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/actions"
	"github.com/mcmx/nitejaguar/internal/database"
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

type WorkflowManager interface {
	Run(ctx context.Context)
	AddWorkflow(Workflow) error
	ExportWorkflowJSON(string) (string, error)
	ExportWorkflowJSONFile(string) error
	SaveWorkflowToDB(string) error
	GetTriggerManager() actions.TriggerManager
	ImportWorkflowJSON(string) error
}

type workflowManager struct {
	Workflows        map[string]WorkflowInt
	Actions2Workflow map[string]string
	TriggerManager   actions.TriggerManager
	ActionManager    actions.ActionManager
	eventsChan       chan common.ResultData
	db               database.Service
	enableActions    bool
}

var wmmInstance *workflowManager

func NewWorkflowManager(enableActions bool, db database.Service) WorkflowManager {
	if wmmInstance != nil {
		return wmmInstance
	}
	if !enableActions {
		log.Printf("Local actions are disabled.")
	}
	wmmInstance = &workflowManager{
		enableActions:    enableActions,
		Workflows:        make(map[string]WorkflowInt),
		Actions2Workflow: make(map[string]string),
		TriggerManager:   *actions.NewTriggerManager(),
		ActionManager:    *actions.NewActionManager(enableActions),
		eventsChan:       make(chan common.ResultData),
		db:               db,
	}
	return wmmInstance
}

// Starts the WorkflowManager and other managers
func (wm *workflowManager) Run(ctx context.Context) {
	log.Println("WorkflowManager running...")
	go wm.TriggerManager.Run(wm.eventsChan, ctx)
	go wm.ActionManager.Run(wm.eventsChan, ctx)
	var result common.ResultData
	for {
		select {
		case result = <-wm.eventsChan:
			result.WorkflowID = wm.Actions2Workflow[result.ActionID]

			n := wm.Workflows[result.WorkflowID].Definition.Nodes[result.ActionID]
			if n.ActionType == "trigger" && result.ExecutionID == "" {
				eId, _ := typeid.WithPrefix("execution")
				result.ExecutionID = eId.String()
			}
			wm.saveResult(result)

			nexts := n.GetNextNodes([]any{}, result)
			fmt.Printf("Current %v and next nodes %v,\n", n, nexts)
			for _, next := range nexts {
				fmt.Printf("Executing next node %v,\n", next)
				// TODO add the inputs to the action
				// or wait for more inputs to be available (check the dependencies)
				err := wm.ActionManager.ExecuteAction(next, result.ExecutionID, []any{})
				if err != nil {
					log.Printf("Error executing action: %s", err)
				}
			}
			// TODO check if all nodes have been executed
			// If so, save the workflow execution
		case <-time.After(10 * time.Millisecond):
			// do nothing
		case <-ctx.Done():
			log.Println("WorkflowManager stopped.")
			return
		}
	}
}

func (wm *workflowManager) saveResult(result common.ResultData) {
	jsonResult, _ := json.MarshalIndent(result, "", "  ")
	jsonFileName := "./results/" + result.ResultID + ".json"
	_ = os.WriteFile(jsonFileName, jsonResult, 0644)
	log.Println("Node Result JSON file saved:", jsonFileName)
}

// type Node
type Node struct {
	Id           string               `json:"id"`
	Name         string               `json:"name"`
	Description  string               `json:"description"`
	ActionType   string               `json:"action_type"` // trigger or action
	ActionName   string               `json:"action_name"` // the type could be infered from this, it's to make it faster
	Arguments    map[string]string    `json:"arguments"`
	Conditions   *conditionDictionary `json:"conditions"` // Dictionary of conditions, it has the next nodes id according to each condition
	Dependencies []string             `json:"dependencies"`
}

// args how this node was called
// The result from this execution
func (n *Node) GetNextNodes(inputs []any, result common.ResultData) []string {
	next_nodes := []string{}
	actionArgs := common.ActionArgs{
		Id:         n.Id,
		Name:       n.Name,
		ActionType: n.ActionType,
		ActionName: n.ActionName,
		Args:       n.Arguments,
	}
	for _, c := range n.Conditions.Entries {
		ok, err := c.Condition.evaluate(actionArgs, inputs, result)
		if err != nil {
			log.Printf("Error evaluating condition: %s", err)
		}
		if ok {
			next_nodes = append(next_nodes, c.Nexts...)
		}
	}
	return next_nodes
}

func (n *Node) GetAllNextNodes() []string {
	next_nodes := []string{}
	for _, c := range n.Conditions.Entries {
		next_nodes = append(next_nodes, c.Nexts...)
	}
	return next_nodes
}

func (wm *workflowManager) AddWorkflow(data Workflow) error {
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
		ActionList:  make(map[string]common.Action),
	}
	for _, n := range data.Nodes {
		fmt.Println("Adding node:", n.Name)
		if n.ActionType == "trigger" {
			cArgs := common.ActionArgs{
				Id:         n.Id,
				Name:       n.Name,
				ActionType: n.ActionType,
				ActionName: n.ActionName,
				Args:       n.Arguments,
			}
			nt, id, err := wm.TriggerManager.AddTrigger(cArgs)
			if err != nil {
				log.Printf("Cannot create new trigger: %s", err)
			}
			wm.Workflows[data.Id].TriggerList[id] = nt
			wm.Actions2Workflow[id] = data.Id
		} else if n.ActionType == "action" {
			if !wm.enableActions {
				continue
			}
			cArgs := common.ActionArgs{
				Id:         n.Id,
				Name:       n.Name,
				ActionType: n.ActionType,
				ActionName: n.ActionName,
				Args:       n.Arguments,
			}
			action, id, err := wm.ActionManager.AddAction(cArgs)
			if err != nil {
				log.Printf("Cannot create new action: %s", err)
				continue
			}
			wm.Workflows[data.Id].ActionList[id] = action
			wm.Actions2Workflow[id] = data.Id
		}
	}

	return nil
}

func (wm *workflowManager) ExportWorkflowJSON(workflowId string) (string, error) {
	if _, ok := wm.Workflows[workflowId]; !ok {
		return "", errors.New("workflow not found")
	}
	jsonDef, err := json.MarshalIndent(wm.Workflows[workflowId].Definition, "", "  ")
	if err != nil {
		log.Printf("Cannot marshal workflow: %s", err)
		return "", err
	}
	return string(jsonDef), nil
}

func (wm *workflowManager) ExportWorkflowJSONFile(workflowId string) error {
	jsonDef, err := wm.ExportWorkflowJSON(workflowId)
	if err != nil {
		return err
	}
	return os.WriteFile(fmt.Sprintf("workflows/%s.json", workflowId), []byte(jsonDef), 0644)
}

func (wm *workflowManager) SaveWorkflowToDB(workflowId string) error {
	jsonDef, err := wm.ExportWorkflowJSON(workflowId)
	if err != nil {
		return err
	}
	return wm.db.SaveWorkflow(workflowId, jsonDef)
}

func (wm *workflowManager) GetTriggerManager() actions.TriggerManager {
	return wm.TriggerManager
}

func (wm *workflowManager) ImportWorkflowJSON(jsonDef string) error {
	data := Workflow{}
	err := json.Unmarshal([]byte(jsonDef), &data)
	if err != nil {
		log.Printf("Cannot unmarshal workflow: %s", err)
		return err
	}
	w, err := wm.db.GetWorkflow(data.Id)
	if err != nil {
		log.Printf("Cannot get workflow: %s", err)
		return err
	}
	// if workflow doesn't exist let's create it with new ids
	if w == nil {
		dId, _ := typeid.WithPrefix("workflow")
		data.Id = dId.String()

		for i, n := range data.Nodes {
			if n.ActionType == "trigger" {
				nId, _ := typeid.WithPrefix("trigger")
				n.Id = nId.String()
			} else if n.ActionType == "action" {
				nId, _ := typeid.WithPrefix("action")
				n.Id = nId.String()
			}
			data.Nodes[n.Id] = n
			delete(data.Nodes, i)
			// TODO update the nexts and dependencies
		}
		data.Name = "Imported Workflow: " + data.Name
		jData, _ := json.MarshalIndent(data, "", "  ")
		log.Printf("Imported new workflow %s\n%s\n", data.Id, string(jData))
	}

	jsonData, _ := json.Marshal(data)
	log.Printf("Updated workflow %s", data.Id)

	return wm.db.SaveWorkflow(data.Id, string(jsonData))
}
