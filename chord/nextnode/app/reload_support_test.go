package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"text/template"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// The reload suite's kit (Pi's test/node/fixtures/reload/{app,plugins}.ts, packages/chord test/node/reload/*, pin c23b9ec1): an app with
// project workers, short host timeouts, its root holding the suite's plugins (Pi's TypeScript, as testdata/reload templates); the test's
// hands (edit a plugin on disk and apply the changes, the latches of calls in flight); and the page, a real Chromium tab running the
// base bundle and the web plugins' React components, driven through the DevTools protocol (reload_web_support_test.go: `reloadTab`, Pi's
// `ReloadPage`). The server's latches are reached through a latch facet on the server's plugins' process (`reload.servercontrol`): Pi's
// server plugins run in the test's own process (D462).

// reloadTimeouts are the hosts' timeouts of the suite's apps (the server's and the workers'): a call outliving a restart's drain is cut
// off after 1 s, an object's close takes at most 2 s, a call waits at most 5 s for its object to go live.
var reloadTimeouts = AppTimeouts{Close: 2 * time.Second, Ready: 5 * time.Second, ReloadDrain: time.Second}

// reloadWait bounds every wait of the suite on the page, the latches and the marks.
const reloadWait = 30 * time.Second

// The templates of testdata/reload, rendered by name.
func reloadTemplate(t testing.TB, name string, params any) string {
	t.Helper()
	text, err := os.ReadFile(filepath.Join("testdata", "reload", name+".tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := template.New(name).Delims("[[", "]]").Parse(string(text))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, params); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out.String()
}

// reloadLatchSource is the latch map and natives' record every plugin of the suite shares within a process.
func reloadLatchSource(t testing.TB) string { return reloadTemplate(t, "latch.ts", nil) }

// counterCode is how a version of the counter's code is written (Pi's CounterCode).
type counterCode struct {
	// Code is what its label and `code()` say: which code answered.
	Code      string
	Placement string // "server" or "worker"
	// Native holds a native (`self.open`): every reload of it restarts the object.
	Native bool
	// Contract is "v1" (the default) or "v2" (the contract file goes with it); Accepts: v2 accepts v1 consumers through a mapping.
	Contract string
	Accepts  bool
	// Broken is a broken version: "build", "evaluation", "setup" or "open" (its native's open throws).
	Broken string
}

func (c counterCode) version() string {
	if c.Contract == "" {
		return "v1"
	}
	return c.Contract
}

// counterPlugin is the counter plugin's files (its contract and its server or worker facet) for code.
func counterPlugin(t testing.TB, code counterCode) map[string]string {
	t.Helper()
	params := map[string]any{
		"Code": code.Code, "CodeJSON": strconv.Quote(code.Code), "Placement": code.Placement, "Native": code.Native, "V2": code.version() == "v2",
		"Accepts": code.version() == "v2" && code.Accepts, "Broken": code.Broken, "Version": code.version(), "Latch": reloadLatchSource(t),
	}
	return map[string]string{
		"contract.ts":          reloadTemplate(t, "counter-contract.ts", params),
		code.Placement + ".ts": reloadTemplate(t, "counter-facet.ts", params),
	}
}

// latchPlugin is the latch plugin: the test's hand on the latches and the natives' record on every project's worker and on the server.
func latchPlugin(t testing.TB) map[string]string {
	t.Helper()
	params := map[string]any{"Latch": reloadLatchSource(t)}
	return map[string]string{"worker.ts": reloadTemplate(t, "latch-worker.ts", params), "server.ts": reloadTemplate(t, "latch-server.ts", params)}
}

// summaryPlugin is the summary plugin's files: a pure object on the server whose factory holds the counter of project `id` on its worker.
func summaryPlugin(t testing.TB, code string) map[string]string {
	t.Helper()
	return map[string]string{
		"contract.ts": reloadTemplate(t, "summary-contract.ts", nil),
		"server.ts":   reloadTemplate(t, "summary-server.ts", map[string]any{"Code": code, "CodeJSON": strconv.Quote(code)}),
	}
}

// tasksPlugin is the tasks plugin's files: a runner with an in-memory job whose run waits on the latch named by its argument and whose
// `finish` records `<code>-finish(<outcome>)` in a persisted list; native: the runner holds a native, so a reload restarts it.
func tasksPlugin(t testing.TB, code string, native bool) map[string]string {
	t.Helper()
	return map[string]string{
		"contract.ts": reloadTemplate(t, "tasks-contract.ts", nil),
		"worker.ts":   reloadTemplate(t, "tasks-worker.ts", map[string]any{"Code": code, "CodeJSON": strconv.Quote(code), "Native": native, "Latch": reloadLatchSource(t)}),
		"web.ts":      reloadTemplate(t, "tasks-web.ts", nil),
	}
}

func counterAddress(placement string) func(*delta.JsonObject) (contracts.Address, error) {
	if placement == "server" {
		return func(*delta.JsonObject) (contracts.Address, error) { return serverKind.Address(nil), nil }
	}
	return func(params *delta.JsonObject) (contracts.Address, error) {
		return project.Address(delta.JsonObjectOf("id", params.Value("id"))), nil
	}
}

var serverKind = mustHostKind("server")

// controlContract is the latch plugin's contract, declared again for the test (contracts are data): on project `id`'s worker
// (`reload.control`) or on the server (`reload.servercontrol`).
func controlContract(placement string) *contracts.Contract {
	name, params := "reload.servercontrol", contracts.Params{}
	if placement == "worker" {
		name, params = "reload.control", contracts.Params{contracts.Prop("id", contracts.Str())}
	}
	return mustRemote(name, contracts.RemoteDeclaration{
		Version: 1, Params: params, HostAddress: counterAddress(placement),
		Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
			contracts.Meth("entered", contracts.NewMethod(contracts.Str(), contracts.Bool())),
			contracts.Meth("open", contracts.NewMethod(contracts.Str())),
			contracts.Meth("natives", contracts.NewMethod(contracts.Schema{}, contracts.Obj(contracts.Prop("opens", contracts.Arr(contracts.Str())), contracts.Prop("closes", contracts.Arr(contracts.Str()))))),
		}})},
	})
}

// edit writes files into plugin dir of the app's root (an edit on disk) and waits until the watcher marked every plugin the edit
// affects dirty: the plugin itself (for one not discovered yet, until the plugin set changed) and the plugins in alsoDirty, the ones
// that import its files. The rollout applies the marks the watcher has made when it runs: one that finds a dependent unmarked leaves
// it out. A plugin already there is waited for by its own mark, not by the set mark an earlier edit left.
func (s started) edit(t testing.TB, dir string, files map[string]string, alsoDirty ...string) {
	t.Helper()
	_, err := os.Stat(filepath.Join(s.root, dir))
	known := err == nil
	writePlugin(t, s.root, dir, "@test/"+dir, files)
	name := "@test/" + dir
	untilMarks(t, s.app, strings.Join(append([]string{name}, alsoDirty...), ", ")+" marked", func(m plugins.PluginsState) bool {
		return (slices.Contains(m.Dirty, name) || (!known && m.Set)) && !slices.ContainsFunc(alsoDirty, func(also string) bool { return !slices.Contains(m.Dirty, also) })
	})
}

// editAndApply edits plugin dir (see edit) and applies the changes (the rollout: server, workers, clients).
func (s started) editAndApply(t testing.TB, dir string, files map[string]string, alsoDirty ...string) plugins.RolloutResult {
	t.Helper()
	s.edit(t, dir, files, alsoDirty...)
	return s.apply(t)
}

// apply applies the changes.
func (s started) apply(t testing.TB) plugins.RolloutResult {
	t.Helper()
	result, err := s.app.ApplyChanges(background)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// removeAndApply removes plugin dir from the app's root, waits until the watcher saw the plugin set change, and applies it.
func (s started) removeAndApply(t testing.TB, dir string) plugins.RolloutResult {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(s.root, dir)); err != nil {
		t.Fatal(err)
	}
	untilMarks(t, s.app, "the plugin set marked changed", func(m plugins.PluginsState) bool { return m.Set })
	return s.apply(t)
}

// dirty is whether plugin dir is still marked dirty.
func (s started) dirty() []string { return s.app.Plugins.Marks().Dirty }

// rolloutFailures are the plugins a rollout's hosts kept on their old code, as `<step>/<host>: <plugin>`, and hosts that failed.
func rolloutFailures(result plugins.RolloutResult) []string {
	failures := []string{}
	for _, step := range result.Steps {
		for _, entry := range step.Hosts {
			switch entry.Kind {
			case plugins.RolloutReported:
				for _, failed := range entry.Report.Failed {
					failures = append(failures, fmt.Sprintf("%s/%s: %s", step.Name, entry.Host, failed.Plugin))
				}
			case plugins.RolloutErrored:
				failures = append(failures, fmt.Sprintf("%s/%s: error %v", step.Name, entry.Host, entry.Error))
			}
		}
	}
	return failures
}

// rolloutFates are the fates a rollout's hosts reported, as `<step>/<host>: <object> <kind>`.
func rolloutFates(result plugins.RolloutResult) []string {
	fates := []string{}
	for _, step := range result.Steps {
		for _, entry := range step.Hosts {
			if entry.Kind != plugins.RolloutReported {
				continue
			}
			for _, each := range entry.Report.Objects {
				fates = append(fates, fmt.Sprintf("%s/%s: %s %s", step.Name, entry.Host, each.Object, each.Kind))
			}
		}
	}
	return fates
}

// latches is the test's hand on the latches and the natives' record where the counter runs (a worker's process or the server's).
type latches struct {
	t       testing.TB
	control *host.Reference
}

// latchesOf are the latches of project `p1`'s worker (through the latch plugin), or of the server's plugins' process.
func latchesOf(t testing.TB, s started, placement string) *latches {
	t.Helper()
	params := delta.NewJsonObject(0)
	if placement == "worker" {
		params = delta.JsonObjectOf("id", "p1")
	}
	control, err := s.app.Host.Get(background, controlContract(placement), params, host.GetOptions{})
	if err != nil {
		t.Fatalf("%v (the app reported %v)", err, reported(s.errors))
	}
	t.Cleanup(control.Release)
	return &latches{t: t, control: control}
}

// entered returns once a call waiting on latch name entered it.
func (l *latches) entered(name string) {
	l.t.Helper()
	ctx, cancel := context.WithTimeout(background, reloadWait)
	defer cancel()
	if _, err := l.control.Call("entered", name, ctx); err != nil {
		l.t.Fatalf("latch %s never entered: %v", name, err)
	}
}

// open opens latch name: the call waiting on it goes on.
func (l *latches) open(name string) {
	l.t.Helper()
	if _, err := l.control.Call("open", name, background); err != nil {
		l.t.Fatal(err)
	}
}

// natives is what the counter's natives did: opens by code, closes as `<code>:<reason>`.
func (l *latches) natives() (opens, closes []string) {
	l.t.Helper()
	value, err := l.control.Call("natives", nil, background)
	if err != nil {
		l.t.Fatal(err)
	}
	object := value.(*delta.JsonObject)
	return stringList(object.Value("opens")), stringList(object.Value("closes"))
}

func stringList(value any) []string {
	list := []string{}
	items, _ := value.([]any)
	for _, item := range items {
		list = append(list, item.(string))
	}
	return list
}

// jsonText is value as compact JSON, the form the page shows and the tests compare (`undefined` is "none").
func jsonText(value delta.JsonValue) string {
	text, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("!%v", err)
	}
	return string(text)
}

// callResult is a call's answer as the tests read it (Pi's CallResult): pending, ok with the value as JSON, or an error.
type callResult struct {
	Status string // "pending", "ok" or "error"
	Value  string
	Code   string
	Reason string
}

func okResult(value any) callResult {
	text, _ := json.Marshal(value)
	return callResult{Status: "ok", Value: string(text)}
}

var pendingResult = callResult{Status: "pending"}

// sleepFor is how long a test lets something that must not happen have to happen (Pi's `setTimeout` before a negative assertion).
func sleepFor(d time.Duration) { <-time.After(d) }

// gapT is the testing.TB of a scenario that is expected to fail (Pi's `it.fails`): a failed check ends the scenario and is recorded
// instead of failing the test. Cleanups, logs and names are the real test's.
type gapT struct {
	testing.TB
	mu     sync.Mutex
	failed string
}

func (g *gapT) record(message string) {
	g.mu.Lock()
	if g.failed == "" {
		g.failed = message
	}
	g.mu.Unlock()
}

func (g *gapT) Fatal(args ...any) { g.record(fmt.Sprint(args...)); runtime.Goexit() }
func (g *gapT) Fatalf(format string, args ...any) {
	g.record(fmt.Sprintf(format, args...))
	runtime.Goexit()
}
func (g *gapT) Error(args ...any)                 { g.record(fmt.Sprint(args...)) }
func (g *gapT) Errorf(format string, args ...any) { g.record(fmt.Sprintf(format, args...)) }
func (g *gapT) FailNow()                          { g.record("FailNow"); runtime.Goexit() }

// expectFailure runs scenario as `it.fails` does: the test passes when a check of the scenario fails, and fails when none does.
func expectFailure(t *testing.T, scenario func(testing.TB)) {
	t.Helper()
	g := &gapT{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		scenario(g)
	}()
	<-done
	if g.failed == "" {
		t.Fatal("the scenario passed: Pi marks it `it.fails` (a documented gap), so a passing run means the gap closed")
	}
	t.Logf("failed as expected: %s", g.failed)
}

// reported is what the app reported so far.
func reported(e *appErrors) []AppError {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.list)
}
