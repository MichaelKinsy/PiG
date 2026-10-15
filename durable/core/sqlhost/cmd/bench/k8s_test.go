// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"testing"
)

// The k8s seed and sample commands do what durable/interop/bench.mjs does: seed writes a 120-turn history in three
// batches (closing and reopening the store between them) with its seed record, and sample prints one line whose fingerprint is the context of the last measured call,
// the same transcript the independently simulated walk of the scripted model reaches.
func TestK8sSeedAndSampleMatchBenchMjs(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	const size = 120
	all, seedCall := seededContext(size)
	if err := seedFixture(ctx, dir, size, "pig-native", workloadStandard); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(dir, "pig-native-120.sqlite")
	var rec seedRecord
	data, err := os.ReadFile(fixture + ".json")
	if err != nil || json.Unmarshal(data, &rec) != nil {
		t.Fatalf("seed record: %v %s", err, data)
	}
	info, err := os.Stat(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Fingerprint != fingerprint(seedCall) || rec.Turns != size || rec.Bytes != info.Size() || rec.Tables["submissions"] != size || rec.SeedMs <= 0 {
		t.Fatalf("seed record %+v (file %d bytes)", rec, info.Size())
	}
	if _, err := os.Stat(filepath.Join(dir, "pig-native-work.sqlite")); !os.IsNotExist(err) {
		t.Fatalf("the seed's work store is left behind: %v", err)
	}

	// The transcript after ten measured eight-lookup turns, walked independently with the scripted model.
	var lastCall []seen
	for i := range measuredRuns {
		all = append(all, seen{role: "user", text: "turn m" + string(rune('0'+i)) + " tools=8"})
		for {
			step := next(all)
			if step.answer != "" {
				lastCall = slices.Clone(all)
				all = append(all, seen{role: "assistant", text: step.answer})
				break
			}
			all = append(all, seen{role: "assistant", calls: []int{step.call}}, seen{role: "tool", text: payload(step.call, 0)})
		}
	}
	var out bytes.Buffer
	if err := sampleFixture(ctx, &out, fixture, size, 3, measuredRuns, "pig-native", "v", workloadStandard); err != nil {
		t.Fatal(err)
	}
	var line sampleLine
	if err := json.Unmarshal(out.Bytes(), &line); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if line.Fingerprint != fingerprint(lastCall) || line.Fixture != rec.Fingerprint {
		t.Fatalf("fingerprint %s fixture %s, want %s and %s", line.Fingerprint, line.Fixture, fingerprint(lastCall), rec.Fingerprint)
	}
	if line.Sample != 3 || line.Version != "v" || len(line.Turn) != measuredRuns || len(line.TurnCPU) != measuredRuns || line.OpenCPU <= 0 || line.Tables["submissions"] != size+measuredRuns || line.PeakRSS <= 0 ||
		line.Retained <= 0 || line.Retained >= line.PeakRSS {
		t.Fatalf("sample line %+v", line)
	}
	// The fixture is measured on a copy: it is unchanged.
	if after, err := os.Stat(fixture); err != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		t.Fatalf("the fixture changed: %v", err)
	}
	if err := sampleFixture(ctx, &out, filepath.Join(dir, "missing.sqlite"), size, 0, measuredRuns, "pig-native", "", workloadStandard); err == nil {
		t.Fatal("a missing fixture was sampled")
	}
	// BENCH_MEASURED_TURNS (the steady-state window's longer sample) continues the same transcript: two more turns.
	for i := measuredRuns; i < measuredRuns+2; i++ {
		all = append(all, seen{role: "user", text: "turn m" + strconv.Itoa(i) + " tools=8"})
		for {
			step := next(all)
			if step.answer != "" {
				lastCall = slices.Clone(all)
				all = append(all, seen{role: "assistant", text: step.answer})
				break
			}
			all = append(all, seen{role: "assistant", calls: []int{step.call}}, seen{role: "tool", text: payload(step.call, 0)})
		}
	}
	out.Reset()
	if err := sampleFixture(ctx, &out, fixture, size, 0, measuredRuns+2, "pig-native", "", workloadStandard); err != nil {
		t.Fatal(err)
	}
	line = sampleLine{}
	if err := json.Unmarshal(out.Bytes(), &line); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if len(line.Turn) != measuredRuns+2 || len(line.TurnCPU) != measuredRuns+2 || line.Tables["submissions"] != size+measuredRuns+2 || line.Fingerprint != fingerprint(lastCall) {
		t.Fatalf("a %d-turn sample: %d turns, %d submissions, fingerprint %s want %s", measuredRuns+2, len(line.Turn), line.Tables["submissions"], line.Fingerprint, fingerprint(lastCall))
	}
	// At a published size the fixture must carry the published seed fingerprint: the 120-turn history sampled as 50 turns
	// is refused, so a wrong or stale fixture cannot pass as the benchmark's history.
	out.Reset()
	if err := sampleFixture(ctx, &out, fixture, 50, 0, measuredRuns, "pig-native", "", workloadStandard); err == nil || out.Len() != 0 {
		t.Fatalf("a fixture without the published 50-turn fingerprint was sampled as 50 turns: %v %s", err, out.String())
	}
}

// retained counts the live heap after a forced collection: garbage the session dropped is not in it, live data is.
func TestRetainedMBIsTheLiveHeapAfterACollection(t *testing.T) {
	live := make([]byte, 16<<20)
	garbage := make([][]byte, 64)
	for i := range garbage {
		garbage[i] = make([]byte, 1<<20)
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	before := float64(ms.HeapAlloc) / (1 << 20)
	runtime.KeepAlive(garbage) // its last use: dead from here on
	got := retainedMB()
	runtime.KeepAlive(live)
	if before < 80 || got < 16 || got > before-48 {
		t.Fatalf("retained %.1f MiB with 16 MiB live and 64 MiB dropped (heap %.1f MiB before)", got, before)
	}
}

// copyFile streams: copying a 64 MiB fixture allocates a bounded buffer at most, never the file, so the sample's peak resident
// set does not grow with the fixture's size. The copy is byte-identical, including data at the end of the file.
func TestCopyFileDoesNotHoldTheFixtureInMemory(t *testing.T) {
	const size = 64 << 20
	dir := t.TempDir()
	from, to := filepath.Join(dir, "fixture.sqlite"), filepath.Join(dir, "copy.sqlite")
	f, err := os.Create(from)
	if err != nil {
		t.Fatal(err)
	}
	head, tail := []byte("SQLite format 3\x00"), []byte("last page")
	if _, err := f.Write(head); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(tail, size-int64(len(tail))); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, bytes.Repeat([]byte{1}, size+4096), 0o644); err != nil { // a longer stale copy is truncated
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if err := copyFile(from, to); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 1<<20 {
		t.Fatalf("copying a %d MiB file allocated %d KiB; the copy must stream", size>>20, grew>>10)
	}
	got, err := os.ReadFile(to)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != size || !bytes.HasPrefix(got, head) || !bytes.HasSuffix(got, tail) || bytes.ContainsRune(got[len(head):size-len(tail)], 1) {
		t.Fatalf("copy of %d bytes is not the fixture (%d bytes)", size, len(got))
	}
}

// BENCH_MEASURED_TURNS: unset is the benchmark's ten turns; a value must leave a cold and a warm turn.
func TestMeasuredTurnsOf(t *testing.T) {
	for v, want := range map[string]int{"": measuredRuns, "30": 30, "2": 2} {
		if got, err := measuredTurnsOf(v); err != nil || got != want {
			t.Errorf("measuredTurnsOf(%q) = %d, %v; want %d", v, got, err, want)
		}
	}
	for _, v := range []string{"1", "0", "-3", "+30", "x", "10.5", " 30"} {
		if _, err := measuredTurnsOf(v); err == nil {
			t.Errorf("measuredTurnsOf(%q) accepted", v)
		}
	}
}

// BENCH_WORKLOAD=summary is bench.mjs's realistic-summary workload. Seeded natively, the 50-turn history is the one bench.mjs
// writes on Pi v1.1.0 and on the TinyGo core (4c8f712e3b0cd3ce) (no compaction runs within 50 turns), and a sample's ten
// turns compact: more than the 288 tasks the turns alone write. Nothing that depends on when a background compaction lands
// is asserted (its transcript, its task count): Pi starts the compaction beside the request (generation.ts), so which model
// call first sees the compacted context depends on timing. On one CPU the native sample wrote 289 to 301 tasks. The
// benchmark compares each sample's fingerprint across targets instead.
func TestK8sSummaryWorkloadMatchesBenchMjs(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := seedFixture(ctx, dir, 50, "pig-native", workloadSummary); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(dir, "pig-native-50.sqlite")
	var rec seedRecord
	data, err := os.ReadFile(fixture + ".json")
	if err != nil || json.Unmarshal(data, &rec) != nil {
		t.Fatalf("seed record: %v %s", err, data)
	}
	if rec.Fingerprint != "4c8f712e3b0cd3ce" || rec.Workload != workloadSummary || rec.Tables["tasks"] != 118 {
		t.Fatalf("seed record %+v", rec)
	}
	var out bytes.Buffer
	if err := sampleFixture(ctx, &out, fixture, 50, 0, measuredRuns, "pig-native", "", workloadSummary); err != nil {
		t.Fatal(err)
	}
	var line sampleLine
	if err := json.Unmarshal(out.Bytes(), &line); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if line.Workload != workloadSummary || line.Tables["tasks"] <= 288 || line.Fixture == "" {
		t.Fatalf("sample line: fingerprint %s workload %q tasks %d", line.Fingerprint, line.Workload, line.Tables["tasks"])
	}
	// The standard workload refuses the summary fixture: its first measured context is not the published 50-turn one.
	out.Reset()
	if err := sampleFixture(ctx, &out, fixture, 50, 0, measuredRuns, "pig-native", "", workloadStandard); err == nil {
		t.Fatalf("the standard workload sampled a summary fixture: %s", out.String())
	}
}

// BENCH_WORKLOAD=summary-blocking is summary with blocking compaction only. Its transcript does not depend on scheduling, so
// the native sample reaches bench.mjs's on Pi v1.1.0 exactly: 50-turn seed 4c8f712e3b0cd3ce, sample f6e992ec7a30f3e1 with 289
// tasks. Three samples of one fixture are compared, since the background workload's sample differs on some runs.
func TestK8sSummaryBlockingWorkloadIsBenchMjsTranscript(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := seedFixture(ctx, dir, 50, "pig-native", workloadSummaryBlocking); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(dir, "pig-native-50.sqlite")
	var rec seedRecord
	data, err := os.ReadFile(fixture + ".json")
	if err != nil || json.Unmarshal(data, &rec) != nil {
		t.Fatalf("seed record: %v %s", err, data)
	}
	if rec.Fingerprint != "4c8f712e3b0cd3ce" || rec.Workload != workloadSummaryBlocking || rec.Tables["tasks"] != 118 {
		t.Fatalf("seed record %+v", rec)
	}
	for i := range 3 {
		var out bytes.Buffer
		if err := sampleFixture(ctx, &out, fixture, 50, i, measuredRuns, "pig-native", "", workloadSummaryBlocking); err != nil {
			t.Fatal(err)
		}
		var line sampleLine
		if err := json.Unmarshal(out.Bytes(), &line); err != nil {
			t.Fatalf("%v: %s", err, out.String())
		}
		if line.Fingerprint != "f6e992ec7a30f3e1" || line.Workload != workloadSummaryBlocking || line.Tables["tasks"] != 289 {
			t.Fatalf("sample %d: fingerprint %s workload %q tasks %d; want bench.mjs's f6e992ec7a30f3e1, 289", i, line.Fingerprint, line.Workload, line.Tables["tasks"])
		}
	}
}

// summaryText is bench.mjs's SUMMARY byte for byte (sha256 of the JavaScript string's UTF-8), and BENCH_WORKLOAD accepts
// exactly bench.mjs's values.
func TestSummaryTextAndWorkloadOf(t *testing.T) {
	if sum := sha256.Sum256([]byte(summaryText)); hex.EncodeToString(sum[:]) != "44ee05b51d3f650065b262aa79230e047fb304a4207bb6ad1b1ef1ecabe22cab" || len(summaryText) != summaryChars {
		t.Fatalf("summaryText: %d bytes, sha256 %x", len(summaryText), sum)
	}
	for v, want := range map[string]string{"": workloadStandard, "standard": workloadStandard, "summary": workloadSummary, "summary-blocking": workloadSummaryBlocking} {
		if got, err := workloadOf(v); err != nil || got != want {
			t.Errorf("workloadOf(%q) = %q, %v; want %q", v, got, err, want)
		}
	}
	for _, v := range []string{"Summary", "stress", " summary", "summary "} {
		if _, err := workloadOf(v); err == nil {
			t.Errorf("workloadOf(%q) accepted", v)
		}
	}
}
