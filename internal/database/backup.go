package database

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// dbFilePath resolves the on-disk file for a DB_URL ("" for in-memory).
func dbFilePath(rawURL string) string {
	dsn := normalizeTursoDSN(rawURL)
	if dsn == ":memory:" {
		return ""
	}
	if i := strings.IndexByte(dsn, '?'); i >= 0 {
		dsn = dsn[:i]
	}
	return dsn
}

// DatabaseFilePath returns the on-disk SQLite/Turso file backing this
// service, or "" when the database is in-memory (nothing to back up).
func (s *service) DatabaseFilePath() string {
	return s.path
}

// Backup writes a transaction-consistent snapshot of the database to dest
// via SQLite's VACUUM INTO, which is safe to run against a live server.
// The destination file must not exist (it is replaced), and missing parent
// directories are created. In-memory databases refuse with an error —
// there is no file to snapshot, so persist to disk first (set DB_URL).
func (s *service) Backup(dest string) error {
	if dest == "" {
		return fmt.Errorf("backup destination is empty")
	}
	if s.DatabaseFilePath() == "" {
		return fmt.Errorf("cannot back up in-memory database: set DB_URL to a file path first")
	}
	if dir := filepath.Dir(dest); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("failed creating backup directory: %w", err)
		}
	}
	// VACUUM INTO refuses to overwrite, so clear a previous snapshot first.
	if err := os.Remove(dest); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed clearing previous backup: %w", err)
	}
	quoted := "'" + strings.ReplaceAll(dest, "'", "''") + "'"
	if _, err := s.db.Exec("VACUUM INTO " + quoted); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}
	return nil
}

// VerifyBackup opens dest read-only and reports the number of workflow
// rows it contains, proving the snapshot is a usable database.
func VerifyBackup(dest string) (int, error) {
	drv, db, err := openTursoEnt(dest)
	if err != nil {
		return 0, fmt.Errorf("failed opening backup: %w", err)
	}
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM workflows").Scan(&n); err != nil {
		return 0, fmt.Errorf("backup is not a usable database: %w", err)
	}
	_ = drv
	return n, nil
}

// FailoverReopen closes this service and forgets the shared instance so a
// follow-up New() reopens the same DB file from disk — the unit-testable
// core of a failover: pending assignments, results, and workflow
// definitions must survive the restart.
func FailoverReopen(s Service) (Service, error) {
	if err := s.Close(); err != nil {
		return nil, fmt.Errorf("failed closing database for failover: %w", err)
	}
	dbInstance = nil
	return New()
}
