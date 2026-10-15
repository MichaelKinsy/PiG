// Command benchhost runs the durable-bench scenario (CONTRACT section 6: W1, W2, W4c, X1 and the H handoff rows) on the
// production Durable core packages as a native bench host. It is the command line impls/native.mjs documents:
//
//	benchhost --db <store, opened in place> --trace <statement trace> [--turns N] [--tools N] [--variant full|incr]
//	          [--payload-kb K] [--label L] [--recover]
//
// The core packages are event-driven and perform no I/O; this program is the host that gives them SQLite (modernc), applies
// each commit of a step in its own transaction, and writes a trace of every commit's statements for the gate to replay.
// CONTRACT_CRASH_AFTER=<seq> kills the process after the commit that consumed that sequence number.
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/MichaelKinsy/PiG/durable/core/contracttest"
	"github.com/MichaelKinsy/PiG/durable/core/contracttest/storehost"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "benchhost:", err)
		os.Exit(1)
	}
}

func run() error {
	db := flag.String("db", "", "store file, opened in place")
	traceFile := flag.String("trace", "", "statement trace to write")
	turns := flag.Int("turns", 0, "measured turns")
	tools := flag.Int("tools", 8, "lookups per measured turn")
	variant := flag.String("variant", "full", "scripted model variant (the answers are the same)")
	payloadKB := flag.Int("payload-kb", 0, "size of every lookup result in KiB (0: the 256 byte / 8 KiB mix)")
	label := flag.String("label", "m", "turn id prefix")
	recoverFlag := flag.Bool("recover", false, "finish the interrupted turn and stop")
	for _, name := range []string{"seed", "stream", "partial-ms", "fault", "window"} {
		flag.String(name, "", "outside the bench slice")
	}
	for _, name := range []string{"parallel", "compaction", "subagent"} {
		flag.Bool(name, false, "outside the bench slice")
	}
	flag.Parse()
	_ = variant
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "seed", "stream", "partial-ms", "fault", "window", "parallel", "compaction", "subagent":
			fmt.Fprintf(os.Stderr, "benchhost: --%s is outside the bench slice of this host\n", f.Name)
			os.Exit(3)
		}
	})
	if *db == "" || *traceFile == "" {
		return fmt.Errorf("--db and --trace are required")
	}
	now := 1800000000000.0
	if v := os.Getenv("DETENV_EPOCH"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return fmt.Errorf("DETENV_EPOCH: %w", err)
		}
		now = f
	}
	w, err := contracttest.NewWriter(*traceFile)
	if err != nil {
		return err
	}
	if err := w.Meta("benchhost", "native"); err != nil {
		return err
	}
	if err := w.Store("s0", *db); err != nil {
		return err
	}
	h, err := storehost.Open(*db, w)
	if err != nil {
		return err
	}
	e, live, err := openEngine(h, now, *payloadKB)
	if err != nil {
		return err
	}
	if *recoverFlag {
		if err := e.recover(live); err != nil {
			return err
		}
		if err := e.drive(w); err != nil {
			return err
		}
	}
	mem, err := contracttest.OpenMemoryLog(os.Getenv("CONTRACT_MEM"))
	if err != nil {
		return err
	}
	defer mem.Close()
	for i := 0; i < *turns; i++ {
		if err := e.turn(w, *label+strconv.Itoa(i), *tools); err != nil {
			return err
		}
		mem.Sample(i)
	}
	if err := h.Close(); err != nil {
		return err
	}
	return w.Close(0)
}
