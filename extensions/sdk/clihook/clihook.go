// Package clihook lets a fused Piglet member own the process command line: it runs before PiG parses its arguments,
// may serve the whole invocation (help, version, subcommands, a protocol on stdio), may rewrite the arguments of a
// session run, may set the default retry config, and may own the output and the exit status of a print run. It exists
// because a fused extension may not write os.Stdout, call os.Exit or os.Chdir, and because a Piglet cannot supply it:
// it needs os.Args before PiG parses them (record D653).
//
// A member calls Register from init, and, in a print run it owns the output of, Stdout and SetExitCode from its
// extension. The remaining functions (Registered, SetStdout, ExitCode) are PiG's own wiring: a member does not call them.
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

// RetryFailure describes one failed model request to RetryDecider: Attempt is the 1-based retry being considered,
// MaxRetries is the resolved retry budget (after settings.json `retry`), ErrorMessage is the assistant error text,
// Status is the HTTP response status (0 when no response arrived), Headers holds the lower-cased response headers when
// available, and StreamStarted reports whether any stream event was emitted before the failure.
type RetryFailure struct {
	Attempt       int
	MaxRetries    int
	ErrorMessage  string
	Status        int
	Headers       map[string]string
	StreamStarted bool
}

// RetryDecision is what RetryDecider decides for one failed model request.
type RetryDecision struct {
	Retry   bool
	DelayMs int
}

// RetryDecider classifies a failed model request and chooses the delay before the retry.
type RetryDecider func(RetryFailure) RetryDecision

// RetryPolicy is the agent-level retry policy the hook asks PiG to use when the user's settings name none: the number of
// retries of a failed model request, the delay before the first one (it doubles each time) and the cap on one delay.
// settings.json `retry` still wins over the numeric fields. Decide, when non-nil, classifies failures and selects
// per-attempt delays in place of PiG's default classifier and exponential formula, and Silent suppresses PiG's built-in
// interactive retry status indicator and final "Retry failed after ..." error line when the piglet renders its own.
type RetryPolicy struct {
	MaxRetries      int
	BaseDelayMs     int
	MaxAgentDelayMs int
	Decide          RetryDecider
	Silent          bool
}

// Result is what the hook decided.
type Result struct {
	Handled bool // the hook served the invocation; PiG parses nothing and exits with Exit
	Exit    int  // the process status when Handled
	// Args is what PiG parses when the hook did not serve the invocation. A nil Args is an empty command line, not
	// "unchanged": return Env.Args to leave the command line as it is.
	Args []string
	// Retry, when set, replaces PiG's built-in retry defaults (3 retries, 2 s base, 60 s cap) for a session run.
	Retry *RetryPolicy
	// HTTPIdleTimeoutMs, when non-nil, replaces PiG's default HTTP idle timeout (300 s) when settings.json names none.
	HTTPIdleTimeoutMs *int
	// OwnPrintOutput: the run is a print run whose stdout the hook's extension writes itself (through Stdout). PiG's
	// print mode then writes nothing to stdout (text or JSON), leaves the words of a failed run's stderr line to the
	// extension, and exits with ExitCode when that is not 0 and the run succeeded in PiG's own terms.
	OwnPrintOutput bool
}

// Hook is called once, before PiG's own command dispatch.
type Hook func(Env) Result

var (
	hook      atomic.Pointer[Hook]
	mu        sync.Mutex
	stdout    io.Writer
	exitCode  int
	exitCodeS bool
)

// Register installs the hook; a fused member calls it from init. A second Register replaces the first, and a nil Hook
// removes it.
func Register(h Hook) { hook.Store(&h) }

// Registered returns the installed hook, or nil. PiG calls it once, before it parses the command line.
func Registered() Hook {
	if p := hook.Load(); p != nil {
		return *p
	}
	return nil
}

// SetStdout is PiG's wiring: its print mode calls it with the raw (pre-takeover) stdout writer of a run whose
// Result.OwnPrintOutput is set. It is exported only because PiG's coding/cli is another package; a member does not call it.
func SetStdout(w io.Writer) { mu.Lock(); stdout = w; mu.Unlock() }

// Stdout is the writer an extension prints a print run's output on: nil outside a print run that owns its output.
func Stdout() io.Writer { mu.Lock(); defer mu.Unlock(); return stdout }

// SetExitCode records the status the process exits with after a print run that succeeded in PiG's own terms. A run that
// failed exits with PiG's 1 whatever is recorded, and a recorded 0 changes nothing.
func SetExitCode(c int) { mu.Lock(); exitCode, exitCodeS = c, true; mu.Unlock() }

// ExitCode returns the recorded status and whether one was recorded. PiG's wiring: it reads it after a print run.
func ExitCode() (int, bool) { mu.Lock(); defer mu.Unlock(); return exitCode, exitCodeS }
