package common

// DesignerFieldOption is one choice in a select/multiselect designer field.
type DesignerFieldOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// DesignerField describes one typed argument in the designer inspector.
// Type is one of: string, number, integer, boolean, select,
// multiselect, textarea, json.
type DesignerField struct {
	Key         string                `json:"key"`
	Label       string                `json:"label"`
	Type        string                `json:"type"`
	Default     any                   `json:"default,omitempty"`
	Placeholder string                `json:"placeholder,omitempty"`
	Help        string                `json:"help,omitempty"`
	Required    bool                  `json:"required,omitempty"`
	Options     []DesignerFieldOption `json:"options,omitempty"`
	Rows        int                   `json:"rows,omitempty"`
}

// DesignerCatalogEntry is one designer picker card plus its typed arg
// schema. Args hold the defaults for a new node; Fields drive the
// per-type HTML controls (number input for integers, checkbox for
// booleans, combo for finite option lists, textareas for JSON blobs).
// Every action/trigger package defines its own entry via CatalogEntry
// next to its implementation, so adding a node never requires editing
// a central schema file — registration happens alongside dispatch in
// internal/actions (AddAction/AddTrigger).
type DesignerCatalogEntry struct {
	ActionType string          `json:"action_type"`
	ActionName string          `json:"action_name"`
	Icon       string          `json:"icon"`
	Label      string          `json:"label"`
	Desc       string          `json:"desc"`
	Args       map[string]any  `json:"args"`
	Fields     []DesignerField `json:"fields"`
}

// FieldOptions builds same-labelled options from plain values.
func FieldOptions(vals ...string) []DesignerFieldOption {
	out := make([]DesignerFieldOption, 0, len(vals))
	for _, v := range vals {
		out = append(out, DesignerFieldOption{Value: v, Label: v})
	}
	return out
}
