package filechange

import (
	"fmt"
	"nitejaguar/internal/triggers"

	"github.com/fsnotify/fsnotify"
)

func Execute(data *triggers.TriggerArgs) error {
	fmt.Println("Executing File Change Trigger with id:", data.Id)
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&fsnotify.Write == fsnotify.Write {
					fmt.Println("modified file:", event.Name)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				fmt.Println("error:", err)
			}
		}
	}()

	err = watcher.Add(data.Args[0])
	if err != nil {
		return err
	}
	<-make(chan struct{})

	return nil
}
