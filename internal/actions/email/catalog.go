package email

import "github.com/mcmx/nitejaguar/common"

// CatalogEntry describes the email action for the visual designer picker
// and typed inspector form. Args are the new-node defaults (dispatchable:
// host/from/to pass New validation). Auth is never an arg — it comes from
// the node's credential_ref (username_password) injected just-in-time.
func CatalogEntry() common.DesignerCatalogEntry {
	return common.DesignerCatalogEntry{
		ActionType: "action", ActionName: "email",
		Icon: "✉️", Label: "Email",
		Desc: "Send an SMTP email with templated headers, HTML, and attachments.",
		Args: map[string]any{
			"host": "smtp.example.com", "port": "587",
			"from": "noreply@example.com", "to": "ops@example.com",
			"subject": "Hello {{ $input.name }}", "body": "Hi $input.name, see attached.",
		},
		Fields: []common.DesignerField{
			{Key: "host", Label: "SMTP host", Type: "string", Required: true, Default: "smtp.example.com", Placeholder: "smtp.example.com", Help: "SMTP host. Supports $input refs and {{...}} templates. Port/auth: port arg + credential_ref (username_password)."},
			{Key: "port", Label: "SMTP port", Type: "integer", Default: "587", Placeholder: "587", Help: "465 implicit TLS, 587 STARTTLS, else plaintext. Supports templates."},
			{Key: "from", Label: "From", Type: "string", Required: true, Default: "noreply@example.com", Placeholder: "noreply@example.com", Help: "Sender address. Supports $input refs and {{...}} templates."},
			{Key: "to", Label: "To", Type: "string", Required: true, Default: "ops@example.com", Placeholder: "a@example.com, b@example.com", Help: "Comma-separated recipients. Supports $input refs and {{...}} templates."},
			{Key: "cc", Label: "Cc", Type: "string", Placeholder: "cc@example.com", Help: "Comma-separated. Supports templates."},
			{Key: "bcc", Label: "Bcc", Type: "string", Placeholder: "bcc@example.com", Help: "Comma-separated, never in headers. Supports templates."},
			{Key: "subject", Label: "Subject", Type: "string", Placeholder: "Deploy finished", Help: "Supports $input refs and {{...}} templates."},
			{Key: "body", Label: "Body (plain)", Type: "textarea", Rows: 4, Placeholder: "Hi $input.name …", Help: "Plain-text body. Supports $input refs and {{...}} templates."},
			{Key: "html", Label: "Body (HTML)", Type: "textarea", Rows: 4, Placeholder: "<b>Hi {{ $input.name }}</b>", Help: "HTML body; with plain body sends multipart/alternative. Supports templates."},
			{Key: "attachments", Label: "Attachments", Type: "textarea", Rows: 3, Placeholder: "/tmp/report.pdf", Help: "Comma-separated paths or JSON array of path strings / {path} / {name, content_base64}. Names and paths support templates; bytes never do. 10MB total cap."},
		},
	}
}
