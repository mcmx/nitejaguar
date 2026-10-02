// Package email implements the `email` workflow action: it sends an SMTP
// message with templated headers, plain and/or HTML bodies, and attachments.
//
// Arguments (host/port from args, auth from credential_ref — never inline):
//   - host: SMTP host (required, templatable).
//   - port: SMTP port (default 587, templatable). 465 uses implicit TLS,
//     587 uses STARTTLS, other ports use plaintext with opportunistic
//     STARTTLS when the server advertises it.
//   - from: envelope + From header (required, templatable).
//   - to: comma-separated recipients (required, templatable).
//   - cc, bcc: comma-separated recipients (optional, templatable).
//   - subject: subject line (optional but at least one of subject/body/html
//     must be non-empty, templatable).
//   - body: plain-text body (optional, templatable).
//   - html: HTML body (optional, templatable). When both body and html are
//     set the message is multipart/alternative; a single set part is sent
//     as-is; attachments upgrade the outer part to multipart/mixed.
//   - attachments: file paths and/or inline content (optional). Accepted
//     forms: a JSON array string, a comma-separated path list, or a native
//     list. JSON elements are either a path string, {"path": "..."}, or
//     {"name": "...", "content_base64"|"content"|"data": "..."}. Paths and
//     names support $input refs and {{...}} templates; inline content bytes
//     never pass through templating. Total decoded bytes are capped at
//     10MB; oversize or unreadable attachments fail with an error result.
//
// Auth comes only from the framework-injected credential binding
// (common.CredentialBinding in the Execute inputs, resolved server-side or
// client-side from the node's credential_ref just before execution). The
// credential must be type username_password ({"username","password"}); a
// node without a binding sends anonymously. Secrets never appear in workflow
// JSON, logs, or result payloads.
//
// Templating uses the shared engine only (common.SplitTemplates /
// common.EvalTemplate / common.ResolveRefPath): every string field except
// attachment content supports bare $input.<path> refs and {{...}} templates.
// $result./$args. refs are rejected (own result does not exist yet).
package email

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/mcmx/nitejaguar/common"
)

// maxAttachmentsBytes caps the total decoded attachment payload per send.
const maxAttachmentsBytes = 10 << 20 // 10MB

type emailAction struct {
	data   common.ActionArgs
	events chan common.ResultData
}

// New creates the action; ActionType is forced to "action". Literal (non-
// templated) host/port/from/to values are validated here so a bad definition
// fails at load; templated values defer to Execute.
func New(events chan common.ResultData, data common.ActionArgs) (common.Action, error) {
	s := &emailAction{events: events, data: data}
	s.data.ActionType = "action"
	rawArgs, err := rawArgsMap(data.Args)
	if err != nil {
		return nil, err
	}
	if err := validateStatic(rawArgs); err != nil {
		return nil, err
	}
	return s, nil
}

func (e *emailAction) Stop() error { return nil }

func (e *emailAction) GetArgs() common.ActionArgs { return e.data }

func (e *emailAction) send(executionID string, payload map[string]any) {
	e.events <- common.ResultData{
		ExecutionID: executionID,
		ActionID:    e.data.Id,
		ActionType:  e.data.ActionType,
		ActionName:  e.data.ActionName,
		Payload:     payload,
	}
}

// Execute resolves templates, builds the MIME message, and sends it.
func (e *emailAction) Execute(executionID string, inputs []any) {
	rawArgs, err := rawArgsMap(e.data.Args)
	if err != nil {
		e.send(executionID, errorPayload("", err.Error()))
		return
	}
	input := findInputResult(inputs)
	cred := common.FindCredential(inputs)

	resolved, err := resolveFields(rawArgs, input)
	if err != nil {
		e.send(executionID, errorPayload("", err.Error()))
		return
	}
	if err := validateResolved(resolved); err != nil {
		e.send(executionID, errorPayload(resolved.host, err.Error()))
		return
	}
	toList, err := parseAddresses(resolved.to)
	if err != nil {
		e.send(executionID, errorPayload(resolved.host, err.Error()))
		return
	}
	ccList, err := parseAddresses(resolved.cc)
	if err != nil {
		e.send(executionID, errorPayload(resolved.host, err.Error()))
		return
	}
	bccList, err := parseAddresses(resolved.bcc)
	if err != nil {
		e.send(executionID, errorPayload(resolved.host, err.Error()))
		return
	}
	fromAddr, err := parseSingleAddress(resolved.from)
	if err != nil {
		e.send(executionID, errorPayload(resolved.host, err.Error()))
		return
	}
	port, err := parsePort(resolved.port)
	if err != nil {
		e.send(executionID, errorPayload(resolved.host, err.Error()))
		return
	}
	atts, err := resolveAttachments(rawArgs, input)
	if err != nil {
		e.send(executionID, errorPayload(resolved.host, err.Error()))
		return
	}
	var total int64
	for _, a := range atts {
		total += int64(len(a.content))
	}
	if total > maxAttachmentsBytes {
		e.send(executionID, errorPayload(resolved.host, fmt.Sprintf("attachments exceed %d bytes (%d)", maxAttachmentsBytes, total)))
		return
	}
	auth, err := smtpAuth(cred, resolved.host)
	if err != nil {
		e.send(executionID, errorPayload(resolved.host, err.Error()))
		return
	}
	msg, err := buildMessage(fromAddr, toList, ccList, resolved.subject, resolved.body, resolved.htmlBody, atts)
	if err != nil {
		e.send(executionID, errorPayload(resolved.host, err.Error()))
		return
	}
	envelope := append(append([]string{}, toList...), ccList...)
	envelope = append(envelope, bccList...)
	if err := sendMail(resolved.host, port, fromAddr, envelope, msg, auth); err != nil {
		e.send(executionID, errorPayload(resolved.host, err.Error()))
		return
	}
	names := make([]string, 0, len(atts))
	for _, a := range atts {
		names = append(names, a.name)
	}
	e.send(executionID, map[string]any{
		"type": "success", "host": resolved.host, "port": port,
		"from": fromAddr, "to": toList, "cc": ccList, "bcc": bccList,
		"subject": resolved.subject, "attachments": names,
		"bytes": len(msg), "result": fmt.Sprintf("Email sent to %d recipient(s)", len(envelope)),
	})
}

func errorPayload(host, msg string) map[string]any {
	return map[string]any{"type": "error", "host": host, "result": msg}
}

// resolvedFields holds the templated string fields after resolution.
type resolvedFields struct {
	host     string
	port     string
	from     string
	to       string
	cc       string
	bcc      string
	subject  string
	body     string
	htmlBody string
}

func resolveFields(rawArgs map[string]any, input *common.ResultData) (resolvedFields, error) {
	get := func(names ...string) (string, error) {
		v, ok := firstPresent(rawArgs, names...)
		if !ok {
			return "", nil
		}
		s, ok := stringifyScalar(v)
		if !ok {
			str, isStr := v.(string)
			if !isStr {
				return "", fmt.Errorf("invalid value for %q: must be a string", names[0])
			}
			s = str
		}
		return resolveStringTemplate(s, input)
	}
	var r resolvedFields
	var err error
	if r.host, err = get("host", "smtp_host", "server"); err != nil {
		return r, err
	}
	if r.port, err = get("port", "smtp_port"); err != nil {
		return r, err
	}
	if r.port == "" {
		r.port = "587"
	}
	if r.from, err = get("from", "sender"); err != nil {
		return r, err
	}
	if r.to, err = get("to", "recipients", "recipient"); err != nil {
		return r, err
	}
	if r.cc, err = get("cc"); err != nil {
		return r, err
	}
	if r.bcc, err = get("bcc"); err != nil {
		return r, err
	}
	if r.subject, err = get("subject"); err != nil {
		return r, err
	}
	if r.body, err = get("body", "text", "plain"); err != nil {
		return r, err
	}
	if r.htmlBody, err = get("html", "html_body", "htmlBody"); err != nil {
		return r, err
	}
	return r, nil
}

// validateStatic rejects bad literal args at load while letting templated
// values ($input./{{...}}) through to Execute-time resolution.
func validateStatic(rawArgs map[string]any) error {
	strOf := func(names ...string) string {
		v, ok := firstPresent(rawArgs, names...)
		if !ok {
			return ""
		}
		s, ok := stringifyScalar(v)
		if !ok {
			if str, isStr := v.(string); isStr {
				return str
			}
			return ""
		}
		return s
	}
	if h := strOf("host", "smtp_host", "server"); h != "" && !looksTemplated(h) {
		if strings.Contains(h, " ") || strings.Contains(h, "/") {
			return fmt.Errorf("invalid host %q", h)
		}
	}
	if p := strOf("port", "smtp_port"); p != "" && !looksTemplated(p) {
		if _, err := parsePort(p); err != nil {
			return err
		}
	}
	if f := strOf("from", "sender"); f != "" && !looksTemplated(f) {
		if _, err := parseSingleAddress(f); err != nil {
			return err
		}
	}
	return nil
}

func validateResolved(r resolvedFields) error {
	if strings.TrimSpace(r.host) == "" {
		return fmt.Errorf("missing host argument (SMTP host)")
	}
	if strings.TrimSpace(r.from) == "" {
		return fmt.Errorf("missing from argument (sender address)")
	}
	if strings.TrimSpace(r.to) == "" && strings.TrimSpace(r.cc) == "" && strings.TrimSpace(r.bcc) == "" {
		return fmt.Errorf("missing to argument (at least one recipient in to/cc/bcc)")
	}
	if strings.TrimSpace(r.subject) == "" && strings.TrimSpace(r.body) == "" && strings.TrimSpace(r.htmlBody) == "" {
		return fmt.Errorf("missing subject/body/html: at least one must be non-empty")
	}
	return nil
}

func looksTemplated(s string) bool {
	return strings.Contains(s, "$input.") || strings.Contains(s, "{{")
}

func parsePort(raw string) (int, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return 587, nil
	}
	n, err := strconv.Atoi(p)
	if err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("invalid port %q: must be 1-65535", raw)
	}
	return n, nil
}

// parseAddresses splits a comma-separated list and validates each entry.
func parseAddresses(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		a, err := mail.ParseAddress(part)
		if err != nil {
			return nil, fmt.Errorf("invalid recipient address %q", part)
		}
		out = append(out, a.Address)
	}
	return out, nil
}

func parseSingleAddress(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("missing from argument (sender address)")
	}
	a, err := mail.ParseAddress(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid from address %q", raw)
	}
	return a.Address, nil
}

// smtpAuth builds PlainAuth from the injected credential binding, or nil for
// anonymous sends. Only username_password is accepted; anything else fails
// closed so a token/generic secret is never misused as SMTP auth.
func smtpAuth(cred *common.CredentialBinding, host string) (smtp.Auth, error) {
	if cred != nil && strings.TrimSpace(cred.Err) != "" {
		return nil, fmt.Errorf("cannot resolve credential %q: %s", cred.Ref, cred.Err)
	}
	if cred == nil || strings.TrimSpace(cred.Secret) == "" {
		return nil, nil
	}
	if cred.Type != "" && cred.Type != "username_password" {
		return nil, fmt.Errorf("credential type %q does not match email action (requires username_password)", cred.Type)
	}
	fields, err := common.DecodeCredentialSecret("username_password", cred.Secret)
	if err != nil {
		// The stored secret may be a legacy raw password without a
		// username: without a username SMTP auth cannot proceed.
		return nil, fmt.Errorf("email credential must be username_password {username, password}: %v", err)
	}
	username := strings.TrimSpace(fields["username"])
	password := fields["password"]
	if username == "" || password == "" {
		return nil, fmt.Errorf("email credential must carry username and password")
	}
	return smtp.PlainAuth("", username, password, host), nil
}

// attachment is one resolved file payload (bytes never templated).
type attachment struct {
	name    string
	content []byte
}

func resolveAttachments(rawArgs map[string]any, input *common.ResultData) ([]attachment, error) {
	v, ok := firstPresent(rawArgs, "attachments", "attachment", "files")
	if !ok {
		return nil, nil
	}
	// Native lists (designer defaults / programmatic args).
	if list, isList := v.([]any); isList {
		return resolveAttachmentList(list, input)
	}
	if s, ok := stringifyScalar(v); ok {
		trimmed := strings.TrimSpace(s)
		if trimmed == "" {
			return nil, nil
		}
		// JSON array form supports both path strings and inline objects.
		if strings.HasPrefix(trimmed, "[") {
			expanded := trimmed
			if strings.Contains(trimmed, "{{") {
				var err error
				expanded, err = common.ExpandJSONTemplates(trimmed, common.InputLookup(input))
				if err != nil {
					return nil, err
				}
			}
			var elems []any
			if err := json.Unmarshal([]byte(expanded), &elems); err != nil {
				return nil, fmt.Errorf("invalid attachments JSON array: %v", err)
			}
			return resolveAttachmentList(elems, input)
		}
		// Otherwise a comma-separated path list (each path templatable).
		var out []attachment
		for _, part := range strings.Split(s, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			name, data, err := loadAttachmentPath(part, input)
			if err != nil {
				return nil, err
			}
			out = append(out, attachment{name: name, content: data})
		}
		return out, nil
	}
	// Maps / other JSON shapes round-trip through JSON: accept a single
	// object {path|name+content} as a one-element list.
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("invalid attachments value: %v", err)
	}
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "[") {
		var elems []any
		if err := json.Unmarshal([]byte(trimmed), &elems); err != nil {
			return nil, fmt.Errorf("invalid attachments value: %v", err)
		}
		return resolveAttachmentList(elems, input)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		return nil, fmt.Errorf("invalid attachments value: must be a path list or JSON array")
	}
	return resolveAttachmentList([]any{obj}, input)
}

func resolveAttachmentList(elems []any, input *common.ResultData) ([]attachment, error) {
	var out []attachment
	for i, e := range elems {
		switch t := e.(type) {
		case string:
			if strings.TrimSpace(t) == "" {
				continue
			}
			// A JSON string element may itself be an inline object string;
			// otherwise it is a file path.
			trimmed := strings.TrimSpace(t)
			if strings.HasPrefix(trimmed, "{") {
				var obj map[string]any
				if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
					a, err := resolveAttachmentObject(obj, input)
					if err != nil {
						return nil, fmt.Errorf("attachments[%d]: %w", i, err)
					}
					out = append(out, a)
					continue
				}
			}
			name, data, err := loadAttachmentPath(t, input)
			if err != nil {
				return nil, fmt.Errorf("attachments[%d]: %w", i, err)
			}
			out = append(out, attachment{name: name, content: data})
		case map[string]any:
			a, err := resolveAttachmentObject(t, input)
			if err != nil {
				return nil, fmt.Errorf("attachments[%d]: %w", i, err)
			}
			out = append(out, a)
		default:
			raw, err := json.Marshal(e)
			if err != nil {
				return nil, fmt.Errorf("attachments[%d]: invalid entry", i)
			}
			var obj map[string]any
			if jerr := json.Unmarshal(raw, &obj); jerr != nil {
				// Fall back to a path string.
				s := strings.TrimSpace(common.StringifyArgValue(e))
				if s == "" {
					continue
				}
				name, data, err := loadAttachmentPath(s, input)
				if err != nil {
					return nil, fmt.Errorf("attachments[%d]: %w", i, err)
				}
				out = append(out, attachment{name: name, content: data})
				continue
			}
			a, err := resolveAttachmentObject(obj, input)
			if err != nil {
				return nil, fmt.Errorf("attachments[%d]: %w", i, err)
			}
			out = append(out, a)
		}
	}
	return out, nil
}

// resolveAttachmentObject handles {"path": "..."} (file) and
// {"name": "...", "content_base64"|"content"|"data": "..."} (inline).
// Only name/path go through templating; content bytes never do.
func resolveAttachmentObject(obj map[string]any, input *common.ResultData) (attachment, error) {
	strField := func(names ...string) string {
		for _, n := range names {
			if v, ok := obj[n]; ok && v != nil {
				if s, ok := stringifyScalar(v); ok && strings.TrimSpace(s) != "" {
					return s
				}
			}
		}
		return ""
	}
	if p := strField("path", "file", "filepath"); strings.TrimSpace(p) != "" {
		name, data, err := loadAttachmentPath(p, input)
		if err != nil {
			return attachment{}, err
		}
		return attachment{name: name, content: data}, nil
	}
	nameRaw := strField("name", "filename", "file_name")
	if strings.TrimSpace(nameRaw) == "" {
		return attachment{}, fmt.Errorf("inline attachment needs a name")
	}
	name, err := resolveStringTemplate(nameRaw, input)
	if err != nil {
		return attachment{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return attachment{}, fmt.Errorf("inline attachment needs a name")
	}
	// Content keys are read verbatim — never templated (binary safety).
	var contentRaw string
	var hasContent bool
	for _, k := range []string{"content_base64", "content", "data", "body"} {
		if v, ok := obj[k]; ok && v != nil {
			if s, ok := v.(string); ok {
				contentRaw, hasContent = s, true
				break
			}
			if s, ok := stringifyScalar(v); ok {
				contentRaw, hasContent = s, true
				break
			}
		}
	}
	if !hasContent {
		return attachment{}, fmt.Errorf("inline attachment %q needs content (content_base64)", name)
	}
	cleaned := strings.TrimSpace(contentRaw)
	decoded, err := base64.StdEncoding.DecodeString(cleaned)
	if err != nil {
		// Accept raw (non-base64) strings as UTF-8 bytes for convenience.
		decoded = []byte(contentRaw)
	}
	return attachment{name: name, content: decoded}, nil
}

// loadAttachmentPath resolves templating in the path, reads the file, and
// returns the base name plus bytes.
func loadAttachmentPath(rawPath string, input *common.ResultData) (string, []byte, error) {
	resolved, err := resolveStringTemplate(rawPath, input)
	if err != nil {
		return "", nil, err
	}
	resolved = strings.TrimSpace(resolved)
	if resolved == "" {
		return "", nil, fmt.Errorf("empty attachment path")
	}
	expanded, err := common.ExpandPath(resolved)
	if err != nil {
		return "", nil, err
	}
	data, err := os.ReadFile(expanded)
	if err != nil {
		return "", nil, fmt.Errorf("cannot read attachment %q: %v", resolved, err)
	}
	name := baseName(expanded)
	if name == "" {
		name = "attachment"
	}
	return name, data, nil
}

func baseName(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// buildMessage assembles the MIME message (stdlib only, no new dependency).
func buildMessage(from string, to, cc []string, subject, body, htmlBody string, atts []attachment) ([]byte, error) {
	hasBody := strings.TrimSpace(body) != ""
	hasHTML := strings.TrimSpace(htmlBody) != ""
	var top bytes.Buffer
	writeHeader(&top, "From", from)
	if len(to) > 0 {
		writeHeader(&top, "To", strings.Join(to, ", "))
	}
	if len(cc) > 0 {
		writeHeader(&top, "Cc", strings.Join(cc, ", "))
	}
	writeHeader(&top, "Subject", mime.QEncoding.Encode("utf-8", subject))
	top.WriteString("MIME-Version: 1.0\r\n")
	if len(atts) == 0 {
		if hasBody && !hasHTML {
			top.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
			top.WriteString(body)
			return top.Bytes(), nil
		}
		if hasHTML && !hasBody {
			top.WriteString("Content-Type: text/html; charset=utf-8\r\n\r\n")
			top.WriteString(htmlBody)
			return top.Bytes(), nil
		}
		// Both parts, no attachments: multipart/alternative.
		w := multipart.NewWriter(&top)
		top.WriteString("Content-Type: multipart/alternative; boundary=" + w.Boundary() + "\r\n\r\n")
		if err := writeTextPart(w, "text/plain", body); err != nil {
			return nil, err
		}
		if err := writeTextPart(w, "text/html", htmlBody); err != nil {
			return nil, err
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		return top.Bytes(), nil
	}
	// With attachments the outer part is multipart/mixed.
	w := multipart.NewWriter(&top)
	top.WriteString("Content-Type: multipart/mixed; boundary=" + w.Boundary() + "\r\n\r\n")
	if hasBody && hasHTML {
		alt := &bytes.Buffer{}
		aw := multipart.NewWriter(alt)
		if err := writeTextPart(aw, "text/plain", body); err != nil {
			return nil, err
		}
		if err := writeTextPart(aw, "text/html", htmlBody); err != nil {
			return nil, err
		}
		if err := aw.Close(); err != nil {
			return nil, err
		}
		h := textproto.MIMEHeader{}
		h.Set("Content-Type", "multipart/alternative; boundary="+aw.Boundary())
		part, err := w.CreatePart(h)
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(alt.Bytes()); err != nil {
			return nil, err
		}
	} else if hasHTML {
		if err := writeTextPart(w, "text/html", htmlBody); err != nil {
			return nil, err
		}
	} else {
		if err := writeTextPart(w, "text/plain", body); err != nil {
			return nil, err
		}
	}
	for _, a := range atts {
		h := textproto.MIMEHeader{}
		h.Set("Content-Type", "application/octet-stream; name="+quoteParam(a.name))
		h.Set("Content-Transfer-Encoding", "base64")
		h.Set("Content-Disposition", "attachment; filename="+quoteParam(a.name))
		part, err := w.CreatePart(h)
		if err != nil {
			return nil, err
		}
		enc := base64.NewEncoder(base64.StdEncoding, part)
		if _, err := enc.Write(a.content); err != nil {
			return nil, err
		}
		if err := enc.Close(); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return top.Bytes(), nil
}

func writeTextPart(w *multipart.Writer, ctype, content string) error {
	h := textproto.MIMEHeader{}
	h.Set("Content-Type", ctype+"; charset=utf-8")
	h.Set("Content-Transfer-Encoding", "8bit")
	part, err := w.CreatePart(h)
	if err != nil {
		return err
	}
	_, err = part.Write([]byte(content))
	return err
}

func writeHeader(buf *bytes.Buffer, key, value string) {
	buf.WriteString(key + ": " + value + "\r\n")
}

func quoteParam(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, "_") + `"`
}

// sendMailFunc is the SMTP transport (overridable in tests).
var sendMailFunc = defaultSendMail

func sendMail(host string, port int, from string, to []string, msg []byte, auth smtp.Auth) error {
	return sendMailFunc(host, port, from, to, msg, auth)
}

// defaultSendMail dials SMTP with auto-TLS by port: implicit TLS on 465,
// STARTTLS on 587 (required when auth is present), opportunistic STARTTLS
// otherwise.
func defaultSendMail(host string, port int, from string, to []string, msg []byte, auth smtp.Auth) error {
	addr := fmt.Sprintf("%s:%d", host, port)
	if port == 465 {
		tlsCfg := &tls.Config{ServerName: host}
		conn, err := tls.Dial("tcp", addr, tlsCfg)
		if err != nil {
			return fmt.Errorf("smtp dial tls %s: %w", addr, err)
		}
		c, err := smtp.NewClient(conn, host)
		if err != nil {
			return fmt.Errorf("smtp client: %w", err)
		}
		defer func() { _ = c.Quit() }()
		if auth != nil {
			if err := c.Auth(auth); err != nil {
				return fmt.Errorf("smtp auth: %w", err)
			}
		}
		return smtpSend(c, from, to, msg)
	}
	c, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("smtp dial %s: %w", addr, err)
	}
	defer func() { _ = c.Quit() }()
	if ok, _ := c.Extension("STARTTLS"); ok {
		tlsCfg := &tls.Config{ServerName: host}
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	} else if auth != nil && port == 587 {
		return fmt.Errorf("smtp server does not advertise STARTTLS; refusing to send credentials in cleartext")
	}
	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	return smtpSend(c, from, to, msg)
}

func smtpSend(c *smtp.Client, from string, to []string, msg []byte) error {
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return fmt.Errorf("smtp rcpt %q: %w", rcpt, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		_ = w.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	return nil
}

// --- templating helpers (shared engine only) ---

var resultRefPattern = regexp.MustCompile(`\$input\.[A-Za-z0-9_.\[\]]+`)

// resolveStringTemplate expands {{...}} templates then bare $input. refs.
// Attachment content never passes through here (binary safety); every other
// string field does.
func resolveStringTemplate(raw string, input *common.ResultData) (string, error) {
	if raw == "" {
		return "", nil
	}
	if strings.Contains(raw, "$result.") || strings.Contains(raw, "$args.") {
		return "", fmt.Errorf("unsupported reference in %q: only $input. resolves against upstream payload in action args", raw)
	}
	if strings.Contains(raw, "{{") && !common.HasTemplate(raw) {
		return "", fmt.Errorf("invalid template in %q: unclosed \"{{\"", raw)
	}
	segs := common.SplitTemplates(raw)
	if len(segs) == 3 && segs[0] == "" && segs[2] == "" {
		v, err := common.EvalTemplate(segs[1], common.InputLookup(input))
		if err != nil {
			return "", err
		}
		return common.TemplateString(v), nil
	}
	var sb strings.Builder
	for i, seg := range segs {
		if i%2 == 1 {
			v, err := common.EvalTemplate(seg, common.InputLookup(input))
			if err != nil {
				return "", err
			}
			sb.WriteString(common.TemplateString(v))
			continue
		}
		if !strings.Contains(seg, "$input.") {
			sb.WriteString(seg)
			continue
		}
		if input == nil {
			return "", fmt.Errorf("no input result available to resolve %q", raw)
		}
		if len(segs) == 1 {
			if trimmed := strings.TrimSpace(seg); resultRefPattern.FindString(trimmed) == trimmed {
				val, err := common.ResolveRefPath(input.Payload, trimmed)
				if err != nil {
					return "", err
				}
				return common.TemplateString(val), nil
			}
		}
		var resolveErr error
		out := resultRefPattern.ReplaceAllStringFunc(seg, func(m string) string {
			if resolveErr != nil {
				return m
			}
			val, err := common.ResolveRefPath(input.Payload, m)
			if err != nil {
				resolveErr = err
				return m
			}
			return common.TemplateString(val)
		})
		if resolveErr != nil {
			return "", resolveErr
		}
		sb.WriteString(out)
	}
	return sb.String(), nil
}

// rawArgsMap normalizes action args while preserving value types (attachments
// need native lists). Nil means no args.
func rawArgsMap(args any) (map[string]any, error) {
	if args == nil {
		return map[string]any{}, nil
	}
	if m, ok := args.(map[string]any); ok {
		out := make(map[string]any, len(m))
		for k, v := range m {
			out[k] = v
		}
		return out, nil
	}
	if m, ok := args.(map[string]string); ok {
		out := make(map[string]any, len(m))
		for k, v := range m {
			out[k] = v
		}
		return out, nil
	}
	v := reflect.ValueOf(args)
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return map[string]any{}, nil
		}
		v = v.Elem()
	}
	if v.Kind() == reflect.Map {
		if v.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("invalid arguments type %T: map key must be string", args)
		}
		out := make(map[string]any, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out[iter.Key().String()] = iter.Value().Interface()
		}
		return out, nil
	}
	return nil, fmt.Errorf("invalid arguments type %T: must be a map", args)
}

func firstPresent(args map[string]any, names ...string) (any, bool) {
	for _, n := range names {
		if v, ok := args[n]; ok && v != nil && strings.TrimSpace(common.StringifyArgValue(v)) != "" {
			return v, true
		}
	}
	return nil, false
}

func stringifyScalar(v any) (string, bool) {
	if v == nil {
		return "", false
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Interface || rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return "", false
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return fmt.Sprint(rv.Interface()), true
	default:
		return "", false
	}
}

// findInputResult returns the first ResultData found in inputs, if any.
func findInputResult(inputs []any) *common.ResultData {
	for _, in := range inputs {
		switch v := in.(type) {
		case common.ResultData:
			c := v
			return &c
		case *common.ResultData:
			if v != nil {
				return v
			}
		}
	}
	return nil
}
