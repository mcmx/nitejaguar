package web

import (
	"encoding/json"

	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/internal/actions"
)

// DesignerCatalog is the visual source of truth for the designer picker
// and typed inspector form. Each action/trigger package defines its own
// schema (CatalogEntry next to the implementation); they are assembled
// next to dispatch in internal/actions, so this stays a thin wrapper.
func DesignerCatalog() []common.DesignerCatalogEntry {
	return actions.DesignerCatalog()
}

// DesignerCatalogJSON renders the catalog for the designer <script> tag.
// encoding/json escapes <, > and & so the payload cannot break out.
func DesignerCatalogJSON() string {
	data, err := json.Marshal(DesignerCatalog())
	if err != nil {
		return "[]"
	}
	return string(data)
}
