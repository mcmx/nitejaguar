package api

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/mcmx/nitejaguar/internal/database"
	"github.com/mcmx/nitejaguar/internal/server"
	"github.com/mcmx/nitejaguar/internal/workflow"
)

type ServerArgs struct {
	EnableActions  bool
	ImportWorkflow string
}

func RunServer(args ServerArgs) {
	var wg sync.WaitGroup
	myDb := database.New()
	defer myDb.Close()
	defer fmt.Println("Finish execution")
	wm := workflow.NewWorkflowManager(args.EnableActions, myDb)
	server := server.NewServer(myDb, wm)
	go func() {
		wg.Add(1)
		defer wg.Done()
		err := server.ListenAndServe()
		if err != nil {
			fmt.Printf("Cannot start server: %s\n", err)
			os.Exit(1)
		}
		log.Println("Starting server...")
	}()

	go func() {
		wg.Add(1)
		defer wg.Done()
		wm.Run()
	}()
	// TODO we should not do this
	// _, _, e := wm.ActionManager.AddAction(common.ActionArgs{
	// 	ActionName: "fileAction",
	// 	ActionType: "action",
	// 	Name:       "Test file action",
	// 	Args:       []string{"rename", "/tmp/test.txt", "/tmp/test2.txt"},
	// })
	// if e != nil {
	// 	log.Println("There was an error", e)
	// }

	// myArgs := common.ActionArgs{
	// 	ActionName: "filechangeTrigger",
	// 	ActionType: "trigger",
	// 	Name:       "Test filechange trigger",
	// 	Args:       []string{"/tmp"},
	// }
	if args.ImportWorkflow != "" {
		log.Println("Importing workflow:", args.ImportWorkflow)
		wImportJSON, e := os.ReadFile(args.ImportWorkflow)
		if e != nil {
			log.Println("error importing workflow 1", e)
		} else {
			e := wm.ImportWorkflowJSON(wImportJSON)
			if e != nil {
				log.Println("error importing workflow 2", e)
			}
		}
	}
	log.Println("Loading workflows...")

	workflows, e := myDb.GetWorkflows()
	if e != nil {
		log.Println("Error getting workflows: ", e)
	}
	for _, wf := range workflows {
		w1 := workflow.Workflow{}
		e = json.Unmarshal(wf, &w1)
		if e != nil {
			log.Println("Error unmarshaling workflow: ", e)
		}

		e = wm.AddWorkflow(w1)
		if e != nil {
			log.Println(e)
		}

	}
	wg.Wait()

}
