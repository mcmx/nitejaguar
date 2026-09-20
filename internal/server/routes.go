package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"

	"fmt"
	"log"
	"time"

	"github.com/mcmx/nitejaguar/cmd/web"
	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/ent"
	"github.com/mcmx/nitejaguar/internal/database"
	"github.com/mcmx/nitejaguar/internal/workflow"
	"go.jetify.com/typeid"

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

type ClientStatus struct {
	ID            string    `json:"client_id"`
	Name          string    `json:"name"`
	Tags          []string  `json:"tags"`
	RegisteredAt  time.Time `json:"registered_at"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	LastPoll      time.Time `json:"last_poll"`
	Online        bool      `json:"online"`
}

type ClientsResponse struct {
	Body struct {
		Clients []ClientStatus `json:"clients"`
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

	e.GET("/", s.workflowsPage)
	e.GET("/workflows/:id", s.workflowPage)
	e.GET("/designer", s.designerPage)
	e.POST("/designer/save", s.designerSaveWorkflow)
	e.GET("/results", s.resultsPage)
	e.GET("/clients", s.clientsPage)
	e.POST("/workflows/:id/enabled", s.setWorkflowEnabled)
	e.POST("/triggers/stop", s.TriggerWebHandler)

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
	huma.Register(apiGrp, huma.Operation{
		OperationID: "get-clients",
		Method:      http.MethodGet,
		Path:        "/clients",
		Summary:     "Registered polling clients and connection status",
	}, s.GetClients)
}

type RegisterClientInput struct {
	Body struct {
		Name     string   `json:"name"`
		TenantID string   `json:"tenant_id,omitempty"`
		Tags     []string `json:"tags"`
	}
}

type RegisterClientOutput struct {
	Body struct {
		ClientID string `json:"client_id"`
		Token    string `json:"token"`
	}
}

type HeartbeatInput struct {
	Authorization string `header:"Authorization"`
	Token         string `header:"X-Client-Token"`
	Body          struct {
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

type AssignmentsInput struct {
	ID            string `path:"id"`
	Authorization string `header:"Authorization"`
	Token         string `header:"X-Client-Token"`
}

type AssignmentsOutput struct {
	Body struct {
		ClientID  string               `json:"client_id"`
		Workflows []WorkflowDefinition `json:"workflows"`
	}
}

type PostResultInput struct {
	Authorization string `header:"Authorization"`
	Token         string `header:"X-Client-Token"`
	Body          struct {
		ResultID    string    `json:"result_id,omitempty"`
		WorkflowID  string    `json:"workflow_id,omitempty"`
		ExecutionID string    `json:"execution_id,omitempty"`
		ActionID    string    `json:"action_id,omitempty"`
		ActionType  string    `json:"action_type,omitempty"`
		ActionName  string    `json:"action_name,omitempty"`
		ExecutorID  string    `json:"executor_id,omitempty"`
		TenantID    string    `json:"tenant_id,omitempty"`
		CreatedAt   time.Time `json:"created_at,omitempty"`
		Payload     any       `json:"payload,omitempty"`
	}
}

type postResultRecord struct {
	workflowID, executionID string
	nexts                   []string
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
	c, token := s.registry().register(input.Body.Name, input.Body.Tags, input.Body.TenantID)
	log.Printf("client registered: id=%s name=%q tenant=%s tags=%v", c.ID, c.Name, c.TenantID, c.Tags)
	return &RegisterClientOutput{
		Body: struct {
			ClientID string `json:"client_id"`
			Token    string `json:"token"`
		}{ClientID: c.ID, Token: token},
	}, nil
}

func (s *Server) ClientHeartbeat(_ context.Context, input *HeartbeatInput) (*HeartbeatOutput, error) {
	if !s.registry().authenticate(input.Body.ClientID, bearerToken(input.Authorization, input.Token)) {
		return nil, huma.Error401Unauthorized("invalid or missing client token")
	}
	if input.Body.ClientID == "" {
		return nil, huma.Error400BadRequest("client_id is required")
	}
	c, ok := s.registry().heartbeat(input.Body.ClientID)
	if !ok {
		return nil, huma.Error404NotFound("client not found")
	}
	log.Printf("client heartbeat: id=%s name=%q", c.ID, c.Name)
	return &HeartbeatOutput{
		Body: struct {
			Ok bool `json:"ok"`
		}{Ok: true},
	}, nil
}

func (s *Server) GetAssignments(_ context.Context, input *AssignmentsInput) (*AssignmentsOutput, error) {
	if !s.registry().authenticate(input.ID, bearerToken(input.Authorization, input.Token)) {
		return nil, huma.Error401Unauthorized("invalid or missing client token")
	}
	client, ok := s.registry().poll(input.ID)
	if !ok {
		return nil, huma.Error404NotFound("client not found")
	}
	log.Printf("client assignment poll: id=%s name=%q", client.ID, client.Name)
	// Remote clients should only receive workflows that are currently enabled.
	rows, err := s.db.GetWorkflows(false, true)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to get workflows")
	}
	workflows := []WorkflowDefinition{}
	for _, row := range rows {
		// Tenant isolation check
		if row.TenantID != "" && row.TenantID != "default" && client.TenantID != "" && client.TenantID != "default" && row.TenantID != client.TenantID {
			continue
		}
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

func (s *Server) GetClients(_ context.Context, _ *struct{}) (*ClientsResponse, error) {
	now := time.Now()
	clients := s.registry().list()
	out := make([]ClientStatus, 0, len(clients))
	for _, c := range clients {
		out = append(out, ClientStatus{
			ID: c.ID, Name: c.Name, Tags: c.Tags, RegisteredAt: c.RegisteredAt,
			LastHeartbeat: c.LastHeartbeat, LastPoll: c.LastPoll,
			Online: now.Sub(c.LastHeartbeat) <= 15*time.Second,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return &ClientsResponse{Body: struct {
		Clients []ClientStatus `json:"clients"`
	}{Clients: out}}, nil
}

func (s *Server) PostResult(_ context.Context, input *PostResultInput) (*PostResultOutput, error) {
	executorID, ok := s.registry().identity(bearerToken(input.Authorization, input.Token))
	if !ok {
		return nil, huma.Error401Unauthorized("invalid or missing client token")
	}
	if input.Body.ActionID == "" {
		return nil, huma.Error400BadRequest("action_id is required")
	}
	log.Printf("client result received: action=%s name=%q", input.Body.ActionID, input.Body.ActionName)
	result := common.ResultData{
		ResultID:    input.Body.ResultID,
		WorkflowID:  input.Body.WorkflowID,
		ExecutionID: input.Body.ExecutionID,
		ActionID:    input.Body.ActionID,
		ActionType:  input.Body.ActionType,
		ActionName:  input.Body.ActionName,
		ExecutorID:  executorID,
		TenantID:    input.Body.TenantID,
		CreatedAt:   input.Body.CreatedAt,
		Payload:     input.Body.Payload,
	}
	if result.ResultID == "" {
		id, _ := typeid.WithPrefix("result")
		result.ResultID = id.String()
	}
	s.resultMu.Lock()
	defer s.resultMu.Unlock()
	if s.results == nil {
		s.results = make(map[string]postResultRecord)
	}
	if prior, ok := s.results[result.ResultID]; ok {
		out := &PostResultOutput{}
		out.Body.WorkflowID, out.Body.ExecutionID, out.Body.Nexts = prior.workflowID, prior.executionID, append([]string(nil), prior.nexts...)
		return out, nil
	}
	stored, nexts, err := s.wm.IngestResult(result)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	s.results[result.ResultID] = postResultRecord{stored.WorkflowID, stored.ExecutionID, append([]string(nil), nexts...)}
	out := &PostResultOutput{}
	out.Body.WorkflowID = stored.WorkflowID
	out.Body.ExecutionID = stored.ExecutionID
	out.Body.Nexts = nexts
	return out, nil
}

func (s *Server) workflowsPage(c echo.Context) error {
	workflows, err := s.db.GetWorkflows(true, true)
	data := &web.WorkflowPageData{Workflows: workflows}
	if err != nil {
		data.Error = "Unable to load workflows"
	}
	templ.Handler(web.WorkflowsPage(data, "")).ServeHTTP(c.Response(), c.Request())
	return nil
}

func (s *Server) workflowPage(c echo.Context) error {
	row, err := s.db.GetWorkflow(c.Param("id"))
	data := &web.WorkflowDetailData{Workflow: row}
	if err != nil {
		data.Error = "Workflow not found"
	} else if err := json.Unmarshal([]byte(row.JSONDefinition), &data.Definition); err != nil {
		data.Error = "Workflow definition is invalid"
	}
	templ.Handler(web.WorkflowDetailPage(data)).ServeHTTP(c.Response(), c.Request())
	return nil
}

func (s *Server) resultsPage(c echo.Context) error {
	data := &web.ResultsPageData{}
	entries, err := os.ReadDir("./results")
	if err != nil && !os.IsNotExist(err) {
		data.Error = "Unable to read results"
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		name := entry.Name()
		if filepath.Base(name) != name {
			continue
		}
		contents, readErr := os.ReadFile(filepath.Join("./results", name))
		if readErr != nil {
			continue
		}
		var result common.ResultData
		if json.Unmarshal(contents, &result) == nil {
			data.Results = append(data.Results, result)
		}
	}
	sort.Slice(data.Results, func(i, j int) bool { return data.Results[i].CreatedAt.After(data.Results[j].CreatedAt) })
	templ.Handler(web.ResultsPage(data)).ServeHTTP(c.Response(), c.Request())
	return nil
}

func (s *Server) clientsPage(c echo.Context) error {
	data := &web.ClientsPageData{}
	for _, client := range s.registry().list() {
		data.Clients = append(data.Clients, web.ClientView{
			ID: client.ID, Name: client.Name, Tags: client.Tags,
			RegisteredAt:  client.RegisteredAt.Format("2006-01-02 15:04:05 MST"),
			LastHeartbeat: client.LastHeartbeat.Format("2006-01-02 15:04:05 MST"),
			LastPoll:      client.LastPoll.Format("2006-01-02 15:04:05 MST"),
			Online:        time.Since(client.LastHeartbeat) <= 15*time.Second,
		})
	}
	sort.Slice(data.Clients, func(i, j int) bool { return data.Clients[i].Name < data.Clients[j].Name })
	templ.Handler(web.ClientsPage(data)).ServeHTTP(c.Response(), c.Request())
	return nil
}

func (s *Server) setWorkflowEnabled(c echo.Context) error {
	enabled := c.FormValue("enabled") == "true"
	if err := s.db.SetWorkflowEnabled(c.Param("id"), enabled); err != nil {
		return c.String(http.StatusNotFound, "workflow not found")
	}
	return c.Redirect(http.StatusSeeOther, "/workflows/"+c.Param("id"))
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

	return c.JSON(http.StatusOK, map[string]string{"status": "trigger stopped", "id": id})
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

func (s *Server) designerPage(c echo.Context) error {
	templ.Handler(web.DesignerPage()).ServeHTTP(c.Response(), c.Request())
	return nil
}

func (s *Server) designerSaveWorkflow(c echo.Context) error {
	jsonDef := c.FormValue("workflow_json")
	if jsonDef == "" {
		return c.String(http.StatusBadRequest, "workflow_json is required")
	}
	if err := s.wm.ImportWorkflowJSON(jsonDef); err != nil {
		return c.String(http.StatusBadRequest, "Failed to import workflow: "+err.Error())
	}
	var wf workflow.Workflow
	if err := json.Unmarshal([]byte(jsonDef), &wf); err == nil && wf.Id != "" {
		return c.Redirect(http.StatusSeeOther, "/workflows/"+wf.Id)
	}
	return c.Redirect(http.StatusSeeOther, "/")
}
