package web

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestDesignerCatalogCoversActions guards the typed inspector: every
// action_name the engine can run must have a catalog entry with typed
// fields (integer -> number input, finite lists -> select/multiselect,
// booleans -> checkbox), and the JSON payload must survive a round-trip
// to the browser script tag.
func TestDesignerCatalogCoversActions(t *testing.T) {
	cat := DesignerCatalog()
	byName := map[string]DesignerCatalogEntry{}
	for _, e := range cat {
		byName[e.ActionType+":"+e.ActionName] = e
	}
	for _, want := range []string{
		"trigger:filechange", "trigger:cron", "trigger:webhook",
		"action:file", "action:datetime", "action:wait",
		"action:transfer", "action:set",
	} {
		e, ok := byName[want]
		if !ok {
			t.Errorf("catalog missing %q", want)
			continue
		}
		if len(e.Fields) == 0 {
			t.Errorf("catalog %q has no typed fields", want)
		}
		for _, f := range e.Fields {
			switch f.Type {
			case "string", "number", "integer", "boolean", "select", "multiselect", "textarea", "json":
			default:
				t.Errorf("catalog %q field %q has unknown type %q", want, f.Key, f.Type)
			}
			if f.Type == "select" || f.Type == "multiselect" {
				if len(f.Options) == 0 {
					t.Errorf("catalog %q field %q is %s with no options", want, f.Key, f.Type)
				}
			}
			if f.Key == "" || f.Label == "" {
				t.Errorf("catalog %q has a field with empty key/label: %+v", want, f)
			}
		}
	}

	// Spot-check the type mapping the issue asks for.
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
	var decoded []DesignerCatalogEntry
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
