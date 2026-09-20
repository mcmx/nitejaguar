package server

import (
	"context"
	"encoding/json"
	"net/http"

	"fmt"
	"log"
	"time"

	"github.com/mcmx/nitejaguar/cmd/web"
	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/ent"
	"github.com/mcmx/nitejaguar/internal/database"
	"github.com/mcmx/nitejaguar/internal/workflow"

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

	api := humaecho.NewV4(e, config)
	addApiRoutes(api, s)

	e.Use(middleware.RequestLogger())
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
	huma.Register(apiGrp, huma.Operation{
		OperationID: "get-workflows",
		Method:      http.MethodGet,
		Path:        "/workflows",
		Summary:     "All workflows",
	}, s.GetWorkflows)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "get-workflow",
		Method:      http.MethodGet,
		Path:        "/workflows/{id}",
		Summary:     "Workflow by id",
	}, s.GetWorkflow)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "post-event",
		Method:      http.MethodPost,
		Path:        "/events",
		Summary:     "Workflow events",
	}, s.WorkflowEvents)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "register-client",
		Method:      http.MethodPost,
		Path:        "/clients/register",
		Summary:     "Register a polling client",
	}, s.RegisterClient)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "client-heartbeat",
		Method:      http.MethodPost,
		Path:        "/clients/heartbeat",
		Summary:     "Client heartbeat",
	}, s.ClientHeartbeat)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "get-assignments",
		Method:      http.MethodGet,
		Path:        "/clients/{id}/assignments",
		Summary:     "Poll workflow/node assignments for a client",
	}, s.GetAssignments)
	huma.Register(apiGrp, huma.Operation{
		OperationID: "post-result",
		Method:      http.MethodPost,
		Path:        "/results",
		Summary:     "Ingest a node result and advance the workflow",
	}, s.PostResult)
}

type RegisterClientInput struct {
	Body struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
}

type RegisterClientOutput struct {
	Body struct {
		ClientID string `json:"client_id"`
	}
}

type HeartbeatInput struct {
	Body struct {
		ClientID string `json:"client_id"`
	}
}

type HeartbeatOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// WorkflowDefinition is the public workflow shape returned to polling clients.
// It intentionally has a distinct name from ent.Workflow so Huma can register
// both schemas without a name collision.
type WorkflowDefinition struct {
	ID    string                   `json:"id"`
	Name  string                   `json:"name"`
	Nodes map[string]workflow.Node `json:"nodes"`
}

type AssignmentsOutput struct {
	Body struct {
		ClientID  string               `json:"client_id"`
		Workflows []WorkflowDefinition `json:"workflows"`
	}
}

type PostResultInput struct {
	Body struct {
		ResultID    string    `json:"result_id,omitempty"`
		WorkflowID  string    `json:"workflow_id,omitempty"`
		ExecutionID string    `json:"execution_id,omitempty"`
		ActionID    string    `json:"action_id,omitempty"`
		ActionType  string    `json:"action_type,omitempty"`
		ActionName  string    `json:"action_name,omitempty"`
		CreatedAt   time.Time `json:"created_at,omitempty"`
		Payload     any       `json:"payload,omitempty"`
	}
}

type PostResultOutput struct {
	Body struct {
		WorkflowID  string   `json:"workflow_id"`
		ExecutionID string   `json:"execution_id"`
		Nexts       []string `json:"nexts"`
	}
}

func (s *Server) RegisterClient(_ context.Context, input *RegisterClientInput) (*RegisterClientOutput, error) {
	if input.Body.Name == "" {
		return nil, huma.Error400BadRequest("name is required")
	}
	c := s.registry().register(input.Body.Name, input.Body.Tags)
	return &RegisterClientOutput{
		Body: struct {
			ClientID string `json:"client_id"`
		}{ClientID: c.ID},
	}, nil
}

func (s *Server) ClientHeartbeat(_ context.Context, input *HeartbeatInput) (*HeartbeatOutput, error) {
	if input.Body.ClientID == "" {
		return nil, huma.Error400BadRequest("client_id is required")
	}
	if _, ok := s.registry().heartbeat(input.Body.ClientID); !ok {
		return nil, huma.Error404NotFound("client not found")
	}
	return &HeartbeatOutput{
		Body: struct {
			Ok bool `json:"ok"`
		}{Ok: true},
	}, nil
}

func (s *Server) GetAssignments(_ context.Context, input *struct {
	ID string `path:"id"`
}) (*AssignmentsOutput, error) {
	client, ok := s.registry().get(input.ID)
	if !ok {
		return nil, huma.Error404NotFound("client not found")
	}
	rows, err := s.db.GetWorkflows(true, true)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to get workflows")
	}
	workflows := []WorkflowDefinition{}
	for _, row := range rows {
		var def workflow.Workflow
		if err := json.Unmarshal([]byte(row.JSONDefinition), &def); err != nil {
			continue
		}
		filtered := make(map[string]workflow.Node, len(def.Nodes))
		for id, n := range def.Nodes {
			if n.AssignedTo(client.ID, client.Tags) {
				filtered[id] = n
			}
		}
		if len(filtered) == 0 {
			continue
		}
		def.Nodes = filtered
		workflows = append(workflows, WorkflowDefinition{
			ID:    def.Id,
			Name:  def.Name,
			Nodes: def.Nodes,
		})
	}
	return &AssignmentsOutput{
		Body: struct {
			ClientID  string               `json:"client_id"`
			Workflows []WorkflowDefinition `json:"workflows"`
		}{ClientID: client.ID, Workflows: workflows},
	}, nil
}

func (s *Server) PostResult(_ context.Context, input *PostResultInput) (*PostResultOutput, error) {
	if input.Body.ActionID == "" {
		return nil, huma.Error400BadRequest("action_id is required")
	}
	result := common.ResultData{
		ResultID:    input.Body.ResultID,
		WorkflowID:  input.Body.WorkflowID,
		ExecutionID: input.Body.ExecutionID,
		ActionID:    input.Body.ActionID,
		ActionType:  input.Body.ActionType,
		ActionName:  input.Body.ActionName,
		CreatedAt:   input.Body.CreatedAt,
		Payload:     input.Body.Payload,
	}
	stored, nexts, err := s.wm.IngestResult(result)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	out := &PostResultOutput{}
	out.Body.WorkflowID = stored.WorkflowID
	out.Body.ExecutionID = stored.ExecutionID
	out.Body.Nexts = nexts
	return out, nil
}

func (s *Server) TriggerWebHandler(c echo.Context) error {
	value := c.FormValue("id")
	if value == "" {
		value = c.FormValue("name")
	}
	if value == "" {
		return c.JSON(http.StatusBadRequest, "Missing id or name")
	}
	fmt.Println("Form value Stopping Trigger:", value)
	t := s.wm.GetTriggerManager()
	id := value
	if resolved, ok := t.FindTriggerIDByName(value); ok {
		id = resolved
	}
	if err := t.RemoveTrigger(id); err != nil {
		return c.JSON(http.StatusNotFound, "Trigger not found")
	}

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
	socket, err := websocket.Accept(w, r, &websocket.AcceptOptions{})

	if err != nil {
		log.Printf("could not open websocket: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("could not open websocket"))
		return err
	}

	defer func() {
		err := socket.Close(websocket.StatusGoingAway, "server closing websocket")
		if err != nil {
			log.Printf("Error closing WebSocket: %v", err)
		}
	}()

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
