package slack

import "github.com/mcmx/nitejaguar/common"

// CatalogEntry describes the slack action for the visual designer picker
// and typed inspector form. Args are the new-node defaults (dispatchable:
// the placeholder webhook_url passes New validation; Execute is never run
// by the catalog probe). The webhook URL is a secret — production nodes
// should leave it empty and set credential_ref to a token/generic
// credential holding the URL instead.
func CatalogEntry() common.DesignerCatalogEntry {
	return common.DesignerCatalogEntry{
		ActionType: "action", ActionName: "slack",
		Icon: "💬", Label: "Slack",
		Desc: "Post a message to Slack via an incoming webhook URL.",
		Args: map[string]any{
			"webhook_url": "https://hooks.slack.com/services/<team>/<app>/<secret>",
			"text":        "Workflow {{ $input.workflow }} failed: $input.result",
		},
		Fields: []common.DesignerField{
			{Key: "webhook_url", Label: "Webhook URL", Type: "string", Placeholder: "https://hooks.slack.com/services/…", Help: "Slack incoming webhook URL. Secret: prefer credential_ref (token/generic) holding the URL and leave this empty. Supports $input refs and {{...}} templates."},
			{Key: "text", Label: "Message text", Type: "textarea", Rows: 4, Required: true, Placeholder: "Deploy {{ $input.version }} failed: $input.result", Help: "Message text. Supports $input refs and {{...}} templates with the shared filter chain."},
			{Key: "channel", Label: "Channel override", Type: "string", Placeholder: "#alerts", Help: "Optional channel override (recent incoming webhooks ignore it). Supports templates."},
			{Key: "username", Label: "Username override", Type: "string", Placeholder: "nitejaguar", Help: "Optional bot display-name override. Supports templates."},
			{Key: "timeout", Label: "Timeout (seconds)", Type: "integer", Default: "10", Placeholder: "10", Help: "HTTP timeout in seconds, 1-60."},
		},
	}
}
