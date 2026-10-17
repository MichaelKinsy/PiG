// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
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

func TestResidentSetIsReadInMiB(t *testing.T) {
	mb, err := residentMB()
	if err != nil {
		t.Fatal(err)
	}
	if mb < 1 || mb > 1<<16 {
		t.Fatalf("rss = %g MiB", mb)
	}
}

// The production path: a store seeded by the gate's trace mode, then one timing run on it on the real clock, appends one
// run.ts line with the fixture's own seed fingerprint and the store's statistics.
func TestATimingRunAppendsOneResultLineForItsFixture(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "s.sqlite")
	synctest.Test(t, func(t *testing.T) {
		if err := run(t.Context(), options{db: db, trace: filepath.Join(dir, "seed.trace"), variant: "full", label: "m", seed: 4, tools: 1}); err != nil {
			t.Fatal(err)
		}
	})
	results := filepath.Join(dir, "results.jsonl")
	o := options{db: db, timing: results, target: "pig-native", version: "abc1234", size: 4, sample: 2, turns: 2, tools: 1, variant: "full", label: "m"}
	if err := run(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(results)
	if err != nil {
		t.Fatal(err)
	}
	var line timingLine
	if err := json.Unmarshal(raw, &line); err != nil || strings.Count(string(raw), "\n") != 1 {
		t.Fatalf("results = %q (%v)", raw, err)
	}
	_, lastCall := seededContext(4)
	if line.Target != "pig-native" || line.Version != "abc1234" || line.Turns != 4 || line.Sample != 2 || len(line.Turn) != 2 || line.Open <= 0 || line.Turn[0] <= 0 {
		t.Fatalf("line = %+v", line)
	}
	if line.Fingerprint != fingerprint(lastCall) {
		t.Fatalf("fingerprint = %s, want the seed's %s", line.Fingerprint, fingerprint(lastCall))
	}
	if line.Bytes <= 0 || line.Tables["entries"] == 0 || line.RSS <= 0 {
		t.Fatalf("stats = %d bytes, tables %v, rss %g", line.Bytes, line.Tables, line.RSS)
	}
	if err := run(t.Context(), options{db: db, timing: results, version: "abc1234", size: 4, turns: 1, trace: "x"}); err == nil {
		t.Fatal("a timing run took a trace")
	}
}

// ABI section 6, effect 3: a model request names the committed context it reads (a range segment: conversation, head marker
// entry, tail entry) and does not list its messages, so its size does not grow with the transcript and the core keeps no
// record of what the host holds. A later request of a conversation names a tail at or above the earlier one's.
func TestModelRequestsNameTheirContext(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "s.sqlite")
	synctest.Test(t, func(t *testing.T) {
		if err := run(t.Context(), options{db: db, trace: filepath.Join(dir, "seed.trace"), variant: "full", label: "m", seed: 4, tools: 2}); err != nil {
			t.Fatal(err)
		}
	})
	type sent struct {
		id    uint32
		conv  uint32
		tail  uint32
		bytes int
	}
	var requests []sent
	o := options{db: db, timing: filepath.Join(dir, "results.jsonl"), target: "pig-native", version: "abc1234", size: 4, turns: 2, tools: 2, variant: "full", label: "m"}
	o.observe = func(e abi.Effect) {
		if e.Kind != abi.EffectModelContext {
			return
		}
		text, ranges := recipeJSON(e.Payload)
		var p struct {
			ConversationID *int64          `json:"conversationId"`
			Extends        *uint32         `json:"extends"`
			Context        json.RawMessage `json:"context"`
		}
		if err := json.Unmarshal(text, &p); err != nil {
			t.Error(err)
			return
		}
		if p.ConversationID == nil {
			t.Errorf("effect %d names no conversation", e.ID)
		}
		if p.Extends != nil {
			t.Errorf("effect %d: the delta form is gone: %s", e.ID, text)
		}
		if len(ranges) != 1 || string(p.Context) != `{"messages":[null]}` {
			t.Errorf("effect %d: want one range segment as the whole of context.messages, got %d in %s", e.ID, len(ranges), p.Context)
			return
		}
		requests = append(requests, sent{e.ID, ranges[0][0], ranges[0][2], len(e.Payload)})
	}
	if err := run(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	if len(requests) < 6 {
		t.Fatalf("2 turns of 2 lookups make at least 6 model requests, got %d", len(requests))
	}
	newest := map[uint32]uint32{}
	for _, r := range requests {
		if r.tail < newest[r.conv] {
			t.Errorf("effect %d names tail %d of conversation %d after tail %d", r.id, r.tail, r.conv, newest[r.conv])
		}
		newest[r.conv] = r.tail
		if r.bytes > 2048 {
			t.Errorf("effect %d is %d bytes: a request that names its context does not carry the transcript", r.id, r.bytes)
		}
	}
}

// recipeJSON is the text of a model_context recipe (ABI section 10.2) with each range replaced by null: the
// framing the request's shape is checked on, without a store to read the messages from. It also returns the ranges (conv,
// head, tail).
func recipeJSON(recipe []byte) ([]byte, [][3]uint32) {
	n := binary.LittleEndian.Uint32(recipe)
	rest := recipe[4:]
	var out []byte
	var ranges [][3]uint32
	for range n {
		kind := rest[0]
		rest = rest[1:]
		switch kind {
		case 0:
			l := binary.LittleEndian.Uint32(rest)
			out = append(out, rest[4:4+l]...)
			rest = rest[4+l:]
		default:
			ranges = append(ranges, [3]uint32{binary.LittleEndian.Uint32(rest), binary.LittleEndian.Uint32(rest[4:]), binary.LittleEndian.Uint32(rest[8:])})
			out = append(out, "null"...)
			rest = rest[12:]
		}
	}
	return out, ranges
}
