package actions

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/actions/cron"
	"github.com/mcmx/nitejaguar/internal/actions/filechange"
	"github.com/mcmx/nitejaguar/internal/actions/webhook"

	"go.jetify.com/typeid"
)

// TriggerCatalog lists the designer schemas for every runnable trigger,
// one entry per trigger package. When adding a trigger, define its
// CatalogEntry next to the implementation and add it here alongside
// the AddTrigger case below.
func TriggerCatalog() []common.DesignerCatalogEntry {
	return []common.DesignerCatalogEntry{
		filechange.CatalogEntry(),
		cron.CatalogEntry(),
		webhook.CatalogEntry(),
	}
}

// DesignerCatalog is the single visual source of truth for the designer
// picker and typed inspector form: every trigger and action the engine
// can run, with new-node defaults and per-argument field schemas.
func DesignerCatalog() []common.DesignerCatalogEntry {
	return append(TriggerCatalog(), ActionCatalog()...)
}

// TriggerManager manages triggers and their events
type TriggerManager struct {
	events   chan common.ResultData
	triggers map[string]common.Action
}

// NewTriggerManager creates a new TriggerManager instance
func NewTriggerManager() *TriggerManager {
	return &TriggerManager{
		triggers: make(map[string]common.Action),
		events:   make(chan common.ResultData),
	}
}

// New creates a new Action instance and adds it to the TriggerList.
// It takes a common.ActionArgs object as input, which contains the action name and other relevant data.
// The function returns a pointer to the newly created Action instance and an error if any occurs.
func (ts *TriggerManager) AddTrigger(data common.ActionArgs) (common.Action, string, error) {
	if data.Id == "" {
		tid, _ := typeid.WithPrefix("trigger")
		data.Id = tid.String()
	}

	// TODO Add a validation that the triger_id doesn't exist in the triggers already
	var trigger common.Action
	var err error
	switch data.ActionName {
	case "filechange":
		trigger, err = filechange.New(ts.events, data)
	case "cron":
		trigger, err = cron.New(ts.events, data)
	case "webhook":
		trigger, err = webhook.New(ts.events, data)
	default:
		return nil, "", fmt.Errorf("unknown action_name: %q", data.ActionName)
	}
	if err != nil {
		return nil, "", err
	}
	ts.triggers[data.Id] = trigger
	// TODO: Add an error handler to the trigger execution
	go trigger.Execute("", nil)
	return trigger, data.Id, nil
}

func (ts *TriggerManager) RemoveTrigger(id string) error {
	trigger, ok := ts.triggers[id]
	if !ok || trigger == nil {
		return fmt.Errorf("trigger not found: %s", id)
	}
	err := trigger.Stop()
	delete(ts.triggers, id)
	if err != nil {
		fmt.Println("Error while stopping action:", err)
		return err
	}
	return nil
}

// FindTriggerIDByName resolves a trigger name to its registry id.
// It returns false when no trigger with the given name exists.
func (ts *TriggerManager) FindTriggerIDByName(name string) (string, bool) {
	for id, trigger := range ts.triggers {
		if trigger == nil {
			continue
		}
		if trigger.GetArgs().Name == name {
			return id, true
		}
	}
	return "", false
}

func (ts *TriggerManager) Run(wmEvents chan common.ResultData, ctx context.Context) {
	log.Println("Starting Trigger Service")
	var value common.ResultData
	for {
		select {
		case value = <-ts.events:
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
			log.Println("Trigger Service stopped.")
			return
		}
	}
}

func (ts *TriggerManager) ListTriggers() {
	for k, v := range ts.triggers {
		fmt.Println("Trigger:", k, v)
	}
}
