// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Command countgate measures what the Durable core costs on a recorded workload (package counts): bytes allocated, mallocs,
// Wasm instructions on an instrumented module, native instructions under callgrind, and the growth of the live set per turn.
// durable/contract/cli/counts.mjs runs it for each build and size and holds the results to the committed baseline, next to the
// counts of its own (rows, crossings, heap, linear memory).
//
//	countgate measure --rec FILE --size N [--tinygo core.wasm] [--go core.wasm]   cells of the replays of one recording
//	countgate instructions --rec FILE --size N   the native instructions of the core's steps, under callgrind (Valgrind)
//	countgate replay --rec FILE [--tinygo core.wasm] [--go core.wasm]   replay a recording (a trap report's ring), print what it cost
//	countgate flat --rec FILE   the live set after each turn of a long recording through the native core; fails unless it is flat (the Durable 0.5.0 bar)
//	countgate replay-native FILE         replay the recording through the native core, nothing else (the callgrind subject)
//
// Every replay compares each step with the step of the recorded run and stops at the first difference.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/durable/core/contracttest/counts"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "measure":
		err = measure(os.Args[2:])
	case "replay":
		err = replay(os.Args[2:])
	case "flat":
		err = flat(os.Args[2:])
	case "replay-native":
		err = replayNative(os.Args[2:])
	case "instructions":
		err = instructions(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "countgate:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: countgate measure|instructions|replay|replay-native ...")
	os.Exit(2)
}

func measure(args []string) error {
	fs := flag.NewFlagSet("measure", flag.ExitOnError)
	rec := fs.String("rec", "", "recording (bench --record)")
	size := fs.Int("size", 0, "turns of history in the fixture the recording started from")
	tinygo := fs.String("tinygo", "", "the TinyGo core module")
	gowasm := fs.String("go", "", "the Go wasip1 core module")
	out := fs.String("out", "", "report file (default stdout)")
	settle := fs.Int("settle", 0, "warm turns to leave out of the warm phase before it is folded (counts.SettleWarm)")
	_ = fs.Parse(args)
	if *rec == "" || *size <= 0 {
		return fmt.Errorf("measure needs --rec and --size")
	}
	r, err := counts.Load(*rec)
	if err != nil {
		return err
	}
	ivs, err := counts.ReplayNative(r)
	if err != nil {
		return err
	}
	ctx := context.Background()
	for _, b := range []struct{ name, path string }{{"tinygo", *tinygo}, {"go", *gowasm}} {
		if b.path == "" {
			continue
		}
		wasm, err := os.ReadFile(b.path)
		if err != nil {
			return err
		}
		// Each pass perturbs the run it measures, so each measure has its own: allocator listeners, mem_stats collections and
		// the instrumented binary. The plain pass's crossings and memory are the authority for the build.
		passes := []counts.WasmOptions{{Build: b.name, Allocs: b.name == "tinygo"}, {Build: b.name, Live: true}, {Build: b.name, Fuel: true}}
		var merged []counts.Interval
		for _, p := range passes {
			got, err := counts.ReplayWasm(ctx, r, wasm, p)
			if err != nil {
				return err
			}
			if merged == nil {
				merged = got
			} else if merged, err = counts.Merge(merged, got); err != nil {
				return err
			}
		}
		if ivs, err = counts.Merge(ivs, merged); err != nil {
			return err
		}
	}
	//portlint:allow all count-gate report with no Pi counterpart: counts.mjs parses it, so member order and HTML escaping are not observed
	data, err := json.MarshalIndent(counts.Cells(*size, counts.Phases(counts.SettleWarm(ivs, *settle))), "", "\t")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if *out == "" {
		_, err = os.Stdout.Write(data)
		return err
	}
	return os.WriteFile(*out, data, 0o644)
}

// replay runs a recording through the native core and each Wasm core given, step by step, and prints what each interval cost.
// It is the deterministic local replay of a trap: a core that traps or panics on the recorded events does it here, under a
// debugger if the caller wants one, and a divergence from a recorded step is reported with its event.
func replay(args []string) error {
	fs := flag.NewFlagSet("replay", flag.ExitOnError)
	rec := fs.String("rec", "", "recording (bench --record, or the host's FlightRecorder.recording())")
	tinygo := fs.String("tinygo", "", "the TinyGo core module")
	gowasm := fs.String("go", "", "the Go wasip1 core module")
	_ = fs.Parse(args)
	if *rec == "" {
		return fmt.Errorf("replay needs --rec")
	}
	r, err := counts.Load(*rec)
	if err != nil {
		return err
	}
	fmt.Printf("%d events\n", len(r.Entries))
	ivs, err := counts.ReplayNative(r)
	if err != nil {
		return err
	}
	fmt.Println("native: replayed")
	for _, b := range []struct{ name, path string }{{"tinygo", *tinygo}, {"go", *gowasm}} {
		if b.path == "" {
			continue
		}
		wasm, err := os.ReadFile(b.path)
		if err != nil {
			return err
		}
		got, err := counts.ReplayWasm(context.Background(), r, wasm, counts.WasmOptions{Build: b.name, Allocs: b.name == "tinygo"})
		if err != nil {
			return err
		}
		fmt.Printf("%s: replayed\n", b.name)
		if ivs, err = counts.Merge(ivs, got); err != nil {
			return err
		}
	}
	//portlint:allow all count-gate report with no Pi counterpart: counts.mjs parses it, so member order and HTML escaping are not observed
	data, err := json.MarshalIndent(counts.Phases(ivs), "", "\t")
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", data)
	return nil
}

// flat replays a recording natively and prints the live set (the heap after a full collection, less the baseline taken before the
// first step) after each turn. The bar is a live set that stops growing: the growth per turn over the second half of the turns is
// 0 bytes, whatever the first turns built (a context window fills, then holds). A short recording cannot show it: the live set of
// five warm turns can fall while a long run keeps climbing.
func flat(args []string) error {
	fs := flag.NewFlagSet("flat", flag.ExitOnError)
	rec := fs.String("rec", "", "a recording of many turns (bench --record --turns N)")
	_ = fs.Parse(args)
	if *rec == "" {
		return fmt.Errorf("flat needs --rec")
	}
	r, err := counts.Load(*rec)
	if err != nil {
		return err
	}
	ivs, err := counts.ReplayNative(r)
	if err != nil {
		return err
	}
	live := counts.LiveCurve(ivs)
	if len(live) < 8 {
		return fmt.Errorf("flat needs at least 8 turns, the recording has %d", len(live))
	}
	for _, i := range []int{0, len(live) / 4, len(live) / 2, 3 * len(live) / 4, len(live) - 1} {
		fmt.Printf("turn %4d  live %d bytes\n", i+1, live[i])
	}
	growth := counts.SteadyGrowth(live)
	fmt.Printf("live-set growth per turn over the last %d turns: %d bytes (the bar is 0)\n", len(live)-1-len(live)/2, growth)
	if growth != 0 {
		return fmt.Errorf("the live set is not flat: %d bytes per turn", growth)
	}
	return nil
}

func replayNative(args []string) error {
	if len(args) != 1 {
		usage()
	}
	r, err := counts.Load(args[0])
	if err != nil {
		return err
	}
	_, err = counts.ReplayNative(r)
	return err
}

var irLine = regexp.MustCompile(`Collected\s*:\s*(\d+)`)

// instructions runs the native replay under callgrind, Valgrind's instruction-counting tool (cachegrind's engine with a
// collection switch), and reports the instructions the core's Step executed: callgrind collects only inside Session.Step
// (--toggle-collect), so the replay's own decoding and the Go runtime's start-up are not in the figure. VALGRIND names the
// executable, the pinned release from durable/contract/tools/build-valgrind.sh; without it the command refuses rather than
// count with another valgrind, since the baseline names the release. The Go runtime preempts with signals that callgrind
// cannot follow, so the run turns asynchronous preemption off, and it runs one P: the instructions of runtime threads are
// the only noise left.
func instructions(args []string) error {
	fs := flag.NewFlagSet("instructions", flag.ExitOnError)
	rec := fs.String("rec", "", "recording")
	size := fs.Int("size", 0, "fixture size")
	_ = fs.Parse(args)
	valgrind := os.Getenv("VALGRIND")
	if valgrind == "" {
		return errors.New("instructions needs the pinned valgrind: sh durable/contract/tools/build-valgrind.sh PREFIX, then VALGRIND=PREFIX/bin/valgrind")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(valgrind, "--tool=callgrind", "--callgrind-out-file=/dev/null",
		"--toggle-collect=*durable/core.(*Session).Step", self, "replay-native", *rec)
	cmd.Env = append(os.Environ(), "GOMAXPROCS=1", "GOGC=400", "GODEBUG=asyncpreemptoff=1")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	var ir int64
	sc := bufio.NewScanner(stderr)
	var tail []string
	for sc.Scan() {
		tail = append(tail, sc.Text())
		if m := irLine.FindStringSubmatch(sc.Text()); m != nil {
			ir, _ = strconv.ParseInt(m[1], 10, 64)
		}
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("%w\n%s", err, strings.Join(tail, "\n"))
	}
	if ir == 0 {
		return fmt.Errorf("callgrind reported no instructions:\n%s", strings.Join(tail, "\n"))
	}
	//portlint:allow all count-gate report with no Pi counterpart: counts.mjs parses it, so member order and HTML escaping are not observed
	data, err := json.MarshalIndent(map[string]map[string]int64{"native/" + strconv.Itoa(*size): {"instructions": ir}}, "", "\t")
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", data)
	return nil
}
