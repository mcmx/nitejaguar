package main

import (
	"fmt"
	"nitejaguar/internal/database"
	"nitejaguar/internal/server"
	"nitejaguar/internal/triggers"
	"nitejaguar/internal/triggers/common"
)

func main() {

	myDb := database.New()
	// TODO Add a Triggers service

	myArgs := common.TriggerArgs{
		TriggerType: "filechange",
		Name:        "Test filechange trigger",
		Args:        []string{"/tmp"},
	}
	go func() {
		var value string
		for {
			for _, t := range triggers.TriggerList {
				value = <-t.Events
				fmt.Println("Trigger Result", value)
			}
		}
	}()

	triggers.New(&myArgs)

	server := server.NewServer(myDb)

	err := server.ListenAndServe()
	if err != nil {
		panic(fmt.Sprintf("cannot start server: %s", err))
	}

}
