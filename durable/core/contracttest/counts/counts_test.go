// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package counts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/abi/abitest"
	"github.com/MichaelKinsy/PiG/durable/core/contracttest/fuel"
)

const fixedUUID = "0190a6b2-0000-7000-8000-000000000001"

// recordSkeleton records the binding conformance script (open, rows, close, an event after close) through a native core.
func recordSkeleton(t *testing.T) string {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "binding", "testdata", "skeleton.events"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	events, err := abitest.ParseScript(f)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "skeleton.rec")
	rec, err := NewRecorder(path)
	if err != nil {
		t.Fatal(err)
	}
	stepper := rec.Stepper(core.NewSession(rec.UUID(func() string { return fixedUUID })))
	for i, ev := range events {
		if i == 2 {
			rec.Mark("turn 0")
		}
		stepper.Step(ev)
	}
	rec.Mark("end")
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func goWasm(t *testing.T) []byte {
	t.Helper()
	if p := os.Getenv("DCORE_WASM_GO"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	out := filepath.Join(t.TempDir(), "core-go.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-trimpath", "-o", out, "../../cmd/corewasm")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go wasip1 build: %v\n%s", err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestRecordingRoundTrip proves a recording groups into the events, uuids and marks it was written with, and that the native
// replay reproduces every recorded step: the digests are the step the core returned the first time.
func TestRecordingRoundTrip(t *testing.T) {
	rec, err := Load(recordSkeleton(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Entries) != 4 {
		t.Fatalf("%d entries, want the script's 4 events", len(rec.Entries))
	}
	if len(rec.Marks) != 2 || rec.Marks[0] != (Mark{"turn 0", 2}) || rec.Marks[1] != (Mark{"end", 4}) {
		t.Fatalf("marks %v", rec.Marks)
	}
	if rec.Entries[0].Kind != abi.EventOpen || rec.Entries[2].Kind != abi.EventClose {
		t.Errorf("kinds %d %d", rec.Entries[0].Kind, rec.Entries[2].Kind)
	}
	ivs, err := ReplayNative(rec)
	if err != nil {
		t.Fatal(err)
	}
	if len(ivs) != 2 || ivs[0].Name != "open" || ivs[1].Name != "turn 0" {
		t.Fatalf("intervals %v", ivs)
	}
	if got := ivs[0].M["storage.events"] + ivs[1].M["storage.events"]; got != 4 {
		t.Errorf("%d events counted, want 4", got)
	}
	if ivs[0].M["storage.commits"] == 0 {
		t.Error("the open of an empty database commits its migration, and the replay counted no commit")
	}
	if ivs[0].M["native.mallocs"] == 0 || ivs[0].M["native.allocBytes"] == 0 {
		t.Error("the replay counted no allocation in a step that opens a session")
	}
}

// TestReplayRefusesADivergentCore is the point of the digests: a count for a core that behaved differently would be a count
// of the wrong thing. A flipped digest, a changed event and a changed abi_id each stop the replay.
func TestReplayRefusesADivergentCore(t *testing.T) {
	data, err := os.ReadFile(recordSkeleton(t))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	rec.Entries[1].Digest[0] ^= 1
	var rerr *ReplayError
	if _, err := ReplayNative(rec); !errors.As(err, &rerr) || rerr.Entry != 1 || !strings.Contains(rerr.What, "differs from the recorded step") {
		t.Fatalf("a flipped digest: %v", err)
	}
	rec, _ = Parse(data)
	rec.Entries[0].Wire = append([]byte(nil), rec.Entries[0].Wire...)
	rec.Entries[0].Wire[len(rec.Entries[0].Wire)-2] = 'X' // the open payload is no longer JSON the core accepts
	if _, err := ReplayNative(rec); err == nil {
		t.Error("a changed open event replayed without an error")
	}
	bad := append([]byte(nil), data...)
	bad[5] ^= 1 // the first byte of the abi_id
	if _, err := Parse(bad); err == nil || !strings.Contains(err.Error(), "abi_id") {
		t.Errorf("a recording of another abi_id parsed: %v", err)
	}
}

// TestWasmReplayMatchesNative runs the same recording through the Go wasip1 build under wazero, plain and instrumented: both
// must return the recorded steps, the instrumented module must count instructions, and the plain one the crossings and memory.
func TestWasmReplayMatchesNative(t *testing.T) {
	rec, err := Load(recordSkeleton(t))
	if err != nil {
		t.Fatal(err)
	}
	wasm := goWasm(t)
	ctx := context.Background()
	plain, err := ReplayWasm(ctx, rec, wasm, WasmOptions{Build: "go", Live: true})
	if err != nil {
		t.Fatal(err)
	}
	metered, err := ReplayWasm(ctx, rec, wasm, WasmOptions{Build: "go", Fuel: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := plain[0].M["go.calls"] + plain[1].M["go.calls"]; got < 4*2-2 {
		t.Errorf("%d crossings for 4 events", got)
	}
	if plain[0].M["go.linearBytes"] == 0 || plain[0].M["go.liveBytes"] == 0 {
		t.Errorf("linear memory %d, live heap %d", plain[0].M["go.linearBytes"], plain[0].M["go.liveBytes"])
	}
	if metered[0].M["go.fuel"] <= 0 || metered[1].M["go.fuel"] <= 0 {
		t.Errorf("instructions %d, %d", metered[0].M["go.fuel"], metered[1].M["go.fuel"])
	}
	again, err := ReplayWasm(ctx, rec, wasm, WasmOptions{Build: "go", Fuel: true})
	if err != nil {
		t.Fatal(err)
	}
	for i := range metered {
		if metered[i].M["go.fuel"] != again[i].M["go.fuel"] {
			t.Errorf("interval %d: %d instructions, then %d: the count is not repeatable", i, metered[i].M["go.fuel"], again[i].M["go.fuel"])
		}
	}
	// A recording of the wrong abi is refused before a step runs.
	rec.ABI = strings.Repeat("0", 64)
	if _, err := ReplayWasm(ctx, rec, wasm, WasmOptions{Build: "go"}); err == nil || !strings.Contains(err.Error(), "abi_id") {
		t.Errorf("a recording of another abi_id replayed: %v", err)
	}
	if _, err := fuel.Instrument(wasm); err != nil {
		t.Errorf("the shipped Go core does not instrument: %v", err)
	}
}

func TestPhasesFoldsCountersAndGauges(t *testing.T) {
	ivs := []Interval{
		{"open", Metrics{"storage.events": 14, "tinygo.linearBytes": 100, "tinygo.liveBytes": 10}},
		{"turn 0", Metrics{"storage.events": 100, "tinygo.linearBytes": 200, "tinygo.liveBytes": 50}},
		{"turn 1", Metrics{"storage.events": 10, "tinygo.linearBytes": 200, "tinygo.liveBytes": 60}},
		{"turn 2", Metrics{"storage.events": 12, "tinygo.linearBytes": 300, "tinygo.liveBytes": 80}},
		{"turn 3", Metrics{"storage.events": 14, "tinygo.linearBytes": 300, "tinygo.liveBytes": 100}},
	}
	p := Phases(ivs)
	if p["cold"]["storage.events"] != 114 || p["warm"]["storage.events"] != 36 {
		t.Errorf("counters: cold is the open and the first turn, warm the rest: %v", p)
	}
	if p["cold"]["tinygo.liveBytes"] != 50 || p["warm"]["tinygo.linearBytes"] != 300 || p["warm"]["tinygo.liveBytes"] != 100 {
		t.Errorf("gauges take the end of the phase: %v", p)
	}
	// The live set grew 60 -> 100 over the two turns after turn 1 (three warm turns): 20 bytes a turn.
	if p["warm"]["tinygo.liveGrowthPerTurn"] != 20 || p["warm"]["turns"] != 3 {
		t.Errorf("growth %d over %d turns", p["warm"]["tinygo.liveGrowthPerTurn"], p["warm"]["turns"])
	}
}

// TestCellsAreTheContractsCells: the counts replay adds to durable/contract/cli/counts.mjs go to its cells under names of their
// own and leave the counts the contract measures itself (rows, crossings, heap, linear memory, the runtime's allocation totals)
// alone: the native MemStats the replay reads stay in the replay report.
func TestCellsAreTheContractsCells(t *testing.T) {
	phases := map[string]Metrics{
		"cold": {"storage.rowsRead": 7, "tinygo.fuel": 90, "tinygo.allocCalls": 5, "tinygo.allocRequestedBytes": 64, "tinygo.gcCycles": 2, "tinygo.linearBytes": 1 << 20, "native.mallocs": 4, "go.fuel": 80},
		"warm": {"tinygo.fuel": 10, "tinygo.gcCycles": 7, "tinygo.liveGrowthPerTurn": 3, "turns": 5},
	}
	got := Cells(1000, phases)
	want := map[string]map[string]int64{
		"wasm-tinygo/1000": {"cold.instructions": 90, "cold.allocCalls": 5, "cold.allocRequestedBytes": 64, "cold.gcCycles": 2, "warm.instructions": 10, "warm.gcCycles": 7, "warm.liveGrowthPerTurn": 3},
		"wasm-go/1000":     {"cold.instructions": 80},
	}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for cell, metrics := range want {
		for k, v := range metrics {
			if got[cell][k] != v {
				t.Errorf("%s %s = %d, want %d", cell, k, got[cell][k], v)
			}
		}
		if len(got[cell]) != len(metrics) {
			t.Errorf("%s has %v", cell, got[cell])
		}
	}
}

// TestReplayOfAProductionRun records the bench workload on the native stack (SQLite, driver, scripted model) in two processes,
// seeding on an empty store and then reopening it, and replays both recordings through the native core and the Go wasip1 build:
// every step, read and uuid the production run took must come out again. The first recording takes uuids (a conversation is
// created), the second reads the store back (pig.read).
func TestReplayOfAProductionRun(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "bench")
	build := exec.Command("go", "build", "-o", exe, "../../sqlhost/cmd/bench")
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build bench: %v\n%s", err, b)
	}
	env := append(os.Environ(), "HOME="+filepath.Join(dir, "home"), "PIG_CODING_AGENT_DIR="+filepath.Join(dir, "agent"), "PI_CODING_AGENT_DIR="+filepath.Join(dir, "agent"),
		"PIG_HOME="+filepath.Join(dir, "pig"), "PI_HOME="+filepath.Join(dir, "pig"), "TZ=UTC", "DETENV_EPOCH=1800000000000")
	db := filepath.Join(dir, "s.sqlite")
	run := func(name string, args ...string) *Recording {
		cmd := exec.Command(exe, append([]string{"--db", db, "--trace", filepath.Join(dir, name+".trace"), "--record", filepath.Join(dir, name+".rec"), "--tools", "1", "--variant", "incr"}, args...)...)
		cmd.Env = env
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("bench %s: %v\n%s", name, err, b)
		}
		rec, err := Load(filepath.Join(dir, name+".rec"))
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	seeded := run("seed", "--seed", "3", "--turns", "0")
	reopened := run("reopen", "--turns", "2")
	wasm := goWasm(t)
	defer testDivergences(t, filepath.Join(dir, "seed.rec"), filepath.Join(dir, "reopen.rec"), wasm)
	for name, rec := range map[string]*Recording{"seed": seeded, "reopen": reopened} {
		var reads, uuids int
		for i := range rec.Entries {
			reads += len(rec.Entries[i].Reads)
			uuids += len(rec.Entries[i].UUIDs)
			if !rec.Entries[i].Verified {
				t.Fatalf("%s: event %d has no recorded step", name, i)
			}
		}
		if name == "seed" && uuids == 0 || name == "reopen" && reads == 0 {
			t.Errorf("%s: %d reads, %d uuids", name, reads, uuids)
		}
		native, err := ReplayNative(rec)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		wasmIvs, err := ReplayWasm(context.Background(), rec, wasm, WasmOptions{Build: "go"})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(native) != len(wasmIvs) || (name == "reopen" && len(native) != 3) {
			t.Errorf("%s: %d native intervals, %d wasm (the open and two turns)", name, len(native), len(wasmIvs))
		}
		// The closing events after the last mark are not part of any interval.
		end := rec.Marks[len(rec.Marks)-1].At
		want := int64(end)
		for i := range end {
			want += int64(len(rec.Entries[i].Reads) + len(rec.Entries[i].UUIDs))
		}
		var cross int64
		for _, iv := range native {
			cross += iv.M["storage.crossings"]
		}
		if cross != want {
			t.Errorf("%s: %d crossings, want %d", name, cross, want)
		}
	}
}

// testDivergences: a replay whose core takes other inputs than the recorded run, or returns another step, stops at that event
// with a ReplayError, natively and under Wasm, whichever way the inputs differ. The seed recording takes uuids, the reopen
// recording reads the store.
func testDivergences(t *testing.T, seedRec, reopenRec string, wasm []byte) {
	if t.Failed() {
		return
	}
	first := func(path string, has func(*Entry) bool) int {
		rec, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		for i := range rec.Entries {
			if has(&rec.Entries[i]) {
				return i
			}
		}
		t.Fatalf("%s: no event with the input the case alters", path)
		return -1
	}
	withUUID := first(seedRec, func(e *Entry) bool { return len(e.UUIDs) > 0 })
	withRead := first(reopenRec, func(e *Entry) bool { return len(e.Reads) > 0 })
	for _, c := range []struct {
		name, path string
		at         int
		alter      func(*Entry)
		want       string
	}{
		{"a uuid missing", seedRec, withUUID, func(e *Entry) { e.UUIDs = e.UUIDs[1:] }, "took a uuid the recorded run did not take"},
		{"an extra uuid", seedRec, withUUID, func(e *Entry) { e.UUIDs = append(e.UUIDs, e.UUIDs[0]) }, "uuids, the recorded run"},
		{"a read missing", reopenRec, withRead, func(e *Entry) { e.Reads = nil }, "a pig.read call the recorded run did not make"},
		{"an extra read", reopenRec, withRead, func(e *Entry) { e.Reads = append(e.Reads, e.Reads[0]) }, "reads and took"},
		{"another read", reopenRec, withRead, func(e *Entry) {
			r := e.Reads[0]
			r.Request = append([]byte(nil), r.Request...)
			r.Request[0] ^= 1 // the statement id
			e.Reads = append([]ReadCall{r}, e.Reads[1:]...)
		}, "asked a different pig.read"},
		{"another step", reopenRec, withRead, func(e *Entry) { e.Digest[0] ^= 1 }, "differs from the recorded step"},
	} {
		for _, build := range []string{"native", "go"} {
			rec, err := Load(c.path)
			if err != nil {
				t.Fatal(err)
			}
			c.alter(&rec.Entries[c.at])
			if build == "native" {
				_, err = ReplayNative(rec)
			} else {
				_, err = ReplayWasm(context.Background(), rec, wasm, WasmOptions{Build: "go"})
			}
			var rerr *ReplayError
			if !errors.As(err, &rerr) || rerr.Entry != c.at || !strings.Contains(rerr.What, c.want) {
				t.Errorf("%s, %s: %v; want a divergence at event %d: %s", c.name, build, err, c.at, c.want)
			}
		}
	}
}

// TestSteadyGrowthIgnoresTheFillAndSeesALeak: a live set that fills and holds is flat however steep the fill; one that keeps
// climbing after the fill is not, and a short window of five turns cannot tell them apart.
func TestSteadyGrowthIgnoresTheFillAndSeesALeak(t *testing.T) {
	ivs := func(live ...int64) []Interval {
		out := []Interval{{Name: "open", M: Metrics{"native.liveBytes": 7}}}
		for i, v := range live {
			out = append(out, Interval{Name: fmt.Sprintf("turn %d", i+1), M: Metrics{"native.liveBytes": v}})
		}
		return append(out, Interval{Name: "end"})
	}
	fills := []int64{100, 900, 1900, 2000, 2000, 2000, 2000, 2000, 2000, 2000}
	leaks := []int64{100, 900, 1900, 2000, 2100, 2200, 2300, 2400, 2500, 2600}
	if got := LiveCurve(ivs(fills...)); len(got) != len(fills) || got[2] != 1900 {
		t.Fatalf("LiveCurve = %v, want the turns' live sizes without open or end", got)
	}
	if g := SteadyGrowth(LiveCurve(ivs(fills...))); g != 0 {
		t.Errorf("a context that filled and holds grows %d per turn, want 0", g)
	}
	if g := SteadyGrowth(LiveCurve(ivs(leaks...))); g != 100 {
		t.Errorf("a leak of 100 bytes a turn reads %d", g)
	}
	// SteadyGrowth reads the half with liveGrowth: one object kept live at the last reading of a flat run is not growth, while
	// the endpoint slope of the half reads it as 2,000 / 9 = 222 bytes a turn and fails countgate flat.
	kept := slices.Concat(fills, []int64{2000, 2000, 2000, 2000, 2000, 2000, 2000, 2000, 2000, 4000})
	if g := SteadyGrowth(LiveCurve(ivs(kept...))); g != 0 {
		t.Errorf("one object live at the last reading of a flat run reads %d bytes a turn, want 0", g)
	}
	leakKept := slices.Concat(leaks, []int64{2700, 2800, 2900, 3000, 3100, 3200, 3300, 3400, 3500, 5600})
	if g := SteadyGrowth(LiveCurve(ivs(leakKept...))); g != 100 {
		t.Errorf("a leak of 100 bytes a turn with one object live at the last reading reads %d", g)
	}
}

// TestLiveGrowthIgnoresOneFalselyRetainedObject: dp-hops item 9 read +3,904 B a turn over six warm turns where thirty read
// 385 (base 365), because one ~19.5 KB object was live at the last reading only, as a conservative collector may keep one.
// The growth per turn must read a real retention of every turn, and not that one object.
func TestLiveGrowthIgnoresOneFalselyRetainedObject(t *testing.T) {
	series := func(n int, base, perTurn int64, extra map[int]int64) []int64 {
		out := make([]int64, n)
		for i := range out {
			out[i] = 400_000 + int64(i)*(base+perTurn) + extra[i]
		}
		return out
	}
	const kept = 19_500
	cases := []struct {
		name     string
		live     []int64
		min, max int64
	}{
		{"base growth, six turns", series(6, 365, 0, nil), 365, 365},
		{"one object live at the last reading", series(6, 365, 0, map[int]int64{5: kept}), 365, 365},
		{"one object live at the first reading", series(6, 365, 0, map[int]int64{0: kept}), 365, 365},
		{"one object live at a middle reading", series(6, 365, 0, map[int]int64{3: kept}), 365, 365},
		{"one object live at the last two readings", series(6, 365, 0, map[int]int64{4: kept, 5: kept}), 365, 365},
		{"one object live at the first two readings", series(6, 365, 0, map[int]int64{0: kept, 1: kept}), 365, 365},
		{"five turns, one object at the fourth reading", series(5, 365, 0, map[int]int64{3: kept}), 365, 365},
		{"a 1 KB retention every turn", series(6, 365, 1024, nil), 1389, 1389},
		{"a 1 KB retention every turn and one object at the end", series(6, 365, 1024, map[int]int64{5: kept}), 1389, 1389},
		{"thirty turns, one object at the end", series(30, 365, 0, map[int]int64{29: kept}), 365, 365},
		{"thirty turns, a 1 KB retention every turn and one object at the end", series(30, 365, 1024, map[int]int64{29: kept}), 1389, 1389},
		{"a set that shrinks", []int64{500, 400, 300, 200}, -100, -100},
		{"two turns", []int64{100, 160}, 60, 60},
		{"three turns", []int64{60, 80, 100}, 20, 20},
		{"three turns, a low middle reading", []int64{60, 50, 100}, 20, 20},
	}
	for _, c := range cases {
		if got := liveGrowth(c.live); got < c.min || got > c.max {
			t.Errorf("%s: %d bytes a turn, want %d..%d", c.name, got, c.min, c.max)
		}
	}
	// The endpoint figure this replaces reads the one object as growth.
	if live := series(6, 365, 0, map[int]int64{5: kept}); (live[5]-live[0])/5 < 4000 {
		t.Fatalf("the case no longer reproduces item 9's reading")
	}
}

// TestPhasesReportsLiveGrowthOfTheHalves: the figure Phases reports for every build is liveGrowth's, so item 9's one object
// held at the last warm reading is not growth in the gate's cells.
func TestPhasesReportsLiveGrowthOfTheHalves(t *testing.T) {
	ivs := []Interval{
		{"open", Metrics{"tinygo.liveBytes": 1000, "native.liveBytes": 500}},
		{"turn 0", Metrics{"tinygo.liveBytes": 2000, "native.liveBytes": 800}},
	}
	for i := range 6 {
		tinygo := 400_000 + int64(i)*365
		if i == 5 {
			tinygo += 19_500
		}
		ivs = append(ivs, Interval{"turn " + strconv.Itoa(i+1), Metrics{"tinygo.liveBytes": tinygo, "native.liveBytes": 900 + int64(i)*100}})
	}
	p := Phases(ivs)
	if got := p["warm"]["tinygo.liveGrowthPerTurn"]; got != 365 {
		t.Errorf("tinygo: %d bytes a turn, want 365: one object live at the last reading is not growth", got)
	}
	if got := p["warm"]["native.liveGrowthPerTurn"]; got != 100 {
		t.Errorf("native: %d bytes a turn, want 100", got)
	}
	if p["warm"]["turns"] != 6 {
		t.Errorf("%d warm turns, want 6", p["warm"]["turns"])
	}
}

// The census of dcore-int (TinyGo at 50 turns, 120 warm turns): the live set rises in three steps by warm turn 19 and is flat
// for the last 100. The halves-minimum slope over all of them reads the steps as growth; over the turns after SettleWarm it is 0,
// while a set that grows every turn keeps its slope.
func TestSettleWarmLeavesABoundedStepOutAndKeepsEveryTurnGrowth(t *testing.T) {
	run := func(live func(turn int) int64, n int) []Interval {
		ivs := []Interval{{"open", Metrics{"tinygo.liveBytes": 100}}}
		for i := range n + 1 {
			ivs = append(ivs, Interval{"turn " + strconv.Itoa(i), Metrics{"storage.events": 1, "tinygo.liveBytes": live(i)}})
		}
		return ivs
	}
	stepped := func(turn int) int64 {
		switch {
		case turn <= 7:
			return 115648
		case turn <= 18:
			return 120688
		case turn == 19:
			return 120944
		default:
			return 125824
		}
	}
	growing := func(turn int) int64 { return 115648 + 300*int64(turn) }
	if got := Phases(run(stepped, 30))["warm"]["tinygo.liveGrowthPerTurn"]; got == 0 {
		t.Fatalf("the steps over 30 warm turns read %d; the case needs the unsettled window to read them as growth", got)
	}
	p := Phases(SettleWarm(run(stepped, 60), 30))
	if p["warm"]["tinygo.liveGrowthPerTurn"] != 0 || p["warm"]["turns"] != 30 || p["warm"]["storage.events"] != 30 {
		t.Errorf("settled stepped set: growth %d over %d turns, events %d; want 0 over 30, 30", p["warm"]["tinygo.liveGrowthPerTurn"], p["warm"]["turns"], p["warm"]["storage.events"])
	}
	if p["cold"]["storage.events"] != 1 {
		t.Errorf("the cold phase keeps the open and the first turn: %v", p["cold"])
	}
	if got := Phases(SettleWarm(run(growing, 60), 30))["warm"]["tinygo.liveGrowthPerTurn"]; got != 300 {
		t.Errorf("a set growing 300 B every turn reads %d after settling; want 300", got)
	}
	if got := SettleWarm(run(growing, 3), 10); len(got) != 2 {
		t.Errorf("settling more warm turns than there are leaves the open and the cold turn: %d intervals", len(got))
	}
	if got := SettleWarm(run(growing, 3), 0); len(got) != 5 {
		t.Errorf("settling none keeps every interval: %d", len(got))
	}
}
