package plugins

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Not upstream's tests (css-modules.ts runs the mapping module in its own process): PiG runs it in a Node subprocess
// (plugins_cssmodules_node.go), which these check against a second Node program that does what Pi does with the module and
// compares the text of the mapping the facet embeds.

// oracleScript is a Node program that takes `{"js": "<mapping module>", "classes": "<mapping text>"}` per line and answers
// `{"expected": "<Pi's mapping text>", "same": <text equal>, "ordered": <keys of the text in the order of the object>}`.
const oracleScript = `const vm = require("node:vm");
require("node:readline").createInterface({ input: process.stdin }).on("line", (line) => {
	const { js, classes } = JSON.parse(line);
	const sandbox = { module: { exports: {} } };
	sandbox.exports = sandbox.module.exports;
	vm.runInNewContext(js, sandbox);
	const object = { ...sandbox.module.exports.default };
	const expected = JSON.stringify(object);
	const same = expected === classes;
	const ordered = JSON.stringify(Object.keys(JSON.parse(classes))) === JSON.stringify(Object.keys(object));
	process.stdout.write(JSON.stringify({ expected, same, ordered }) + "\n");
});`

// nodeOracle is a running oracleScript.
type nodeOracle struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	output *bufio.Reader
}

func startNodeOracle(t testing.TB) *nodeOracle {
	t.Helper()
	path, err := discoverNode(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(path, "-e", oracleScript)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	oracle := &nodeOracle{cmd: cmd, input: input, output: bufio.NewReader(output)}
	t.Cleanup(func() {
		_ = input.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return oracle
}

// check is whether classes is what Node makes of the mapping module js, and its key order.
func (o *nodeOracle) check(t testing.TB, js, classes string) (expected string, same, ordered bool) {
	t.Helper()
	request, err := json.Marshal(map[string]string{"js": js, "classes": classes})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.input.Write(append(request, '\n')); err != nil {
		t.Fatal(err)
	}
	line, err := o.output.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var reply struct {
		Expected string `json:"expected"`
		Same     bool   `json:"same"`
		Ordered  bool   `json:"ordered"`
	}
	if err := json.Unmarshal(line, &reply); err != nil {
		t.Fatalf("%q: %v", line, err)
	}
	return reply.Expected, reply.Same, reply.Ordered
}

// differentialNodeCases is how many generated modules the differential over Node runs.
const differentialNodeCases = 3000

// The mapping the facet embeds is, case by case, what Node gives for esbuild's mapping module (Pi's `vm` evaluation and
// spread, `JSON.stringify`ed), with the keys in the object's order: 3000 generated modules, 0 differences.
func TestScopeCSSModuleGivesWhatNodeGivesForEsbuildsMappingModuleOverGeneratedCases(t *testing.T) {
	oracle := startNodeOracle(t)
	evaluator := newMappingEvaluator(NodeOptions{})
	defer evaluator.close()
	differing := 0
	for i, css := range cssModuleCases(differentialNodeCases) {
		if !agreesWithNode(t, oracle, evaluator, css) {
			differing++
			if differing > 8 {
				break
			}
			t.Errorf("case %d: %q", i, css)
		}
	}
	if differing > 0 {
		t.Fatalf("%d cases differ from Node", differing)
	}
}

// agreesWithNode scopes the CSS module css and compares the mapping with Node's, reporting what differs.
func agreesWithNode(t testing.TB, oracle *nodeOracle, evaluator *mappingEvaluator, css string) bool {
	t.Helper()
	_, js, err := scopeBuild("p", "/d", "/d/x.module.css", css)
	if err != nil {
		// esbuild refuses the module: nothing is evaluated.
		return true
	}
	scoped, err := scopeCSSModule("p", "/d", "/d/x.module.css", css, evaluator, context.Background())
	if err != nil {
		t.Errorf("scoping: %v", err)
		return false
	}
	expected, same, ordered := oracle.check(t, js, scoped.Classes)
	if !same || !ordered {
		t.Errorf("mapping %s, Node's is %s (same text %v, same order %v)", scoped.Classes, expected, same, ordered)
	}
	return same && ordered
}

// FuzzScopeCSSModuleMatchesNode is the same differential over any CSS text; the corpus starts from the cases that told the
// scoping and Pi apart.
func FuzzScopeCSSModuleMatchesNode(f *testing.F) {
	for _, css := range cssModuleCases(40) {
		f.Add(css)
	}
	for _, css := range []string{`.a\"\'{color:red}`, `.__proto__{} .\31 {} .\30 {} .a{}`, `.\34 294967295{} .\34 294967294{}`, `.a\2028 {} .a\2029 b{} .\D800 {}`} {
		f.Add(css)
	}
	evaluator := newMappingEvaluator(NodeOptions{})
	defer evaluator.close()
	oracle := startNodeOracle(f)
	f.Fuzz(func(t *testing.T, css string) {
		if !agreesWithNode(t, oracle, evaluator, css) {
			t.Fail()
		}
	})
}

// A module that does not evaluate is that module's error: the process stays, and the next module is read.
func TestMappingEvaluatorReportsAModuleThatDoesNotEvaluateAndGoesOn(t *testing.T) {
	evaluator := newMappingEvaluator(NodeOptions{})
	defer evaluator.close()
	if _, err := evaluator.evaluate(context.Background(), `throw new Error("no");`); err == nil || !strings.Contains(err.Error(), "Error: no") {
		t.Fatalf("err = %v, want the module's Error: no", err)
	}
	got, err := evaluator.evaluate(context.Background(), `module.exports = { default: { a: "b", 1: "c" } };`)
	if err != nil || got != `{"1":"c","a":"b"}` {
		t.Fatalf("got %q, %v", got, err)
	}
	// A module with no default export has an empty mapping, as `{ ...undefined }` is.
	if got, err := evaluator.evaluate(context.Background(), ``); err != nil || got != `{}` {
		t.Fatalf("got %q, %v", got, err)
	}
}

// A ctx that ends before or during an evaluation is that evaluation's error (its cause) and ends the process; the next
// evaluation, with a ctx that lives, starts another.
func TestMappingEvaluatorEndsAnEvaluationWithItsContextAndGoesOnWithTheNext(t *testing.T) {
	cause := errors.New("the build was cancelled")
	minute := time.Minute
	evaluator := newMappingEvaluator(NodeOptions{ReadyTimeout: &minute})
	defer evaluator.close()
	if _, err := evaluator.evaluate(context.Background(), `module.exports = { default: {} };`); err != nil {
		t.Fatal(err)
	}
	ended, end := context.WithCancelCause(context.Background())
	end(cause)
	if _, err := evaluator.evaluate(ended, `module.exports = { default: {} };`); !errors.Is(err, cause) {
		t.Fatalf("err = %v, want the cause of the context", err)
	}
	// Cancelled while the module runs: the cancellation ends it, not the (here distant) timeout.
	running, stop := context.WithCancelCause(context.Background())
	time.AfterFunc(200*time.Millisecond, func() { stop(cause) })
	began := time.Now()
	if _, err := evaluator.evaluate(running, `while (true) {}`); !errors.Is(err, cause) {
		t.Fatalf("err = %v, want the cause of the context", err)
	}
	if took := time.Since(began); took > 10*time.Second {
		t.Fatalf("the cancellation took %s to end the evaluation: only the timeout did", took)
	}
	if got, err := evaluator.evaluate(context.Background(), `module.exports = { default: { a: "b" } };`); err != nil || got != `{"a":"b"}` {
		t.Fatalf("got %q, %v after the process was ended", got, err)
	}
}

// A closed evaluator evaluates nothing, and close ends its process.
func TestMappingEvaluatorRefusesAfterCloseAndEndsItsProcess(t *testing.T) {
	evaluator := newMappingEvaluator(NodeOptions{})
	if _, err := evaluator.evaluate(context.Background(), `module.exports = { default: {} };`); err != nil {
		t.Fatal(err)
	}
	cmd := evaluator.cmd
	evaluator.close()
	if cmd.ProcessState == nil {
		t.Fatal("the process is still running after close")
	}
	if _, err := evaluator.evaluate(context.Background(), `module.exports = {};`); err == nil {
		t.Fatal("a closed evaluator evaluated a module")
	}
}

// A process that does not answer is killed after NodeOptions.ReadyTimeout, and that is the evaluation's error; the next
// evaluation starts another process.
func TestMappingEvaluatorKillsAProcessThatDoesNotAnswer(t *testing.T) {
	timeout := 300 * time.Millisecond
	evaluator := newMappingEvaluator(NodeOptions{ReadyTimeout: &timeout})
	defer evaluator.close()
	_, err := evaluator.evaluate(context.Background(), `while (true) {}`)
	if err == nil || !strings.Contains(err.Error(), "did not answer within 300ms") {
		t.Fatalf("err = %v, want it to say the process did not answer within 300ms", err)
	}
	if got, err := evaluator.evaluate(context.Background(), `module.exports = { default: {} };`); err != nil || got != `{}` {
		t.Fatalf("got %q, %v after the process was killed", got, err)
	}
}

// A kill that an evaluation's timer or context made runs late, on a goroutine of its own: it must not land on the process of the
// evaluation after it. Here the timer of a slow module fires, the kill is held back until the module has answered, and then
// it is let go: the next module is evaluated on a live process.
func TestMappingEvaluatorALateKillOfAnEvaluationDoesNotHitTheNextOne(t *testing.T) {
	timeout := 50 * time.Millisecond
	evaluator := newMappingEvaluator(NodeOptions{ReadyTimeout: &timeout})
	defer evaluator.close()
	release, finished := make(chan struct{}), make(chan struct{})
	evaluator.killThrough = func(kill func()) {
		<-release
		kill()
		close(finished)
	}
	// The timer fires 50 ms in; the module answers after 150 ms, with the kill still held.
	got, err := evaluator.evaluate(context.Background(), `const t = Date.now(); while (Date.now() - t < 150) {} module.exports = { default: { a: "b" } };`)
	if err != nil || got != `{"a":"b"}` {
		t.Fatalf("got %q, %v: the held kill ended the evaluation", got, err)
	}
	close(release)
	<-finished
	evaluator.killThrough = nil
	if got, err := evaluator.evaluate(context.Background(), `module.exports = { default: { c: "d" } };`); err != nil || got != `{"c":"d"}` {
		t.Fatalf("got %q, %v: the late kill of the evaluation before landed on this one", got, err)
	}
}

// A build that waits for the evaluator while another build's module runs sees its own cancellation at once, as Pi's does:
// it does not wait for that module to end.
func TestMappingEvaluatorACancelledBuildDoesNotWaitForTheEvaluationInFlight(t *testing.T) {
	minute := time.Minute
	evaluator := newMappingEvaluator(NodeOptions{ReadyTimeout: &minute})
	defer evaluator.close()
	holding, release := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = evaluator.evaluate(holding, `while (true) {}`)
	}()
	defer func() { release(); <-done }()
	time.Sleep(300 * time.Millisecond) // the module is running
	cause := errors.New("the waiting build was cancelled")
	waiting, stop := context.WithCancelCause(context.Background())
	time.AfterFunc(100*time.Millisecond, func() { stop(cause) })
	answered := make(chan error, 1)
	go func() {
		_, err := evaluator.evaluate(waiting, `module.exports = { default: {} };`)
		answered <- err
	}()
	select {
	case err := <-answered:
		if !errors.Is(err, cause) {
			t.Fatalf("err = %v, want the cause of the waiting build's context", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a build whose context ended kept waiting for the evaluator's lock")
	}
}
