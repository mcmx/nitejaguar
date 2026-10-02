package set

import "github.com/mcmx/nitejaguar/common"

// CatalogEntry describes the set action for the visual designer picker
// and typed inspector form. Args are the new-node defaults.
func CatalogEntry() common.DesignerCatalogEntry {
	return common.DesignerCatalogEntry{
		ActionType: "action", ActionName: "set",
		Icon: "🧩", Label: "Set",
		Desc: "Shape a payload from literals and $input refs.",
		Args: map[string]any{"mode": "manual", "field.kind": "contact", "keep_only_set": false},
		Fields: []common.DesignerField{
			{Key: "mode", Label: "Mode", Type: "select", Default: "manual", Options: common.FieldOptions("manual", "json"), Help: "manual assigns fields / field.<name>; json parses the json template object."},
			{Key: "fields", Label: "Fields (JSON object)", Type: "json", Rows: 4, Placeholder: "{\"full_name\": \"$input.name\"}", Help: "Manual mode: object of assignments. field.<name> args (below in JSON) win on conflicts."},
			{Key: "json", Label: "JSON template", Type: "json", Rows: 4, Placeholder: "{\"newKey\": \"$input.name\"}", Help: "Json mode: object template parsed after $input / {{...}} resolution."},
			{Key: "keep_only_set", Label: "Keep only set fields", Type: "boolean", Default: false, Help: "True outputs only assigned fields; false merges upstream underneath."},
			{Key: "include", Label: "Include", Type: "select", Help: "Overrides keep_only_set: all merges input, none keeps only assigned.", Options: []common.DesignerFieldOption{{Value: "", Label: "(unset)"}, {Value: "all", Label: "all"}, {Value: "none", Label: "none"}}},
			{Key: "dot_notation", Label: "Dot notation", Type: "boolean", Default: true, Help: "Dotted names (address.city) build nested objects."},
			{Key: "ignore_type_errors", Label: "Ignore type errors", Type: "boolean", Default: false, Help: "Skip assignments that fail to resolve instead of erroring."},
		},
	}
}
