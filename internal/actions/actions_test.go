package actions

import (
	"testing"
)

func TestRequiredCredentialTypesFromCollections(t *testing.T) {
	// Core collection and unknown actions need no credential.
	for _, action := range []string{"file", "datetime", "wait", "transfer", "filechange", "unknown"} {
		if got := RequiredCredentialTypes(action); len(got) != 0 {
			t.Fatalf("RequiredCredentialTypes(%q) = %v, want empty", action, got)
		}
	}
	// AWS collection actions share the single aws type.
	for _, action := range []string{"ec2", "s3"} {
		got := RequiredCredentialTypes(action)
		if len(got) != 1 || got[0] != "aws" {
			t.Fatalf("RequiredCredentialTypes(%q) = %v, want [aws]", action, got)
		}
	}
}

func TestSupportedCredentialTypesExcludesLegacyS3(t *testing.T) {
	types := SupportedCredentialTypes()
	set := make(map[string]struct{}, len(types))
	for _, typ := range types {
		set[typ] = struct{}{}
	}
	for _, want := range []string{"generic", "token", "username_password", "ssh_key", "aws"} {
		if _, ok := set[want]; !ok {
			t.Fatalf("SupportedCredentialTypes() = %v, missing %q", types, want)
		}
	}
	if _, ok := set["s3"]; ok {
		t.Fatalf("SupportedCredentialTypes() = %v, legacy s3 must be absent", types)
	}
}
