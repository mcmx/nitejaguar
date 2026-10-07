package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"net/url"
	"strings"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	turso "turso.tech/database/tursogo"
)

// normalizeTursoDSN translates legacy sqlite-style DB_URL values into a
// tursogo DSN (plain file path, or ":memory:").
//
// Accepted inputs:
//   - "" or anything with mode=memory → ":memory:"
//   - "file:./test.db?..." (legacy mattn sqlite form) → "./test.db"
//   - plain paths ("./test.db", "/tmp/x.db") → unchanged
//
// A _busy_timeout is preserved when present, defaulting to 10000ms.
// Legacy sqlite-only params (_fk, cache, mode) are dropped.
func normalizeTursoDSN(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == ":memory:" {
		return ":memory:"
	}
	lower := strings.ToLower(raw)
	if strings.Contains(lower, "mode=memory") || strings.Contains(lower, ":memory:") {
		return ":memory:"
	}
	path := raw
	query := ""
	if i := strings.IndexByte(raw, '?'); i >= 0 {
		path = raw[:i]
		query = raw[i+1:]
	}
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "file:")
	path = strings.TrimSpace(path)
	if path == "" {
		return ":memory:"
	}
	busyTimeout := "10000"
	if query != "" {
		if vals, err := url.ParseQuery(query); err == nil {
			if v := vals.Get("_busy_timeout"); v != "" {
				busyTimeout = v
			}
		}
	}
	return path + "?_busy_timeout=" + busyTimeout
}

// foreignKeysConnector enables PRAGMA foreign_keys=ON on every pooled
// connection. tursogo DSNs carry no _fk flag (unlike mattn go-sqlite3),
// and SQLite/Turso default the pragma to off, so without this ent's
// Schema.Create init check fails and FK constraints go unenforced.
type foreignKeysConnector struct {
	driver.Connector
}

// Connect implements driver.Connector.
func (c foreignKeysConnector) Connect(ctx context.Context) (driver.Conn, error) {
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
		return nil, fmt.Errorf("turso: enable foreign keys: %w", err)
	}
	return conn, nil
}

// openTursoEnt opens a Turso (SQLite-compatible, no-CGO) database and wraps
// it as an ent SQLite-dialect driver. In-memory databases are pinned to a
// single connection since each connection owns a separate transient DB.
func openTursoEnt(rawDSN string) (*entsql.Driver, *sql.DB, error) {
	dsn := normalizeTursoDSN(rawDSN)
	connector, err := turso.NewConnector(dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("failed creating turso connector: %w", err)
	}
	db := sql.OpenDB(foreignKeysConnector{Connector: connector})
	if dsn == ":memory:" {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	}
	return entsql.OpenDB(dialect.SQLite, db), db, nil
}
