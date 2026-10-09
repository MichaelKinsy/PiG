package daemon

// Ports packages/env/daemon/src/exec.rs
//
// `exec` is Durable's NodeExecutionEnv.exec (docs/semantics.md), which the Rust daemon re-implements; this daemon
// runs it through durable/env/node and streams its output as `output` events.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/env/node"
)

// unsentPoll is how often a delivery waiting for the link to drain looks again.
const unsentPoll = 20 * time.Millisecond

type spillRequest struct {
	afterBytes int64
	afterLines int64
}

type execRequest struct {
	command    *string
	argv       []string
	hasArgv    bool
	cwd        string
	env        map[string]string
	inheritEnv bool
	shellPath  string
	timeout    *float64
	spill      *spillRequest
	window     *window
}

func parseExecRequest(request Object) (*execRequest, *Failure) {
	result := &execRequest{inheritEnv: true, env: map[string]string{}}
	if command, ok := request["command"].(string); ok {
		result.command = &command
	}
	if items, ok := request["argv"].([]any); ok {
		result.hasArgv = true
		for _, item := range items {
			text, isString := item.(string)
			if !isString {
				result.hasArgv = false
				result.argv = nil
				break
			}
			result.argv = append(result.argv, text)
		}
	}
	cwd, ok := request["cwd"].(string)
	if !ok {
		return nil, newFailure("EINVAL", "exec needs cwd")
	}
	result.cwd = cwd
	if environment, ok := request["env"].(Object); ok {
		for key, value := range environment {
			if text, isString := value.(string); isString {
				result.env[key] = text
			}
		}
	}
	if inherit, ok := request["inheritEnv"].(bool); ok {
		result.inheritEnv = inherit
	}
	result.shellPath, _ = request["shellPath"].(string)
	if milliseconds, ok := request["timeoutMs"].(float64); ok {
		seconds := milliseconds / 1000
		result.timeout = &seconds
	}
	if spill, ok := request["spill"].(Object); ok {
		afterBytes, okBytes := unsignedNumber(spill["afterBytes"])
		afterLines, okLines := unsignedNumber(spill["afterLines"])
		if okBytes && okLines {
			result.spill = &spillRequest{int64(min(afterBytes, 1<<53)), int64(min(afterLines, 1<<53))}
		}
	}
	result.window = windowFromJSON(request["window"])
	return result, nil
}

func outputFrame(id uint32, stream, text string, skip *skipped) *Frame {
	value := Object{"kind": "output", "stream": stream}
	if skip != nil {
		value["skipped"] = Object{"bytes": skip.bytes, "newlines": skip.newlines, "endsWithNewline": skip.endsWithNewline}
	}
	return &Frame{Kind: FrameEvent, ID: id, JSON: value, Payload: []byte(text)}
}

// windowedOutput holds command output and delivers it paced like the caller's commits.
type windowedOutput struct {
	out    *output
	id     uint32
	mu     sync.Mutex
	held   *pending
	unsent atomic.Int64
	wake   chan struct{}
}

func (w *windowedOutput) push(stream, text string) {
	w.mu.Lock()
	w.held.push(stream, text)
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// deliver sends what is held when the caller's pace allows it and no earlier frame is unsent; force sends it anyway.
func (w *windowedOutput) deliver(force bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.held.isEmpty() || (!force && (time.Now().Before(w.held.nextSend) || w.unsent.Load() > 0)) {
		return
	}
	for _, event := range w.held.take() {
		w.out.sendBulk(outputFrame(w.id, event.stream, event.text, event.skipped), &w.unsent)
	}
}

// run delivers until stop is closed.
func (w *windowedOutput) run(stop <-chan struct{}) {
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		w.mu.Lock()
		wait := time.Hour
		if !w.held.isEmpty() {
			if w.unsent.Load() > 0 {
				wait = unsentPoll
			} else {
				wait = max(time.Until(w.held.nextSend), 0)
			}
		}
		w.mu.Unlock()
		timer.Reset(wait)
		select {
		case <-stop:
			return
		case <-w.wake:
		case <-timer.C:
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		w.deliver(false)
	}
}

// runExec runs one command, sending output events for request id, until it settles.
func (s *server) runExec(id uint32, request *execRequest, ctl *control) (Object, *Failure) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctl.attach(cancel)

	environment := node.NewNodeExecutionEnv(node.NodeExecutionEnvOptions{Cwd: request.cwd, ShellPath: request.shellPath})
	s.trackRun(environment)
	defer s.untrackRun(environment)

	options := &durableenv.ShellExecOptions{
		Cwd:        request.cwd,
		Env:        request.env,
		InheritEnv: &request.inheritEnv,
		Timeout:    request.timeout,
	}
	if request.spill != nil {
		options.Spill = &durableenv.ShellSpillOptions{AfterBytes: request.spill.afterBytes, AfterLines: request.spill.afterLines}
	}
	var windowed *windowedOutput
	var outputs sync.WaitGroup
	stopOutput := make(chan struct{})
	if request.window != nil {
		windowed = &windowedOutput{out: s.out, id: id, held: newPending(*request.window), wake: make(chan struct{}, 1)}
		outputs.Go(func() { windowed.run(stopOutput) })
		options.OnOutput = func(_ context.Context, text string, info durableenv.ShellOutputInfo) {
			windowed.push(string(info.Stream), text)
		}
	} else {
		options.OnOutput = func(callContext context.Context, text string, info durableenv.ShellOutputInfo) {
			s.out.sendBulk(outputFrame(id, string(info.Stream), text, nil), nil)
			// Without a window, output is not coalesced; reading then waits for the link.
			s.out.waitForRoom(callContext.Done())
		}
	}

	// A kill without abort, as cleanup() does: the command settles with the killed process's status. The command may
	// not have started yet, so the kill repeats until the command settles.
	settled := make(chan struct{})
	var killer sync.WaitGroup
	killer.Go(func() {
		select {
		case <-ctl.kill:
		case <-settled:
			return
		}
		for {
			_ = environment.Cleanup(ctx)
			select {
			case <-settled:
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	})

	var command any
	switch {
	case request.command != nil:
		command = *request.command
	case request.hasArgv:
		command = request.argv
	default:
		close(settled)
		killer.Wait()
		close(stopOutput)
		outputs.Wait()
		return nil, newFailure("EINVAL", "exec needs command or argv")
	}
	result, err := environment.Exec(ctx, command, options)
	close(settled)
	killer.Wait()
	close(stopOutput)
	outputs.Wait()
	if windowed != nil {
		windowed.deliver(true)
	}
	if err != nil {
		return nil, execFailure(err)
	}
	value := Object{"exitCode": result.ExitCode}
	if result.SpillPath != "" {
		value["spillPath"] = result.SpillPath
	}
	return value, nil
}

// execFailure is an ExecutionError as an error frame: its code and, for a timeout or abort after the output crossed
// the spill thresholds, the spill file.
func execFailure(err error) *Failure {
	if executionError, ok := errors.AsType[*durableenv.ExecutionError](err); ok {
		failure := newFailure(string(executionError.Code), executionError.Message)
		if executionError.SpillPath != "" {
			failure.Extra = Object{"spillPath": executionError.SpillPath}
		}
		return failure
	}
	return newFailure("unknown", err.Error())
}
