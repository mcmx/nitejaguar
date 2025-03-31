package api

import (
	"encoding/json"
	"log"

	"github.com/mcmx/nitejaguar/internal/database"
	"github.com/mcmx/nitejaguar/internal/server"
	"github.com/mcmx/nitejaguar/internal/workflow"
)

func RunServer(enableActions bool) {
	myDb := database.New()
	defer myDb.Close()
	wm := workflow.NewWorkflowManager(enableActions, myDb)
	go wm.Run()
	// TODO we should not do this
	// _, _, e := wm.ActionManager.AddAction(common.ActionArgs{
	// 	ActionName: "fileAction",
	// 	ActionType: "action",
	// 	Name:       "Test file action",
	// 	Args:       []string{"rename", "/tmp/test.txt", "/tmp/test2.txt"},
	// })
	// if e != nil {
	// 	fmt.Println("There was an error", e)
	// }

	// Handle server action if specified
	if enableActions {
		// myArgs := common.ActionArgs{
		// 	ActionName: "filechangeTrigger",
		// 	ActionType: "trigger",
		// 	Name:       "Test filechange trigger",
		// 	Args:       []string{"/tmp"},
		// }
		log.Println("Loading workflows...")

		workflows, e := myDb.GetWorkflows()
		if e != nil {
			log.Println("Error getting workflows: ", e)
		}
		w1 := workflow.Workflow{}

		for _, workflow := range workflows {
			e = json.Unmarshal(workflow, &w1)
			if e != nil {
				log.Println("Error unmarshaling workflow: ", e)
			}

			e = wm.AddWorkflow(w1)
			if e != nil {
				log.Println(e)
			}

		}
	}

	server := server.NewServer(myDb, wm)
	log.Println("Starting server...")
	err := server.ListenAndServe()
	if err != nil {
		log.Fatalf("Cannot start server: %s", err)
	}
}
