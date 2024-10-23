package filechange

import (
	"fmt"

	"encoding/json"
	"nitejaguar/internal/actions/common"

	"github.com/fsnotify/fsnotify"
)

type filechange struct {
	data    common.ActionArgs
	watcher *fsnotify.Watcher
	events  chan string
}

func (t *filechange) Stop() error {
	fmt.Println("Stopping the filechange trigger")
	return t.watcher.Close()
}

func New(events chan string, data common.ActionArgs) (*filechange, error) {
	s := &filechange{
		data:   data,
		events: events,
	}
	s.data.ActionType = "trigger"
	fmt.Println("Initializing File Change Trigger with id:", s.data.Id)
	s.watcher, _ = fsnotify.NewWatcher()
	return s, nil
}

func (t *filechange) Execute() error {
	fmt.Println("Executing File Change Trigger with id:", t.data.Id)
	// defer t.watcher.Close()

	go func() {
		for {
			select {
			case event, ok := <-t.watcher.Events:
				if !ok {
					return
				}
				if event.Op.Has(fsnotify.Write) {
					t.events <- t.sendResult("write", event.Name)
				}
				if event.Op.Has(fsnotify.Create) {
					t.events <- t.sendResult("create", event.Name)
				}
				if event.Op.Has(fsnotify.Rename) {
					t.events <- t.sendResult("rename", event.Name)
				}
			case err, ok := <-t.watcher.Errors:
				if !ok {
					return
				}
				fmt.Println("error:", err)
			}
		}
	}()

	err := t.watcher.Add(t.data.Args[0])
	if err != nil {
		return err
	}
	<-make(chan struct{})

	return nil
}

type resultData struct {
	ActionID   string    `json:"action_id"`
	ActionType string    `json:"action_type"`
	ActionName string    `json:"name"`
	Results    []results `json:"results"`
}

type results struct {
	File string `json:"file"`
	Type string `json:"type"`
}

func (t *filechange) sendResult(event string, file string) string {
	r := &resultData{
		ActionID:   t.data.Id,
		ActionType: t.data.ActionType,
		ActionName: t.data.Name,
		Results: []results{
			{File: file, Type: event},
		},
	}
	res, _ := json.Marshal(r)
	return string(res)
}
