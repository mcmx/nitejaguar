package actions

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/actions/fileaction"
)

// ActionManager manages a collection of actions
type ActionManager struct {
	actions map[string]common.Action
	events  chan common.ResultData
}

// NewActionManager creates a new ActionManager instance
func NewActionManager() *ActionManager {
	return &ActionManager{
		actions: make(map[string]common.Action),
	}
}

// AddAction adds a new action to the manager
func (am *ActionManager) AddAction(data common.ActionArgs) (common.Action, string, error) {
	if data.Id == "" {
		data.Id = uuid.New().String()
	}

	switch data.ActionName {
	case "fileAction":
		action, err := fileaction.New(am.events, data)
		if err != nil {
			return nil, "", err
		}
		am.actions[data.Id] = action
		// TODO: Add an error handler to the trigger execution
		go action.Execute()
		return action, data.Id, nil
	}
	fmt.Println("Action added with id:", data.Id)
	return nil, "", nil
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
func (am *ActionManager) ExecuteAction(id string) error {
	action, exists := am.actions[id]
	if !exists {
		return fmt.Errorf("action with id %s does not exist", id)
	}
	action.Execute()
	return nil
}

// ListActions lists all actions managed by the ActionManager
func (am *ActionManager) ListActions() {
	for id := range am.actions {
		fmt.Println("Managed Action ID:", id)
	}
}
