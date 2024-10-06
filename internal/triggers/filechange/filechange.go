package filechange

import (
	"fmt"

	"github.com/fsnotify/fsnotify"
	"nitejaguar/internal/triggers/common"
)

type filechange struct {
	data    *common.TriggerArgs
	watcher *fsnotify.Watcher
}

func New(data *common.TriggerArgs) (*filechange, error) {
	s := &filechange{
		data: data,
	}
	fmt.Println("Initializing File Change Trigger with id:", s.data.Id)
	s.watcher, _ = fsnotify.NewWatcher()
	return s, nil
}

func (s *filechange) Execute() error {
	fmt.Println("Executing File Change Trigger with id:", s.data.Id)
	defer s.watcher.Close()

	go func() {
		for {
			select {
			case event, ok := <-s.watcher.Events:
				if !ok {
					return
				}
				if event.Op.Has(fsnotify.Write) {
					fmt.Println("write file:", event.Name)
				}
				if event.Op.Has(fsnotify.Create) {
					fmt.Println("create file:", event.Name)
				}
				if event.Op.Has(fsnotify.Rename) {
					fmt.Println("rename file:", event.Name)
				}
			case err, ok := <-s.watcher.Errors:
				if !ok {
					return
				}
				fmt.Println("error:", err)
			}
		}
	}()

	err := s.watcher.Add(s.data.Args[0])
	if err != nil {
		return err
	}
	<-make(chan struct{})

	return nil
}
