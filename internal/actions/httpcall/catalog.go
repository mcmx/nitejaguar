package httpcall

import "github.com/mcmx/nitejaguar/common"

// CatalogEntry describes the http action for the visual designer picker
// and typed inspector form. Args are the new-node defaults (dispatchable:
// the placeholder URL and POST method pass New validation).
func CatalogEntry() common.DesignerCatalogEntry {
	return common.DesignerCatalogEntry{
		ActionType: "action", ActionName: "http",
		Icon: "🌐", Label: "HTTP",
		Desc: "Call an outbound webhook / HTTP endpoint with a templated body.",
		Args: map[string]any{
			"url":    "https://example.com/webhook",
			"method": "POST",
			"body":   `{"text": "Workflow {{ $input.workflow }} finished: $input.result"}`,
		},
		Fields: []common.DesignerField{
			{Key: "url", Label: "URL", Type: "string", Required: true, Placeholder: "https://example.com/webhook", Help: "Target URL (http/https only). Supports $input refs and {{...}} templates."},
			{Key: "method", Label: "Method", Type: "select", Default: "POST", Options: common.FieldOptions("GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"), Help: "HTTP method. GET/HEAD never send a body."},
			{Key: "headers", Label: "Headers", Type: "textarea", Rows: 3, Placeholder: `{"X-Alert-Source": "nitejaguar"}`, Help: "Extra headers as a JSON object. Names and values support templates."},
			{Key: "body", Label: "Body", Type: "textarea", Rows: 4, Placeholder: `{"text": "Deploy {{ $input.version }} done"}`, Help: "Request body. JSON stays JSON (sent as application/json); anything else sends as text. Supports $input refs and {{...}} templates. Ignored for GET/HEAD."},
			{Key: "auth", Label: "Auth", Type: "select", Options: common.FieldOptions("", "bearer"), Help: "bearer injects credential_ref (token/generic) as Authorization: Bearer. Empty sends anonymously."},
			{Key: "timeout", Label: "Timeout (seconds)", Type: "integer", Default: "10", Placeholder: "10", Help: "HTTP timeout in seconds, 1-60."},
		},
	}
}
