package api

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/database"
	"github.com/mcmx/nitejaguar/internal/server"
	"github.com/mcmx/nitejaguar/internal/workflow"
)

var (
	actionName    string
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
	if enableActions && actionName != "" {
		// myArgs := common.ActionArgs{
		// 	ActionName: "filechangeTrigger",
		// 	ActionType: "trigger",
		// 	Name:       "Test filechange trigger",
		// 	Args:       []string{"/tmp"},
		// }

		b, e := os.ReadFile("./workflows/workflow_01jq9c43qhejts98g7n1krqpgd.json")
		if e != nil {
			log.Println("Error reading file: ", e)
		}
		log.Println(string(b))
		w1 := workflow.Workflow{}
		e = json.Unmarshal(b, &w1)
		if e != nil {
			log.Println("Error unmarshaling file: ", e)
		}

		e = wm.AddWorkflow(w1)
		if e != nil {
			log.Println(e)
		}

	}

	server := server.NewServer(myDb, *wm)
	log.Println("Starting server...")
	err := server.ListenAndServe()
	if err != nil {
		log.Fatalf("Cannot start server: %s", err)
	}
}
