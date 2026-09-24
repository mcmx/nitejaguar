package common

import (
	"testing"
)

func TestProviderForAction(t *testing.T) {
	cases := []struct {
		action   string
		provider string
		ctype    string
		known    bool
	}{
		{"file", "core", "", true},
		{"datetime", "core", "", true},
		{"wait", "core", "", true},
		{"filechange", "core", "", true},
		{"ec2", "aws", "aws", true},
		{"s3", "aws", "aws", true},
		{"nope", "", "", false},
	}
	for _, c := range cases {
		p, ok := ProviderForAction(c.action)
		if ok != c.known {
			t.Fatalf("ProviderForAction(%q) known = %v, want %v", c.action, ok, c.known)
		}
		if !c.known {
			continue
		}
		if p.Name != c.provider {
			t.Fatalf("ProviderForAction(%q) provider = %q, want %q", c.action, p.Name, c.provider)
		}
		ctype, known := CredentialTypeForAction(c.action)
		if !known || ctype != c.ctype {
			t.Fatalf("CredentialTypeForAction(%q) = (%q, %v), want (%q, true)", c.action, ctype, known, c.ctype)
		}
	}
	if _, known := CredentialTypeForAction("nope"); known {
		t.Fatalf("CredentialTypeForAction(nope) should be unknown")
	}
}

func TestKnownCredentialTypes(t *testing.T) {
	types := KnownCredentialTypes()
	set := make(map[string]struct{}, len(types))
	for _, typ := range types {
		set[typ] = struct{}{}
	}
	for _, want := range []string{"generic", "token", "username_password", "ssh_key", "aws"} {
		if _, ok := set[want]; !ok {
			t.Fatalf("KnownCredentialTypes() = %v, missing %q", types, want)
		}
	}
	// The legacy s3 type is superseded: S3 uses the aws collection type.
	if _, ok := set["s3"]; ok {
		t.Fatalf("KnownCredentialTypes() = %v, legacy s3 must be absent", types)
	}
}
