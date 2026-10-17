// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/durable/harness"
)

// seedFingerprints are workload.mjs SEED_FINGERPRINTS: the context the last seeding model call saw, per published fixture.
var seedFingerprints = map[int]string{50: "b017b487524e44a4", 250: "dcea9f30b0917245", 1000: "ac520308146f2a8f", 3500: "0a8c8e4b0d9a0794"}

// timingLine is one durable-bench bench/run.ts result line for the native build: open is the time from opening the store
// to an open root conversation (run.ts /wake), turn[i] each measured turn's wall time, bytes the database size
// (page_count * page_size, SqlStorage.databaseSize) and tables the row count of each table (durable-bench serve.ts).
type timingLine struct {
	Target      string           `json:"target"`
	Version     string           `json:"version"`
	Turns       int              `json:"turns"`
	Sample      int              `json:"sample"`
	Open        float64          `json:"open"`
	Turn        []float64        `json:"turn"`
	RSS         float64          `json:"rss"`
	Bytes       int64            `json:"bytes"`
	Tables      map[string]int64 `json:"tables"`
	Fingerprint string           `json:"fingerprint"`
	At          string           `json:"at"`
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// fixtureFingerprint is the seed fingerprint of the fixture a run started from, taken from the first measured model call's
// context: that context is the last seeding call's context followed by that call's answer and the first measured input.
func fixtureFingerprint(first []seen) (string, error) {
	if len(first) < 2 {
		return "", fmt.Errorf("the first measured call saw %d messages; a seeded fixture has at least two", len(first))
	}
	return fingerprint(first[:len(first)-2]), nil
}

// checkFixture refuses a run whose fixture is not the published one of its size, as durable-bench refuses a stale fixture.
func checkFixture(size int, got string) error {
	if want, ok := seedFingerprints[size]; ok && got != want {
		return fmt.Errorf("fixture of %d turns has fingerprint %s, want %s", size, got, want)
	}
	return nil
}

// storeStats reads the closed store's size and table row counts.
func storeStats(ctx context.Context, path string) (int64, map[string]int64, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = db.Close() }() // a read-only connection: its close cannot lose data
	var pages, size int64
	if err := db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages); err != nil {
		return 0, nil, err
	}
	if err := db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&size); err != nil {
		return 0, nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE '\_cf\_%' ESCAPE '\' AND name NOT LIKE 'sqlite\_%' ESCAPE '\'`)
	if err != nil {
		return 0, nil, err
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return 0, nil, err
		}
		names = append(names, name)
	}
	if err := rows.Close(); err != nil {
		return 0, nil, err
	}
	tables := make(map[string]int64, len(names))
	for _, name := range names {
		var n int64
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM "`+strings.ReplaceAll(name, `"`, `""`)+`"`).Scan(&n); err != nil {
			return 0, nil, err
		}
		tables[name] = n
	}
	return pages * size, tables, nil
}

// residentMB is the process's resident set in MiB (VmRSS), the unit of durable-bench's rss().
func residentMB() (float64, error) {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }() // read only
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		if rest, ok := strings.CutPrefix(scan.Text(), "VmRSS:"); ok {
			kb, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(rest), " kB"), 64)
			if err != nil {
				return 0, err
			}
			return kb / 1024, nil
		}
	}
	if err := scan.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("/proc/self/status has no VmRSS")
}

// appendLine appends line as one JSON line to path.
func appendLine(path string, line any) error {
	b, err := json.Marshal(line)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// finishTiming checks the fixture, reads the resident set and the closed store's statistics and appends the result line.
func finishTiming(ctx context.Context, o options, session harness.Harness, sc *script, open time.Duration, turns []float64) error {
	fp, err := fixtureFingerprint(sc.firstSeen)
	if err != nil {
		return err
	}
	if err := checkFixture(o.size, fp); err != nil {
		return err
	}
	rss, err := residentMB()
	if err != nil {
		return err
	}
	if err := session.Close(ctx); err != nil {
		return err
	}
	bytes, tables, err := storeStats(ctx, o.db)
	if err != nil {
		return err
	}
	return appendLine(o.timing, timingLine{
		Target: o.target, Version: o.version, Turns: o.size, Sample: o.sample, Open: ms(open), Turn: turns,
		RSS: rss, Bytes: bytes, Tables: tables, Fingerprint: fp, At: time.Now().UTC().Format(time.RFC3339Nano),
	})
}
