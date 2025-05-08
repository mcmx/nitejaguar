package server

import (
	"context"
	"net/http"

	"fmt"
	"log"
	"time"

	"github.com/mcmx/nitejaguar/cmd/web"
	"github.com/mcmx/nitejaguar/ent"
	"github.com/mcmx/nitejaguar/internal/database"

	"github.com/a-h/templ"
	"github.com/coder/websocket"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

type HealthResponse struct {
	Body struct {
		Database *database.HealthResponse `json:"database"`
	}
}

type WorkflowsResponse struct {
	Body struct {
		Workflows []*ent.Workflow `json:"workflows"`
	}
}

func (s *Server) RegisterRoutes() http.Handler {
	e := echo.New()
	config := huma.DefaultConfig(
		"NiteJaguar API",
		"1.0.0",
	)
	//	config.DocsPath = "/docs"

	api := humaecho.New(e, config)
	addApiRoutes(api, s)

	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Static("/assets", "cmd/web/assets")

	e.GET("/", echo.WrapHandler(templ.Handler(web.HelloForm())))
	// e.POST("/hello", echo.WrapHandler(http.HandlerFunc(web.HelloWebHandler)))
	e.POST("/hello", s.TriggerWebHandler)

	e.GET("/websocket", s.websocketHandler)

	return e
}

func addApiRoutes(api huma.API, s *Server) {
	huma.Get(api, "/health", s.HealthHandler)

	apiGrp := huma.NewGroup(api, "/api")
	huma.Get(apiGrp, "/workflows", s.GetWorkflows)
	huma.Get(apiGrp, "/workflows/{id}", s.GetWorkflow)
	huma.Post(apiGrp, "/events", s.WorkflowEvents)
}

func (s *Server) TriggerWebHandler(c echo.Context) error {
	name := c.FormValue("name")
	if name == "" {
		return c.JSON(http.StatusBadRequest, "Missing name")
	}
	fmt.Println("Form value Stopping Trigger:", name)
	t := s.wm.GetTriggerManager()
	t.RemoveTrigger(name)

	return c.JSON(http.StatusOK, "Ok Hello")
}

func (s *Server) GetWorkflows(c context.Context, input *struct{}) (*WorkflowsResponse, error) {
	workflows, err := s.db.GetWorkflows(true, true)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to get workflows")
	}
	return &WorkflowsResponse{
		Body: struct {
			Workflows []*ent.Workflow `json:"workflows"`
		}{
			Workflows: workflows,
		},
	}, nil
}

func (s *Server) GetWorkflow(c context.Context, input *struct {
	ID string `path:"id"`
}) (*struct {
	Body struct {
		Workflow *ent.Workflow `json:"workflow"`
	}
}, error) {
	fmt.Println("GetWorkflow", input)
	workflow, err := s.db.GetWorkflow(input.ID)
	if err != nil {
		return nil, huma.Error404NotFound("workflow not found")
	}
	return &struct {
		Body struct {
			Workflow *ent.Workflow `json:"workflow"`
		}
	}{
		Body: struct {
			Workflow *ent.Workflow `json:"workflow"`
		}{
			Workflow: workflow,
		},
	}, nil
}

func (s *Server) WorkflowEvents(c context.Context, input *struct{}) (*WorkflowsResponse, error) {
	workflows, err := s.db.GetWorkflows(true, true)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to get workflows")
	}
	return &WorkflowsResponse{
		Body: struct {
			Workflows []*ent.Workflow `json:"workflows"`
		}{
			Workflows: workflows,
		},
	}, nil
}

func (s *Server) HealthHandler(c context.Context, input *struct{}) (*HealthResponse, error) {
	return &HealthResponse{
		Body: struct {
			Database *database.HealthResponse `json:"database"`
		}{
			Database: s.db.Health(),
		},
	}, nil
}

func (s *Server) websocketHandler(c echo.Context) error {
	w := c.Response().Writer
	r := c.Request()
	socket, err := websocket.Accept(w, r, nil)

	if err != nil {
		log.Printf("could not open websocket: %v", err)
		_, _ = w.Write([]byte("could not open websocket"))
		w.WriteHeader(http.StatusInternalServerError)
		return nil
	}

	defer socket.Close(websocket.StatusGoingAway, "server closing websocket")

	ctx := r.Context()
	socketCtx := socket.CloseRead(ctx)

	for {
		payload := fmt.Sprintf("server timestamp: %d", time.Now().UnixNano())
		err := socket.Write(socketCtx, websocket.MessageText, []byte(payload))
		if err != nil {
			break
		}
		time.Sleep(time.Second * 2)
	}
	return nil
}
