package filechange

// Package filechange implements a workflow trigger that watches for file system
// events (create, write, rename, remove, chmod) on a specified path using fsnotify.
// When a matching event occurs, it emits a result to the workflow engine.

import (
	"fmt"
	"log"
	"reflect"
	"strings"

	"github.com/mcmx/nitejaguar/common"

	"github.com/fsnotify/fsnotify"
)

// Event struct for event handling
type event struct {
	Type string `json:"type"` // Event type
	File any    `json:"file"` // Generic payload for event-specific data
}

type filechange struct {
	data    common.ActionArgs
	watcher *fsnotify.Watcher
	events  chan common.ResultData
}

func (t *filechange) Stop() error {
	log.Println("Stopping the filechange trigger")
	return t.watcher.Close()
}

func New(events chan common.ResultData, data common.ActionArgs) (common.Action, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}

	s := &filechange{
		data:    data,
		events:  events,
		watcher: watcher,
	}
	s.data.ActionType = "trigger"
	log.Println("Initializing File Change Trigger with id:", s.data.Id)

	return s, nil
}

func (t *filechange) Execute(executionId string, inputs []any) {
	log.Println("Executing File Change Trigger with id:", t.data.Id)
	// Add the path to watch
	if reflect.TypeOf(t.data.Args).Kind() != reflect.Map {
		log.Println("[filechange] Invalid arguments type")
		return
	}
	args := t.data.Args.(map[string]string)
	if args["path"] == "" {
		log.Println("[filechange] Invalid path")
		return
	}
	// adds the path to the watcher
	err := t.watcher.Add(args["path"])
	if err != nil {
		log.Println("Error adding watcher:", err)
		return
	}

	eventTypeParam, ok := args["event_type"]
	eventType := fsnotify.Create | fsnotify.Write | fsnotify.Rename | fsnotify.Remove | fsnotify.Chmod

	if ok {
		switch eventTypeParam {
		case "create":
			eventType = fsnotify.Create
		case "write":
			eventType = fsnotify.Write
		case "rename":
			eventType = fsnotify.Rename
		case "remove":
			eventType = fsnotify.Remove
		case "chmod":
			eventType = fsnotify.Chmod
		}
	}
	log.Printf("Adding watcher to: '%s' on events: %s", args["path"], eventType)
	// Start watching in a goroutine
	go func() {
		for {
			select {
			case event, ok := <-t.watcher.Events:
				if !ok {
					log.Println("Watcher closed")
					return
				}
				if event.Op.Has(eventType) {
					log.Println(event)
					t.events <- t.sendResult(executionId, strings.ToLower(event.Op.String()), event.Name)
				}
			case err, ok := <-t.watcher.Errors:
				if !ok {
					log.Println("Watcher error closed")
					return
				}
				log.Println("error:", err)
			}
		}
	}()

	// Keep the Execute method running without blocking
	select {} // This keeps the method running without consuming resources

}

func (t *filechange) sendResult(executionId string, eventType string, file string) common.ResultData {
	return common.ResultData{
		ExecutionID: executionId,
		ActionID:    t.data.Id,
		ActionType:  t.data.ActionType,
		ActionName:  t.data.Name,
		Payload:     event{Type: eventType, File: file},
	}
}

// GetArgs returns the ActionArgs associated with the filechange
func (t *filechange) GetArgs() common.ActionArgs {
	return t.data
}
