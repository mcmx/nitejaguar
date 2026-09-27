package common

import (
	"testing"
)

func TestEvalTemplateRefAndFilters(t *testing.T) {
	lookup := PayloadLookup(map[string]any{"name": "aDa", "file": "r.pdf", "n": float64(3)})
	cases := map[string]any{
		"$input.file":                "r.pdf",
		"$input.name | upper":        "ADA",
		"$input.name | lower":        "ada",
		"$input.name|trim|upper":     "ADA",
		"$input.missing | default:x": "x",
		`"hi" | upper`:               "HI",
	}
	for inner, want := range cases {
		got, err := EvalTemplate(inner, lookup)
		if err != nil {
			t.Fatalf("EvalTemplate(%q) error: %v", inner, err)
		}
		if got != want {
			t.Fatalf("EvalTemplate(%q) = %v (%T), want %v", inner, got, got, want)
		}
	}
	// Filter-free refs keep native types.
	if got, err := EvalTemplate("$input.n", lookup); err != nil || got != float64(3) {
		t.Fatalf("expected native number, got %v, %v", got, err)
	}
}

func TestEvalTemplateErrors(t *testing.T) {
	lookup := PayloadLookup(map[string]any{"a": "1"})
	for _, inner := range []string{
		"",
		"nope",
		"$result.a",
		"$input.missing",
		"$input.a | frobnicate",
		"$input.a | upper:extra",
		"$input.a | replace:onlyone",
	} {
		if _, err := EvalTemplate(inner, lookup); err == nil {
			t.Fatalf("EvalTemplate(%q) expected error, got nil", inner)
		}
	}
	// No input available.
	if _, err := EvalTemplate("$input.a", InputLookup(nil)); err == nil {
		t.Fatalf("expected no-input error")
	}
	// ...unless a default covers it.
	if got, err := EvalTemplate("$input.a | default:d", InputLookup(nil)); err != nil || got != "d" {
		t.Fatalf("expected default fallback, got %v, %v", got, err)
	}
}

func TestExpandTemplatesConcat(t *testing.T) {
	lookup := PayloadLookup(map[string]any{"file": "r.pdf", "id": float64(23423532)})
	got, err := ExpandTemplates("{{ $input.file }}.bkp ({{ $input.id }})", lookup)
	if err != nil {
		t.Fatal(err)
	}
	if got != "r.pdf.bkp (23423532)" {
		t.Fatalf("unexpected expansion: %q", got)
	}
	if _, err := ExpandTemplates("{{ $input.a", lookup); err == nil {
		t.Fatalf("expected unclosed error")
	}
}

func TestSingleTemplateDetection(t *testing.T) {
	if _, ok := SingleTemplate("{{ $input.a }}"); !ok {
		t.Fatalf("expected single template match")
	}
	if _, ok := SingleTemplate("x {{ $input.a }}"); ok {
		t.Fatalf("embedded must not count as single")
	}
	if _, ok := SingleTemplate("{{ $input.a }} {{ $input.b }}"); ok {
		t.Fatalf("two templates must not count as single")
	}
}

func TestExpandJSONTemplatesModes(t *testing.T) {
	lookup := PayloadLookup(map[string]any{"file": "r.pdf", "n": float64(3), "name": "ada"})
	got, err := ExpandJSONTemplates(`{"f": "{{ $input.file }}.bkp", "n": {{ $input.n }}, "u": "{{ $input.name | upper }}"}`, lookup)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"f": "r.pdf.bkp", "n": 3, "u": "ADA"}`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSplitTemplatesAlternation(t *testing.T) {
	segs := SplitTemplates("$input.now{{ext}} and {{ $input.tag | upper }}!")
	want := []string{"$input.now", "ext", " and ", "$input.tag | upper", "!"}
	if len(segs) != len(want) {
		t.Fatalf("segs = %q, want %q", segs, want)
	}
	for i := range want {
		if segs[i] != want[i] {
			t.Fatalf("segs = %q, want %q", segs, want)
		}
	}
	if got := SplitTemplates("plain"); len(got) != 1 || got[0] != "plain" {
		t.Fatalf("plain string must yield one literal segment, got %q", got)
	}
	if HasTemplate("plain") || !HasTemplate("{{ x }}") {
		t.Fatalf("HasTemplate mismatch")
	}
}
