package api

import (
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/database"
	"github.com/mcmx/nitejaguar/internal/server"
	"github.com/mcmx/nitejaguar/internal/workflow"
)

var (
	actionName    string
	actionArgs    string
	enableActions bool
)

func RunServer() {
	myDb := database.New()
	defer myDb.Close()
	wm := workflow.NewWorkflowManager()

	go wm.TriggerManager.Run()

	wm.ActionManager.AddAction(common.ActionArgs{
		ActionName: "fileAction",
		ActionType: "action",
		Name:       "Test file action",
		Args:       []string{"rename", "/tmp/test.txt", "/tmp/test2.txt"},
	})

	// Handle server action if specified
	enableActions = true
	actionName = "filechangeTrigger"
	actionArgs = "/tmp"
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
			Id:         uuid.New().String(),
			ActionName: actionName,
			ActionType: "trigger",
			Name:       fmt.Sprintf("CLI Trigger: %s", actionName),
			Args:       args,
		}

		_, err := wm.TriggerManager.AddTrigger(myArgs)
		if err != nil {
			log.Fatalf("Cannot create new trigger: %s", err)
		}
		log.Printf("Created new trigger: %s, with args: %v", actionName, args)
	}

	server := server.NewServer(myDb, *wm)
	log.Println("Starting server...")
	err := server.ListenAndServe()
	if err != nil {
		log.Fatalf("Cannot start server: %s", err)
	}
}
