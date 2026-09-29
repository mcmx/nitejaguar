package client

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/actions/webhook"
)

// findWebhookNode resolves a webhook trigger id to its workflow and method
// config from the workflows currently assigned to this client. The server
// already filtered assignments by targeting (explicit client, matching
// tags, or workflow defaults), so any webhook node present here is bound
// to this client — default, explicit, or tag-selected. Only active
// assignments match: removed or disabled workflows (retired) stop firing.
// Unknown ids report not-found without leaking other clients' workflows.
func (r *runner) findWebhookNode(id string) (workflowID, methodArg, actionID string, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, mw := range r.activeWorkflows {
		for nodeID, cfg := range mw.meta {
			if cfg.actionName != "webhook" {
				continue
			}
			resolved := nodeID
			if a, exists := mw.actions[nodeID]; exists {
				if got := a.GetArgs().Id; got != "" {
					resolved = got
				}
			}
			if nodeID != id && resolved != id {
				continue
			}
			return mw.workflowID, cfg.webhookMethod, resolved, true
		}
	}
	return "", "", "", false
}

// webhookHTTPHandler answers /webhook/{id} on the client listener for every
// HTTP method. Only triggers assigned to this client (see findWebhookNode)
// fire; anything else is a 404. The trigger's `method` argument selects
// which methods are accepted (others get 405). Accepted requests queue
// their payload (method, query, headers, decoded body) into the runner
// event stream, which posts it to the server as the trigger result — so
// conditions route on $result the same way as server-side webhooks.
func (r *runner) webhookHTTPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/webhook/", func(w http.ResponseWriter, req *http.Request) {
		id := strings.TrimPrefix(req.URL.Path, "/webhook/")
		if i := strings.IndexByte(id, '/'); i != -1 {
			id = id[:i]
		}
		if strings.TrimSpace(id) == "" {
			http.Error(w, `{"error":"webhook not found"}`, http.StatusNotFound)
			return
		}
		workflowID, methodArg, actionID, ok := r.findWebhookNode(id)
		if !ok {
			http.Error(w, `{"error":"webhook not found"}`, http.StatusNotFound)
			return
		}
		allowed, _, err := webhook.ParseAllowedMethods(methodArg)
		if err != nil {
			r.log.Error("webhook misconfigured", "trigger_id", id, "error", err)
			http.Error(w, `{"error":"webhook misconfigured"}`, http.StatusInternalServerError)
			return
		}
		method := strings.ToUpper(strings.TrimSpace(req.Method))
		if !webhook.MethodAllowed(allowed, method) {
			http.Error(w, `{"error":"method not allowed for this webhook"}`, http.StatusMethodNotAllowed)
			return
		}

		query := map[string]string{}
		for k, vs := range req.URL.Query() {
			if len(vs) > 0 {
				query[k] = vs[len(vs)-1]
			}
		}
		headers := map[string]string{}
		for k, vs := range req.Header {
			headers[strings.ToLower(k)] = strings.Join(vs, ", ")
		}
		var raw []byte
		if req.Body != nil {
			raw, err = io.ReadAll(io.LimitReader(req.Body, webhook.MaxBodyBytes+1))
			if err != nil {
				http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
				return
			}
			if int64(len(raw)) > webhook.MaxBodyBytes {
				http.Error(w, `{"error":"request body too large"}`, http.StatusRequestEntityTooLarge)
				return
			}
		}
		var body any
		if len(strings.TrimSpace(string(raw))) > 0 {
			body = webhook.ParseBody(raw)
		}

		result := common.ResultData{
			WorkflowID: workflowID,
			ActionID:   actionID,
			ActionType: "trigger",
			ActionName: "webhook",
			Payload:    webhook.BuildPayload(method, req.URL.Path, query, req.URL.RawQuery, headers, body, string(raw)),
		}
		select {
		case r.events <- result:
		case <-req.Context().Done():
			http.Error(w, `{"error":"request cancelled"}`, http.StatusServiceUnavailable)
			return
		}
		r.log.Info("webhook fired", "trigger_id", actionID, "workflow_id", workflowID, "method", method)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "workflow_id": workflowID, "trigger_id": actionID,
		})
	})
	return mux
}

// serveWebhooks runs the client-side webhook listener until ctx ends. A
// failure to bind is reported to the logger without stopping the polling
// loop — webhooks stay unavailable while assignments keep flowing.
func serveWebhooks(ctx context.Context, r *runner, addr string, logger *slog.Logger) {
	if strings.TrimSpace(addr) == "" {
		return
	}
	srv := &http.Server{
		Addr:         addr,
		Handler:      r.webhookHTTPHandler(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	logger.Info("client webhook listener starting", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Warn("client webhook listener failed; polling continues without webhooks", "addr", addr, "error", err)
	}
}
