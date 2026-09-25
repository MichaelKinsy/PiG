// Package buildprogress reports product-neutral diagnostics for explicit builds.
package buildprogress

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"unicode/utf8"
)

// Event describes work that has started, or a line emitted by its toolchain.
type Event struct {
	Phase  string
	Step   string
	Member string
	Output string
}

type observerKey struct{}
type memberKey struct{}
type observer struct {
	notify  func(Event)
	verbose bool
}

// Observe selects build diagnostics for this operation only. Callbacks are synchronous.
// pig additive (D18): only explicit build callers select progress reporting.
func Observe(ctx context.Context, notify func(Event), verbose bool) context.Context {
	return context.WithValue(ctx, observerKey{}, observer{notify, verbose})
}

// Enabled reports whether this operation has a progress observer.
func Enabled(ctx context.Context) bool {
	_, ok := ctx.Value(observerKey{}).(observer)
	return ok
}

// Verbose reports whether this build requests raw toolchain output.
func Verbose(ctx context.Context) bool {
	o, _ := ctx.Value(observerKey{}).(observer)
	return o.verbose
}

// Member labels toolchain output with the actual member or packed member set.
func Member(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, memberKey{}, name)
}

// Phase reports a phase immediately before its work begins.
func Phase(ctx context.Context, phase, step string) {
	if o, ok := ctx.Value(observerKey{}).(observer); ok {
		o.notify(Event{Phase: phase, Step: step})
	}
}

// ToolArgs changes diagnostic verbosity only; it does not change build inputs.
func ToolArgs(ctx context.Context, language string, args []string) []string {
	if !Verbose(ctx) {
		return args
	}
	args = slices.Clone(args)
	if language == "rust" {
		return slices.DeleteFunc(args, func(arg string) bool { return arg == "--quiet" })
	}
	if language == "go" && len(args) > 0 && args[0] == "build" {
		return append([]string{"build", "-v"}, args[1:]...)
	}
	return args
}

// CombinedOutput preserves the compiler diagnostics used by build failure classification while streaming them to the selected observer.
func CombinedOutput(ctx context.Context, cmd *exec.Cmd) ([]byte, error) {
	_, ok := ctx.Value(observerKey{}).(observer)
	if !ok {
		return cmd.CombinedOutput()
	}
	var output bytes.Buffer
	err := runWithOutput(ctx, cmd, &output)
	return output.Bytes(), err
}

// Run streams toolchain output and retains only its diagnostic tail on failure.
func Run(ctx context.Context, cmd *exec.Cmd) error {
	var tail outputTail
	if err := runWithOutput(ctx, cmd, &tail); err != nil {
		return fmt.Errorf("%w\n%s", err, tail)
	}
	return nil
}

func runWithOutput(ctx context.Context, cmd *exec.Cmd, output io.Writer) error {
	writer := output
	if o, ok := ctx.Value(observerKey{}).(observer); ok {
		member, _ := ctx.Value(memberKey{}).(string)
		lines := &toolLines{emit: func(line string) { o.notify(Event{Member: member, Output: line}) }}
		writer = io.MultiWriter(output, lines)
		defer lines.flush()
	}
	cmd.Stdout, cmd.Stderr = writer, writer
	return cmd.Run()
}

// A newline-free compiler diagnostic must not grow the streaming line buffer without bound.
const maxLine = 4096

type toolLines struct {
	pending string
	emit    func(string)
}

func (w *toolLines) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		take := min(len(p), maxLine-len(w.pending))
		w.pending += string(p[:take])
		p = p[take:]
		for {
			line, rest, ok := strings.Cut(w.pending, "\n")
			if !ok {
				break
			}
			w.emit(line + "\n")
			w.pending = rest
		}
		if len(w.pending) == maxLine {
			cut := len(w.pending)
			for i := cut - 1; i >= max(0, cut-utf8.UTFMax); i-- {
				if utf8.RuneStart(w.pending[i]) {
					if !utf8.FullRuneInString(w.pending[i:]) {
						cut = i
					}
					break
				}
			}
			w.emit(w.pending[:cut] + "\n")
			w.pending = w.pending[cut:]
		}
	}
	return n, nil
}

func (w *toolLines) flush() {
	if w.pending != "" {
		w.emit(w.pending + "\n")
		w.pending = ""
	}
}
