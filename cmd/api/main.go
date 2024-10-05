package main

import (
	"fmt"
	"nitejaguar/internal/database"
	"nitejaguar/internal/server"
	"nitejaguar/internal/triggers"
	"nitejaguar/internal/triggers/filechange"
)

func main() {

	myDb := database.New()
	// TODO Add a Triggers service

	myArgs := triggers.TriggerArgs{
		Id:   "filechange",
		Name: "Test filechange trigger",
		Args: []string{"/tmp"},
	}
	filechange, _ := filechange.New(&myArgs)
	go filechange.Execute()

	server := server.NewServer(myDb)

	err := server.ListenAndServe()
	if err != nil {
		panic(fmt.Sprintf("cannot start server: %s", err))
	}

}
