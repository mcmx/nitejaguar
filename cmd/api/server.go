package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Set up signal handling

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigs
		fmt.Println("Received interrupt, shutting down...")
		cancel()
	}()

	myDb := database.New()
	defer myDb.Close()
	defer fmt.Println("Finish execution")
	wm := workflow.NewWorkflowManager(args.EnableActions, myDb)
	server := server.NewServer(myDb, wm)

	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Println("Starting server...")

		go func() {
			<-ctx.Done()
			// gracefuly shutdown the http server
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer shutdownCancel()
			_ = server.Shutdown(shutdownCtx)
		}()

		err := server.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			fmt.Printf("Cannot start server: %s\n", err)
			os.Exit(1)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		wm.Run(ctx)
	}()

	if args.ImportWorkflow != "" {
		log.Println("Importing workflow:", args.ImportWorkflow)
		wImportJSON, e := os.ReadFile(args.ImportWorkflow)
		if e != nil {
			log.Println("error importing workflow 1", e)
		} else {
			e = wm.ImportWorkflowJSON(string(wImportJSON))
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
		var workflowDef workflow.Workflow
		e = json.Unmarshal([]byte(wf), &workflowDef)
		if e != nil {
			log.Println("Error unmarshaling workflow: ", e)
		}

		e = wm.AddWorkflow(workflowDef)
		if e != nil {
			log.Println(e)
		}

	}
	wg.Wait()

}
