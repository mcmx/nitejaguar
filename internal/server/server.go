package server

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "github.com/joho/godotenv/autoload"

	"nitejaguar/internal/actions"
	"nitejaguar/internal/database"
)

type Server struct {
	port int

	db database.Service
	ts actions.TriggerService
}

func NewServer(myDb database.Service, myTs actions.TriggerService) *http.Server {
	port, _ := strconv.Atoi(os.Getenv("PORT"))
	NewServer := &Server{
		port: port,
		db:   myDb,
		ts:   myTs,
	}
	if port == 0 {
		NewServer.port = 8080
		fmt.Println("No port specified. Using default port 8080")
	} else {
		fmt.Printf("Starting server on port %d\n", NewServer.port)
	}
	// Declare Server config
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", NewServer.port),
		Handler:      NewServer.RegisterRoutes(),
		IdleTimeout:  time.Minute,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	return server
}
