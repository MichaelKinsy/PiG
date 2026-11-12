// Package clihook lets a fused Piglet member own the process command line: it runs before PiG parses its arguments,
// may serve the whole invocation (help, version, subcommands, a protocol on stdio) and may rewrite the arguments of a
// session run. It exists because a fused extension may not write os.Stdout, call os.Exit or os.Chdir.
package clihook

import (
	"io"
	"sync"
	"sync/atomic"
)

// Env is the process as the hook may use it.
type Env struct {
	Args           []string // os.Args[1:]
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Getenv         func(string) string
	Chdir          func(string) error
	StdinTTY       bool
	StdoutTTY      bool
}

// Result is what the hook decided.
type Result struct {
	Handled bool     // the hook served the invocation; PiG exits with Exit
	Exit    int      //
	Args    []string // PiG's arguments when not Handled
	// OwnPrintOutput: the run is a print run whose stdout the hook's extension writes itself (through Stdout). PiG's
	// print mode then writes nothing to stdout, and exits with ExitCode when that is not 0.
	OwnPrintOutput bool
}

// Hook is called once, before PiG's own command dispatch.
type Hook func(Env) Result

var (
	hook      atomic.Pointer[Hook]
	mu        sync.Mutex
	own       bool
	stdout    io.Writer
	exitCode  int
	exitCodeS bool
)

// Register installs the hook; a fused member calls it from init.
func Register(h Hook) { hook.Store(&h) }

// Registered returns the installed hook, or nil.
func Registered() Hook {
	if p := hook.Load(); p != nil {
		return *p
	}
	return nil
}

// SetOwnPrintOutput is called by PiG with Result.OwnPrintOutput.
func SetOwnPrintOutput(v bool) { mu.Lock(); own = v; mu.Unlock() }

// OwnPrintOutput reports whether the hook's extension writes the print run's stdout.
func OwnPrintOutput() bool { mu.Lock(); defer mu.Unlock(); return own }

// SetStdout is called by PiG's print mode with the raw (pre-takeover) stdout writer.
func SetStdout(w io.Writer) { mu.Lock(); stdout = w; mu.Unlock() }

// Stdout is the writer an extension prints a print run's output on (nil outside a print run that owns its output).
func Stdout() io.Writer { mu.Lock(); defer mu.Unlock(); return stdout }

// SetExitCode records the status the process exits with after a print run that succeeded in PiG's own terms.
func SetExitCode(c int) { mu.Lock(); exitCode, exitCodeS = c, true; mu.Unlock() }

// ExitCode returns the recorded status and whether one was recorded.
func ExitCode() (int, bool) { mu.Lock(); defer mu.Unlock(); return exitCode, exitCodeS }
