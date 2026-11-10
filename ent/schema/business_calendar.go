package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// BusinessCalendar stores tenant-owned weekday allowlists and excluded dates
// referenced by cron triggers.
type BusinessCalendar struct {
	ent.Schema
}

func (BusinessCalendar) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			Immutable().
			Unique().
			NotEmpty(),
		field.String("tenant_id").
			Default("default"),
		field.String("name").
			NotEmpty(),
		field.String("weekdays_json").
			Default("[]"),
		field.String("excluded_dates_json").
			Default("[]"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

func (BusinessCalendar) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "name").Unique(),
	}
}
