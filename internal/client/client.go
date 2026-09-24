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
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/actions/datetime"
	"github.com/mcmx/nitejaguar/internal/actions/fileaction"
	"github.com/mcmx/nitejaguar/internal/actions/filechange"
	transferaction "github.com/mcmx/nitejaguar/internal/actions/transfer"
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
	ID                string                   `json:"id"`
	Name              string                   `json:"name"`
	Revision          string                   `json:"revision"`
	DefaultClient     string                   `json:"default_client,omitempty"`
	DefaultClientTags []string                 `json:"default_client_tags,omitempty"`
	Nodes             map[string]workflow.Node `json:"nodes"`
}
type PendingAssignment struct {
	ID             string `json:"id"`
	WorkflowID     string `json:"workflow_id"`
	ExecutionID    string `json:"execution_id"`
	NodeID         string `json:"node_id"`
	ParentActionID string `json:"parent_action_id,omitempty"`
	Payload        any    `json:"payload,omitempty"`
}
type AssignmentsResponse struct {
	ClientID  string              `json:"client_id"`
	Workflows []Workflow          `json:"workflows"`
	Pending   []PendingAssignment `json:"pending,omitempty"`
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

// CredentialFetch is the just-in-time secret delivery for a node
// credential_ref. The secret must be held in memory only for TTLSeconds
// and never persisted to disk, logs, or results.
type CredentialFetch struct {
	CredentialID string `json:"credential_id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	Secret       string `json:"secret"`
	TTLSeconds   int    `json:"ttl_seconds"`
}

// FetchCredential fetches the secret for a node credential reference with
// the client's own token. The server never ships secrets inside workflow
// assignments; every fetch is audited server-side.
func (a API) FetchCredential(ctx context.Context, ref, userID string, groups []string, workflowID, nodeID string) (CredentialFetch, error) {
	var out CredentialFetch
	path := "/api/credentials/" + url.PathEscape(ref) + "/fetch"
	query := []string{}
	if userID != "" {
		query = append(query, "user_id="+userID)
	}
	if len(groups) > 0 {
		query = append(query, "groups="+strings.Join(groups, ","))
	}
	if workflowID != "" {
		query = append(query, "workflow_id="+workflowID)
	}
	if nodeID != "" {
		query = append(query, "node_id="+nodeID)
	}
	if len(query) > 0 {
		path += "?" + strings.Join(query, "&")
	}
	err := a.request(ctx, http.MethodGet, path, nil, &out)
	return out, err
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
	pendingSeen     map[string]bool
	credCache       map[string]credentialCacheEntry
	selfID          string
	transfersSeen   map[string]bool
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
// reporting the result. Transfer routing fields let the runner intercept
// transfer nodes addressed to another client and run the distributed
// (P2P-first, relay-fallback) sender flow instead of a local copy.
type nodeConfig struct {
	mergeInput    bool
	dependencies  []string
	credentialRef string
	actionName    string
	transferDest  transferTarget
}

// transferTarget names the receiver of a transfer node. Empty destClient
// and destTags mean local execution (plain copy on this client).
type transferTarget struct {
	destClient string
	destTags   []string
}

// credentialCacheEntry is a short-lived in-memory secret. Secrets are never
// persisted; entries expire after the server-advertised TTL.
type credentialCacheEntry struct {
	secret    string
	expiresAt time.Time
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
		pendingSeen:     make(map[string]bool),
		credCache:       make(map[string]credentialCacheEntry),
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
		mw.meta[id] = nodeConfig{
			mergeInput: n.MergeInput, dependencies: n.Dependencies,
			credentialRef: n.CredentialRef, actionName: n.ActionName,
			transferDest: transferTargetFromArgs(n.ActionName, n.Arguments),
		}
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
// clients can run any server-side action (e.g. file, datetime, wait, transfer).
func newClientAction(events chan common.ResultData, args common.ActionArgs) (common.Action, error) {
	switch args.ActionName {
	case "file":
		return fileaction.New(events, args)
	case "datetime":
		return datetime.New(events, args)
	case "wait":
		return waitaction.New(events, args)
	case "transfer":
		return transferaction.New(events, args)
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
		r.executeNode(id, result.ExecutionID, []any{result})
	}
}

// drainPending executes server-persisted cross-client handoffs assigned to
// this runner. Each pending item runs at most once per process (pendingSeen);
// completion is confirmed server-side when the node's result is posted.
// Nodes carrying a credential_ref resolve their secret just-in-time before
// execution; a failed fetch fails closed and the item is retried on the
// next poll.
func (r *runner) drainPending(ctx context.Context, pending []PendingAssignment) {
	for _, p := range pending {
		if p.NodeID == "" || p.ExecutionID == "" {
			continue
		}
		r.mu.Lock()
		if r.pendingSeen == nil {
			r.pendingSeen = make(map[string]bool)
		}
		key := p.ID
		if key == "" {
			key = p.WorkflowID + "/" + p.ExecutionID + "/" + p.NodeID
		}
		if r.pendingSeen[key] {
			r.mu.Unlock()
			continue
		}
		r.pendingSeen[key] = true
		a := r.nodeAction[p.NodeID]
		credRef := r.nodeMeta[p.NodeID].credentialRef
		// Seed history with the parent payload so merge_input can fold
		// cross-client upstream data even though the parent ran elsewhere.
		if p.ParentActionID != "" && p.ExecutionID != "" {
			if r.history == nil {
				r.history = make(map[string]map[string]any)
			}
			byNode, ok := r.history[p.ExecutionID]
			if !ok {
				byNode = make(map[string]any)
				r.history[p.ExecutionID] = byNode
			}
			if _, exists := byNode[p.ParentActionID]; !exists {
				byNode[p.ParentActionID] = p.Payload
			}
		}
		r.mu.Unlock()
		if credRef != "" {
			if _, err := r.fetchCredentialCached(ctx, p.NodeID); err != nil {
				r.log.Error("credential fetch failed; deferring pending assignment", "node_id", p.NodeID, "execution_id", p.ExecutionID, "error", err)
				r.mu.Lock()
				delete(r.pendingSeen, key)
				r.mu.Unlock()
				continue
			}
		}
		if a == nil {
			r.log.Warn("pending assignment for unknown local node; skipping", "node_id", p.NodeID, "execution_id", p.ExecutionID)
			continue
		}
		parent := common.ResultData{
			WorkflowID:  p.WorkflowID,
			ExecutionID: p.ExecutionID,
			ActionID:    p.ParentActionID,
			Payload:     p.Payload,
		}
		r.log.Info("executing pending assignment", "node_id", p.NodeID, "execution_id", p.ExecutionID)
		r.executeNode(p.NodeID, p.ExecutionID, []any{parent})
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

// fetchCredentialCached resolves a node's credential_ref just-in-time and
// caches the secret in memory only until the server-advertised TTL expires.
// Provider actions call this at execution time; the secret is never written
// to disk, logs, or results. Nodes without a credential_ref return "".
func (r *runner) fetchCredentialCached(ctx context.Context, nodeID string) (string, error) {
	r.mu.Lock()
	cfg := r.nodeMeta[nodeID]
	workflowID := r.nodeWorkflow[nodeID]
	if cfg.credentialRef == "" {
		r.mu.Unlock()
		return "", nil
	}
	if entry, ok := r.credCache[cfg.credentialRef]; ok && time.Now().Before(entry.expiresAt) {
		secret := entry.secret
		r.mu.Unlock()
		return secret, nil
	}
	ref := cfg.credentialRef
	r.mu.Unlock()
	fetched, err := r.api.FetchCredential(ctx, ref, "", nil, workflowID, nodeID)
	if err != nil {
		return "", err
	}
	ttl := fetched.TTLSeconds
	if ttl <= 0 {
		ttl = 60
	}
	r.mu.Lock()
	if r.credCache == nil {
		r.credCache = make(map[string]credentialCacheEntry)
	}
	r.credCache[ref] = credentialCacheEntry{secret: fetched.Secret, expiresAt: time.Now().Add(time.Duration(ttl) * time.Second)}
	r.mu.Unlock()
	return fetched.Secret, nil
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
		r.mu.Lock()
		r.selfID = id
		r.mu.Unlock()
		if err := r.api.HeartbeatWithDial(ctx, id, transferDialInfo); err != nil {
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
		r.drainPending(ctx, assignments.Pending)
		r.drainTransfers(ctx, id)
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
