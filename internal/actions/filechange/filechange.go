package filechange

import (
	"fmt"
	"reflect"

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
	fmt.Println("Stopping the filechange trigger")
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
	fmt.Println("Initializing File Change Trigger with id:", s.data.Id)

	return s, nil
}

func (t *filechange) Execute() {
	fmt.Println("Executing File Change Trigger with id:", t.data.Id)

	// Start watching in a goroutine
	go func() {
		for {
			select {
			case event, ok := <-t.watcher.Events:
				if !ok {
					fmt.Println("Watcher closed")
					return
				}
				if event.Op.Has(fsnotify.Write) {
					t.events <- t.sendResult("write", event.Name)
				}
				if event.Op.Has(fsnotify.Create) {
					fmt.Println("Create event:", event.Name)
					t.events <- t.sendResult("create", event.Name)
				}
				if event.Op.Has(fsnotify.Rename) {
					t.events <- t.sendResult("rename", event.Name)
				}
				if event.Op.Has(fsnotify.Remove) {
					t.events <- t.sendResult("remove", event.Name)
				}
			case err, ok := <-t.watcher.Errors:
				if !ok {
					fmt.Println("Watcher error closed")
					return
				}
				fmt.Println("error:", err)
			}
		}
	}()

	// Add the path to watch
	if reflect.TypeOf(t.data.Args).Kind() != reflect.Map {
		fmt.Println("Invalid arguments type")
		return
	}
	args := t.data.Args.(map[string]string)
	if args["path"] == "" {
		fmt.Println("Invalid path")
		return
	}
	fmt.Println("Adding watcher to:", args["path"])
	err := t.watcher.Add(args["path"])
	if err != nil {
		fmt.Println("Error adding watcher:", err)
		return
	}

	// Keep the Execute method running without blocking
	select {} // This keeps the method running without consuming resources

}

func (t *filechange) sendResult(eventType string, file string) common.ResultData {
	return common.ResultData{
		ActionID:   t.data.Id,
		ActionType: t.data.ActionType,
		ActionName: t.data.Name,
		Payload:    event{Type: eventType, File: file},
	}
}

// GetArgs returns the ActionArgs associated with the filechange
func (t *filechange) GetArgs() common.ActionArgs {
	return t.data
}
