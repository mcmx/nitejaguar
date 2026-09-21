package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/actions"
	"github.com/mcmx/nitejaguar/internal/database"
	"go.jetify.com/typeid"
)

type Workflow struct {
	Id       string          `json:"id"`
	Name     string          `json:"name"`
	TenantID string          `json:"tenant_id,omitempty"`
	Nodes    map[string]Node `json:"nodes"`
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
	CloneWorkflowJSON(string) error
	IngestResult(common.ResultData) (common.ResultData, []string, error)
}

type workflowManager struct {
	Workflows        map[string]WorkflowInt
	Actions2Workflow map[string]string
	TriggerManager   actions.TriggerManager
	ActionManager    actions.ActionManager
	eventsChan       chan common.ResultData
	db               database.Service
	enableActions    bool
	executorID       string
	// execHistory tracks the latest payload per (execution, node) so the
	// framework can apply merge_input in the Run loop and re-apply it in
	// IngestResult. Best-effort: entries are only present for executions
	// seen by this server instance.
	mu          sync.Mutex
	execHistory map[string]map[string]any
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
		executorID:       "server",
		execHistory:      make(map[string]map[string]any),
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
			if result.ExecutorID == "" {
				result.ExecutorID = wm.executorID
			}
			result.WorkflowID = wm.Actions2Workflow[result.ActionID]

			n := wm.Workflows[result.WorkflowID].Definition.Nodes[result.ActionID]
			if n.ActionType == "trigger" && result.ExecutionID == "" {
				eId, _ := typeid.WithPrefix("execution")
				result.ExecutionID = eId.String()
			}
			// Framework-level merge_input: fold the recorded upstream
			// dependency payloads into the result. Actions stay unaware
			// of the flag; every action is compliant by default.
			if n.MergeInput {
				result.Payload = wm.applyMergeInput(result, n)
			}
			wm.saveResult(result)
			wm.rememberResult(result)

			nexts := n.GetNextNodes([]any{}, result)
			fmt.Printf("Current %v and next nodes %v,\n", n, nexts)
			for _, next := range nexts {
				fmt.Printf("Executing next node %v,\n", next)
				// Thread the triggering result into downstream actions so
				// fileaction (and others) can resolve $input. references.
				// Signature stays inputs []any for compatibility.
				err := wm.ActionManager.ExecuteAction(next, result.ExecutionID, []any{result})
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
	jsonResult, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		log.Printf("Cannot marshal result %s: %s", result.ResultID, err)
		return
	}
	if err := os.MkdirAll("./results", 0o755); err != nil {
		log.Printf("Cannot create results directory: %s", err)
		return
	}
	jsonFileName := "./results/" + result.ResultID + ".json"
	if err := os.WriteFile(jsonFileName, jsonResult, 0o600); err != nil {
		log.Printf("Cannot save result JSON file %s: %s", jsonFileName, err)
		return
	}
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
	Client       string               `json:"client,omitempty"`
	ClientTags   []string             `json:"client_tags,omitempty"`
	// MergeInput merges the upstream $input payload into this node's
	// $result payload (deep merge, $result keys win). Exported/imported
	// as part of the workflow JSON and forwarded to remote clients via
	// assignments; applied by the framework (workflow manager, client
	// runner), never by action implementations.
	MergeInput bool `json:"merge_input,omitempty"`
}

// AssignedTo reports whether a node is assigned to the given client.
// A node with no client targeting (empty Client and ClientTags) is
// broadcast and assigned to every client. Otherwise the node matches
// when the client ID equals Client or when any tag overlaps.
func (n *Node) AssignedTo(clientID string, tags []string) bool {
	if n.Client == "" && len(n.ClientTags) == 0 {
		return true
	}
	if n.Client != "" && n.Client == clientID {
		return true
	}
	if len(n.ClientTags) > 0 && len(tags) > 0 {
		set := make(map[string]struct{}, len(tags))
		for _, t := range tags {
			set[t] = struct{}{}
		}
		for _, t := range n.ClientTags {
			if _, ok := set[t]; ok {
				return true
			}
		}
	}
	return false
}

// args how this node was called
// The result from this execution
func (n *Node) GetNextNodes(inputs []any, result common.ResultData) []string {
	next_nodes := []string{}
	if n.Conditions == nil {
		return next_nodes
	}
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
	if n.Conditions == nil {
		return next_nodes
	}
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
	// First pass: register all action nodes so downstream consumers exist
	// before any trigger starts firing.
	for _, n := range data.Nodes {
		fmt.Println("[debug-remove] Adding node:", n.Name)
		if n.ActionType != "action" {
			continue
		}
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
	// Second pass: start triggers. AddTrigger begins trigger execution
	// immediately, so this must happen only after all actions are registered.
	for _, n := range data.Nodes {
		fmt.Println("[debug-remove] Adding node:", n.Name)
		if n.ActionType != "trigger" {
			continue
		}
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
			continue
		}
		wm.Workflows[data.Id].TriggerList[id] = nt
		wm.Actions2Workflow[id] = data.Id
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
	return os.WriteFile(fmt.Sprintf("workflows/%s.json", workflowId), []byte(jsonDef), 0600)
}

func (wm *workflowManager) SaveWorkflowToDB(workflowId string) error {
	jsonDef, err := wm.ExportWorkflowJSON(workflowId)
	if err != nil {
		return err
	}
	var data Workflow
	_ = json.Unmarshal([]byte(jsonDef), &data)
	return wm.db.SaveWorkflow(workflowId, jsonDef, data.TenantID)
}

func (wm *workflowManager) GetTriggerManager() actions.TriggerManager {
	return wm.TriggerManager
}

// ImportWorkflowJSON saves a workflow definition verbatim, keeping the ids
// and name from the JSON. If a workflow with the same id already exists it is
// overwritten (upsert).
func (wm *workflowManager) ImportWorkflowJSON(jsonDef string) error {
	data := Workflow{}
	err := json.Unmarshal([]byte(jsonDef), &data)
	if err != nil {
		log.Printf("Cannot unmarshal workflow: %s", err)
		return err
	}
	log.Printf("Importing workflow %s", data.Id)
	return wm.saveWorkflow(data)
}

// CloneWorkflowJSON creates a new independent copy of a workflow definition:
// it mints a fresh id for the workflow and for every node, rewrites the
// conditions and dependencies edges accordingly, and prefixes the name. The
// original definition is left untouched.
func (wm *workflowManager) CloneWorkflowJSON(jsonDef string) error {
	data := Workflow{}
	err := json.Unmarshal([]byte(jsonDef), &data)
	if err != nil {
		log.Printf("Cannot unmarshal workflow: %s", err)
		return err
	}

	dId, _ := typeid.WithPrefix("workflow")
	data.Id = dId.String()

	idMap := make(map[string]string, len(data.Nodes))
	clonedNodes := make(map[string]Node, len(data.Nodes))
	for oldID, n := range data.Nodes {
		newID := oldID
		switch n.ActionType {
		case "trigger":
			nId, _ := typeid.WithPrefix("trigger")
			newID = nId.String()
		case "action":
			nId, _ := typeid.WithPrefix("action")
			newID = nId.String()
		}
		if newID != oldID {
			idMap[oldID] = newID
		}
		n.Id = newID
		clonedNodes[newID] = n
	}
	data.Nodes = clonedNodes

	// Rewrite the edges that referenced the original node ids
	for id, n := range data.Nodes {
		if n.Conditions != nil {
			for cid, entry := range n.Conditions.Entries {
				for i, next := range entry.Nexts {
					if mapped, ok := idMap[next]; ok {
						entry.Nexts[i] = mapped
					}
				}
				n.Conditions.Entries[cid] = entry
			}
		}
		for i, dep := range n.Dependencies {
			if mapped, ok := idMap[dep]; ok {
				n.Dependencies[i] = mapped
			}
		}
		data.Nodes[id] = n
	}

	data.Name = "Clone of: " + data.Name

	jData, _ := json.MarshalIndent(data, "", "  ")
	log.Printf("Cloned workflow %s\n%s\n", data.Id, string(jData))
	return wm.saveWorkflow(data)
}

// IngestResult ingests a remotely reported ResultData (POST /api/results path).
// It mirrors what the Run loop does for local results: resolve the workflow,
// mint an ExecutionID for trigger roots, persist via saveResult, compute the
// next nodes and kick off downstream actions. It returns the next node IDs.
func (wm *workflowManager) IngestResult(result common.ResultData) (common.ResultData, []string, error) {
	if result.ActionID == "" {
		return result, nil, errors.New("action_id is required")
	}
	if result.ResultID == "" {
		rID, _ := typeid.WithPrefix("result")
		result.ResultID = rID.String()
	}
	if result.CreatedAt.IsZero() {
		result.CreatedAt = time.Now()
	}
	if result.ExecutorID == "" {
		result.ExecutorID = wm.executorID
	}

	// Resolve workflow ID: prefer explicit field, then in-memory index,
	// then scan in-memory definitions and the DB for the node.
	workflowID := result.WorkflowID
	if workflowID == "" {
		workflowID = wm.Actions2Workflow[result.ActionID]
	}
	var node Node
	var nodeFound bool
	if workflowID != "" {
		if wf, ok := wm.Workflows[workflowID]; ok {
			if n, ok := wf.Definition.Nodes[result.ActionID]; ok {
				node = n
				nodeFound = true
			}
		}
	}
	if !nodeFound {
		for id, wf := range wm.Workflows {
			if n, ok := wf.Definition.Nodes[result.ActionID]; ok {
				node = n
				nodeFound = true
				workflowID = id
				break
			}
		}
	}
	if !nodeFound && wm.db != nil {
		rows, err := wm.db.GetWorkflows(true, true)
		if err == nil {
			for _, row := range rows {
				var def Workflow
				if err := json.Unmarshal([]byte(row.JSONDefinition), &def); err != nil {
					continue
				}
				if n, ok := def.Nodes[result.ActionID]; ok {
					node = n
					nodeFound = true
					workflowID = def.Id
					if workflowID == "" {
						workflowID = row.ID
					}
					break
				}
			}
		}
	}
	if !nodeFound {
		return result, nil, fmt.Errorf("unknown action_id %q", result.ActionID)
	}
	result.WorkflowID = workflowID

	if node.ActionType == "trigger" && result.ExecutionID == "" {
		eID, _ := typeid.WithPrefix("execution")
		result.ExecutionID = eID.String()
	}
	// Server-side enforcement of merge_input: fold the recorded upstream
	// dependency payloads (in Dependencies order) into the reported
	// payload. Deep merge, reported $result wins. This is a no-op when
	// the producer already merged, and best-effort when history is
	// missing (e.g. after a restart).
	if node.MergeInput {
		result.Payload = wm.applyMergeInput(result, node)
	}
	wm.saveResult(result)
	wm.rememberResult(result)

	nexts := node.GetNextNodes([]any{}, result)
	for _, next := range nexts {
		if !wm.enableActions {
			continue
		}
		if err := wm.ActionManager.ExecuteAction(next, result.ExecutionID, []any{result}); err != nil {
			// Best effort: remote clients may own the downstream node, so
			// a locally unknown action is not fatal.
			log.Printf("IngestResult: skipping local execute of %s: %s", next, err)
		}
	}
	if nexts == nil {
		nexts = []string{}
	}
	return result, nexts, nil
}

func (wm *workflowManager) saveWorkflow(data Workflow) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		log.Printf("Cannot marshal workflow: %s", err)
		return err
	}
	return wm.db.SaveWorkflow(data.Id, string(jsonData), data.TenantID)
}

// rememberResult records a result payload for later merge_input lookups.
func (wm *workflowManager) rememberResult(result common.ResultData) {
	if result.ExecutionID == "" || result.ActionID == "" {
		return
	}
	wm.mu.Lock()
	defer wm.mu.Unlock()
	if wm.execHistory == nil {
		wm.execHistory = make(map[string]map[string]any)
	}
	byNode, ok := wm.execHistory[result.ExecutionID]
	if !ok {
		byNode = make(map[string]any)
		wm.execHistory[result.ExecutionID] = byNode
	}
	byNode[result.ActionID] = result.Payload
}

// applyMergeInput folds recorded dependency payloads into result.Payload.
// Dependencies merge in order, the reported result wins on conflicts.
func (wm *workflowManager) applyMergeInput(result common.ResultData, node Node) any {
	if result.ExecutionID == "" || len(node.Dependencies) == 0 {
		return result.Payload
	}
	wm.mu.Lock()
	byNode := wm.execHistory[result.ExecutionID]
	inputs := make([]any, 0, len(node.Dependencies))
	for _, dep := range node.Dependencies {
		if byNode != nil {
			if p, ok := byNode[dep]; ok {
				inputs = append(inputs, p)
			}
		}
	}
	wm.mu.Unlock()
	merged := result.Payload
	for _, in := range inputs {
		merged = common.MergePayloads(in, merged)
	}
	return merged
}
