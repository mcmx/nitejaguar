// Package client implements the polling side of the Nitejaguar client API.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/actions/datetime"
	"github.com/mcmx/nitejaguar/internal/actions/fileaction"
	"github.com/mcmx/nitejaguar/internal/actions/filechange"
	waitaction "github.com/mcmx/nitejaguar/internal/actions/wait"
	"github.com/mcmx/nitejaguar/internal/workflow"
	"go.jetify.com/typeid"
)

type Config struct {
	Server, ClientID, Name, Token string
	EnrollmentToken               string
	PollInterval, RetryInitial    time.Duration
}
type RegisterRequest struct {
	Name            string   `json:"name"`
	Tags            []string `json:"tags"`
	EnrollmentToken string   `json:"enrollment_token,omitempty"`
}
type RegisterResponse struct {
	ClientID string `json:"client_id"`
	Token    string `json:"token"`
}
type Workflow struct {
	ID       string                   `json:"id"`
	Name     string                   `json:"name"`
	Revision string                   `json:"revision"`
	Nodes    map[string]workflow.Node `json:"nodes"`
}
type AssignmentsResponse struct {
	ClientID  string     `json:"client_id"`
	Workflows []Workflow `json:"workflows"`
}
type API struct {
	BaseURL, Token string
	HTTPClient     *http.Client
}

type clientState struct {
	ClientID string `json:"client_id"`
	Token    string `json:"token"`
}

const stateFileName = "client_state.json"

func loadClientState() clientState {
	b, err := os.ReadFile(stateFileName)
	if err != nil {
		return clientState{}
	}
	var state clientState
	_ = json.Unmarshal(b, &state)
	return state
}

func saveClientState(state clientState) {
	b, _ := json.MarshalIndent(state, "", "  ")
	_ = os.WriteFile(stateFileName, b, 0600)
}

func (c Config) normalized() Config {
	if c.PollInterval <= 0 {
		c.PollInterval = 2 * time.Second
	}
	if c.RetryInitial <= 0 {
		c.RetryInitial = 500 * time.Millisecond
	}
	c.Server = strings.TrimRight(c.Server, "/")
	return c
}
func (a API) client() *http.Client {
	if a.HTTPClient != nil {
		return a.HTTPClient
	}
	return http.DefaultClient
}
func (a API) request(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(a.BaseURL, "/")+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if a.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.Token)
		req.Header.Set("X-Client-Token", a.Token)
	}
	resp, err := a.client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("server returned %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
func (a API) Register(ctx context.Context, name string) (RegisterResponse, error) {
	return a.RegisterWithToken(ctx, name, "")
}

func (a API) RegisterWithToken(ctx context.Context, name, enrollmentToken string) (RegisterResponse, error) {
	var out RegisterResponse
	err := a.request(ctx, http.MethodPost, "/api/clients/register", RegisterRequest{Name: name, Tags: []string{}, EnrollmentToken: enrollmentToken}, &out)
	return out, err
}
func (a API) Heartbeat(ctx context.Context, id string) error {
	return a.request(ctx, http.MethodPost, "/api/clients/heartbeat", map[string]string{"client_id": id}, nil)
}
func (a API) Assignments(ctx context.Context, id string) (AssignmentsResponse, error) {
	var out AssignmentsResponse
	err := a.request(ctx, http.MethodGet, "/api/clients/"+id+"/assignments", nil, &out)
	return out, err
}
func (a API) PostResult(ctx context.Context, r common.ResultData) ([]string, error) {
	var out struct {
		Nexts []string `json:"nexts"`
	}
	err := a.request(ctx, http.MethodPost, "/api/results", r, &out)
	return out.Nexts, err
}

// UpsertWorkflowResponse is the server reply for the workflow
// import/clone endpoints.
type UpsertWorkflowResponse struct {
	Ok         bool   `json:"ok"`
	WorkflowID string `json:"workflow_id"`
}

// ImportWorkflow sends a workflow definition to the server, which saves it
// verbatim (upsert; ids and name untouched).
func (a API) ImportWorkflow(ctx context.Context, wf workflow.Workflow) (UpsertWorkflowResponse, error) {
	var out UpsertWorkflowResponse
	err := a.request(ctx, http.MethodPost, "/api/workflows/import", wf, &out)
	return out, err
}

// CloneWorkflow sends a workflow definition to the server, which saves an
// independent copy with fresh ids, rewritten edges, and a "Clone of: "
// name prefix.
func (a API) CloneWorkflow(ctx context.Context, wf workflow.Workflow) (UpsertWorkflowResponse, error) {
	var out UpsertWorkflowResponse
	err := a.request(ctx, http.MethodPost, "/api/workflows/clone", wf, &out)
	return out, err
}

type runner struct {
	api             API
	log             *slog.Logger
	mu              sync.Mutex
	events          chan common.ResultData
	activeWorkflows map[string]*managedWorkflow
	retired         map[string]map[string]*managedWorkflow
	nodeWorkflow    map[string]string
	nodeRevision    map[string]string
	nodeMeta        map[string]nodeConfig
	nodeAction      map[string]common.Action
	history         map[string]map[string]any
}

// managedWorkflow is one installed revision of a workflow.
type managedWorkflow struct {
	workflowID string
	revision   string
	actions    map[string]common.Action
	triggers   map[string]common.Action
	meta       map[string]nodeConfig
}

// nodeConfig carries the framework-level merge behavior for a node.
// Actions stay unaware of it; the runner applies the merge before
// reporting the result.
type nodeConfig struct {
	mergeInput   bool
	dependencies []string
}

func newRunner(api API, logger *slog.Logger) *runner {
	return &runner{
		api:             api,
		log:             logger,
		events:          make(chan common.ResultData, 32),
		activeWorkflows: make(map[string]*managedWorkflow),
		retired:         make(map[string]map[string]*managedWorkflow),
		nodeWorkflow:    make(map[string]string),
		nodeRevision:    make(map[string]string),
		nodeMeta:        make(map[string]nodeConfig),
		nodeAction:      make(map[string]common.Action),
		history:         make(map[string]map[string]any),
	}
}

func (r *runner) install(w Workflow) {
	r.syncAssignments([]Workflow{w})
}

func (r *runner) syncAssignments(workflows []Workflow) {
	r.mu.Lock()
	defer r.mu.Unlock()

	fetchedMap := make(map[string]Workflow, len(workflows))
	for _, w := range workflows {
		fetchedMap[w.ID] = w
	}

	// 1. Check active workflows: disable/remove or update
	for wID, active := range r.activeWorkflows {
		w, fetched := fetchedMap[wID]
		if !fetched {
			r.log.Info("workflow disabled or removed; stopping triggers", "workflow_id", wID)
			for _, trig := range active.triggers {
				_ = trig.Stop()
			}
			if r.retired[wID] == nil {
				r.retired[wID] = make(map[string]*managedWorkflow)
			}
			r.retired[wID][active.revision] = active
			delete(r.activeWorkflows, wID)
			continue
		}

		if w.Revision != active.revision {
			r.log.Info("workflow updated; rotating triggers", "workflow_id", wID, "old_revision", active.revision, "new_revision", w.Revision)
			for _, trig := range active.triggers {
				_ = trig.Stop()
			}
			if r.retired[wID] == nil {
				r.retired[wID] = make(map[string]*managedWorkflow)
			}
			r.retired[wID][active.revision] = active

			mw := r.buildManagedWorkflow(w)
			r.activeWorkflows[wID] = mw
			r.registerWorkflowIndices(mw)
		}
	}

	// 2. Install new workflows
	for wID, w := range fetchedMap {
		if _, ok := r.activeWorkflows[wID]; !ok {
			if r.retired[wID] != nil {
				delete(r.retired[wID], w.Revision)
				if len(r.retired[wID]) == 0 {
					delete(r.retired, wID)
				}
			}
			r.log.Info("installing new workflow", "workflow_id", wID, "revision", w.Revision)
			mw := r.buildManagedWorkflow(w)
			r.activeWorkflows[wID] = mw
			r.registerWorkflowIndices(mw)
		}
	}
}

func (r *runner) buildManagedWorkflow(w Workflow) *managedWorkflow {
	mw := &managedWorkflow{
		workflowID: w.ID,
		revision:   w.Revision,
		actions:    make(map[string]common.Action),
		triggers:   make(map[string]common.Action),
		meta:       make(map[string]nodeConfig),
	}
	for id, n := range w.Nodes {
		mw.meta[id] = nodeConfig{mergeInput: n.MergeInput, dependencies: n.Dependencies}
		switch n.ActionType {
		case "action":
			a, err := newClientAction(r.events, common.ActionArgs{Id: n.Id, Name: n.Name, ActionType: n.ActionType, ActionName: n.ActionName, Args: n.Arguments})
			if err == nil {
				mw.actions[id] = a
			} else {
				r.log.Error("failed to create client action", "action_id", id, "error", err)
			}
		case "trigger":
			t, err := filechange.New(r.events, common.ActionArgs{Id: n.Id, Name: n.Name, ActionType: n.ActionType, ActionName: n.ActionName, Args: n.Arguments})
			if err == nil {
				mw.actions[id] = t
				mw.triggers[id] = t
				execution, _ := typeid.WithPrefix("execution")
				go t.Execute(execution.String(), nil)
			} else {
				r.log.Error("failed to create client trigger", "trigger_id", id, "error", err)
			}
		}
	}
	return mw
}

func (r *runner) registerWorkflowIndices(mw *managedWorkflow) {
	for id, a := range mw.actions {
		r.nodeWorkflow[id] = mw.workflowID
		r.nodeRevision[id] = mw.revision
		if cfg, ok := mw.meta[id]; ok {
			r.nodeMeta[id] = cfg
		}
		r.nodeAction[id] = a
	}
}

// newClientAction dispatches action construction by action_name so remote
// clients can run any server-side action (e.g. file, datetime, wait).
func newClientAction(events chan common.ResultData, args common.ActionArgs) (common.Action, error) {
	switch args.ActionName {
	case "file":
		return fileaction.New(events, args)
	case "datetime":
		return datetime.New(events, args)
	case "wait":
		return waitaction.New(events, args)
	default:
		return nil, fmt.Errorf("unknown action_name: %q", args.ActionName)
	}
}
func (r *runner) runResult(ctx context.Context, result common.ResultData) {
	r.mu.Lock()
	result.Payload = r.mergedPayloadLocked(result)
	r.rememberLocked(result)
	r.mu.Unlock()
	nexts, err := r.api.PostResult(ctx, result)
	if err != nil {
		r.log.Error("post result", "error", err)
		return
	}
	for _, id := range nexts {
		r.mu.Lock()
		a := r.nodeAction[id]
		r.mu.Unlock()
		if a != nil {
			go a.Execute(result.ExecutionID, []any{result})
		}
	}
}

// mergedPayloadLocked folds recorded upstream dependency payloads into
// the result when the node opts into merge_input. Deep merge, reported
// $result wins. Callers must hold r.mu.
func (r *runner) mergedPayloadLocked(result common.ResultData) any {
	cfg := r.nodeMeta[result.ActionID]
	if !cfg.mergeInput || result.ExecutionID == "" || len(cfg.dependencies) == 0 {
		return result.Payload
	}
	byNode := r.history[result.ExecutionID]
	merged := result.Payload
	for _, dep := range cfg.dependencies {
		if byNode != nil {
			if p, ok := byNode[dep]; ok {
				merged = common.MergePayloads(p, merged)
			}
		}
	}
	return merged
}

// rememberLocked records a result payload for later merge_input lookups.
// Callers must hold r.mu.
func (r *runner) rememberLocked(result common.ResultData) {
	if result.ExecutionID == "" || result.ActionID == "" {
		return
	}
	if r.history == nil {
		r.history = make(map[string]map[string]any)
	}
	byNode, ok := r.history[result.ExecutionID]
	if !ok {
		byNode = make(map[string]any)
		r.history[result.ExecutionID] = byNode
	}
	byNode[result.ActionID] = result.Payload
}
func (r *runner) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, mw := range r.activeWorkflows {
		for _, a := range mw.actions {
			_ = a.Stop()
		}
	}
	for _, revs := range r.retired {
		for _, mw := range revs {
			for _, a := range mw.actions {
				_ = a.Stop()
			}
		}
	}
}

func Run(ctx context.Context, cfg Config, logger *slog.Logger) error {
	cfg = cfg.normalized()
	if cfg.Server == "" {
		return errors.New("server is required")
	}
	if logger == nil {
		logger = slog.Default()
	}

	state := loadClientState()
	if cfg.ClientID == "" && cfg.Token == "" {
		if state.ClientID != "" && state.Token != "" {
			cfg.ClientID = state.ClientID
			cfg.Token = state.Token
		}
	} else if cfg.ClientID != "" && cfg.Token == "" {
		if state.ClientID == cfg.ClientID && state.Token != "" {
			cfg.Token = state.Token
		}
	}

	api := API{BaseURL: cfg.Server, Token: cfg.Token}
	r := newRunner(api, logger)
	defer r.close()
	id := cfg.ClientID
	backoff := cfg.RetryInitial
	for {
		if id == "" {
			reg, err := api.RegisterWithToken(ctx, cfg.Name, cfg.EnrollmentToken)
			if err != nil {
				logger.Warn("client registration failed; retrying", "server", cfg.Server, "error", err, "after", backoff)
				if !wait(ctx, backoff) {
					return ctx.Err()
				}
				backoff = min(backoff*2, 30*time.Second)
				continue
			}
			id = reg.ClientID
			api.Token = reg.Token
			r.api.Token = reg.Token
			saveClientState(clientState{ClientID: id, Token: reg.Token})
			backoff = cfg.RetryInitial
			logger.Info("client registered", "server", cfg.Server, "client_id", id, "name", cfg.Name)
		}
		if err := api.Heartbeat(ctx, id); err != nil {
			logger.Warn("client heartbeat failed; retrying", "client_id", id, "error", err, "after", backoff)
			id = ""
			api.Token = ""
			r.api.Token = ""
			saveClientState(clientState{})
			if !wait(ctx, backoff) {
				return ctx.Err()
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		assignments, err := api.Assignments(ctx, id)
		if err != nil {
			logger.Warn("client assignment poll failed; retrying", "client_id", id, "error", err, "after", backoff)
			id = ""
			api.Token = ""
			r.api.Token = ""
			saveClientState(clientState{})
			if !wait(ctx, backoff) {
				return ctx.Err()
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		backoff = cfg.RetryInitial
		logger.Debug("client assignment poll succeeded", "client_id", id, "workflows", len(assignments.Workflows))
		r.syncAssignments(assignments.Workflows)
		select {
		case result := <-r.events:
			r.mu.Lock()
			if result.WorkflowID == "" {
				result.WorkflowID = r.nodeWorkflow[result.ActionID]
			}
			result.ExecutorID = id
			r.mu.Unlock()
			r.runResult(ctx, result)
		case <-time.After(cfg.PollInterval):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
func wait(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
