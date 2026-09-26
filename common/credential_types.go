package common

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// CredentialField describes one input of a credential type's secret. The
// web form renders these (name attribute "secret_"+Name) and the server
// assembles them into the stored secret envelope via
// EncodeCredentialSecret.
type CredentialField struct {
	// Name is the key inside the stored secret object and the form field
	// suffix (e.g. field "username" renders as "secret_username").
	Name string
	// Label is the human-readable form label.
	Label string
	// Input selects the form widget: "text", "password", or "textarea".
	Input string
	// Required marks fields that must be non-empty at creation.
	Required bool
	// Placeholder is shown as the form input placeholder.
	Placeholder string
	// Help is a short hint rendered under the input.
	Help string
}

// CredentialTypeDef declares the secret shape of one credential type. It
// is the single source of truth for the credential form, the API
// secret_fields encoding, and creation-time validation.
type CredentialTypeDef struct {
	// Name is the canonical credential type (e.g. "aws").
	Name string
	// Label is the human-readable type label for the form select.
	Label string
	// Description explains what the type is for.
	Description string
	// Fields lists the secret inputs in display order.
	Fields []CredentialField
	// Raw means the secret is stored verbatim as a single string (no
	// JSON envelope). Only single-field types use this; multi-field
	// types store a JSON object of field name -> value.
	Raw bool
}

// CredentialTypeDefs returns the field specs for every known credential
// type in stable display order: generic families first, then each
// collection-declared type.
func CredentialTypeDefs() []CredentialTypeDef {
	defs := []CredentialTypeDef{
		{
			Name:        "generic",
			Label:       "Generic secret",
			Description: "Single opaque secret value usable by any node.",
			Fields: []CredentialField{
				{Name: "value", Label: "Secret value", Input: "password", Required: true, Placeholder: "s3cr3t-value", Help: "Stored verbatim; returned as-is on fetch."},
			},
			Raw: true,
		},
		{
			Name:        "token",
			Label:       "API token",
			Description: "Single bearer/API token string.",
			Fields: []CredentialField{
				{Name: "token", Label: "Token", Input: "password", Required: true, Placeholder: "ghp_… / Bearer token", Help: "Sent as-is to the client on fetch."},
			},
			Raw: true,
		},
		{
			Name:        "username_password",
			Label:       "Username + password",
			Description: "Login pair for basic-auth style services.",
			Fields: []CredentialField{
				{Name: "username", Label: "Username", Input: "text", Required: true, Placeholder: "db-admin", Help: "Login / account name."},
				{Name: "password", Label: "Password", Input: "password", Required: true, Placeholder: "••••••••", Help: "Stored encrypted; never displayed again."},
			},
		},
		{
			Name:        "ssh_key",
			Label:       "SSH key",
			Description: "Private key for SSH/SCP-style access.",
			Fields: []CredentialField{
				{Name: "username", Label: "Username (optional)", Input: "text", Required: false, Placeholder: "deploy", Help: "Remote login user, if the action needs it."},
				{Name: "private_key", Label: "Private key (PEM)", Input: "textarea", Required: true, Placeholder: "-----BEGIN OPENSSH PRIVATE KEY-----", Help: "PEM-encoded private key material."},
				{Name: "passphrase", Label: "Passphrase (optional)", Input: "password", Required: false, Placeholder: "key passphrase", Help: "Only if the private key is encrypted."},
			},
		},
		{
			Name:        "aws",
			Label:       "AWS IAM",
			Description: "AWS access keys for the aws collection (ec2, s3).",
			Fields: []CredentialField{
				{Name: "access_key_id", Label: "Access key ID", Input: "text", Required: true, Placeholder: "AKIA…", Help: "AWS access key ID."},
				{Name: "secret_access_key", Label: "Secret access key", Input: "password", Required: true, Placeholder: "••••••••", Help: "AWS secret access key."},
				{Name: "region", Label: "Region (optional)", Input: "text", Required: false, Placeholder: "eu-west-1", Help: "Default region when the node omits one."},
				{Name: "session_token", Label: "Session token (optional)", Input: "password", Required: false, Placeholder: "temporary session token", Help: "Only for temporary STS credentials."},
			},
		},
	}
	// Append any future provider-declared types that have no explicit field
	// spec yet as single-value fallbacks so the form never hides a known
	// type.
	known := make(map[string]struct{}, len(defs))
	for _, d := range defs {
		known[d.Name] = struct{}{}
	}
	for _, t := range KnownCredentialTypes() {
		if _, ok := known[t]; ok {
			continue
		}
		tt := t
		defs = append(defs, CredentialTypeDef{
			Name:        tt,
			Label:       tt,
			Description: "Provider-declared credential type.",
			Fields: []CredentialField{
				{Name: "value", Label: "Secret value", Input: "password", Required: true},
			},
			Raw: true,
		})
	}
	return defs
}

// CredentialTypeDefFor returns the field spec for a credential type.
func CredentialTypeDefFor(name string) (CredentialTypeDef, bool) {
	for _, d := range CredentialTypeDefs() {
		if d.Name == name {
			return d, true
		}
	}
	return CredentialTypeDef{}, false
}

// EncodeCredentialSecret validates per-type required fields and builds the
// stored secret string: raw verbatim for single-value types, a JSON object
// of field name -> value for multi-field types (optional empty fields are
// omitted). Keys are validated against the type spec so typos fail fast.
func EncodeCredentialSecret(ctype string, values map[string]string) (string, error) {
	if ctype == "" {
		ctype = "generic"
	}
	def, ok := CredentialTypeDefFor(ctype)
	if !ok {
		return "", fmt.Errorf("unknown credential type %q", ctype)
	}
	allowed := make(map[string]CredentialField, len(def.Fields))
	for _, f := range def.Fields {
		allowed[f.Name] = f
	}
	for k := range values {
		if _, ok := allowed[k]; !ok {
			return "", fmt.Errorf("unknown field %q for credential type %q", k, ctype)
		}
	}
	// Edge whitespace is trimmed for the emptiness check and the stored
	// value; secrets with meaningful leading/trailing whitespace are not
	// supported. Interior content (including PEM newlines) is preserved.
	trimmed := make(map[string]string, len(def.Fields))
	for _, f := range def.Fields {
		v := strings.TrimSpace(values[f.Name])
		if f.Required && v == "" {
			return "", fmt.Errorf("missing required field %q for credential type %q", f.Name, ctype)
		}
		trimmed[f.Name] = v
	}
	if def.Raw {
		return trimmed[def.Fields[0].Name], nil
	}
	out := make(map[string]string, len(def.Fields))
	for _, f := range def.Fields {
		if trimmed[f.Name] == "" {
			continue
		}
		out[f.Name] = trimmed[f.Name]
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("failed to encode credential type %q: %w", ctype, err)
	}
	return string(raw), nil
}

// ValidateCredentialSecret checks a to-be-stored secret string against the
// type spec: raw non-empty for single-value types, a JSON object carrying
// every required field for multi-field types.
func ValidateCredentialSecret(ctype, secret string) error {
	if ctype == "" {
		ctype = "generic"
	}
	def, ok := CredentialTypeDefFor(ctype)
	if !ok {
		return fmt.Errorf("unknown credential type %q", ctype)
	}
	if def.Raw {
		if strings.TrimSpace(secret) == "" {
			return fmt.Errorf("secret is required")
		}
		return nil
	}
	if strings.TrimSpace(secret) == "" {
		return fmt.Errorf("secret is required")
	}
	var obj map[string]any
	dec := json.NewDecoder(strings.NewReader(secret))
	if err := dec.Decode(&obj); err != nil {
		required := requiredFieldNames(def)
		return fmt.Errorf("credential type %q needs a JSON object with %s (e.g. %s)", ctype, strings.Join(required, ", "), exampleSecret(def))
	}
	for _, f := range def.Fields {
		if !f.Required {
			continue
		}
		v, ok := obj[f.Name]
		if !ok {
			return fmt.Errorf("missing required field %q for credential type %q", f.Name, ctype)
		}
		s, ok := v.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return fmt.Errorf("missing required field %q for credential type %q", f.Name, ctype)
		}
	}
	return nil
}

// DecodeCredentialSecret parses a stored secret into field name -> value.
// Single-value types return a one-entry map keyed by the field name;
// multi-field types parse the JSON object. It is a helper for future
// consumers (actions fetch the opaque string today).
func DecodeCredentialSecret(ctype, secret string) (map[string]string, error) {
	if ctype == "" {
		ctype = "generic"
	}
	def, ok := CredentialTypeDefFor(ctype)
	if !ok {
		return nil, fmt.Errorf("unknown credential type %q", ctype)
	}
	if def.Raw {
		return map[string]string{def.Fields[0].Name: secret}, nil
	}
	var obj map[string]string
	if err := json.Unmarshal([]byte(secret), &obj); err != nil {
		return nil, fmt.Errorf("credential type %q holds a JSON object: %w", ctype, err)
	}
	return obj, nil
}

func requiredFieldNames(def CredentialTypeDef) []string {
	var out []string
	for _, f := range def.Fields {
		if f.Required {
			out = append(out, strconv_quote(f.Name))
		}
	}
	return out
}

func exampleSecret(def CredentialTypeDef) string {
	keys := make([]string, 0, len(def.Fields))
	for _, f := range def.Fields {
		if f.Required {
			keys = append(keys, f.Name)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, strconv_quote(k)+": …")
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func strconv_quote(s string) string {
	return `"` + s + `"`
}
