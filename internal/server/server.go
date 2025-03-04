package server

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "github.com/joho/godotenv/autoload"

	"github.com/mcmx/nitejaguar/internal/actions"
	"github.com/mcmx/nitejaguar/internal/database"
)

type Server struct {
	port int

	db database.Service
	ts actions.TriggerService
	am actions.ActionManager
}

func NewServer(myDb database.Service, myTs actions.TriggerService, myAm actions.ActionManager) *http.Server {
	port, _ := strconv.Atoi(os.Getenv("PORT"))
	intServer := &Server{
		port: port,
		db:   myDb,
		ts:   myTs,
		am:   myAm,
	}
	if port == 0 {
		intServer.port = 8080
		fmt.Println("No port specified. Using default port 8080")
	} else {
		fmt.Printf("Starting server on port %d\n", intServer.port)
	}
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
