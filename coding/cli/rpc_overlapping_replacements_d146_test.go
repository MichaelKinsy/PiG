package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// D146, two Session-replacing commands in flight. Pi starts every stdin line at once (rpc-mode.ts:833), and a replacement acts on whichever Session is current
// when it starts (agent-session-runtime.ts:167-185 teardownCurrent and apply, :226-259 newSession, :187-194 finishSessionReplacement, rpc-mode.ts:456-463 and
// :321 rebindSession, the runtime's callback): the second replacement shuts down the Session the first has not yet applied, or the first's own Session while its session_start
// handler still runs. PiG runs the second after the first applied (the <-previous wait in
// awaitReplacement), so every Session gets one session_shutdown and one session_start (the runtime's rebind callback is the only rebind, as Pi's).
// testdata/d146-overlap-probe.mjs logs each session_start and session_shutdown with the Session id its ctx reports and holds the first replacement's first
// session_start for one second (the longer handler); D146_TOUCH=1 makes that handler use the captured pi after the wait.

// d146Pair is two commands of one batch with the shutdown reason each gives the Session it replaces.
type d146Pair struct {
	name         string
	first, other func(target string) string
	reasons      [2]string
}

var d146Pairs = []d146Pair{
	{"new_session+new_session", func(string) string { return `{"id":"a","type":"new_session"}` }, func(string) string { return `{"id":"b","type":"new_session"}` }, [2]string{"new", "new"}},
	{"new_session+switch_session", func(string) string { return `{"id":"a","type":"new_session"}` }, func(target string) string {
		return `{"id":"b","type":"switch_session","sessionPath":` + jsonString(target) + `}`
	}, [2]string{"new", "resume"}},
	{"fork+new_session", func(string) string { return `{"id":"a","type":"fork","entryId":"user-1"}` }, func(string) string { return `{"id":"b","type":"new_session"}` }, [2]string{"fork", "new"}},
}

const d146Source, d146Target = "d146-source", "d146-target"

func d146Fixture(id, cwd string) string {
	return strings.Join([]string{
		fmt.Sprintf(`{"type":"session","version":3,"id":%q,"timestamp":"2026-01-01T00:00:00Z","cwd":%s}`, id, jsonString(cwd)),
		`{"type":"model_change","id":"model-1","parentId":null,"timestamp":"2026-01-01T00:00:01Z","provider":"test-faux","modelId":"faux-1"}`,
		`{"type":"thinking_level_change","id":"thinking-1","parentId":"model-1","timestamp":"2026-01-01T00:00:02Z","thinkingLevel":"off"}`,
		`{"type":"message","id":"user-1","parentId":"thinking-1","timestamp":"2026-01-01T00:00:03Z","message":{"role":"user","content":[{"type":"text","text":"fork this prompt"}],"timestamp":1767225603000}}`,
		`{"type":"message","id":"assistant-1","parentId":"user-1","timestamp":"2026-01-01T00:00:04Z","message":{"role":"assistant","content":[{"type":"text","text":"answer"}],"api":"test-faux","provider":"test-faux","model":"faux-1","usage":{"input":10,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":12,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1767225604000}}`,
	}, "\n") + "\n"
}

// d146Event is one line of the extension's log.
type d146Event struct {
	Ev     string `json:"ev"`
	Reason string `json:"reason"`
	Phase  string `json:"phase"`
	Sid    string `json:"sid"`
}

// d146Outcome is what one run of a pair showed.
type d146Outcome struct {
	order    []string
	events   []d146Event
	errors   []rpcRecord
	finalSID string
}

// startD146 starts pig or the pinned Pi on a persisted source Session with the probe extension and returns the target Session file and the log.
func startD146(t *testing.T, side string, touch bool) (p *rpcProcess, target, log string) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cwd, sessions, home := filepath.Join(dir, "cwd"), filepath.Join(dir, "sessions"), filepath.Join(dir, "home")
	for _, d := range []string{cwd, sessions, home} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	source, target := filepath.Join(dir, "source.jsonl"), filepath.Join(dir, "target.jsonl")
	for path, id := range map[string]string{source: d146Source, target: d146Target} {
		if err := os.WriteFile(path, []byte(d146Fixture(id, cwd)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	log = filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(log, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(root, "coding", "cli", "testdata", "d146-overlap-probe.mjs")
	probeEnv := []string{"D146_LOG=" + log, "D146_HOLD_MS=1000", "D146_TOUCH=0"}
	if touch {
		probeEnv[2] = "D146_TOUCH=1"
	}
	common := []string{"--no-extensions", "--no-skills", "--no-prompt-templates", "--no-context-files", "--system-prompt", "P.", "--model", "test-faux/faux-1", "--session", source, "--session-dir", sessions}
	if side == "pig" {
		env := append([]string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1"}, probeEnv...)
		return startRPCProcessAt(t, cwd, env, append(common, "-e", probe)...), target, log
	}
	piRoot := filepath.Join(root, "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent")
	metadata, err := os.ReadFile(filepath.Join(piRoot, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct{ Version string }
	if err := json.Unmarshal(metadata, &pkg); err != nil || pkg.Version != pigversion.UpstreamPackageVersion {
		t.Fatalf("Pi version = %q, %v", pkg.Version, err)
	}
	env := append([]string{"HOME=" + home, "PI_HOME=" + filepath.Join(home, ".pi"), "PI_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PI_SKIP_VERSION_CHECK=1"}, probeEnv...)
	argv := append([]string{filepath.Join(piRoot, "dist", "cli.js"), "--mode", "rpc"}, common...)
	argv = append(argv, "-e", filepath.Join(root, "test", "parity", "testdata", "test-faux-provider.ts"), "-e", probe)
	return startJSONLProcessAt(t, cwd, env, "node", argv...), target, log
}

// runD146 writes the pair (in one write, or the second line once the first replacement's session_start handler runs), waits for both answers, then for a get_state answered after them, and reads the log.
func runD146(t *testing.T, side string, pair d146Pair, during bool, touch bool) d146Outcome {
	t.Helper()
	p, target, log := startD146(t, side, touch)
	p.sendJSON(map[string]any{"id": "s0", "type": "get_state"})
	p.await("the first get_state", func(record rpcRecord) bool { return record["id"] == "s0" })
	first, other := pair.first(target), pair.other(target)
	if !during {
		if _, err := io.WriteString(p.stdin, first+"\n"+other+"\n"); err != nil {
			t.Fatal(err)
		}
	} else {
		p.send(first)
		deadline := time.Now().Add(p.budget)
		for !d146ReplacementHandlerEntered(t, log) {
			if time.Now().After(deadline) {
				t.Fatalf("the first replacement's session_start handler never ran\n%s", p.stderr.String())
			}
			time.Sleep(5 * time.Millisecond)
		}
		p.send(other)
	}
	var out d146Outcome
	collect := func(record rpcRecord) {
		switch record["type"] {
		case "extension_error":
			out.errors = append(out.errors, record)
		case "response":
			if record["success"] != true {
				t.Fatalf("%v failed: %v", record["id"], record)
			}
		}
	}
	p.await("both answers", func(record rpcRecord) bool {
		collect(record)
		if record["type"] == "response" {
			out.order = append(out.order, record["id"].(string))
		}
		return len(out.order) == 2
	})
	p.sendJSON(map[string]any{"id": "s1", "type": "get_state"})
	p.await("the get_state after both answers", func(record rpcRecord) bool {
		collect(record)
		if record["id"] != "s1" {
			return false
		}
		data, _ := record["data"].(map[string]any)
		out.finalSID, _ = data["sessionId"].(string)
		return true
	})
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var event d146Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		out.events = append(out.events, event)
	}
	return out
}

// d146ReplacementHandlerEntered reports whether a session_start handler of a replacement (not the startup one) has been entered.
func d146ReplacementHandlerEntered(t *testing.T, log string) bool {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		var event d146Event
		if json.Unmarshal([]byte(line), &event) == nil && event.Ev == "session_start" && event.Phase == "enter" && event.Reason != "startup" {
			return true
		}
	}
	return false
}

// d146Shape is what D146 compares of a run: the answer order, the reasons of every session_shutdown by the role of the Session that got it (the source, the first
// replacement's Session, the final one), how many session_start handlers were entered by Session (the startup one is not counted), whether the first replacement's
// session_shutdown arrived while its first session_start handler was still running, and the extension_error lines.
type d146Shape struct {
	Order               string
	Shutdowns           map[string]string
	Starts              map[string]int
	ShutdownInHeldStart bool
	Errors              []string
}

func (o d146Outcome) shape() d146Shape {
	shape := d146Shape{Order: strings.Join(o.order, ","), Shutdowns: map[string]string{}, Starts: map[string]int{}}
	// The first replacement's Session is the one the first handler outside startup reports.
	var firstSID string
	for _, event := range o.events {
		if event.Ev == "session_start" && event.Reason != "startup" {
			firstSID = event.Sid
			break
		}
	}
	role := func(sid string) string {
		switch sid {
		case d146Source:
			return "source"
		case firstSID:
			return "first"
		case o.finalSID:
			return "final"
		}
		return "other:" + sid
	}
	inHeldStart := false
	for _, event := range o.events {
		switch {
		case event.Ev == "session_start" && event.Phase == "enter" && event.Reason != "startup":
			shape.Starts[role(event.Sid)]++
			inHeldStart = inHeldStart || shape.Starts["first"] == 1 && role(event.Sid) == "first"
		case event.Ev == "session_start" && event.Phase == "exit" && event.Reason != "startup" && shape.Starts["first"] == 1:
			// The held handler's exit: its ctx may be stale by now, so its Session id is not compared.
			inHeldStart = false
		case event.Ev == "session_shutdown":
			shape.Shutdowns[role(event.Sid)] += event.Reason + ","
			if role(event.Sid) == "first" && inHeldStart {
				shape.ShutdownInHeldStart = true
			}
		}
	}
	for _, record := range o.errors {
		message, _ := record["error"].(string)
		if strings.HasPrefix(message, d146StaleError) {
			message = "stale ctx"
		}
		shape.Errors = append(shape.Errors, fmt.Sprintf("%v: %s", record["event"], message))
	}
	return shape
}

// d146Gaps are the two ways the pair reaches the process: in one write, and the second line written once the first replacement's session_start handler has been entered (it then
// holds for D146_HOLD_MS, so the first replacement has applied and its handler is still running). The write is caused by the handler entering, not by a delay, so a loaded machine cannot move it.
var d146Gaps = []struct {
	name   string
	during bool
}{{"one write", false}, {"second while the first handler runs", true}}

const d146StaleError = "This extension ctx is stale after session replacement or reload."

// requireD146Final fails unless the pair ended on the Session the second command asked for: the target of a switch_session, else a Session of its own.
func requireD146Final(t *testing.T, pair d146Pair, out d146Outcome) {
	t.Helper()
	switch {
	case pair.reasons[1] == "resume" && out.finalSID != d146Target:
		t.Errorf("final Session = %q, want the switch_session target %q", out.finalSID, d146Target)
	case pair.reasons[1] != "resume" && (out.finalSID == d146Source || out.finalSID == d146Target || out.finalSID == ""):
		t.Errorf("final Session = %q, want the Session the second command created", out.finalSID)
	}
}

// D146, recorded from the pinned Pi, and a PIN: it goes red the day Pi changes what it does with two replacements in flight, which is when D146's remove-when is checked.
// With the first replacement's session_start handler the longer one, Pi answers the second command first, in one write and with the second written while that handler runs, for new_session + new_session,
// new_session + switch_session and fork + new_session. Sent in one write, Pi sends session_shutdown to the outgoing Session twice (once per command) and never to the
// first replacement's Session, and binds each replacement's Session once. Sent while the first replacement's handler runs, the second command's session_shutdown reaches the first replacement's Session
// while its session_start handler still runs, and a handler that uses its captured pi after that gets the stale-context extension_error.
func TestPiOverlappingReplacementsD146(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the pinned Pi under Node")
	}
	for _, pair := range d146Pairs {
		for _, g := range d146Gaps {
			for _, touch := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/touch=%v", pair.name, g.name, touch), func(t *testing.T) {
					out := runD146(t, "pi", pair, g.during, touch)
					requireD146Final(t, pair, out)
					// Starts: Pi beffcde92dea (#12230, #10470) makes the runtime's callback the only rebind of a replacement: AgentSessionRuntime.finishSessionReplacement
					// (agent-session-runtime.ts:187-190) calls the callback set at rpc-mode.ts:317-319, whose rebindSession (:321) binds the extensions of the Session
					// that replacement applied, and the closing `await rebindSession()` after runtimeHost.newSession/switchSession/fork (rpc-mode.ts:456-463, :625-647) is gone.
					// Two replacements are two rebinds, one per Session, so each Session gets one session_start: first 1, final 1. Before the commit each command
					// also ended with that explicit rebind, which binds whichever Session is current by then (the final one): four rebinds, first 1, final 3.
					want := d146Shape{Order: "b,a", Starts: map[string]int{"first": 1, "final": 1}}
					if !g.during {
						want.Shutdowns = map[string]string{"source": pair.reasons[0] + "," + pair.reasons[1] + ","}
					} else {
						want.Shutdowns = map[string]string{"source": pair.reasons[0] + ",", "first": pair.reasons[1] + ","}
						want.ShutdownInHeldStart = true
						if touch {
							want.Errors = []string{"session_start: stale ctx"}
						}
					}
					got := out.shape()
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("Pi's run of %s, %s, touch=%v:\n got %+v\nwant %+v\nevents %+v", pair.name, g.name, touch, got, want, out.events)
					}
				})
			}
		}
	}
}

// D146, pig. The same pairs, written the same two ways, are answered in the order they were sent and every Session gets its session_shutdown once: the source by the
// first command, the first replacement's Session by the second, after its session_start handlers returned; each replacement's Session binds once and no extension_error is
// written, because the second replacement starts when the first applied (the <-previous wait of awaitReplacement), not while the first replacement's handler runs.
// mutation-checked: the select on previous before replace (the start-after-previous wait) removed; see D146's Evidence for what the run does then.
func TestRPCOverlappingReplacementsRunOneAfterTheOtherD146(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	for _, pair := range d146Pairs {
		for _, g := range d146Gaps {
			for _, touch := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/touch=%v", pair.name, g.name, touch), func(t *testing.T) {
					out := runD146(t, "pig", pair, g.during, touch)
					requireD146Final(t, pair, out)
					want := d146Shape{
						Order:     "a,b",
						Shutdowns: map[string]string{"source": pair.reasons[0] + ",", "first": pair.reasons[1] + ","},
						// One rebind per replacement, the runtime's callback (rpcHost.bind), so each Session gets one session_start: the second rebind of each command
						// (rpcHost.rebindAgain, `err = host.rebindAgain(ctx)` in awaitReplacement's replace functions) was deleted by the follow of Pi beffcde92dea
						// (4da149a979), which made it two per Session.
						Starts: map[string]int{"first": 1, "final": 1},
					}
					if got := out.shape(); !reflect.DeepEqual(got, want) {
						t.Fatalf("pig's run of %s, %s, touch=%v:\n got %+v\nwant %+v\nevents %+v", pair.name, g.name, touch, got, want, out.events)
					}
				})
			}
		}
	}
}
