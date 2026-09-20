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

	db      database.Service
	wm      workflow.WorkflowManager
	clients *clientRegistry
}

// registry returns the in-memory client registry, lazily creating it so
// handlers stay safe when Server is built struct-literally (e.g. in tests).
func (s *Server) registry() *clientRegistry {
	if s.clients == nil {
		s.clients = newClientRegistry()
	}
	return s.clients
}

func NewServer(myDb database.Service, myWm workflow.WorkflowManager) *http.Server {
	port, _ := strconv.Atoi(os.Getenv("PORT"))
	intServer := &Server{
		port:    port,
		db:      myDb,
		wm:      myWm,
		clients: newClientRegistry(),
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
