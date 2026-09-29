package ai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// nodeProbeOutput is what a probe script under testdata/node-microtasks prints.
type nodeProbeOutput struct {
	Node      string              `json:"node"`
	Scenarios map[string][]string `json:"scenarios"`
}

func readPinnedNodeVersion(t *testing.T) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", ".node-version"))
	if err != nil {
		t.Fatal(err)
	}
	return "v" + strings.TrimSpace(string(content))
}

func readNodeProbeGolden(t *testing.T, name string) nodeProbeOutput {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", "node-microtasks", name+".golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var golden nodeProbeOutput
	if err := json.Unmarshal(content, &golden); err != nil {
		t.Fatal(err)
	}
	if want := readPinnedNodeVersion(t); golden.Node != want {
		t.Fatalf("%s golden was recorded on Node %s, want the pinned %s; regenerate it with the probe", name, golden.Node, want)
	}
	return golden
}

// runNodeProbe runs a probe with the Node on PATH. A Node other than the pinned version is not evidence for the pinned semantics, so the live comparison is skipped and the checked-in golden alone gates the Go replay.
func runNodeProbe(t *testing.T, name string) nodeProbeOutput {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required: %v", err)
	}
	var stdout, stderr bytes.Buffer
	command := exec.CommandContext(t.Context(), node, filepath.Join("testdata", "node-microtasks", name+".mjs"))
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("probe %s failed: %v\n%s", name, err, stderr.String())
	}
	var output nodeProbeOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("probe %s printed invalid JSON: %v", name, err)
	}
	return output
}

// verifyNodeProbe compares a Go replay with the golden log for every scenario, and the golden with a live Node run when Node is the pinned version.
func verifyNodeProbe(t *testing.T, name string, replays map[string]func() []string) {
	t.Helper()
	golden := readNodeProbeGolden(t, name)
	for scenario := range golden.Scenarios {
		if replays[scenario] == nil {
			t.Errorf("golden scenario %q has no Go replay", scenario)
		}
	}
	for scenario, replay := range replays {
		want, ok := golden.Scenarios[scenario]
		if !ok {
			t.Errorf("Go replay %q has no golden scenario", scenario)
			continue
		}
		t.Run(scenario, func(t *testing.T) {
			if got := replay(); !equalStrings(got, want) {
				t.Fatalf("microtask order differs from Node %s\n got: %s\nwant: %s", golden.Node, strings.Join(got, " | "), strings.Join(want, " | "))
			}
		})
	}
	t.Run("live-node", func(t *testing.T) {
		live := runNodeProbe(t, name)
		if live.Node != golden.Node {
			t.Skipf("Node %s is not the pinned %s; the golden replay above is the gate", live.Node, golden.Node)
		}
		for scenario, want := range golden.Scenarios {
			if !equalStrings(live.Scenarios[scenario], want) {
				t.Errorf("golden is stale for %q\nlive: %s\ngold: %s", scenario, strings.Join(live.Scenarios[scenario], " | "), strings.Join(want, " | "))
			}
		}
		if len(live.Scenarios) != len(golden.Scenarios) {
			t.Errorf("live probe printed %d scenarios, golden has %d", len(live.Scenarios), len(golden.Scenarios))
		}
	})
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// microtaskScript is the Go counterpart of a probe scenario: one consumer turn (the probe's async function), a ticker chain that makes reaction depth visible, and external completions that run only after the reaction queue drains.
type microtaskScript struct {
	executor *continuationExecutor
	turn     *continuationTurn
	log      []string
}

func runMicrotaskScript(body func(*microtaskScript)) []string {
	script := &microtaskScript{executor: &continuationExecutor{}}
	script.executor.run(func(turn *continuationTurn) {
		script.turn = turn
		body(script)
	})
	return script.log
}

func (script *microtaskScript) logf(format string, args ...any) {
	script.log = append(script.log, fmt.Sprintf(format, args...))
}

func (script *microtaskScript) ticker(prefix string, count int) {
	var step func(n int)
	step = func(n int) {
		if n > count {
			return
		}
		script.executor.post(func() {
			script.logf("%s%d", prefix, n)
			step(n + 1)
		})
	}
	step(1)
}

// external models a macrotask such as socket data: setImmediate in the probe.
func (script *microtaskScript) external(label string, action func()) {
	script.executor.postExternal(func() {
		script.logf("external %s", label)
		action()
		script.ticker(label+".t", 6)
	})
}

// scriptAwait is `await promise` in the probe's async function; a rejection is returned, as a try/catch would see it.
func scriptSettle[T any](script *microtaskScript, promise *jsPromise[T]) (T, error) {
	return jsAwait(script.turn, jsPromiseOperand(promise))
}

// scriptAwait is scriptSettle for a promise that must fulfill.
func scriptAwait[T any](script *microtaskScript, promise *jsPromise[T]) T {
	value, err := scriptSettle(script, promise)
	if err != nil {
		panic(err)
	}
	return value
}

var errProbeBoom = errors.New("boom")
