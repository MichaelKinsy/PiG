// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package store_test

import (
	"database/sql"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/store"
)

// piSchemaNames are the names the Cloudflare Agents SDK's PiHarness (agents 0.28.0, harness/pi) renames: durable_schema
// and every table and index Pi's SQLITE_MIGRATIONS create (packages/durable/src/storage/sqlite/migrations.ts, 1.0.4).
// The first nine are tables.
var piSchemaNames = []string{
	"durable_schema", "durable_metadata", "record_ids", "conversations", "entries", "tasks", "submissions", "documents", "document_revisions",
	"conversations_by_owner_conversation", "conversations_by_owner_task", "entries_by_conversation", "entry_heads_by_conversation",
	"tasks_by_status", "tasks_by_conversation", "tasks_by_kind", "tasks_by_abort_requested", "tasks_by_background",
	"submissions_by_request", "submissions_by_conversation", "submissions_by_status",
	"documents_by_address", "documents_by_scope", "documents_by_scope_kind", "document_revisions_by_kind",
}

var (
	//portlint:allow regexfold the pattern folds only ASCII SQL keywords, where JS and Go folding agree
	createName = regexp.MustCompile(`(?i)\bCREATE\s+(?:UNIQUE\s+)?(?:TABLE|INDEX)\s+(?:IF\s+NOT\s+EXISTS\s+)?([A-Za-z_][A-Za-z0-9_]*)`)
	sqlLiteral = regexp.MustCompile(`'(?:[^']|'')*'`)
)

// renameTables is the adapter's rewrite: each name on a word boundary, outside '...' string literals, gets the prefix.
func renameTables(prefix string) func(string) string {
	pattern := regexp.MustCompile(`\b(` + strings.Join(piSchemaNames, "|") + `)\b`)
	outside := func(s string) string { return pattern.ReplaceAllString(s, prefix+"$1") }
	return func(text string) string {
		var b strings.Builder
		at := 0
		for _, lit := range sqlLiteral.FindAllStringIndex(text, -1) {
			b.WriteString(outside(text[at:lit[0]]))
			b.WriteString(text[lit[0]:lit[1]])
			at = lit[1]
		}
		b.WriteString(outside(text[at:]))
		return b.String()
	}
}

// The adapter's name list is Pi's; the core creates no other table or index under a Pi name. pig_sidecar is the core's
// own table and is not in SQLITE_MIGRATIONS, so the adapter leaves it unrenamed.
func TestRenamedNamesAreTheCoresSchema(t *testing.T) {
	var got []string
	for _, text := range store.SQL() {
		for _, m := range createName.FindAllStringSubmatch(text, -1) {
			// tasks_v2 is migration 2's temporary table: Pi renames it to tasks, and it never outlives the migration.
			if m[1] != "pig_sidecar" && m[1] != "tasks_v2" && !slices.Contains(got, m[1]) {
				got = append(got, m[1])
			}
		}
	}
	want := slices.Clone(piSchemaNames)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("the core creates %v, Pi's migrations %v", got, want)
	}
}

// A store whose tables an adapter renames in every SQL text reopens and continues, as Pi's does: Pi finds its schema by
// identifier (applySqliteMigrations: CREATE TABLE IF NOT EXISTS durable_schema, INSERT OR IGNORE, SELECT version), and so
// must the core. A probe that names the tables as data (sqlite_master ... name IN ('durable_schema')) finds nothing
// under the adapter, re-runs migration 1 on the reopen and fails: "table pi_durable_metadata already exists".
func TestReopenThroughATableRenamingAdapter(t *testing.T) {
	script := randomScript(41, 40)
	ref, _, want := runScript(t, script)

	first, rest := script.split(20)
	rename := renameTables("pi_")
	h := newHost(t, "")
	h.rewrite = rename
	st, _, err := h.openStore(store.Options{EmptyNew: true})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	record := func(h *testHost, st *store.Store, part *commitScript) {
		for _, c := range part.commits {
			seq, err := applyCommit(t, h, st, c)
			if err != nil {
				got = append(got, "error="+err.Error())
			} else {
				got = append(got, fmt.Sprintf("seq=%d", seq))
			}
		}
	}
	record(h, st, first)
	h.close()

	again := newHost(t, h.path)
	again.rewrite = rename
	convs, tasks := first.scopesOf()
	st = coldOpenOn(t, again, store.Options{EmptyNew: true}, convs, tasks)
	if again.commits != 1 {
		t.Fatalf("the reopen applied %d commits, want only the schema prelude (CREATE TABLE IF NOT EXISTS durable_schema)", again.commits)
	}
	record(again, st, rest)
	if !slices.Equal(got, want) {
		t.Fatalf("outcomes through the adapter %v, without it %v", got, want)
	}
	again.close()

	last := newHost(t, h.path)
	last.rewrite = rename
	_, live, err := last.openStore(store.Options{EmptyNew: true})
	if err != nil {
		t.Fatal(err)
	}
	var nextID string
	var nextSeq int64
	if err := last.db.QueryRow("SELECT next_id, next_seq FROM pi_durable_metadata").Scan(&nextID, &nextSeq); err != nil {
		t.Fatal(err)
	}
	var refID string
	var refSeq int64
	if err := ref.db.QueryRow("SELECT next_id, next_seq FROM durable_metadata").Scan(&refID, &refSeq); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(live.NextID) != nextID || live.NextSeq != nextSeq || nextID != refID || nextSeq != refSeq || nextSeq <= 1 {
		t.Fatalf("second reopen: live %d/%d, stored %s/%d, without the adapter %s/%d", live.NextID, live.NextSeq, nextID, nextSeq, refID, refSeq)
	}

	// The renamed file holds Pi's tables under the prefix only, with the rows the unrenamed store holds.
	var tables []string
	rows, err := last.db.Query("SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, n)
	}
	_ = rows.Close()
	for _, name := range piSchemaNames[:9] {
		if slices.Contains(tables, name) || !slices.Contains(tables, "pi_"+name) {
			t.Fatalf("tables %v: want pi_%s and no %s", tables, name, name)
		}
		if a, b := dumpTable(t, ref.db, name), dumpTable(t, last.db, "pi_"+name); a != b {
			t.Fatalf("%s differs through the adapter:\n%s\nwithout it:\n%s", name, b, a)
		}
	}
}

// dumpTable renders every row of a table in rowid order.
func dumpTable(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	rows, err := db.Query("SELECT * FROM " + name + " ORDER BY rowid") //nolint:gosec // name is one of piSchemaNames, with or without the fixed prefix
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	cols, _ := rows.Columns()
	var b strings.Builder
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for _, v := range vals {
			if s, ok := v.([]byte); ok {
				v = string(s)
			}
			fmt.Fprintf(&b, "%v|", v)
		}
		b.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}
