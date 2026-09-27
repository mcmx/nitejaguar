package common

import (
	"testing"
)

func TestResolveRefPathBasics(t *testing.T) {
	payload := map[string]any{
		"name": "ada",
		"user": map[string]any{"id": float64(7)},
		"tags": []any{"a", "b"},
	}
	for ref, want := range map[string]any{
		"$input.name":    "ada",
		"$input.user.id": float64(7),
		"$input.tags[0]": "a",
		"$input.tags[1]": "b",
	} {
		got, err := ResolveRefPath(payload, ref)
		if err != nil {
			t.Fatalf("ResolveRefPath(%q) error: %v", ref, err)
		}
		if got != want {
			t.Fatalf("ResolveRefPath(%q) = %v, want %v", ref, got, want)
		}
	}
}

func TestResolveRefPathStructTags(t *testing.T) {
	type inner struct {
		Name string `json:"name"`
		Skip string
	}
	got, err := ResolveRefPath(map[string]any{"in": inner{Name: "x"}}, "$input.in.name")
	if err != nil || got != "x" {
		t.Fatalf("expected json-tag lookup, got %v, %v", got, err)
	}
}

func TestResolveRefPathErrors(t *testing.T) {
	payload := map[string]any{"a": "1", "list": []any{"x"}}
	for _, ref := range []string{
		"$result.a",
		"$input.",
		"$input.a..b",
		"$input.missing",
		"$input.list[5]",
		"$input.list[x]",
		"$input.a.b",
	} {
		if _, err := ResolveRefPath(payload, ref); err == nil {
			t.Fatalf("ResolveRefPath(%q) expected error, got nil", ref)
		}
	}
	if _, err := ResolveRefPath(payload, "$input"); err == nil {
		t.Fatalf("expected error for missing $input. prefix")
	}
}
