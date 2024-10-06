package filechange

import (
	"fmt"

	"encoding/json"
	"github.com/fsnotify/fsnotify"
	"nitejaguar/internal/triggers/common"
)

type filechange struct {
	data    *common.TriggerArgs
	watcher *fsnotify.Watcher
	events  chan string
}

func New(events chan string, data *common.TriggerArgs) (*filechange, error) {
	s := &filechange{
		data:   data,
		events: events,
	}
	fmt.Println("Initializing File Change Trigger with id:", s.data.Id)
	s.watcher, _ = fsnotify.NewWatcher()
	return s, nil
}

func (t *filechange) Execute() error {
	fmt.Println("Executing File Change Trigger with id:", t.data.Id)
	defer t.watcher.Close()

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

type result struct {
	TriggerID string `json:"trigger_id"`
	Trigger   string `json:"trigger"`
	File      string `json:"file"`
	Type      string `json:"type"`
}

func (t *filechange) sendResult(event string, file string) string {
	r := &result{
		TriggerID: t.data.Id,
		Trigger:   t.data.TriggerType,
		File:      file,
		Type:      event,
	}
	res, _ := json.Marshal(r)
	return string(res)
}
