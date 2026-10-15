// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/MichaelKinsy/PiG/durable/harness"
)

// The seed and sample commands of durable/interop/bench.mjs, for the native core: same workload, same scripted model, same
// files and the same result line, so the benchmark runner (bench/k8s) measures pi-durable and the core alike.
//
//	bench seed DIR SIZE [TARGET]            write DIR/<TARGET>-<SIZE>.sqlite from zero and its .json seed record
//	bench sample FIXTURE SIZE [INDEX] [TARGET] [VERSION]
//	                                        one measured run on a copy of FIXTURE: one result line on stdout
//
// TARGET defaults to pig-native. seed writes the history in batches of seedBatch turns, closing and reopening the store
// between batches as bench.mjs does. sample runs measuredRuns turns of measuredTools lookups each, or
// BENCH_MEASURED_TURNS turns when it is set (measuredTurnsOf). BENCH_WORKLOAD=summary runs the realistic-summary workload
// (workloadOf) in both commands.

const (
	seedBatch    = 50
	measuredRuns = 10
)

// seedRecord is bench.mjs's seed .json: the fingerprint of the last seeding model call's context, the wall time of the
// whole seed, the store's size and the row count of each table.
type seedRecord struct {
	Target      string           `json:"target"`
	Workload    string           `json:"workload,omitempty"`
	Turns       int              `json:"turns"`
	Fingerprint string           `json:"fingerprint"`
	SeedMs      int64            `json:"seedMs"`
	Bytes       int64            `json:"bytes"`
	Tables      map[string]int64 `json:"tables"`
}

// sampleLine is bench.mjs's sample line. open and turn are wall times, openCpu and turnCpu the process CPU time of the same
// spans, cpuSeconds the CPU time from the open to the last turn, rss and peakRss the peak resident set (MiB), retained the
// heap the open session keeps after a forced collection (MiB, retainedMB), bytes the
// closed store's file size, fingerprint the context of the last measured model call (equal across targets that ran the
// same transcript) and fixture the seed fingerprint the run started from.
type sampleLine struct {
	Target      string           `json:"target"`
	Workload    string           `json:"workload,omitempty"`
	Version     string           `json:"version"`
	Turns       int              `json:"turns"`
	Sample      int              `json:"sample"`
	Open        float64          `json:"open"`
	Turn        []float64        `json:"turn"`
	OpenCPU     float64          `json:"openCpu"`
	TurnCPU     []float64        `json:"turnCpu"`
	RSS         float64          `json:"rss"`
	PeakRSS     float64          `json:"peakRss"`
	Retained    float64          `json:"retained"`
	CPUSeconds  float64          `json:"cpuSeconds"`
	Bytes       int64            `json:"bytes"`
	Tables      map[string]int64 `json:"tables"`
	Fingerprint string           `json:"fingerprint"`
	Fixture     string           `json:"fixture"`
	At          string           `json:"at"`
}

// retainedMB is bench.mjs's retained for the native core: the Go heap the open session keeps after a forced collection, in
// MiB. SQLite's allocations are outside it, as they are outside Node's JS heap and external memory.
func retainedMB() float64 {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return float64(ms.HeapAlloc) / (1 << 20)
}

func k8sMain(ctx context.Context, cmd string, args []string) error {
	if !haveRusage {
		return fmt.Errorf("bench %s needs getrusage for the process CPU time and peak resident set, which this platform lacks", cmd)
	}
	arg := func(i int, fallback string) string {
		if i < len(args) && args[i] != "" {
			return args[i]
		}
		return fallback
	}
	size, err := strconv.Atoi(arg(1, ""))
	if err != nil || size <= 0 || arg(0, "") == "" {
		return fmt.Errorf("usage: bench seed DIR SIZE [TARGET] | bench sample FIXTURE SIZE [INDEX] [TARGET] [VERSION]")
	}
	workload, err := workloadOf(os.Getenv("BENCH_WORKLOAD"))
	if err != nil {
		return err
	}
	if cmd == "seed" {
		return seedFixture(ctx, arg(0, ""), size, arg(2, "pig-native"), workload)
	}
	index, err := strconv.Atoi(arg(2, "0"))
	if err != nil {
		return fmt.Errorf("sample index %q: %w", arg(2, ""), err)
	}
	turns, err := measuredTurnsOf(os.Getenv("BENCH_MEASURED_TURNS"))
	if err != nil {
		return err
	}
	return sampleFixture(ctx, os.Stdout, arg(0, ""), size, index, turns, arg(3, "pig-native"), arg(4, ""), workload)
}

// measuredTurnsOf is bench.mjs's BENCH_MEASURED_TURNS: the measured turns of one sample, measuredRuns when unset. The
// benchmark's gated numbers use measuredRuns; a longer sample serves a reported steady-state window (turns 20-29 of 30),
// whose transcript, store and memory differ from the gated sample's. A sample needs a cold turn and a warm one.
func measuredTurnsOf(v string) (int, error) {
	if v == "" {
		return measuredRuns, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 2 || v[0] == '+' { // digits only, as bench.mjs reads it
		return 0, fmt.Errorf("BENCH_MEASURED_TURNS %q: want an integer of at least 2", v)
	}
	return n, nil
}

// k8sOptions are the options of a run the k8s commands make: the bench.mjs workload on the real clock, untraced.
func k8sOptions(db, workload string, finish func(context.Context, harness.Harness, *script, measured) error) options {
	o := options{db: db, variant: "full", label: "m", tools: measuredTools, workload: workload, finish: finish}
	if summarizes(workload) {
		o.payloadKB = summaryPayloadKB
	}
	return o
}

// labelOf is a seed record's or sample line's workload: omitted for the standard workload, as bench.mjs omits it.
func labelOf(workload string) string {
	if workload == workloadStandard {
		return ""
	}
	return workload
}

func seedFixture(ctx context.Context, dir string, size int, target, workload string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	work := filepath.Join(dir, target+"-work.sqlite")
	removeStore(work)
	started := time.Now()
	fp := ""
	for done := 0; done < size; done += seedBatch {
		o := k8sOptions(work, workload, func(ctx context.Context, s harness.Harness, sc *script, _ measured) error {
			fp = sc.benchFingerprint()
			return s.Close(ctx)
		})
		o.seedFrom, o.seed = done, min(done+seedBatch, size)
		if err := run(ctx, o); err != nil {
			return fmt.Errorf("seed %d-%d: %w", o.seedFrom, o.seed, err)
		}
	}
	seedMs := time.Since(started).Milliseconds()
	fixture := filepath.Join(dir, fmt.Sprintf("%s-%d.sqlite", target, size))
	removeStore(fixture)
	if err := os.Rename(work, fixture); err != nil {
		return err
	}
	removeStore(work)
	bytes, tables, err := fileStats(ctx, fixture)
	if err != nil {
		return err
	}
	rec, err := json.Marshal(seedRecord{Target: target, Workload: labelOf(workload), Turns: size, Fingerprint: fp, SeedMs: seedMs, Bytes: bytes, Tables: tables})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "%s\n", rec)
	return os.WriteFile(fixture+".json", rec, 0o644)
}

func sampleFixture(ctx context.Context, w io.Writer, fixture string, size, index, turns int, target, version, workload string) error {
	dir, err := os.MkdirTemp("", "durable-core-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }() // a scratch copy
	work := filepath.Join(dir, "store.sqlite")
	if err := copyFile(fixture, work); err != nil {
		return err
	}
	var line sampleLine
	o := k8sOptions(work, workload, func(ctx context.Context, s harness.Harness, sc *script, m measured) error {
		fixtureFP, err := fixtureFingerprint(sc.firstSeen)
		if err != nil {
			return err
		}
		// The published fingerprints are the standard workload's; a summary fixture is checked across targets by the caller.
		if workload == workloadStandard {
			if err := checkFixture(size, fixtureFP); err != nil {
				return err
			}
		}
		cpu := m.openCPU
		for _, c := range m.turnsCPU {
			cpu += c
		}
		peak := peakResidentMB()
		line = sampleLine{Target: target, Workload: labelOf(workload), Version: version, Turns: size, Sample: index, Open: m.open, Turn: m.turns, OpenCPU: m.openCPU, TurnCPU: m.turnsCPU,
			RSS: peak, PeakRSS: peak, Retained: retainedMB(), CPUSeconds: cpu / 1000, Fingerprint: sc.benchFingerprint(), Fixture: fixtureFP}
		return s.Close(ctx)
	})
	o.turns = turns
	if err := run(ctx, o); err != nil {
		return err
	}
	if line.Bytes, line.Tables, err = fileStats(ctx, work); err != nil {
		return err
	}
	line.At = time.Now().UTC().Format(time.RFC3339Nano)
	b, err := json.Marshal(line)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", b)
	return err
}

// fileStats is bench.mjs's bytes (the closed store's file size) and tables (row count per table).
func fileStats(ctx context.Context, path string) (int64, map[string]int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, nil, err
	}
	_, tables, err := storeStats(ctx, path)
	return info.Size(), tables, err
}

func removeStore(path string) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(path + suffix) // absent is fine
	}
}

// copyFile streams the fixture to its scratch copy, so the measured process's peak resident set does not include the fixture's
// size (bench.mjs copies with copyFileSync, outside the JS heap).
func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }() // read-only: a close error loses nothing
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close() // the copy error is the one to report
		return err
	}
	return out.Close()
}
