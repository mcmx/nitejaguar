package server

import (
	"github.com/gofiber/fiber/v2"

	"nitejaguar/internal/database"
)

type FiberServer struct {
	*fiber.App

	db database.Service
}

func New() *FiberServer {
	server := &FiberServer{
		App: fiber.New(fiber.Config{
			ServerHeader: "nitejaguar",
			AppName:      "nitejaguar",
		}),

		db: database.New(),
	}

	return server
}