package actions

import (
	"errors"
	"fmt"

	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/actions/fileaction"
	"go.jetify.com/typeid"
)

// ActionManager manages a collection of actions
type ActionManager struct {
	actions       map[string]common.Action
	events        chan common.ResultData
	enableActions bool
}

// NewActionManager creates a new ActionManager instance
func NewActionManager(enableActions bool) *ActionManager {
	return &ActionManager{
		enableActions: enableActions,
		actions:       make(map[string]common.Action),
	}
}

// AddAction adds a new action to the manager
func (am *ActionManager) AddAction(data common.ActionArgs) (common.Action, string, error) {
	if !am.enableActions {
		return nil, "", errors.New("actions are disabled")
	}
	if data.Id == "" {
		tid, _ := typeid.WithPrefix("action")
		data.Id = tid.String()
	}

	var action common.Action
	var err error
	switch data.ActionName {

	case "fileAction":
		action, err = fileaction.New(am.events, data)
	}

	if err != nil {
		return nil, "", err
	}
	am.actions[data.Id] = action
	// TODO: Add an error handler to the trigger execution
	fmt.Println("Action added with id:", data.Id)
	return action, data.Id, nil
}

// RemoveAction removes an action from the manager by ID
func (am *ActionManager) RemoveAction(id string) {
	e := am.actions[id].Stop()
	if e != nil {
		fmt.Println("Error stopping action:", e)
	}
	delete(am.actions, id)
	fmt.Println("Action removed with id:", id)
}

// ExecuteAction executes an action by ID
func (am *ActionManager) ExecuteAction(id string, executionId string, inputs []any) error {
	action, exists := am.actions[id]
	if !exists {
		return fmt.Errorf("action with id %s does not exist", id)
	}
	fmt.Println("Executing action:", action)
	go action.Execute(executionId, inputs)
	return nil
}

// ListActions lists all actions managed by the ActionManager
func (am *ActionManager) ListActions() {
	for id := range am.actions {
		fmt.Println("Managed Action ID:", id)
	}
}
