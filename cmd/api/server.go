package api

import (
	"fmt"
	"log"

	"nitejaguar/common"
	"nitejaguar/internal/actions"
	"nitejaguar/internal/database"
	"nitejaguar/internal/server"
)

var (
	actionName    string
	actionArgs    string
	enableActions bool
)

func RunServer() {
	myDb := database.New()
	ts := actions.TriggerService{}
	go ts.Run()

	// Handle server action if specified
	if enableActions && actionName != "" {
		args := []string{}
		if actionArgs != "" {
			args = append(args, actionArgs)
		}

		// myArgs := common.ActionArgs{
		// 	ActionName: "filechangeTrigger",
		// 	ActionType: "trigger",
		// 	Name:       "Test filechange trigger",
		// 	Args:       []string{"/tmp"},
		// }

		myArgs := common.ActionArgs{
			ActionName: actionName,
			ActionType: "trigger",
			Name:       fmt.Sprintf("CLI Trigger: %s", actionName),
			Args:       args,
		}

		ts.New(myArgs)
		log.Printf("Created new trigger: %s, with args: %v", actionName, args)
	}

	server := server.NewServer(myDb, ts)
	log.Println("Starting server...")
	err := server.ListenAndServe()
	if err != nil {
		log.Fatalf("Cannot start server: %s", err)
	}
}
