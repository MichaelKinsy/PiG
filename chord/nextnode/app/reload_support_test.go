package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
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

	"github.com/gorilla/websocket"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// The reload suite's kit (Pi's test/node/fixtures/reload/{app,plugins,web-api}.ts, packages/chord test/node/reload/*, pin c23b9ec1):
// an app with project workers, short host timeouts, its root holding the suite's plugins (Pi's TypeScript, as testdata/reload
// templates); the test's hands (edit a plugin on disk and apply the changes, the latches of calls in flight); and the page.
//
// Go forced differences (D606): Pi's page is a Chromium tab running the base bundle and the web plugins' React components, driven
// through CDP. Its Go form is a tab host connected to the server's links endpoint as the owner (a device session's token), which
// holds the counter and the other objects through its own references, tracks each reference's states and the values of its
// members, and calls from them: what a web plugin's component shows is the reference's last label, count and state. Page-only
// things (DOM, typing, scroll, page loads, a link dropped from the page's script, the web step of the rollout) are the browser
// rows (Playwright, the web surface). The server's latches are reached through a latch facet on the server's plugins' process
// (`reload.servercontrol`): Pi's server plugins run in the test's own process.

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
	}
}

// The page's copies of the contracts (an old page keeps its copy of a contract while the counter moves).

func counterAddress(placement string) func(*delta.JsonObject) (contracts.Address, error) {
	if placement == "server" {
		return func(*delta.JsonObject) (contracts.Address, error) { return serverKind.Address(nil), nil }
	}
	return func(params *delta.JsonObject) (contracts.Address, error) {
		return project.Address(delta.JsonObjectOf("id", params.Value("id"))), nil
	}
}

var serverKind = mustHostKind("server")

// counterContract is the counter's contract as a version of its code has it: v1; v2 evolved (its label an object, `add` taking and
// returning objects).
func counterContract(t testing.TB, placement, version string) *contracts.Contract {
	t.Helper()
	v1 := mustRemote("reload.counter", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())}, HostAddress: counterAddress(placement),
		Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{
			State: []contracts.Property{contracts.Prop("label", contracts.Str()), contracts.Prop("count", contracts.Int())},
			Methods: []contracts.MethodEntry{
				contracts.Meth("add", contracts.NewMethod(contracts.Int(), contracts.Str())),
				contracts.Meth("slow", contracts.NewMethod(contracts.Str(), contracts.Str())),
				contracts.Meth("slowAdd", contracts.NewMethod(contracts.Str(), contracts.Str())),
				contracts.Meth("code", contracts.NewMethod(contracts.Schema{}, contracts.Str())),
				contracts.Meth("visits", contracts.NewMethod(contracts.Schema{}, contracts.Int())),
			},
		})},
	})
	if version != "v2" {
		return v1
	}
	v2, err := contracts.Evolve(v1, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
		return e.State("main", "label", contracts.Obj(contracts.Prop("text", contracts.Str()), contracts.Prop("code", contracts.Str()))).
			Method("add", contracts.Obj(contracts.Prop("n", contracts.Int())), contracts.Obj(contracts.Prop("text", contracts.Str())))
	})
	if err != nil {
		t.Fatal(err)
	}
	return v2
}

// summaryContract is the summary's contract: on the server, its label the counter's seen through the summary's code.
var summaryContract = mustRemote("reload.summary", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())}, HostAddress: counterAddress("server"),
	Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{
		State:   []contracts.Property{contracts.Prop("label", contracts.Str()), contracts.Prop("count", contracts.Int())},
		Methods: []contracts.MethodEntry{contracts.Meth("code", contracts.NewMethod(contracts.Schema{}, contracts.Str()))},
	})},
})

// tasksContract is the tasks' contract: on project `id`'s worker, the finished runs' entries and how many run.
var tasksContract = mustRemote("reload.tasks", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())}, HostAddress: counterAddress("worker"),
	Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{
		State:   []contracts.Property{contracts.Prop("entries", contracts.Arr(contracts.Str())), contracts.Prop("running", contracts.Int())},
		Methods: []contracts.MethodEntry{contracts.Meth("start", contracts.NewMethod(contracts.Str(), contracts.Str()))},
	})},
})

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

// startReloadApp starts the suite's app with plugins (directory name to files; each plugin is `@test/<dir>`) and project workers.
func startReloadApp(t testing.TB, plugins map[string]map[string]string) started {
	t.Helper()
	return startTestApp(t, plugins, func(o *AppOptions) {
		o.Workers = []AppWorkerKind{testWorker(t, project, "")}
		o.Timeouts = reloadTimeouts
	})
}

// edit writes files into plugin dir of the app's root (an edit on disk) and waits until the watcher marked it dirty (or, for a plugin
// not discovered yet, the plugin set changed).
func (s started) edit(t testing.TB, dir string, files map[string]string) {
	t.Helper()
	writePlugin(t, s.root, dir, "@test/"+dir, files)
	name := "@test/" + dir
	untilMarks(t, s.app, name+" marked", func(m plugins.PluginsState) bool { return slices.Contains(m.Dirty, name) || m.Set })
}

// editAndApply edits plugin dir and applies the changes (the rollout: server, workers, clients).
func (s started) editAndApply(t testing.TB, dir string, files map[string]string) plugins.RolloutResult {
	t.Helper()
	s.edit(t, dir, files)
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
		t.Fatalf("%v (the app reported %v)", err, s.errors.all())
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

// pageView is what a web plugin's component shows of a reference it holds, and the probe's record of it (Pi's PageProbe: every state
// the reference went through, every value of its watched member).
type pageView struct {
	ref *host.Reference
	// states are every state the reference went through, as `live`, `loading`, `failed:<kind>:<reason or code>`, `ended:<code>`.
	states []string
	// frames are every value (or `failed:<reason>`) of the watched member.
	frames []delta.JsonValue
	label  delta.JsonValue
	count  delta.JsonValue
	state  string
}

// stateText is a reference's state as one string.
func stateText(state *host.RefState) string {
	switch state.Kind {
	case host.RefStateFailed:
		reason := string(state.Cause.Error.Reason)
		if reason == "" {
			reason = string(state.Cause.Error.Code)
		}
		return fmt.Sprintf("failed:%s:%s", state.Cause.Kind, reason)
	case host.RefStateEnded:
		return "ended:" + string(state.Error.Code)
	}
	return string(state.Kind)
}

// failureText is a watch failure's error as text: a Chord error's reason or code, else its message.
func failureText(err error) string {
	if chordErr, ok := data.IsChordError(err); ok {
		if chordErr.Reason != "" {
			return string(chordErr.Reason)
		}
		return string(chordErr.Code)
	}
	return err.Error()
}

// reloadPage is the page as the tests see and drive it: a tab host on the server's links endpoint, its tracked references, their
// states and member values, and the answers of calls from them.
type reloadPage struct {
	t      testing.TB
	tab    host.Host
	socket *pageSocket

	ctx      context.Context
	watchers sync.WaitGroup

	mu      sync.Mutex
	changed chan struct{}
	views   map[string]*pageView
	results []callResult
}

// signIn opens a page as the owner: a device session's token on the links endpoint.
func signIn(t testing.TB, s started) *reloadPage {
	t.Helper()
	sessions, err := s.app.Host.Get(background, Sessions, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer sessions.Release()
	issued, err := sessions.Call("issue", delta.JsonObjectOf("principal", testOwner, "label", "page", "kind", "device"), background)
	if err != nil {
		t.Fatal(err)
	}
	token := issued.(*delta.JsonObject).Value("token").(string)
	tab, err := host.CreateHost(host.HostOptions{OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tab.Load(nil, background); err != nil {
		t.Fatal(err)
	}
	socket, err := dialPage(s.port, token)
	if err != nil {
		t.Fatal(err)
	}
	if err := tab.Connect(link.WebSocketLink(socket), host.LinkOptions{Principal: testServer}, background); err != nil {
		t.Fatal(err)
	}
	socket.start()
	ctx, cancel := context.WithCancel(background)
	page := &reloadPage{t: t, tab: tab, socket: socket, ctx: ctx, changed: make(chan struct{}), views: map[string]*pageView{}}
	// Registered after the app's, so it runs before the app closes: nothing of the page outlives the test.
	t.Cleanup(func() {
		cancel()
		_ = tab.Close(background)
		socket.Close()
		page.watchers.Wait()
	})
	return page
}

// notify wakes every wait on the page.
func (p *reloadPage) notify() {
	close(p.changed)
	p.changed = make(chan struct{})
}

// record runs edit on the view name under the page's lock and wakes the waits.
func (p *reloadPage) record(name string, edit func(*pageView)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if view := p.views[name]; view != nil {
		edit(view)
	}
	p.notify()
}

// hold holds the object of project id (or the server's) as contract, tracked as name, its calls parking readyTimeout (0: the host's),
// its member `member` watched for frames; it also follows `label` and `count` as a view of the counter shows them.
func (p *reloadPage) hold(name string, contract *contracts.Contract, id string, readyTimeout time.Duration, member string) {
	p.t.Helper()
	options := host.GetOptions{}
	if readyTimeout != 0 {
		options.ReadyTimeout = &readyTimeout
	}
	params := delta.NewJsonObject(0)
	if id != "" {
		params = delta.JsonObjectOf("id", id)
	}
	ref, err := p.tab.Hold(background, contract, params, options)
	if err != nil {
		p.t.Fatal(err)
	}
	p.t.Cleanup(ref.Release)
	view := &pageView{ref: ref, state: "none"}
	p.mu.Lock()
	if old := p.views[name]; old != nil {
		// A new reference takes the name: the old component unmounted and released its reference.
		view.states, view.frames = old.states, old.frames
		view.states = append(view.states, "remount")
		old.ref.Release()
	}
	p.views[name] = view
	initial := ref.State().Value()
	view.states = append(view.states, stateText(initial))
	view.state = string(initial.Kind)
	p.mu.Unlock()
	stateWatch, err := ref.State().Watch(p.ctx)
	if err != nil {
		p.t.Fatal(err)
	}
	stateWatch.Start(func(_ context.Context, call chord.NativeWatchCall[*host.RefState]) error {
		if call.Kind == chord.WatchCallValue {
			p.record(name, func(v *pageView) {
				if v.ref == ref {
					v.states = append(v.states, stateText(call.Value))
					v.state = string(call.Value.Kind)
				}
			})
		}
		return nil
	})
	follows := []string{"label", "count"}
	if !slices.Contains(follows, member) {
		follows = append(follows, member)
	}
	for _, follow := range follows {
		if follow == "" || ref.Member(follow) == nil {
			continue
		}
		// A member's watch waits until the reference binds (Pi's probe awaits it as a promise): a goroutine of the page's own.
		p.watchers.Add(1)
		go func() {
			defer p.watchers.Done()
			watch, err := ref.Member(follow).Watch(p.ctx)
			if err != nil {
				return
			}
			if value, err := ref.Member(follow).Value(); err == nil {
				p.record(name, func(v *pageView) { v.set(follow, value) })
			}
			watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
				p.record(name, func(v *pageView) {
					if v.ref != ref {
						return
					}
					if call.Kind == chord.WatchCallValue {
						v.set(follow, call.Value)
						if follow == member {
							v.frames = append(v.frames, call.Value)
						}
					} else if follow == member {
						v.frames = append(v.frames, "failed:"+failureText(call.Error))
					}
				})
				return nil
			})
		}()
	}
}

func (v *pageView) set(member string, value delta.JsonValue) {
	switch member {
	case "label":
		v.label = value
	case "count":
		v.count = value
	}
}

// view is the tracked view name.
func (p *reloadPage) view(name string) *pageView {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.views[name]
}

// labelText is the text of the view's label as a component shows it: a string as is, an object as JSON, `none` before a value.
func (p *reloadPage) labelText(name string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	label := p.views[name].label
	switch label := label.(type) {
	case nil:
		return "none"
	case string:
		return label
	default:
		return jsonText(label)
	}
}

// countText is the text of the view's count: the number, `none` before a value.
func (p *reloadPage) countText(name string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if count := p.views[name].count; count != nil {
		return jsonText(count)
	}
	return "none"
}

// stateKind is the view's reference's state as a component shows it (its kind).
func (p *reloadPage) stateKind(name string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.views[name].state
}

// states are every state the view's reference went through.
func (p *reloadPage) states(name string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.views[name].states)
}

// frames are every value of the view's watched member.
func (p *reloadPage) frames(name string) []delta.JsonValue {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.views[name].frames)
}

// until returns once ok holds, woken by every change of the page (no polling); it fails the test after reloadWait.
func (p *reloadPage) until(what string, timeout time.Duration, ok func() bool) {
	p.t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		p.mu.Lock()
		changed := p.changed
		p.mu.Unlock()
		if ok() {
			return
		}
		select {
		case <-changed:
		case <-deadline.C:
			p.t.Fatalf("timed out waiting for %s; the page: %s", what, p.dump())
		}
	}
}

// waitLabel waits until the view's label shows text.
func (p *reloadPage) waitLabel(name, text string) {
	p.t.Helper()
	p.until(fmt.Sprintf("%s's label %q (now %q)", name, text, p.labelText(name)), reloadWait, func() bool { return p.labelText(name) == text })
}

// waitState waits until the view's reference is in kind (`live`, `loading`, `failed`).
func (p *reloadPage) waitState(name, kind string) {
	p.t.Helper()
	p.until(fmt.Sprintf("%s's state %q", name, kind), reloadWait, func() bool { return p.stateKind(name) == kind })
}

// waitStateText waits until the reference's last state is state as the probe writes it (`failed:waiting:contract_mismatch`).
func (p *reloadPage) waitStateText(name, state string, timeout time.Duration) {
	p.t.Helper()
	p.until(fmt.Sprintf("%s reaching %s (states %v)", name, state, p.states(name)), timeout, func() bool {
		states := p.states(name)
		return len(states) > 0 && states[len(states)-1] == state
	})
}

// call calls method with args on the view's reference: the number of its answer in the page's results. The call runs on a goroutine
// of its own; its answer is kept by number.
func (p *reloadPage) call(name, method string, args delta.JsonValue) int {
	p.t.Helper()
	view := p.view(name)
	p.mu.Lock()
	index := len(p.results)
	p.results = append(p.results, pendingResult)
	p.notify()
	p.mu.Unlock()
	go func() {
		value, err := view.ref.Call(method, args, background)
		p.mu.Lock()
		defer p.mu.Unlock()
		if err == nil {
			p.results[index] = callResult{Status: "ok", Value: jsonText(value)}
		} else if chordErr, ok := data.IsChordError(err); ok {
			p.results[index] = callResult{Status: "error", Code: string(chordErr.Code), Reason: string(chordErr.Reason)}
		} else {
			p.results[index] = callResult{Status: "error", Code: "thrown", Reason: err.Error()}
		}
		p.notify()
	}()
	return index
}

// peek is call index's answer now (pending or not).
func (p *reloadPage) peek(index int) callResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.results[index]
}

// result is call index's answer, once it has one.
func (p *reloadPage) result(index int) callResult {
	p.t.Helper()
	p.until(fmt.Sprintf("call %d's answer", index), reloadWait, func() bool { return p.results[index].Status != "pending" })
	return p.peek(index)
}

// pageSocket is a gorilla WebSocket as a link's socket: binary frames, a reader started once the link is attached (the server's
// `hello` comes with the upgrade, so the link must listen before the first frame is read).
type pageSocket struct {
	conn  *websocket.Conn
	write sync.Mutex

	mu        sync.Mutex
	next      int
	listeners map[link.WebSocketEvent]map[int]func(any)
	closed    bool
}

// dialPage opens the links endpoint of the app on port as the bearer of token.
func dialPage(port int, token string) (*pageSocket, error) {
	header := http.Header{"Authorization": {"Bearer " + token}}
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, response, err := dialer.Dial("ws://"+net.JoinHostPort("127.0.0.1", strconv.Itoa(port))+"/chord", header) //nolint:bodyclose // the dialer documents its handshake response body as needing no close
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("the links endpoint refused with %d: %w", response.StatusCode, err)
		}
		return nil, err
	}
	return &pageSocket{conn: conn, listeners: map[link.WebSocketEvent]map[int]func(any){}}, nil
}

// ReadyState implements link.WebSocketLike.
func (s *pageSocket) ReadyState() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 3
	}
	return 1
}

// SetBinaryType implements link.WebSocketLike.
func (s *pageSocket) SetBinaryType(string) {}

// Send implements link.WebSocketLike.
func (s *pageSocket) Send(frame []byte) error {
	s.write.Lock()
	defer s.write.Unlock()
	return s.conn.WriteMessage(websocket.BinaryMessage, frame)
}

// Close implements link.WebSocketLike.
func (s *pageSocket) Close() {
	s.write.Lock()
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	s.write.Unlock()
	_ = s.conn.Close()
}

// AddEventListener implements link.WebSocketLike.
func (s *pageSocket) AddEventListener(event link.WebSocketEvent, listener func(data any)) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listeners[event] == nil {
		s.listeners[event] = map[int]func(any){}
	}
	id := s.next
	s.next++
	s.listeners[event][id] = listener
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.listeners[event], id)
	}
}

func (s *pageSocket) emit(event link.WebSocketEvent, payload any) {
	s.mu.Lock()
	listeners := make([]func(any), 0, len(s.listeners[event]))
	for _, listener := range s.listeners[event] {
		listeners = append(listeners, listener)
	}
	s.mu.Unlock()
	for _, listener := range listeners {
		listener(payload)
	}
}

// start reads frames until the socket closes.
func (s *pageSocket) start() {
	go func() {
		for {
			kind, frame, err := s.conn.ReadMessage()
			if err != nil {
				s.mu.Lock()
				s.closed = true
				s.mu.Unlock()
				s.emit(link.WebSocketClose, nil)
				return
			}
			if kind == websocket.BinaryMessage {
				s.emit(link.WebSocketMessage, frame)
			} else {
				s.emit(link.WebSocketMessage, string(frame))
			}
		}
	}()
}

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

// dump is every view's states and label, for a failure's message.
func (p *reloadPage) dump() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for name, view := range p.views {
		out = append(out, fmt.Sprintf("%s{states %v, label %v, count %v, frames %v}", name, view.states, jsonText(view.label), jsonText(view.count), view.frames))
	}
	return strings.Join(out, " ")
}
