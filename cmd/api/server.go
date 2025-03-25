package api

import (
	"fmt"
	"log"

	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/database"
	"github.com/mcmx/nitejaguar/internal/server"
	"github.com/mcmx/nitejaguar/internal/workflow"
	"go.jetify.com/typeid"
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
	go wm.Run()
	// TODO we should not do this
	_, _, e := wm.ActionManager.AddAction(common.ActionArgs{
		ActionName: "fileAction",
		ActionType: "action",
		Name:       "Test file action",
		Args:       []string{"rename", "/tmp/test.txt", "/tmp/test2.txt"},
	})
	if e != nil {
		fmt.Println("There was an error", e)
	}

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

		aId, _ := typeid.WithPrefix("trigger")
		myArgs := common.ActionArgs{
			Id:         aId.String(),
			ActionName: actionName,
			ActionType: "trigger",
			Name:       fmt.Sprintf("CLI Trigger: %s", actionName),
			Args:       args,
		}

		// crear un peque workflow
		w := workflow.Workflow{
			Name:        "First Workflow",
			TriggerList: make(map[string]common.ActionArgs),
			ActionList:  make(map[string]common.ActionArgs),
			Nodes:       make(map[string]workflow.Node),
		}
		w.TriggerList[myArgs.Id] = myArgs
		e := wm.AddWorkflow(w)
		if e != nil {
			log.Println(e)
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
