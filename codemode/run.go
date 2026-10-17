//go:build !tinygo

package codemode

import (
	"context"
	"errors"
	"math"
)

// Run is a running Sandbox.Start execution.
//
// Ports packages/codemode/src/types.ts (CodemodeRun).
type Run struct {
	execution *execution
}

// Result waits for the run to end and returns its outcome. A script failure is a Result with OK false.
func (r *Run) Result() Result {
	<-r.execution.exited
	return r.execution.result
}

// Finished reports whether the run has its outcome: the script ended, timed out or failed, or the run was aborted. It is
// true as soon as that is decided, before Result returns, and calls can no longer be answered. It does not mean the VM
// has exited.
//
// Ports packages/codemode/src/types.ts (CodemodeRun.finished).
func (r *Run) Finished() bool { return r.execution.isFinished() }

// Deliver answers call id as result number seq. Results are numbered 1, 2, ... without gaps, and the VM applies them
// strictly in that order, whatever order they arrive in: after a gap it waits for the missing number. While it processes
// this one, its clock reads now (ms since the epoch), or the previous value if now is earlier. It fails for a call that is
// not waiting for an answer, a seq used before, a now that is not finite and an answer JSON cannot hold; the call then
// still waits. After the run ended it does nothing.
//
// Ports packages/codemode/src/types.ts (CodemodeRun.deliver).
func (r *Run) Deliver(seq int, now float64, id int, outcome Outcome) error {
	return r.execution.deliver(seq, now, id, outcome)
}

// Start runs code deterministically with the caller answering its calls: the same script, start time, seed, tools,
// globals and store, answered with the same results in the same seq order, makes the same calls with the same arguments
// and produces the same output, on any VM with the same fingerprint. A host can therefore replay it after a restart. The
// VM runs in UTC, its clock reads StartedAt and then the now of the result being processed, its randomness comes from
// Seed, and WeakRef and FinalizationRegistry do not exist.
//
// The sandbox's own tools and globals are not used: the run declares its own, and every call goes to options.OnCall. Script
// failures are in the Result; cancelling ctx ends the run with an error the script cannot catch (kind aborted, the context's
// cause as the message). Start returns at once; Result waits for the run.
//
// Ports packages/codemode/src/runtime/execution.ts (CodemodeSandboxBase.start).
func (s *Sandbox) Start(ctx context.Context, code string, options RunOptions) (*Run, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, errSandboxClosed
	}
	if math.IsNaN(options.StartedAt) || math.IsInf(options.StartedAt, 0) || options.StartedAt < 0 {
		return nil, errors.New("startedAt must be a non-negative number of milliseconds since the epoch")
	}
	names := map[string]bool{}
	scriptNames := map[string]bool{}
	tools := make([]VmStartTool, len(options.Tools))
	for i, tool := range options.Tools {
		if names[tool.Name] {
			return nil, errors.New("Tool names must be unique")
		}
		names[tool.Name] = true
		jsName := tool.JSName
		if jsName == "" {
			jsName = ToCodemodeIdentifier(tool.Name)
		}
		scriptNames[jsName] = true
		tools[i] = VmStartTool{tool.Name, jsName, tool.Description}
	}
	if len(scriptNames) != len(options.Tools) {
		return nil, errors.New("Tool script names must be unique")
	}
	globalNames := make([]string, len(options.Globals))
	globals := make([]VmStartGlobal, len(options.Globals))
	for i, global := range options.Globals {
		globalNames[i] = global.Name
		globals[i] = VmStartGlobal{Name: global.Name, Spread: global.Spread} //nolint:staticcheck // S1016: the interface ledger's P1 rule closes a member row only on a field read (autobind detect.go exercised), which a struct conversion hides
	}
	if err := checkGlobals(globalNames); err != nil {
		return nil, err
	}
	timeoutMs := math.Inf(1)
	if options.TimeoutMs != nil {
		timeoutMs = *options.TimeoutMs
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errSandboxClosed
	}
	e := newExecution(s, code, timeoutMs, options.Store, nil, nil)
	e.deterministic = &VmDeterministic{StartedAt: options.StartedAt, Seed: options.Seed}
	e.control = &options
	e.startTools, e.startGlobals = tools, globals
	e.delivered = map[int]bool{}
	s.running[e] = struct{}{}
	s.mu.Unlock()
	//portlint:allow asyncorder execution.ts:530 start returns the CodemodeRun at once and the script runs on; Run.Result waits on exited, and calls and results are ordered by seq
	go func() {
		e.run(ctx)
		s.mu.Lock()
		delete(s.running, e)
		s.mu.Unlock()
	}()
	return &Run{execution: e}, nil
}
