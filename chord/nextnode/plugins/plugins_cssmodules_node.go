package plugins

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// No Pi file corresponds to this one: it is where Pi's `new Script(js).runInNewContext({ module, exports })` of css-modules.ts
// runs in PiG (see the note at the top of plugins_cssmodules.go).

// mappingScript is the program of the Node subprocess that evaluates mapping modules: one request per line on its input
// (`{"js": "<esbuild's mapping module, CommonJS>"}`), one reply per line on its output (`{"classes": "<JSON text>"}` or
// `{"error": "<text>"}`). It does what css-modules.ts does with the module: runs it alone in a new context, spreads its
// default export (an own, enumerable copy: a `__proto__` setter of the literal is not a key, integer-like keys come first)
// and serialises the copy with `JSON.stringify`, the text the facet's module embeds. It ends with its input.
const mappingScript = `const { Script } = require("node:vm");
const lines = require("node:readline").createInterface({ input: process.stdin });
lines.on("line", (line) => {
	let reply;
	try {
		const module = { exports: {} };
		new Script(JSON.parse(line).js).runInNewContext({ module, exports: module.exports });
		reply = { classes: JSON.stringify({ ...module.exports.default }) };
	} catch (error) {
		reply = { error: String(error) };
	}
	process.stdout.write(JSON.stringify(reply) + "\n");
});`

// mappingEvaluator evaluates mapping modules in one Node subprocess, started by the first evaluation and ended by close (or
// by a failed or abandoned evaluation: the next one starts another). It serves one evaluation at a time and is shared by the
// builds of a component. The process is the one NodeOptions starts Node with (discoverNode, Command).
type mappingEvaluator struct {
	options NodeOptions

	mu     sync.Mutex
	cmd    *exec.Cmd
	input  io.WriteCloser
	output *bufio.Reader
	stderr *tailWriter
	closed bool
	// killThrough, when set (a test), runs a kill of the process through it: it may hold the kill back.
	killThrough func(kill func())
}

// newMappingEvaluator is an evaluator that starts its process when it is first used.
func newMappingEvaluator(options NodeOptions) *mappingEvaluator {
	return &mappingEvaluator{options: options}
}

// mappingRequest is a line of the process's input.
type mappingRequest struct {
	JS string `json:"js"`
}

// mappingReply is a line of the process's output: the mapping as JSON text, or why the module did not evaluate.
type mappingReply struct {
	Classes *string `json:"classes"`
	Error   *string `json:"error"`
}

// evaluate runs the mapping module js and returns its mapping as `JSON.stringify` writes it. It is the cause of ctx when ctx
// ends before or during the evaluation (the process is killed: the next evaluation starts another). An error of the process (it
// did not start, died, did not answer) is this evaluation's alone, and so is a module that does not evaluate.
func (e *mappingEvaluator) evaluate(ctx context.Context, js string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return "", errors.New("the evaluator of CSS module mappings is closed")
	}
	if err := ctx.Err(); err != nil {
		return "", context.Cause(ctx)
	}
	reply, err := e.ask(ctx, js)
	if err != nil {
		tail := ""
		if e.stderr != nil {
			tail = e.stderr.text()
		}
		e.stop()
		switch {
		case ctx.Err() != nil:
			return "", context.Cause(ctx)
		case tail != "":
			return "", fmt.Errorf("%w: %s", err, tail)
		}
		return "", err
	}
	switch {
	case reply.Error != nil:
		return "", errors.New("evaluating the mapping: " + *reply.Error)
	case reply.Classes == nil:
		e.stop()
		return "", errors.New("the Node subprocess that evaluates CSS module mappings answered neither a mapping nor an error")
	}
	return *reply.Classes, nil
}

// close ends the process, if one started; later evaluations fail.
func (e *mappingEvaluator) close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
	e.stop()
}

// stop ends the process and waits for it. Its input ends and it is killed: it has no work in hand but the evaluation that
// failed, and ends with its input anyway.
func (e *mappingEvaluator) stop() {
	if e.cmd == nil {
		return
	}
	_ = e.input.Close()
	_ = e.cmd.Process.Kill()
	_ = e.cmd.Wait()
	e.cmd, e.input, e.output = nil, nil, nil
}

// timeout is how long finding Node, and each evaluation, may take: NodeOptions.ReadyTimeout.
func (e *mappingEvaluator) timeout() time.Duration {
	if e.options.ReadyTimeout != nil {
		return *e.options.ReadyTimeout
	}
	return defaultReadyTimeout
}

// start starts the process. It outlives ctx, which only bounds finding Node: close or a failed evaluation ends it.
func (e *mappingEvaluator) start(ctx context.Context) error {
	timeout := e.timeout()
	finding, stopFinding := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("finding a Node.js executable did not finish within %s", timeout))
	nodePath, err := discoverNode(finding, e.options.Command)
	stopFinding()
	if err != nil {
		return err
	}
	command := e.options.Command
	if command == nil {
		command = exec.CommandContext
	}
	cmd := command(context.WithoutCancel(ctx), nodePath, "-e", mappingScript)
	e.stderr = &tailWriter{}
	cmd.Stderr = e.stderr
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting node: %w", err)
	}
	e.cmd, e.input, e.output = cmd, input, bufio.NewReader(output)
	return nil
}

// ask sends js to the process (started if it is not) and reads its reply. A ctx that ends, or a process that does not answer
// within the timeout, kills the process, which ends the read.
func (e *mappingEvaluator) ask(ctx context.Context, js string) (mappingReply, error) {
	if e.cmd == nil {
		if err := e.start(ctx); err != nil {
			return mappingReply{}, err
		}
	}
	request, err := json.Marshal(mappingRequest{JS: js})
	if err != nil {
		return mappingReply{}, err
	}
	timeout := e.timeout()
	cmd := e.cmd
	// A kill runs on a goroutine of its own, and may be late: it only counts while this ask is, so it never lands on the process
	// of the next one. A process killed during the ask is ended before ask returns, whatever the ask got.
	var guard sync.Mutex
	live, killed, silenced := true, false, false
	kill := func(byTimer bool) func() {
		do := func() {
			guard.Lock()
			defer guard.Unlock()
			if live {
				killed, silenced = true, silenced || byTimer
				_ = cmd.Process.Kill()
			}
		}
		return func() {
			if e.killThrough != nil {
				e.killThrough(do)
				return
			}
			do()
		}
	}
	silent := time.AfterFunc(timeout, kill(true))
	stopWatching := context.AfterFunc(ctx, kill(false))
	defer func() {
		silent.Stop()
		stopWatching()
		guard.Lock()
		live = false
		ended := killed
		guard.Unlock()
		if ended {
			e.stop()
		}
	}()
	if _, err := e.input.Write(append(request, '\n')); err != nil {
		return mappingReply{}, fmt.Errorf("the Node subprocess that evaluates CSS module mappings did not take the module: %w", err)
	}
	line, err := readLine(e.output, maxFrame)
	if err != nil {
		guard.Lock()
		timedOut := silenced
		guard.Unlock()
		if timedOut {
			return mappingReply{}, fmt.Errorf("the Node subprocess that evaluates CSS module mappings did not answer within %s", timeout)
		}
		return mappingReply{}, fmt.Errorf("the Node subprocess that evaluates CSS module mappings ended: %w", err)
	}
	var reply mappingReply
	if err := json.Unmarshal(line, &reply); err != nil {
		return mappingReply{}, fmt.Errorf("the Node subprocess that evaluates CSS module mappings answered %q: %w", line, err)
	}
	return reply, nil
}

// readLine reads one line of r (without its end), at most limit bytes.
func readLine(r *bufio.Reader, limit int) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > limit {
			return nil, fmt.Errorf("a line longer than %d bytes", limit)
		}
		if err == nil {
			return line[:len(line)-1], nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, err
		}
	}
}
