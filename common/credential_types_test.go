package common

import (
	"encoding/json"
	"testing"
)

func TestCredentialTypeDefsCoverKnownTypes(t *testing.T) {
	defs := CredentialTypeDefs()
	byName := make(map[string]CredentialTypeDef, len(defs))
	for _, d := range defs {
		if _, dup := byName[d.Name]; dup {
			t.Fatalf("duplicate credential type def %q", d.Name)
		}
		byName[d.Name] = d
		if len(d.Fields) == 0 {
			t.Fatalf("type %q has no fields", d.Name)
		}
		seen := map[string]struct{}{}
		for _, f := range d.Fields {
			if f.Name == "" || f.Label == "" {
				t.Fatalf("type %q has a field without name/label: %+v", d.Name, f)
			}
			if _, dup := seen[f.Name]; dup {
				t.Fatalf("type %q duplicates field %q", d.Name, f.Name)
			}
			seen[f.Name] = struct{}{}
		}
		if d.Raw && len(d.Fields) != 1 {
			t.Fatalf("raw type %q must have exactly one field", d.Name)
		}
	}
	for _, want := range KnownCredentialTypes() {
		if _, ok := byName[want]; !ok {
			t.Fatalf("CredentialTypeDefs() missing known type %q", want)
		}
	}
}

func TestEncodeCredentialSecret(t *testing.T) {
	// Single-value types store verbatim.
	raw, err := EncodeCredentialSecret("token", map[string]string{"token": "abc"})
	if err != nil || raw != "abc" {
		t.Fatalf("EncodeCredentialSecret(token) = %q, %v", raw, err)
	}
	// Multi-field types encode a JSON object; empty optionals are omitted.
	enc, err := EncodeCredentialSecret("aws", map[string]string{
		"access_key_id": "AKIA", "secret_access_key": "shh",
	})
	if err != nil {
		t.Fatalf("encode aws: %v", err)
	}
	var obj map[string]string
	if err := json.Unmarshal([]byte(enc), &obj); err != nil {
		t.Fatalf("aws secret is not JSON: %v (%q)", err, enc)
	}
	if obj["access_key_id"] != "AKIA" || obj["secret_access_key"] != "shh" {
		t.Fatalf("aws secret = %q", enc)
	}
	if _, ok := obj["region"]; ok {
		t.Fatalf("empty optional field should be omitted: %q", enc)
	}
	// Missing required field fails.
	if _, err := EncodeCredentialSecret("username_password", map[string]string{"username": "u"}); err == nil {
		t.Fatalf("expected missing-password error")
	}
	// Unknown field (typo) fails.
	if _, err := EncodeCredentialSecret("aws", map[string]string{"access_key_id": "A", "secret_access_key": "B", "regoin": "x"}); err == nil {
		t.Fatalf("expected unknown-field error")
	}
}

func TestValidateCredentialSecret(t *testing.T) {
	if err := ValidateCredentialSecret("token", "abc"); err != nil {
		t.Fatalf("token raw: %v", err)
	}
	if err := ValidateCredentialSecret("username_password", "not-json"); err == nil {
		t.Fatalf("expected raw username_password to fail")
	}
	if err := ValidateCredentialSecret("username_password", `{"username":"u","password":"p"}`); err != nil {
		t.Fatalf("valid pair: %v", err)
	}
	if err := ValidateCredentialSecret("username_password", `{"username":"u"}`); err == nil {
		t.Fatalf("expected missing-password error")
	}
	if err := ValidateCredentialSecret("ssh_key", `{"private_key":"PEM"}`); err != nil {
		t.Fatalf("ssh key without optionals: %v", err)
	}
	if err := ValidateCredentialSecret("aws", `{"access_key_id":"A","secret_access_key":"B","region":"eu-west-1"}`); err != nil {
		t.Fatalf("aws with region: %v", err)
	}
}
