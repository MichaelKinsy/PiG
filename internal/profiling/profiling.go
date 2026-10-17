// SPDX-License-Identifier: MIT

// Package profiling writes Go runtime profiles when PIG_PROFILE names them.
//
// With PIG_PROFILE unset, Start performs one environment lookup and returns a
// no-op, so release binaries pay nothing for the capability. With it set, pig
// profiles the whole process and writes the files on exit:
//
//	PIG_PROFILE=cpu,heap pig -p "hello"
//	PIG_PROFILE=trace PIG_PROFILE_DIR=/tmp/pig-profile pig
//
// Kinds are cpu, heap, allocs, block, mutex, goroutine, trace, metrics, and flight.
// Files go to PIG_PROFILE_DIR (default: the current directory) as
// pig-<pid>-<kind>.pprof, pig-<pid>.trace for the execution trace, or
// pig-<pid>-metrics.json for the whole-process resource counts of
// [ProcessMetrics]. Read the profiles with `go tool pprof` and `go tool trace`,
// or run `make profile` from a source checkout.
//
// flight keeps the last seconds of the execution trace in memory (a trace.FlightRecorder) and writes nothing at
// exit: DumpFlight writes the window when something goes wrong, which the exit record does beside the crash output file
// when the process ends without a user request. It costs the trace's steady overhead while it runs, so it is off unless
// named.
package profiling

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
	"slices"
	"strings"
	"sync"
	"time"
)

// Kinds lists the accepted PIG_PROFILE values in the order they are written.
var Kinds = []string{"cpu", "heap", "allocs", "block", "mutex", "goroutine", "trace", "metrics", "flight"}

// Start begins the profiles named by PIG_PROFILE and returns the function that
// stops them and writes every file. The returned function is safe to call more
// than once; only the first call writes.
func Start() (stop func()) {
	spec := os.Getenv("PIG_PROFILE")
	if spec == "" {
		return func() {}
	}
	return StartWith(spec, os.Getenv("PIG_PROFILE_DIR"), os.Stderr)
}

// StartWith is Start with explicit inputs. Problems are reported to diag and
// never stop pig: a profile that cannot be written is skipped.
func StartWith(spec, dir string, diag io.Writer) (stop func()) {
	if dir == "" {
		dir = "."
	}
	kinds := map[string]bool{}
	for kind := range strings.SplitSeq(spec, ",") {
		kind = strings.TrimSpace(kind)
		if kind == "" {
			continue
		}
		if !slices.Contains(Kinds, kind) {
			warnf(diag, "pig: ignoring unknown PIG_PROFILE kind %q (valid: %s)\n", kind, strings.Join(Kinds, ", "))
			continue
		}
		kinds[kind] = true
	}
	if len(kinds) == 0 {
		return func() {}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		warnf(diag, "pig: PIG_PROFILE_DIR: %v\n", err)
		return func() {}
	}
	prefix := filepath.Join(dir, fmt.Sprintf("pig-%d", os.Getpid()))
	var cpuFile, traceFile *os.File
	if kinds["cpu"] {
		cpuFile = create(prefix+"-cpu.pprof", diag)
		if cpuFile != nil {
			if err := pprof.StartCPUProfile(cpuFile); err != nil {
				warnf(diag, "pig: cpu profile: %v\n", err)
				_ = cpuFile.Close() // The profile never started; the empty file carries nothing.
				cpuFile = nil
			}
		}
	}
	if kinds["trace"] {
		traceFile = create(prefix+".trace", diag)
		if traceFile != nil {
			if err := trace.Start(traceFile); err != nil {
				warnf(diag, "pig: trace: %v\n", err)
				_ = traceFile.Close() // The trace never started; the empty file carries nothing.
				traceFile = nil
			}
		}
	}
	if kinds["block"] {
		runtime.SetBlockProfileRate(1)
	}
	if kinds["mutex"] {
		runtime.SetMutexProfileFraction(1)
	}
	if kinds["flight"] {
		startFlight(diag)
	}
	startFDs := int64(-1)
	if kinds["metrics"] {
		startFDs = openFDs()
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			stopFlight()
			var written []string
			if cpuFile != nil {
				pprof.StopCPUProfile()
				if err := cpuFile.Close(); err != nil {
					warnf(diag, "pig: cpu profile: %v\n", err)
				} else {
					written = append(written, cpuFile.Name())
				}
			}
			if traceFile != nil {
				trace.Stop()
				if err := traceFile.Close(); err != nil {
					warnf(diag, "pig: trace: %v\n", err)
				} else {
					written = append(written, traceFile.Name())
				}
			}
			for _, kind := range []string{"heap", "allocs", "block", "mutex", "goroutine"} {
				if !kinds[kind] {
					continue
				}
				if kind == "heap" || kind == "allocs" {
					collectRetained() // Report objects as of exit, not the last GC cycle: the allocation counts lag one cycle behind otherwise.
				}
				path := prefix + "-" + kind + ".pprof"
				if f := create(path, diag); f != nil {
					err := pprof.Lookup(kind).WriteTo(f, 0)
					if closeErr := f.Close(); err == nil {
						err = closeErr
					}
					if err != nil {
						warnf(diag, "pig: %s profile: %v\n", kind, err)
					} else {
						written = append(written, path)
					}
				}
			}
			// The metrics record is taken last, so it counts the work of writing the other profiles; gates request it alone.
			if kinds["metrics"] {
				record := CollectMetrics() // Before the file opens, so the descriptor count is the process's own.
				record.OpenFDsAtStart = startFDs
				path := prefix + "-metrics.json"
				if f := create(path, diag); f != nil {
					err := writeMetrics(f, record)
					if closeErr := f.Close(); err == nil {
						err = closeErr
					}
					if err != nil {
						warnf(diag, "pig: metrics: %v\n", err)
					} else {
						written = append(written, path)
					}
				}
			}
			for _, path := range written {
				warnf(diag, "pig: wrote %s\n", path)
			}
		})
	}
}

// flightWindow and flightBytes size the flight recorder: the last 10 seconds of the trace, within 8 MiB.
const (
	flightWindow = 10 * time.Second
	flightBytes  = 8 << 20
)

var (
	flightMu       sync.Mutex
	flightRecorder *trace.FlightRecorder
)

func startFlight(diag io.Writer) {
	flightMu.Lock()
	defer flightMu.Unlock()
	if flightRecorder != nil {
		return
	}
	recorder := trace.NewFlightRecorder(trace.FlightRecorderConfig{MinAge: flightWindow, MaxBytes: flightBytes})
	if err := recorder.Start(); err != nil {
		warnf(diag, "pig: flight recorder: %v\n", err)
		return
	}
	flightRecorder = recorder
}

func stopFlight() {
	flightMu.Lock()
	defer flightMu.Unlock()
	if flightRecorder != nil {
		flightRecorder.Stop()
		flightRecorder = nil
	}
}

// FlightActive reports whether PIG_PROFILE started the flight recorder.
func FlightActive() bool {
	flightMu.Lock()
	defer flightMu.Unlock()
	return flightRecorder != nil
}

// DumpFlight writes the flight recorder's window to path. It does nothing, and returns nil, when the recorder is not
// running. The file is created with mode 0600: a trace carries goroutine activity of the whole process.
func DumpFlight(path string) error {
	flightMu.Lock()
	recorder := flightRecorder
	flightMu.Unlock()
	if recorder == nil {
		return nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = recorder.WriteTo(file)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}

func create(path string, diag io.Writer) *os.File {
	f, err := os.Create(path)
	if err != nil {
		warnf(diag, "pig: %v\n", err)
		return nil
	}
	return f
}

// warnf reports a best-effort diagnostic. A failed diagnostic write has no
// better channel, so it is dropped.
func warnf(diag io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(diag, format, args...)
}

// collectRetained runs two garbage collections, so a heap measurement taken after it counts only memory the process retains. The first cycle moves every sync.Pool's contents to the pool's victim cache, which keeps them reachable until the next cycle; after a single collection, buffers that pools hold for reuse (encoding/json's encoder buffer, as large as the largest document encoded) count as live.
func collectRetained() {
	runtime.GC()
	runtime.GC()
}
