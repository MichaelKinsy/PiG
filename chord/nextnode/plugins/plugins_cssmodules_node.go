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

// mappingEvaluator evaluates mapping modules in one Node subprocess, started by the first and ended by close. It serves one
// evaluation at a time. The process is the one NodeOptions starts Node with (discoverNode, Command), ended with ctx.
type mappingEvaluator struct {
	options NodeOptions
	ctx     context.Context

	mu     sync.Mutex
	cmd    *exec.Cmd
	input  io.WriteCloser
	output *bufio.Reader
	stderr tailWriter
	// failed is why the process cannot evaluate any more (it did not start, died or lost ctx): every later evaluation fails
	// with it.
	failed error
	// aborted is set when ctx ended an evaluation: the build is aborted, not failed.
	aborted bool
}

// newMappingEvaluator is an evaluator that starts its process when it is first used.
func newMappingEvaluator(options NodeOptions, ctx context.Context) *mappingEvaluator {
	return &mappingEvaluator{options: options, ctx: ctx}
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
// ended the evaluation, and an error of the process (it did not start, died, did not answer) for this and every later one; a
// module that does not evaluate is that module's error alone.
func (e *mappingEvaluator) evaluate(js string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.failed != nil {
		return "", e.failed
	}
	reply, err := e.ask(js)
	if err != nil {
		if e.ctx.Err() != nil {
			e.aborted = true
			err = context.Cause(e.ctx)
		} else if tail := e.stderr.text(); tail != "" {
			err = fmt.Errorf("%w: %s", err, tail)
		}
		e.failed = err
		e.stop()
		return "", err
	}
	switch {
	case reply.Error != nil:
		return "", errors.New("evaluating the mapping: " + *reply.Error)
	case reply.Classes == nil:
		e.failed = errors.New("the Node subprocess that evaluates CSS module mappings answered neither a mapping nor an error")
		e.stop()
		return "", e.failed
	}
	return *reply.Classes, nil
}

// abort is the cause of ctx when it ended an evaluation, else nil: what a build that read a CSS module returns instead of a
// failed result.
func (e *mappingEvaluator) abort() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.aborted {
		return context.Cause(e.ctx)
	}
	return nil
}

// close ends the process, if one started.
func (e *mappingEvaluator) close() {
	e.mu.Lock()
	defer e.mu.Unlock()
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

// start starts the process.
func (e *mappingEvaluator) start() error {
	timeout := e.timeout()
	finding, stopFinding := context.WithTimeoutCause(e.ctx, timeout, fmt.Errorf("finding a Node.js executable did not finish within %s", timeout))
	nodePath, err := discoverNode(finding, e.options.Command)
	stopFinding()
	if err != nil {
		return err
	}
	command := e.options.Command
	if command == nil {
		command = exec.CommandContext
	}
	cmd := command(e.ctx, nodePath, "-e", mappingScript)
	cmd.Stderr = &e.stderr
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

// ask sends js to the process (started if it is not) and reads its reply.
func (e *mappingEvaluator) ask(js string) (mappingReply, error) {
	if err := e.ctx.Err(); err != nil {
		return mappingReply{}, err
	}
	if e.cmd == nil {
		if err := e.start(); err != nil {
			return mappingReply{}, err
		}
	}
	request, err := json.Marshal(mappingRequest{JS: js})
	if err != nil {
		return mappingReply{}, err
	}
	// A process that does not answer is killed, which ends the read.
	timeout := e.timeout()
	cmd := e.cmd
	silent := time.AfterFunc(timeout, func() { _ = cmd.Process.Kill() })
	defer silent.Stop()
	if _, err := e.input.Write(append(request, '\n')); err != nil {
		return mappingReply{}, fmt.Errorf("the Node subprocess that evaluates CSS module mappings did not take the module: %w", err)
	}
	line, err := readLine(e.output, maxFrame)
	if err != nil {
		if !silent.Stop() {
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
