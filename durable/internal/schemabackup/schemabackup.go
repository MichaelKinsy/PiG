// Package schemabackup copies a file-backed durable SQLite store before PiG upgrades its schema (D188).
package schemabackup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	// Registers the pure-Go "sqlite" database/sql driver the durable hosts use.
	_ "modernc.org/sqlite"
)

// BeforeUpgrade copies the store at path to a new file next to it when the schema version recorded in schemaTable is at
// least 1 and below current: a store an older Pi release wrote, which the upgrade makes unreadable to that release. It
// returns the copy's path, or "" when nothing needed copying: path is empty, ":memory:" or a URI, the file is missing or
// empty, or the store has no version row or is already at current or newer. schemaTable must be a validated SQLite
// identifier. An error means the store must not be upgraded: PiG fails the open rather than lose the way back.
func BeforeUpgrade(ctx context.Context, path, schemaTable string, current int) (string, error) {
	if path == "" || path == ":memory:" || strings.HasPrefix(path, "file:") {
		return "", nil
	}
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if info.Size() == 0 {
		return "", nil
	}
	db, err := sql.Open("sqlite", dataSourceName(path))
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		return "", err
	}
	var tables int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", schemaTable).Scan(&tables); err != nil {
		return "", err
	}
	if tables == 0 {
		return "", nil
	}
	var version int64
	err = db.QueryRowContext(ctx, "SELECT version FROM "+schemaTable+" WHERE singleton = 1").Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if version < 1 || version >= int64(current) {
		return "", nil
	}
	backup, err := freeName(path, version)
	if err != nil {
		return "", err
	}
	// VACUUM INTO writes a consistent copy, including what is still in the WAL, and refuses an existing target.
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", backup); err != nil {
		return "", fmt.Errorf("copy the schema version %d store %s before upgrading it to version %d: %w", version, path, current, err)
	}
	return backup, nil
}

// freeName is <path>.schema-<version>.bak, or the first free <path>.schema-<version>-<n>.bak when an earlier copy is
// there: an earlier copy is never replaced.
func freeName(path string, version int64) (string, error) {
	for n := 1; n <= 1000; n++ {
		name := fmt.Sprintf("%s.schema-%d.bak", path, version)
		if n > 1 {
			name = fmt.Sprintf("%s.schema-%d-%d.bak", path, version, n)
		}
		_, err := os.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) {
			return name, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("no free backup name for %s: 1000 schema version %d copies are already there", path, version)
}

// dataSourceName escapes a path the driver would read query parameters from, as chord/sqlite/node does.
func dataSourceName(path string) string {
	if !strings.ContainsAny(path, "?#") {
		return path
	}
	location := url.URL{Scheme: "file", Opaque: (&url.URL{Path: filepath.ToSlash(path)}).EscapedPath()}
	return location.String()
}
