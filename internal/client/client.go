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
	"strings"
	"sync"
	"time"

	"github.com/mcmx/nitejaguar/common"
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
	Name string `json:"name"`
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
	err := a.request(ctx, http.MethodPost, "/api/clients/register", RegisterRequest{Name: name}, &out)
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
	events       chan common.ResultData
}

func newRunner(api API, logger *slog.Logger) *runner {
	return &runner{api: api, log: logger, workflows: map[string]map[string]common.Action{}, nodeWorkflow: map[string]string{}, events: make(chan common.ResultData, 32)}
}
func (r *runner) install(w Workflow) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.workflows[w.ID]; !ok {
		r.workflows[w.ID] = map[string]common.Action{}
	}
	for id, n := range w.Nodes {
		r.nodeWorkflow[id] = w.ID
		if n.ActionType == "action" {
			if _, ok := r.workflows[w.ID][id]; ok {
				continue
			}
			a, err := fileaction.New(r.events, common.ActionArgs{Id: n.Id, Name: n.Name, ActionType: n.ActionType, ActionName: n.ActionName, Args: n.Arguments})
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
func (r *runner) runResult(ctx context.Context, result common.ResultData) {
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
	api := API{BaseURL: cfg.Server, Token: cfg.Token}
	r := newRunner(api, logger)
	defer r.close()
	id := cfg.ClientID
	backoff := cfg.RetryInitial
	for {
		if id == "" {
			reg, err := api.Register(ctx, cfg.Name)
			if err != nil {
				if !wait(ctx, backoff) {
					return ctx.Err()
				}
				backoff = min(backoff*2, 30*time.Second)
				continue
			}
			id = reg.ClientID
			api.Token = reg.Token
			backoff = cfg.RetryInitial
			logger.Info("registered client", "client_id", id)
		}
		if err := api.Heartbeat(ctx, id); err != nil {
			if cfg.ClientID == "" {
				id = ""
			}
			if !wait(ctx, backoff) {
				return ctx.Err()
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		assignments, err := api.Assignments(ctx, id)
		if err != nil {
			if cfg.ClientID == "" {
				id = ""
			}
			if !wait(ctx, backoff) {
				return ctx.Err()
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		backoff = cfg.RetryInitial
		for _, w := range assignments.Workflows {
			r.install(w)
		}
		select {
		case result := <-r.events:
			r.mu.Lock()
			result.WorkflowID = r.nodeWorkflow[result.ActionID]
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
