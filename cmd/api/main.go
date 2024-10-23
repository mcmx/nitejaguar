package main

import (
	"fmt"
	"nitejaguar/internal/actions"
	"nitejaguar/internal/actions/common"
	"nitejaguar/internal/database"
	"nitejaguar/internal/server"
)

func main() {

	myDb := database.New()
	// TODO Add a Triggers service

	myArgs := common.ActionArgs{
		ActionName: "filechangeTrigger",
		ActionType: "trigger",
		Name:       "Test filechange trigger",
		Args:       []string{"/tmp"},
	}

	ts := actions.TriggerService{}

	go ts.Run()
	ts.New(myArgs)

	myArgs.Args = []string{"/workspace"}
	myArgs.Name = "Test filechange trigger 2"

	ts.New(myArgs)

	server := server.NewServer(myDb)

	err := server.ListenAndServe()
	if err != nil {
		panic(fmt.Sprintf("cannot start server: %s", err))
	}

}
