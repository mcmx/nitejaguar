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
	"github.com/mcmx/nitejaguar/internal/workflow"
	"go.jetify.com/typeid"
)

type Config struct {
	Server, ClientID, Name, Token string
	PollInterval, RetryInitial    time.Duration
}
type RegisterRequest struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}
type RegisterResponse struct {
	ClientID string `json:"client_id"`
	Token    string `json:"token"`
}
type Workflow struct {
	ID    string                   `json:"id"`
	Name  string                   `json:"name"`
	Nodes map[string]workflow.Node `json:"nodes"`
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
	var out RegisterResponse
	err := a.request(ctx, http.MethodPost, "/api/clients/register", RegisterRequest{Name: name, Tags: []string{}}, &out)
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

type runner struct {
	api          API
	log          *slog.Logger
	mu           sync.Mutex
	workflows    map[string]map[string]common.Action
	nodeWorkflow map[string]string
	nodeMeta     map[string]nodeConfig
	history      map[string]map[string]any
	events       chan common.ResultData
}

// nodeConfig carries the framework-level merge behavior for a node.
// Actions stay unaware of it; the runner applies the merge before
// reporting the result.
type nodeConfig struct {
	mergeInput   bool
	dependencies []string
}

func newRunner(api API, logger *slog.Logger) *runner {
	return &runner{api: api, log: logger, workflows: map[string]map[string]common.Action{}, nodeWorkflow: map[string]string{}, nodeMeta: map[string]nodeConfig{}, history: map[string]map[string]any{}, events: make(chan common.ResultData, 32)}
}
func (r *runner) install(w Workflow) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.workflows[w.ID]; !ok {
		r.workflows[w.ID] = map[string]common.Action{}
	}
	for id, n := range w.Nodes {
		r.nodeWorkflow[id] = w.ID
		r.nodeMeta[id] = nodeConfig{mergeInput: n.MergeInput, dependencies: n.Dependencies}
		if n.ActionType == "action" {
			if _, ok := r.workflows[w.ID][id]; ok {
				continue
			}
			a, err := newClientAction(r.events, common.ActionArgs{Id: n.Id, Name: n.Name, ActionType: n.ActionType, ActionName: n.ActionName, Args: n.Arguments})
			if err == nil {
				r.workflows[w.ID][id] = a
			}
		} else if n.ActionType == "trigger" {
			if _, ok := r.workflows[w.ID][id]; ok {
				continue
			}
			t, err := filechange.New(r.events, common.ActionArgs{Id: n.Id, Name: n.Name, ActionType: n.ActionType, ActionName: n.ActionName, Args: n.Arguments})
			if err == nil {
				r.workflows[w.ID][id] = t
				execution, _ := typeid.WithPrefix("execution")
				go t.Execute(execution.String(), nil)
			}
		}
	}
}
// newClientAction dispatches action construction by action_name so remote
// clients can run any server-side action (e.g. fileAction, datetimeAction).
func newClientAction(events chan common.ResultData, args common.ActionArgs) (common.Action, error) {
	switch args.ActionName {
	case "fileAction":
		return fileaction.New(events, args)
	case "datetimeAction":
		return datetime.New(events, args)
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
	r.mu.Lock()
	actions := r.workflows[result.WorkflowID]
	r.mu.Unlock()
	for _, id := range nexts {
		if a := actions[id]; a != nil {
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
	for _, nodes := range r.workflows {
		for _, a := range nodes {
			_ = a.Stop()
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
			reg, err := api.Register(ctx, cfg.Name)
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
		for _, w := range assignments.Workflows {
			r.install(w)
		}
		select {
		case result := <-r.events:
			r.mu.Lock()
			result.WorkflowID = r.nodeWorkflow[result.ActionID]
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
