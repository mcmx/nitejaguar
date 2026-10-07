package database

import (
	"context"
	"testing"

	"github.com/mcmx/nitejaguar/ent"
)

func TestNormalizeTursoDSN(t *testing.T) {
	cases := map[string]string{
		"":         ":memory:",
		":memory:": ":memory:",
		"file:ent.db?mode=memory&cache=shared&_fk=1":           ":memory:",
		"file:test_save_result?mode=memory&cache=shared&_fk=1": ":memory:",
		"file:./test.db?_fk=1&cache=shared":                    "./test.db?_busy_timeout=10000",
		"file:./test.db?_fk=1&cache=shared&_busy_timeout=5000": "./test.db?_busy_timeout=5000",
		"./test.db":      "./test.db?_busy_timeout=10000",
		"./data/nite.db": "./data/nite.db?_busy_timeout=10000",
		"/tmp/nite.db":   "/tmp/nite.db?_busy_timeout=10000",
	}
	for in, want := range cases {
		if got := normalizeTursoDSN(in); got != want {
			t.Errorf("normalizeTursoDSN(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTursoMemoryRoundTrip(t *testing.T) {
	drv, db, err := openTursoEnt(":memory:")
	if err != nil {
		t.Fatalf("open turso memory: %v", err)
	}
	defer func() { _ = db.Close() }()
	client := ent.NewClient(ent.Driver(drv))
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	if err := client.Schema.Create(ctx); err != nil {
		t.Fatalf("schema create: %v", err)
	}
	w, err := client.Workflow.Create().SetID("wf_tursotest1").SetJSONDefinition(`{}`).Save(ctx)
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	if w.ID != "wf_tursotest1" {
		t.Fatalf("unexpected id %q", w.ID)
	}
	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("pragma check: %v", err)
	}
	if fk != 1 {
		t.Fatalf("foreign_keys pragma = %d, want 1", fk)
	}
}
