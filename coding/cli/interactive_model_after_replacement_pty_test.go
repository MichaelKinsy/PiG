//go:build linux

package cli

import (
	"bytes"
	"cmp"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

// waitModelAfterReplacementRecords returns the fixture's records once ready accepts them.
func waitModelAfterReplacementRecords(t *testing.T, p *interactivePig, what string, ready func([]modelAfterReplacementRecord) bool) []modelAfterReplacementRecord {
	t.Helper()
	deadline := time.Now().Add(testbudget.Wait(t))
	for {
		records := readModelAfterReplacementRecords(t, p.log)
		if ready(records) {
			return records
		}
		if time.Now().After(deadline) {
			t.Fatalf("the extension never recorded %s; records %+v; screen tail: %q", what, records, tail(p.output.since(0), 1500))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// selectModel types /model <spec> and submits it: the first Enter accepts the argument completion the editor shows and the second submits.
func (p *interactivePig) selectModel(spec string) {
	p.t.Helper()
	p.send("\x15/model " + spec)
	time.Sleep(300 * time.Millisecond)
	p.send("\r")
	time.Sleep(300 * time.Millisecond)
	p.send("\r")
}

// modelAfterReplacementRow is one way a Session gets replaced.
type modelAfterReplacementRow struct {
	name string
	// persist sends a prompt first: only a Session with an assistant reply exists on disk, so only it can be resumed, cloned or forked.
	persist bool
	// replace starts the replacement and reports the reason of the session_start it ends with ("" for no replacement).
	replace func(p *interactivePig) string
}

var modelAfterReplacementRows = []modelAfterReplacementRow{
	{name: "startup (no replacement)", replace: func(*interactivePig) string { return "" }},
	{name: "new", replace: func(p *interactivePig) string { p.command("/new"); return "new" }},
	{name: "resume", persist: true, replace: func(p *interactivePig) string {
		p.command("/resume")
		time.Sleep(700 * time.Millisecond)
		p.send("\r")
		return "resume"
	}},
	{name: "clone", persist: true, replace: func(p *interactivePig) string { p.command("/clone"); return "fork" }},
	{name: "fork", persist: true, replace: func(p *interactivePig) string {
		p.command("/fork")
		time.Sleep(700 * time.Millisecond)
		p.send("\r")
		return "fork"
	}},
	{name: "reload", replace: func(p *interactivePig) string { p.command("/reload"); return "reload" }},
}

func modelAfterReplacementRowNamed(name string) modelAfterReplacementRow {
	for _, row := range modelAfterReplacementRows {
		if row.name == name {
			return row
		}
	}
	panic("no replacement row " + name)
}

// modelAfterReplacementShape is one way an extension declares its provider and a Session starts on it.
type modelAfterReplacementShape struct {
	name string
	// env extends the process environment the fixture reads (PIG_TEST_MODEL_AFTER_NEW_SHAPE).
	env []string
	// args are the model flags pig starts with.
	args []string
	// prepare seeds the home before the process starts.
	prepare func(t *testing.T, home string)
	// reply is the text the model answers "reply with exactly: first" with ("" is the fixture provider's "done"), and settle how long the Session takes to reach the disk after it.
	reply  string
	settle time.Duration
	// thinking is the level every model_select reports: the level the Session started with, kept by the selection ("" leaves it unchecked).
	thinking string
}

var (
	// shapeAPIKey is an OpenAI-compatible provider under a custom base URL, with an API key; the Session starts on it.
	shapeAPIKey = modelAfterReplacementShape{name: "api key", args: []string{"--model", "metaprov/model-a", "--thinking", "medium"}, thinking: "medium"}
	// shapeOAuth registers the provider with an OAuth login and a stored credential, so its models are available; the Session starts on it.
	shapeOAuth = modelAfterReplacementShape{
		name: "oauth", env: []string{"PIG_TEST_MODEL_AFTER_NEW_SHAPE=oauth"}, args: []string{"--model", "metaprov/model-a", "--thinking", "medium"}, thinking: "medium",
		prepare: func(t *testing.T, home string) {
			t.Helper()
			credential := `{"metaprov":{"type":"oauth","refresh":"r","access":"a","expires":4102444800000}}`
			if err := os.WriteFile(filepath.Join(home, "agent", "auth.json"), []byte(credential), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	}
	// shapeNoDefaultModel starts on another provider's model: the extension's provider is neither the Session's model nor a provider with a default model of its own.
	shapeNoDefaultModel = modelAfterReplacementShape{name: "no default model", args: []string{"--model", "test-faux/faux-1"}, reply: "first", settle: 1500 * time.Millisecond}
)

// modelAfterReplacementServer answers every completion request of the fixture's provider with the reply "done".
func modelAfterReplacementServer(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"probe\",\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	return server.URL + "/v1"
}

// expectModelSelected runs trigger, which selects spec, and expects the extension to see the registered model of spec, at the thinking level of the shape, and the footer to show its window.
// Pi redraws the footer when the terminal next renders, not when an extension's setModel returns (parity scenario 109 shows both programs keep the old window until then), so a selection
// that does not draw by itself passes key, a keystroke that is the next render, once the extension has seen the event.
func expectModelSelected(t *testing.T, p *interactivePig, shape modelAfterReplacementShape, spec string, key string, trigger func()) {
	t.Helper()
	before := len(readModelAfterReplacementRecords(t, p.log))
	mark := p.output.mark()
	trigger()
	records := waitModelAfterReplacementRecords(t, p, "the model_select of "+spec, func(records []modelAfterReplacementRecord) bool { return len(records) > before })
	got := records[before]
	want := registeredModels[spec]
	if got.Event != "model_select" || got.Source != "set" || got.Model == nil || got.Model.ID != spec {
		t.Fatalf("selecting %s recorded %+v; screen tail: %q", spec, got, tail(p.output.since(0), 1200))
	}
	if gotJSON, wantJSON := marshalModelFields(t, *got.Model), marshalModelFields(t, want); gotJSON != wantJSON {
		t.Errorf("selecting %s gave the model %s\nwant the registered %s", spec, gotJSON, wantJSON)
	}
	if shape.thinking != "" && got.Thinking != shape.thinking {
		t.Errorf("selecting %s left the thinking level %q, want %s", spec, got.Thinking, shape.thinking)
	}
	if key != "" {
		p.send(key)
	}
	// The footer reads "0.0%/777k (auto)": the used share, then the window.
	window := "/" + strconv.Itoa(want.ContextWindow/1000) + "k"
	p.output.waitQuiet(mark, []byte(window), 300*time.Millisecond, testbudget.Wait(t))
	if !bytes.Contains(p.output.since(mark), []byte(window)) {
		t.Errorf("the footer never showed the window %s after selecting %s; screen tail: %q", window, spec, tail(p.output.since(mark), 800))
	}
}

// runModelAfterReplacement starts pig with the fixture, replaces the Session as the row does, and selects each model of the provider with /model: the extension sees the registered
// model, the footer shows its window, the thinking level stays, and the next turn does not compact.
func runModelAfterReplacement(t *testing.T, fixture string, shape modelAfterReplacementShape, row modelAfterReplacementRow) {
	t.Helper()
	p := startInteractivePigOptions(t, fixture, interactivePigOptions{
		args: shape.args,
		env:  append([]string{"PIG_TEST_MODEL_AFTER_NEW_URL=" + modelAfterReplacementServer(t)}, shape.env...),
		prepare: func(home string) {
			if shape.prepare != nil {
				shape.prepare(t, home)
			}
		},
	})
	if row.persist {
		reply, settle := cmp.Or(shape.reply, "done"), cmp.Or(shape.settle, 500*time.Millisecond)
		p.send("reply with exactly: first\r")
		p.output.waitQuiet(0, []byte(reply), 300*time.Millisecond, testbudget.Wait(t))
		time.Sleep(settle)
	}
	if reason := row.replace(p); reason != "" {
		waitModelAfterReplacementRecords(t, p, "the replacement's session_start ("+reason+")", func(records []modelAfterReplacementRecord) bool {
			return slices.ContainsFunc(records, func(r modelAfterReplacementRecord) bool { return r.Event == "session_start" && r.Reason == reason })
		})
		// The replacement's extensions are bound once the mode rebinds; a fork also prefills the editor a moment later.
		time.Sleep(700 * time.Millisecond)
	}
	for _, spec := range []string{"metaprov/model-b", "metaprov/model-a"} {
		expectModelSelected(t, p, shape, spec, "", func() { p.selectModel(spec) })
	}
	// The next turn runs on the selected model, and its window decides when the Session compacts (shouldCompact: tokens > window - reserve): a window of 0 puts every reply over the threshold.
	mark := p.output.mark()
	p.send("\x15reply with exactly: after\r")
	p.output.waitQuiet(mark, []byte("done"), 300*time.Millisecond, testbudget.Wait(t))
	time.Sleep(1500 * time.Millisecond)
	if screen := p.output.since(mark); bytes.Contains(bytes.ToLower(screen), []byte("compact")) {
		t.Errorf("a reply on a model with window %d started a compaction; screen tail: %q", registeredModels["metaprov/model-a"].ContextWindow, tail(screen, 1200))
	}
	for _, entry := range readSessionEntries(t, filepath.Join(p.home, "sessions")) {
		switch entry["type"] {
		case "compaction":
			t.Errorf("the Session recorded a compaction: %v", entry)
		case "thinking_level_change":
			if entry["thinkingLevel"] == "off" && shape.thinking != "off" && shape.thinking != "" {
				t.Errorf("the Session recorded a thinking level change to off: %v", entry)
			}
		}
	}
	p.quit()
}

// Pi's /model selects a model object of the Session's ModelRuntime, which carries what the provider registered: its window, output limit, costs, input kinds and reasoning
// (model-selector.ts, interactive-mode.ts selectModel, agent-session.ts setModel, which clamps the thinking level to the model). A Session replacement (/new, /resume, /clone, /fork)
// builds the next Session's extensions and registry, and the next /model must select from that registry: the model the extension sees has the registered metadata, the footer shows its
// window, and the thinking level stays where it was. Issue #522: interactive mode kept the startup build's model builder, whose registry no longer held the provider, so every
// model of an extension provider came back with window 0 and no thinking.
func TestInteractiveModelAfterSessionReplacementKeepsRegisteredMetadata(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and extension processes")
	}
	fixture, err := filepath.Abs(filepath.Join("testdata", "model-after-new.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range modelAfterReplacementRows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			runModelAfterReplacement(t, fixture, shapeAPIKey, row)
		})
	}
}

// The provider's declaration decides what the extension registered, not what happens after a replacement: an OAuth provider and a provider the Session did not start on get the same models.
func TestInteractiveModelAfterSessionReplacementProviderShapes(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and extension processes")
	}
	fixture, err := filepath.Abs(filepath.Join("testdata", "model-after-new.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, shape := range []modelAfterReplacementShape{shapeOAuth, shapeNoDefaultModel} {
		for _, rowName := range []string{"new", "resume"} {
			t.Run(shape.name+"/"+rowName, func(t *testing.T) {
				t.Parallel()
				runModelAfterReplacement(t, fixture, shape, modelAfterReplacementRowNamed(rowName))
			})
		}
	}
}

// An extension's setModel (pi.setModel) selects through the Session's model runtime as /model does: after a replacement the model the extension sees has what its provider registered.
func TestInteractiveExtensionSetModelAfterNewKeepsRegisteredMetadata(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and extension processes")
	}
	fixture, err := filepath.Abs(filepath.Join("testdata", "model-after-new.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, rowName := range []string{"startup (no replacement)", "new", "resume"} {
		t.Run(rowName, func(t *testing.T) {
			t.Parallel()
			row := modelAfterReplacementRowNamed(rowName)
			p := startInteractivePigOptions(t, fixture, interactivePigOptions{args: shapeAPIKey.args, env: []string{"PIG_TEST_MODEL_AFTER_NEW_URL=" + modelAfterReplacementServer(t)}})
			if row.persist {
				p.send("reply with exactly: first\r")
				p.output.waitQuiet(0, []byte("done"), 300*time.Millisecond, testbudget.Wait(t))
				time.Sleep(500 * time.Millisecond)
			}
			if reason := row.replace(p); reason != "" {
				waitModelAfterReplacementRecords(t, p, "the replacement's session_start ("+reason+")", func(records []modelAfterReplacementRecord) bool {
					return slices.ContainsFunc(records, func(r modelAfterReplacementRecord) bool { return r.Event == "session_start" && r.Reason == reason })
				})
				time.Sleep(700 * time.Millisecond)
			}
			expectModelSelected(t, p, shapeAPIKey, "metaprov/model-b", "x", func() { p.command("\x15/pick") })
			p.quit()
		})
	}
}

// Every SDK that registers a provider hands the Session's models the same way: the Go, Rust, Python and Lisp realizations of the fixture start the Session on their provider, replace it with /new, and
// select the provider's models.
func TestInteractiveModelAfterNewAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and extension processes in four languages")
	}
	for _, tool := range []string{"python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s is required for the SDK realizations: %v", tool, err)
		}
	}
	if _, err := exec.LookPath(toolchain.SBCLExecutable(runtime.GOOS, exec.LookPath)); err != nil {
		t.Fatalf("sbcl is required for the Lisp SDK realization: %v", err)
	}
	fixtures := map[string]string{}
	for name, file := range map[string]string{"python": "model-after-new.py", "lisp": "model-after-new.lisp"} {
		path, err := filepath.Abs(filepath.Join("testdata", file))
		if err != nil {
			t.Fatal(err)
		}
		fixtures[name] = path
	}
	fixtures["go"] = buildGoSDKFixture(t, "model-after-new-go")
	fixtures["rust"] = buildRustSDKFixture(t, "model-after-new-rs")
	for _, name := range []string{"go", "rust", "python", "lisp"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			runModelAfterReplacement(t, fixtures[name], shapeAPIKey, modelAfterReplacementRowNamed("new"))
		})
	}
}

// readSessionEntries returns every entry of every Session file under dir.
func readSessionEntries(t *testing.T, dir string) []map[string]any {
	t.Helper()
	var entries []map[string]any
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
			var entry map[string]any
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				return err
			}
			entries = append(entries, entry)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}
