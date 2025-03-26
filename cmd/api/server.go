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

		aId, _ := typeid.WithPrefix("trigger")

		// crear un peque workflow
		w := workflow.Workflow{
			Name:  "First Workflow",
			Nodes: make(map[string]workflow.Node),
		}

		n := workflow.Node{
			Id:          aId.String(),
			Name:        "CLI Trigger: filechangeTrigger",
			Description: "CLI Trigger: filechangeTrigger",
			ActionType:  "trigger",
			ActionName:  "filechangeTrigger",
			Conditions:  workflow.NewConditionDictionary(),
			Arguments:   make(map[string]string),
		}
		n.Arguments["argument1"] = "/tmp"
		// n.Conditions.AddEntry("c1", NewBooleanCondition(true), []string{"n2", "n3", "n4"})
		// n.Conditions.AddEntry("condition2", NewComparison(10, ">=", 5), []string{"node4", "node5", "node6"})
		w.Nodes[n.Id] = n

		e := wm.AddWorkflow(w)
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
