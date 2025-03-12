package server

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "github.com/joho/godotenv/autoload"

	"github.com/mcmx/nitejaguar/internal/database"
	"github.com/mcmx/nitejaguar/internal/workflow"
)

type Server struct {
	port int

	db database.Service
	wm workflow.WorkflowManager
}

func NewServer(myDb database.Service, myWm workflow.WorkflowManager) *http.Server {
	port, _ := strconv.Atoi(os.Getenv("PORT"))
	intServer := &Server{
		port: port,
		db:   myDb,
		wm:   myWm,
	}
	if port == 0 {
		intServer.port = 8080
		fmt.Println("No port specified. Using default port 8080")
	}
	fmt.Printf("Starting server on http://0.0.0.0:%d and http://[::0]:%d\n", intServer.port, intServer.port)

	// Declare Server config
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", intServer.port),
		Handler:      intServer.RegisterRoutes(),
		IdleTimeout:  time.Minute,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	return server
}
