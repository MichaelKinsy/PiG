// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// seededContext is the full-walk transcript of size history turns as the scripted model sees it, and the context of the
// last seeding call (the one that answered the last history turn).
func seededContext(size int) (all, lastCall []seen) {
	for _, h := range historyTurns(0, size) {
		all = append(all, seen{role: "user", text: h.text})
		for {
			step := next(all)
			if step.answer != "" {
				lastCall = append([]seen(nil), all...)
				all = append(all, seen{role: "assistant", text: step.answer})
				break
			}
			all = append(all, seen{role: "assistant", calls: []int{step.call}}, seen{role: "tool", text: payload(step.call, 0)})
		}
	}
	return all, lastCall
}

// A timing run checks the published fixture it starts from with workload.mjs SEED_FINGERPRINTS, read off the first
// measured call: that context is the last seeding call's context, its answer and the first measured input.
func TestTimingReadsTheFixtureFingerprintOffTheFirstMeasuredCall(t *testing.T) {
	for _, size := range []int{50, 250} {
		all, lastCall := seededContext(size)
		if got := fingerprint(lastCall); got != seedFingerprints[size] {
			t.Fatalf("simulated seed %d = %s, want the published %s", size, got, seedFingerprints[size])
		}
		first := slices.Concat(all, []seen{{role: "user", text: "turn m0 tools=8"}})
		got, err := fixtureFingerprint(first)
		if err != nil {
			t.Fatal(err)
		}
		if err := checkFixture(size, got); err != nil {
			t.Fatal(err)
		}
		if err := checkFixture(size, fingerprint(first)); err == nil {
			t.Fatalf("a wrong fixture of %d turns passed the check", size)
		}
	}
	if _, err := fixtureFingerprint([]seen{{role: "user", text: "turn m0 tools=8"}}); err == nil {
		t.Fatal("an unseeded store passed as a fixture")
	}
}

// storeStats is durable-bench's /stats: databaseSize (page_count * page_size) and the row count of each table except
// SQLite's and Cloudflare's own.
func TestStoreStatsCountsTheBenchTablesAndThePages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE entries (id INTEGER PRIMARY KEY, body TEXT)`, `CREATE TABLE "odd""name" (x)`, `CREATE TABLE _cf_KV (k)`,
		`INSERT INTO entries (body) VALUES ('a'), ('b'), ('c')`, `INSERT INTO "odd""name" VALUES (1)`, `INSERT INTO _cf_KV VALUES (1)`,
		`CREATE TABLE seq (id INTEGER PRIMARY KEY AUTOINCREMENT)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	var pages, size int64
	if err := db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("PRAGMA page_size").Scan(&size); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	bytes, tables, err := storeStats(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes != pages*size || bytes == 0 {
		t.Fatalf("bytes = %d, want %d pages of %d", bytes, pages, size)
	}
	var got []string
	for name, n := range tables {
		got = append(got, name+"="+string(rune('0'+n)))
	}
	if len(tables) != 3 || tables["entries"] != 3 || tables[`odd"name`] != 1 || tables["seq"] != 0 {
		t.Fatalf("tables = %s", strings.Join(got, " "))
	}
}
