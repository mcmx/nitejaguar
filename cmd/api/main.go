package main

import (
	"fmt"
	"nitejaguar/internal/database"
	"nitejaguar/internal/server"
	"nitejaguar/internal/triggers"
)

func main() {

	myDb := database.New()
	// TODO Add a Triggers service

	myArgs := triggers.TriggerArgs{
		TriggerType: "filechange",
		Name:        "Test filechange trigger",
		Args:        []string{"/tmp"},
	}

	// filechange, _ := filechange.New(&myArgs)
	triggers.New(&myArgs)

	server := server.NewServer(myDb)

	err := server.ListenAndServe()
	if err != nil {
		panic(fmt.Sprintf("cannot start server: %s", err))
	}

}
