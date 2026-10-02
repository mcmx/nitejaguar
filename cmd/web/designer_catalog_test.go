package web

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/actions"
)

// TestDesignerCatalogCoversActions guards the typed inspector: the web
// catalog must expose every entry assembled in internal/actions, each
// with typed fields (integer -> number input, finite lists ->
// select/multiselect, booleans -> checkbox), and the JSON payload must
// survive a round-trip to the browser script tag.
func TestDesignerCatalogCoversActions(t *testing.T) {
	cat := DesignerCatalog()
	want := actions.DesignerCatalog()
	if len(cat) != len(want) {
		t.Fatalf("web catalog entries = %d, actions catalog = %d", len(cat), len(want))
	}
	byName := map[string]common.DesignerCatalogEntry{}
	for _, e := range cat {
		key := e.ActionType + ":" + e.ActionName
		if _, dup := byName[key]; dup {
			t.Errorf("catalog has duplicate entry %q", key)
		}
		byName[key] = e
	}
	for _, e := range want {
		key := e.ActionType + ":" + e.ActionName
		got, ok := byName[key]
		if !ok {
			t.Errorf("web catalog missing %q", key)
			continue
		}
		if len(got.Fields) != len(e.Fields) {
			t.Errorf("web catalog %q fields = %d, actions catalog = %d", key, len(got.Fields), len(e.Fields))
		}
	}
	for key, e := range byName {
		if len(e.Fields) == 0 {
			t.Errorf("catalog %q has no typed fields", key)
		}
		for _, f := range e.Fields {
			switch f.Type {
			case "string", "number", "integer", "boolean", "select", "multiselect", "textarea", "json":
			default:
				t.Errorf("catalog %q field %q has unknown type %q", key, f.Key, f.Type)
			}
			if f.Type == "select" || f.Type == "multiselect" {
				if len(f.Options) == 0 {
					t.Errorf("catalog %q field %q is %s with no options", key, f.Key, f.Type)
				}
			}
			if f.Key == "" || f.Label == "" {
				t.Errorf("catalog %q has a field with empty key/label: %+v", key, f)
			}
		}
	}

	// Spot-check the type mapping the designer relies on.
	checks := map[string]string{
		"trigger:filechange:debounce_ms": "integer",
		"trigger:cron:run_on_start":      "boolean",
		"trigger:webhook:method":         "select",
		"action:file:action":             "select",
		"action:set:mode":                "select",
		"action:set:keep_only_set":       "boolean",
	}
	for key, wantType := range checks {
		parts := strings.Split(key, ":")
		e := byName[parts[0]+":"+parts[1]]
		found := ""
		for _, f := range e.Fields {
			if f.Key == parts[2] {
				found = f.Type
			}
		}
		if found != wantType {
			t.Errorf("field %q type = %q, want %q", key, found, wantType)
		}
	}

	// The browser payload must be valid JSON without a literal </script>.
	raw := DesignerCatalogJSON()
	var decoded []common.DesignerCatalogEntry
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("catalog JSON invalid: %v", err)
	}
	if len(decoded) != len(cat) {
		t.Errorf("catalog JSON entries = %d, want %d", len(decoded), len(cat))
	}
	if strings.Contains(strings.ToLower(raw), "</script>") {
		t.Errorf("catalog JSON contains a literal </script>")
	}
}
