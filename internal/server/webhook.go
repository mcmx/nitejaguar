package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/actions/webhook"
	"github.com/mcmx/nitejaguar/internal/workflow"
	"go.jetify.com/typeid"
)

// webhookResponse is the ack answered on /webhook/{id}. The execution_id
// lets the caller correlate follow-up results; nexts mirrors the routing
// decision for the trigger node.
type webhookResponse struct {
	Ok          bool   `json:"ok"`
	WorkflowID  string `json:"workflow_id"`
	ExecutionID string `json:"execution_id"`
	TriggerID   string `json:"trigger_id"`
}

// findWebhookNode resolves a webhook trigger id to its workflow, tenant and
// node definition. Only enabled workflows fire: disabled (or unknown) ids
// report not-found so the endpoint reveals nothing about other tenants.
// Both the map key and the node's own Id match, mirroring
// triggerOwnedByTenant.
func (s *Server) findWebhookNode(id string) (workflowID, tenantID string, node workflow.Node, found bool) {
	if s.db == nil || strings.TrimSpace(id) == "" {
		return "", "", workflow.Node{}, false
	}
	rows, err := s.db.GetWorkflows(false, true)
	if err != nil {
		return "", "", workflow.Node{}, false
	}
	for _, row := range rows {
		if row == nil {
			continue
		}
		var def workflow.Workflow
		if err := json.Unmarshal([]byte(row.JSONDefinition), &def); err != nil {
			continue
		}
		for key, n := range def.Nodes {
			if n.ActionType != "trigger" || n.ActionName != "webhook" {
				continue
			}
			if key != id && n.Id != id {
				continue
			}
			wid := def.Id
			if wid == "" {
				wid = row.ID
			}
			return wid, workflowTenant(row.TenantID, row.JSONDefinition), n, true
		}
	}
	return "", "", workflow.Node{}, false
}

// webhookHandler answers the server-side webhook endpoint for every HTTP
// method (registered via e.Any("/webhook/:id")). The trigger's `method`
// argument selects which methods fire; anything else is a 405 and never
// produces a result. Accepted requests post their payload in the result
// (method, query, headers, decoded body) so conditions can route on
// $result.method / $result.query.* / $result.body.* like any other trigger.
func (s *Server) webhookHandler(c echo.Context) error {
	id := c.Param("id")
	workflowID, tenantID, node, found := s.findWebhookNode(id)
	if !found {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "webhook not found"})
	}
	// Suspension fails closed: a suspended tenant's webhooks stop firing.
	if s.db != nil && s.db.IsTenantSuspended(normalizeTenantID(tenantID)) {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "tenant is suspended"})
	}

	allowed, _, err := webhook.ParseAllowedMethods(node.Arguments["method"])
	if err != nil {
		log.Printf("webhook %s has invalid method config: %v", id, err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "webhook misconfigured"})
	}
	method := strings.ToUpper(strings.TrimSpace(c.Request().Method))
	if !webhook.MethodAllowed(allowed, method) {
		return c.JSON(http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed for this webhook"})
	}

	// Query: last value wins per key; the raw string stays for signatures.
	query := map[string]string{}
	values := c.Request().URL.Query()
	for k, vs := range values {
		if len(vs) > 0 {
			query[k] = vs[len(vs)-1]
		}
	}
	queryString := c.Request().URL.RawQuery

	// Headers: lowercased names, multi-values joined — enough for
	// $result.headers.* conditions without leaking hop-by-hop noise.
	headers := map[string]string{}
	for k, vs := range c.Request().Header {
		headers[strings.ToLower(k)] = strings.Join(vs, ", ")
	}

	// Body: buffered up to MaxBodyBytes; JSON decodes to native values,
	// anything else stays a string; empty stays nil.
	var raw []byte
	if c.Request().Body != nil {
		raw, err = io.ReadAll(io.LimitReader(c.Request().Body, webhook.MaxBodyBytes+1))
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "failed to read request body"})
		}
		if int64(len(raw)) > webhook.MaxBodyBytes {
			return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
		}
	}
	var body any
	if len(strings.TrimSpace(string(raw))) > 0 {
		body = webhook.ParseBody(raw)
	}
	path := c.Request().URL.Path

	// ExecutorID stays empty so IngestResult attributes the result to
	// this server process — exactly like filechange/cron results flowing
	// through the TriggerManager. The downstream nodes then run locally
	// (when started with -e) instead of stalling as a remote-client
	// handoff no client would ever pick up.
	result := common.ResultData{
		WorkflowID: workflowID,
		ActionID:   node.Id,
		ActionType: "trigger",
		ActionName: "webhook",
		TenantID:   normalizeTenantID(tenantID),
		Payload:    webhook.BuildPayload(method, path, query, queryString, headers, body, string(raw)),
	}
	if result.ActionID == "" {
		result.ActionID = id
	}
	if result.ResultID == "" {
		rid, _ := typeid.WithPrefix("result")
		result.ResultID = rid.String()
	}

	stored, _, err := s.wm.IngestResult(result)
	if err != nil {
		log.Printf("webhook %s ingest failed: %v", id, err)
		return c.JSON(http.StatusNotFound, map[string]string{"error": "webhook not found"})
	}
	log.Printf("webhook fired: trigger=%s workflow=%s execution=%s method=%s", id, stored.WorkflowID, stored.ExecutionID, method)
	return c.JSON(http.StatusOK, webhookResponse{
		Ok: true, WorkflowID: stored.WorkflowID,
		ExecutionID: stored.ExecutionID, TriggerID: result.ActionID,
	})
}
