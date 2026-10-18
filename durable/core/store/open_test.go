// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package store_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/store"
)

func TestFreshOpenInitialisesSchema(t *testing.T) {
	h := newHost(t, "")
	_, live, err := h.openStore(store.Options{EmptyNew: true})
	if err != nil {
		t.Fatal(err)
	}
	if live.NextID != 2 || live.NextSeq != 1 || len(live.Tasks) != 0 {
		t.Fatalf("live = %+v", live)
	}
	var version int
	if err := h.db.QueryRow("SELECT version FROM durable_schema").Scan(&version); err != nil || version != store.SupportedSchemaVersion {
		t.Fatalf("schema version %d %v", version, err)
	}
	var next string
	if err := h.db.QueryRow("SELECT next_id FROM durable_metadata").Scan(&next); err != nil || next != "2" {
		t.Fatalf("next_id %q %v", next, err)
	}
	// A second cold open reads the metadata, not the schema again.
	_, live, err = h.openStore(store.Options{EmptyNew: true})
	if err != nil || live.NextID != 2 || live.NextSeq != 1 {
		t.Fatalf("reopen: %+v %v", live, err)
	}
}

func TestOpenRefusesNewerSchema(t *testing.T) {
	h := newHost(t, "")
	if _, _, err := h.openStore(store.Options{EmptyNew: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Exec("UPDATE durable_schema SET version = ?", store.SupportedSchemaVersion+1); err != nil {
		t.Fatal(err)
	}
	_, _, err := h.openStore(store.Options{EmptyNew: true})
	var newer store.SchemaNewerError
	if !errors.As(err, &newer) || newer.Version != store.SupportedSchemaVersion+1 {
		t.Fatalf("err = %v", err)
	}
	if want := "SQLite schema version 3 is newer than supported version 2"; err.Error() != want {
		t.Fatalf("message %q, want %q", err.Error(), want)
	}
}

func TestOpenRejectsEmptyDatabaseWithoutEmptyNew(t *testing.T) {
	h := newHost(t, "")
	if _, _, err := h.openStore(store.Options{}); err == nil {
		t.Fatal("an empty database opened without EmptyNew")
	}
}

// Cold open follows applySqliteMigrations (packages/durable/src/storage/sqlite/migrations.ts): the schema table is created
// if absent and its version read, so the schema is found by identifier. Pi runs that prelude and the migration in one
// transaction; the core commits the prelude (no row change) before its version read, and the seed of the version row
// heads the migration commit, so the rows of a new store still change in one transaction, as Pi's commit 0. A reopen
// commits the prelude only.
func TestColdOpenCommitsPisSchemaPreludeThenOneMigration(t *testing.T) {
	isPrelude := func(c []string) bool {
		return len(c) == 1 && strings.HasPrefix(c[0], "CREATE TABLE IF NOT EXISTS durable_schema")
	}
	h := newHost(t, "")
	if _, _, err := h.openStore(store.Options{EmptyNew: true}); err != nil {
		t.Fatal(err)
	}
	if len(h.applied) != 2 || !isPrelude(h.applied[0]) {
		t.Fatalf("a new store's commits: %q", h.applied)
	}
	migration := h.applied[1]
	if !strings.HasPrefix(migration[0], "INSERT OR IGNORE INTO durable_schema") || !strings.HasPrefix(migration[len(migration)-1], "UPDATE durable_schema SET version") || len(migration) < 4 {
		t.Fatalf("the migration commit: %q", migration)
	}
	h.applied = nil
	if _, _, err := h.openStore(store.Options{EmptyNew: true}); err != nil {
		t.Fatal(err)
	}
	if len(h.applied) != 1 || !isPrelude(h.applied[0]) {
		t.Fatalf("a reopen's commits: %q", h.applied)
	}
}

// A schema table without its row (a store left between the prelude commit and the migration commit) reads as version 0
// and is migrated, as applySqliteMigrations' INSERT OR IGNORE then SELECT version does.
func TestColdOpenMigratesASchemaTableWithoutItsRow(t *testing.T) {
	h := newHost(t, "")
	if _, err := h.db.Exec("CREATE TABLE durable_schema (singleton INTEGER PRIMARY KEY CHECK (singleton = 1), version INTEGER NOT NULL CHECK (version >= 0)) STRICT"); err != nil {
		t.Fatal(err)
	}
	_, live, err := h.openStore(store.Options{EmptyNew: true})
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := h.db.QueryRow("SELECT version FROM durable_schema").Scan(&version); err != nil || version != store.SupportedSchemaVersion || live.NextID != 2 || live.NextSeq != 1 {
		t.Fatalf("version %d %v, live %+v", version, err, live)
	}
}
