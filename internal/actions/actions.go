package actions

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/actions/datetime"
	"github.com/mcmx/nitejaguar/internal/actions/fileaction"
	"github.com/mcmx/nitejaguar/internal/actions/set"
	"github.com/mcmx/nitejaguar/internal/actions/transfer"
	"github.com/mcmx/nitejaguar/internal/actions/wait"
	"go.jetify.com/typeid"
)

// SupportedCredentialTypes lists the credential types accepted at
// creation. The provider-collection registry in common/ is the source of
// truth (provider-declared types, not a hardcoded list); this wrapper
// stays for callers that import this package.
func SupportedCredentialTypes() []string {
	return common.KnownCredentialTypes()
}

// RequiredCredentialTypes declares which credential type(s) an action
// needs by action_name, derived from its collection's single shared
// type. An empty slice means the action runs without a credential
// (the `core` collection, or an unknown action). Nodes that set
// credential_ref fetch the secret just-in-time regardless; fetches for
// collection-typed nodes are strictly enforced server-side.
func RequiredCredentialTypes(actionName string) []string {
	ctype, known := common.CredentialTypeForAction(actionName)
	if !known || ctype == "" {
		return nil
	}
	return []string{ctype}
}

// nodeRegistration binds a node's constructor to its designer schema in
// a single entry, so dispatch and the designer catalog can never drift:
// adding a node means adding one line to the registry below.
type nodeRegistration struct {
	entry common.DesignerCatalogEntry
	build func(chan common.ResultData, common.ActionArgs) (common.Action, error)
}

var actionRegistry = []nodeRegistration{
	{fileaction.CatalogEntry(), fileaction.New},
	{datetime.CatalogEntry(), datetime.New},
	{wait.CatalogEntry(), wait.New},
	{transfer.CatalogEntry(), transfer.New},
	{set.CatalogEntry(), set.New},
}

// ActionCatalog lists the designer schemas for every runnable action,
// derived from the same registry AddAction dispatches through.
func ActionCatalog() []common.DesignerCatalogEntry {
	out := make([]common.DesignerCatalogEntry, 0, len(actionRegistry))
	for _, r := range actionRegistry {
		out = append(out, r.entry)
	}
	return out
}

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
		events:        make(chan common.ResultData),
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

	var build func(chan common.ResultData, common.ActionArgs) (common.Action, error)
	for _, r := range actionRegistry {
		if r.entry.ActionName == data.ActionName {
			build = r.build
			break
		}
	}
	if build == nil {
		return nil, "", fmt.Errorf("unknown action_name: %q", data.ActionName)
	}
	action, err := build(am.events, data)
	if err != nil {
		return nil, "", err
	}
	am.actions[data.Id] = action
	// TODO: Add an error handler to the trigger execution
	fmt.Println("Action added with id:", data.Id)
	return action, data.Id, nil
}

func (am *ActionManager) Run(wmEvents chan common.ResultData, ctx context.Context) {
	log.Println("Starting Action Service")
	var value common.ResultData
	for {
		select {
		case value = <-am.events:
			if value.CreatedAt.IsZero() {
				value.CreatedAt = time.Now()
			}
			if value.ResultID == "" {
				vId, _ := typeid.WithPrefix("result")
				value.ResultID = vId.String()
			}
			// send the event result to workmanager
			wmEvents <- value
		case <-time.After(50 * time.Millisecond):
			// do nothing
		case <-ctx.Done():
			log.Println("Action Service stopped.")
			return
		}
	}
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
