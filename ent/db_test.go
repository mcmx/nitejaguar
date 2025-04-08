package ent

import (
	"context"
	"fmt"
	"log"
	"testing"

	// "github.com/mcmx/nitejaguar/ent"

	"entgo.io/ent/dialect"
	_ "github.com/mattn/go-sqlite3"
)

func Test_Ent(t *testing.T) {
	// Create an ent.Client with in-memory SQLite database.
	client, err := Open(dialect.SQLite, "file:ent?mode=memory&cache=shared&_fk=1")
	if err != nil {
		log.Fatalf("failed opening connection to sqlite: %v", err)
	}
	defer client.Close()
	ctx := context.Background()
	// Run the automatic migration tool to create all schema resources.
	if err := client.Schema.Create(ctx); err != nil {
		log.Fatalf("failed creating schema resources, %v", err)
	}
	w1, err := client.Workflow.
		Create().
		SetID("wf_1").
		SetJSONDefinition("{}").
		Save(ctx)
	if err != nil {
		log.Fatalf("failed creating a workflow: %v", err)
	}
	fmt.Println(w1)

	// Output:
	// Workflow(id=1)

}
