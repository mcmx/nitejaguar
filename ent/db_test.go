package ent

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	turso "turso.tech/database/tursogo"
)

// fkConnector enables PRAGMA foreign_keys=ON on every pooled connection.
// tursogo carries no _fk DSN flag, and ent's SQLite migrator refuses to run
// with the pragma off.
type fkConnector struct {
	driver.Connector
}

// Connect implements driver.Connector.
func (c fkConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	execer, ok := conn.(driver.ExecerContext)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("turso: connection does not support exec")
	}
	if _, err := execer.ExecContext(ctx, "PRAGMA foreign_keys=ON", nil); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func Test_Ent(t *testing.T) {
	// Create an ent.Client with an in-memory Turso database
	// (SQLite-compatible, no CGO). Turso reads existing SQLite files
	// as-is, so this is also the local migration path.
	connector, err := turso.NewConnector(":memory:?_busy_timeout=10000")
	if err != nil {
		t.Fatalf("failed creating turso connector: %v", err)
	}
	sqldb := sql.OpenDB(fkConnector{Connector: connector})
	sqldb.SetMaxOpenConns(1)
	client := NewClient(Driver(entsql.OpenDB(dialect.SQLite, sqldb)))
	defer func() {
		if err := client.Close(); err != nil {
			t.Errorf("Error closing database client: %v", err)
		}
	}()
	ctx := context.Background()
	// Run the automatic migration tool to create all schema resources.
	if err := client.Schema.Create(ctx); err != nil {
		t.Fatalf("failed creating schema resources, %v", err)
	}
	_, err = client.Workflow.
		Create().
		SetID("wf_1").
		SetJSONDefinition("{}").
		Save(ctx)
	if err != nil {
		t.Fatalf("failed creating a workflow: %v", err)
	}
	// fmt.Println(w1)

	// Output:
	// Workflow(id=1)

}
