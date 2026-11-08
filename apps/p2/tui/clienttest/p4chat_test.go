package clienttest

// test/p2/tui-p4.test.ts, the tests of the chat screen: model and thinking selection (owner; guests read-only), /new, /name and /session, the compaction
// controls, closing overlays across a reload, the ports card and a refused hold.

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chattui "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/tui"
	modelscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	modelstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/models/tui"
	portstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/ports/tui"
	searchtui "github.com/MichaelKinsy/PiG/apps/p2/plugins/search/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	sessionstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/tui"
	statuscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	statustui "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/tui"
	subagentstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/tui"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/jsvalue"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	tuiruntime "github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tools"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tuitest"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/schema"
	pitui "github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

const (
	ctrlL    = "\x0c"
	ctrlS    = "\x13"
	ctrlP    = "\x10"
	shiftTab = "\x1b[Z"
	altUp    = "\x1b[1;3A"
)

// asJSON is a value as JSON text, to compare what a call carried.
func asJSON(t testing.TB, value any) string {
	t.Helper()
	text, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

// p4ModelMeta is the chat's `model` (test/p2/tui-p4.test.ts META).
func p4ModelMeta() *delta.JsonObject {
	return delta.JsonObjectOf("provider", "faux", "modelId", "faux-1", "name", "Faux one", "contextWindow", float64(1000), "reasoning", true, "thinkingLevels", []any{"off", "low", "high"})
}

// p4View is `viewOf`: an empty conversation whose agent document names the model; live is the `pi.live` document, nil for none.
func p4View(t testing.TB, live *delta.JsonObject) delta.JsonValue {
	agent := delta.JsonObjectOf("model", delta.JsonObjectOf("provider", "faux", "modelId", "faux-1"), "thinkingLevel", "off")
	if live == nil {
		return tuitest.View(t, 1, nil, tuitest.DocsOf(t, "pi.agent", agent), 0)
	}
	return tuitest.View(t, 1, nil, tuitest.DocsOf(t, "pi.agent", agent, "pi.live", live), 0)
}

// p4ModelsStub is the owner's Models of the server (plugins/models/server.ts): the catalog, the scope [faux-2:low, faux-1] and the default.
func p4ModelsStub(c *calls) p2test.Stub {
	return p2test.Stub{Contract: modelscontract.Models, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, _ p2test.StubSelf) p2test.StubMembers {
		c.record("models", nil)
		recorder := func(name string) p2test.StubMethod {
			return func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
				c.record(name, args)
				return nil, nil
			}
		}
		return p2test.StubMembers{
			Variant: "owner",
			State: map[string]any{
				"models": []any{
					delta.JsonObjectOf("provider", "faux", "modelId", "faux-1", "name", "Faux one", "thinkingLevels", []any{"off", "low", "high"}, "contextWindow", float64(1000)),
					delta.JsonObjectOf("provider", "faux", "modelId", "faux-2", "name", "Faux two", "thinkingLevels", []any{"off"}, "contextWindow", float64(2000)),
				},
				"resources":     delta.JsonObjectOf("version", float64(1), "settings", delta.JsonObjectOf(), "credentials", []any{}, "providers", []any{}, "models", []any{}, "errors", []any{}),
				"defaultModel":  delta.JsonObjectOf("provider", "faux", "modelId", "faux-1", "thinkingLevel", "off"),
				"errors":        []any{},
				"enabledModels": []any{"faux-2:low", "faux-1"},
				"scopedModels": []any{
					delta.JsonObjectOf("provider", "faux", "modelId", "faux-2", "thinkingLevel", "low"),
					delta.JsonObjectOf("provider", "faux", "modelId", "faux-1"),
				},
			},
			Methods: map[string]p2test.StubMethod{"setDefault": recorder("setDefault"), "setEnabledModels": recorder("setEnabledModels")},
		}
	}}
}

// p4MetricsStub is SessionMetrics: two viewers.
func p4MetricsStub() p2test.Stub {
	return p2test.Stub{Contract: statuscontract.SessionMetrics, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, _ p2test.StubSelf) p2test.StubMembers {
		return p2test.StubMembers{Variant: "reader", State: map[string]any{"clients": float64(2), "usage": nil}}
	}}
}

type p4ChatOptions struct {
	live  *delta.JsonObject
	guest bool
	// extra are more server stubs.
	extra []p2test.Stub
	// facets replace the chat's facets: the core facet is the caller's.
	facets []host.Facet
}

type p4Chat struct {
	*tuitest.Topology
	calls *calls
	chat  *tuitest.ChatStub
}

// newP4Chat is the chat screen of session "s" with the server and worker stubs that record every call (`setup`).
func newP4Chat(t *testing.T, options p4ChatOptions) *p4Chat {
	t.Helper()
	s := &p4Chat{calls: &calls{}}
	record := func(name string) p2test.StubMethod {
		return func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
			s.calls.record(name, args)
			return nil, nil
		}
	}
	s.chat = tuitest.NewChatStub(t, tuitest.ChatStubOptions{
		Views: func(int) delta.JsonValue { return p4View(t, options.live) }, Model: p4ModelMeta(),
		Members: func(_ int, self p2test.StubSelf, members *p2test.StubMembers) {
			members.State["stats"] = self.Cell(delta.JsonObjectOf("contextTokens", float64(100)))
		},
		Methods: map[string]p2test.StubMethod{
			"stop": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
				s.calls.record("stop", nil)
				return delta.JsonObjectOf("restored", []any{}), nil
			},
			"configure": record("configure"),
			"compact": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
				s.calls.record("compact", args)
				return delta.JsonObjectOf("taskId", float64(9)), nil
			},
			"abortCompaction": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
				s.calls.record("abortCompaction", nil)
				return delta.JsonObjectOf("aborted", float64(1)), nil
			},
			"reset": record("reset"),
			"dequeueOwn": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
				s.calls.record("dequeueOwn", nil)
				return delta.JsonObjectOf("restored", []any{"first queued", "second queued"}, "skipped", float64(1)), nil
			},
		},
	})
	rows := []contract.Session{fixtureSession()}
	server := append(p4Stubs(s.calls, rows, !options.guest), p4MetricsStub())
	if !options.guest {
		server = append(server, p4ModelsStub(s.calls))
	}
	server = append(server, options.extra...)
	facets := options.facets
	if facets == nil {
		facets = []host.Facet{tui.CoreFacet, chattui.Facet, modelstui.Facet, sessionstui.Facet, statustui.Facet, subagentstui.Facet, searchtui.Facet}
	}
	role := tuiruntime.RoleOwner
	if options.guest {
		role = tuiruntime.RoleGuest
	}
	s.Topology = tuitest.NewTopology(t, tuitest.TopologyOptions{
		Server: server, Worker: []p2test.Stub{s.chat.Stub, tuitest.DraftsStub()}, Facets: facets, Role: role, Columns: 100,
	})
	return s
}

// pollCalls waits for at least n calls of name, showing the screen when they never come.
func (s *p4Chat) pollCalls(t *testing.T, name string, n int) {
	t.Helper()
	pollScreen(t, s.Fixture, func(string) bool { return s.calls.count(name) >= n })
}

func (s *p4Chat) configured(n int) func() bool {
	return func() bool { return s.calls.count("configure") == n }
}

// anyNotice says a status line, of any kind, has the text.
func anyNotice(f *tuitest.Fixture, text string) func() bool {
	return func() bool {
		found := false
		f.Do(func() {
			for _, notice := range f.Runtime.Notices {
				if strings.Contains(notice.Text, text) {
					found = true
				}
			}
		})
		return found
	}
}

func TestModelSelectorConfiguresTheConversationCtrlSSavesTheDefaultModelProviderIDSwitches(t *testing.T) {
	s := newP4Chat(t, p4ChatOptions{})
	pollContains(t, s.Fixture, "(faux) faux-1")
	s.Send(ctrlL) // app.model.select
	pollScreen(t, s.Fixture, func(screen string) bool { return strings.Contains(screen, "Scope:") })
	if !matches(`→ ✓ faux-1 \[faux\]|faux-2 \[faux\]`)(s.Screen()) {
		t.Fatalf("selector:\n%s", s.Screen())
	}
	// The scope exists, so it shows first: [faux-2, faux-1] with the current faux-1 selected; Down wraps to faux-2.
	s.Send(down)
	s.Send(enter)
	tuitest.Poll(t, s.configured(1))
	if got := asJSON(t, s.calls.get("configure")[0]); got != `{"model":{"provider":"faux","modelId":"faux-2"}}` {
		t.Fatalf("configure = %s", got)
	}
	s.Send(ctrlL)
	pollContains(t, s.Fixture, "Model Name")
	s.Send(ctrlS)
	tuitest.Poll(t, func() bool { return s.calls.count("setDefault") == 1 })
	if got := s.calls.get("setDefault")[0].(*delta.JsonObject).Value("modelId"); got != "faux-1" {
		t.Fatalf("setDefault modelId = %v: the current model is preselected", got)
	}
	typed(s.Fixture, "/model faux/faux-1")
	pollContains(t, s.Fixture, "Faux one")
	s.Send(enter)
	s.pollCalls(t, "configure", 3)
	configures := s.calls.get("configure")
	if got := asJSON(t, configures[len(configures)-1]); got != `{"model":{"provider":"faux","modelId":"faux-1"}}` {
		t.Fatalf("last configure = %s", got)
	}
}

func TestShiftTabCyclesTheModelsThinkingLevelsCtrlPCyclesTheScopeWithItsLevelThinkingSelects(t *testing.T) {
	s := newP4Chat(t, p4ChatOptions{})
	pollContains(t, s.Fixture, "(faux) faux-1")
	s.Send(shiftTab)
	tuitest.Poll(t, s.configured(1))
	if got := asJSON(t, s.calls.get("configure")[0]); got != `{"thinkingLevel":"low"}` {
		t.Fatalf("configure 1 = %s", got)
	}
	s.Send(ctrlP)
	tuitest.Poll(t, s.configured(2))
	// The scope is [faux-2:low, faux-1]; the current model is faux-1, so the next is faux-2 with its level.
	if got := asJSON(t, s.calls.get("configure")[1]); got != `{"model":{"provider":"faux","modelId":"faux-2"},"thinkingLevel":"low"}` {
		t.Fatalf("configure 2 = %s", got)
	}
	typed(s.Fixture, "/thinking")
	s.Send(enter)
	pollContains(t, s.Fixture, "Thinking Level")
	s.Send(down)
	s.Send(down)
	s.Send(enter)
	tuitest.Poll(t, s.configured(3))
	if got := asJSON(t, s.calls.get("configure")[2]); got != `{"thinkingLevel":"high"}` {
		t.Fatalf("configure 3 = %s", got)
	}
	typed(s.Fixture, "/scoped-models")
	s.Send(enter)
	pollContains(t, s.Fixture, "Scoped models")
	s.Send(enter) // toggles the first model
	s.Send(ctrlS)
	tuitest.Poll(t, func() bool { return s.calls.count("setEnabledModels") == 1 })
	if got := asJSON(t, s.calls.get("setEnabledModels")[0]); got != `{"patterns":["faux/faux-2"]}` {
		t.Fatalf("setEnabledModels = %s", got)
	}
}

func TestGuestsSeeTheModelReadOnlyNoSelectorNoCyclingNeverTheOwnersCatalog(t *testing.T) {
	s := newP4Chat(t, p4ChatOptions{guest: true})
	pollContains(t, s.Fixture, "faux-1")
	// No surface or command asks for the owner-only Models (the footer uses Chat.model).
	s.Do(func() {})
	if s.calls.count("models") != 0 {
		t.Fatal("guests never acquire Models")
	}
	if anyNotice(s.Fixture, "owner-only")() {
		t.Fatal("a notice says owner-only")
	}
	s.Send(ctrlL)
	tuitest.Poll(t, anyNotice(s.Fixture, "only the owner changes it"))
	s.Send(shiftTab)
	s.Send(ctrlP)
	s.Do(func() {}) // the keys are handled: nothing is left to arrive
	if s.calls.count("configure") != 0 {
		t.Fatalf("configure = %v", s.calls.get("configure"))
	}
	// A known command that is not theirs is refused, not sent as a message.
	typed(s.Fixture, "/new-context x")
	s.Send(enter)
	tuitest.Poll(t, anyNotice(s.Fixture, "/new-context is not available here"))
	if len(s.chat.Sent(1)) != 0 || s.calls.count("reset") != 0 {
		t.Fatalf("sent %v, reset %v", s.chat.Sent(1), s.calls.get("reset"))
	}
	// The footer's denominator comes from the guest-safe model metadata.
	pollScreen(t, s.Fixture, matches(`\d+\.\d%/1\.0k`))
}

func TestNewNameAndSession(t *testing.T) {
	s := newP4Chat(t, p4ChatOptions{})
	pollContains(t, s.Fixture, "(faux) faux-1")
	typed(s.Fixture, "/name Better title")
	s.Send(enter)
	tuitest.Poll(t, func() bool { return s.calls.count("rename") == 1 })
	if got := s.calls.get("rename")[0]; got != [2]any{"s", "Better title"} {
		t.Fatalf("rename = %v", got)
	}
	typed(s.Fixture, "/session")
	s.Send(enter)
	pollContains(t, s.Fixture, "Whole session (children and tools)")
	if !matches(`Viewers +2`)(s.Screen()) {
		t.Fatalf("session info:\n%s", s.Screen())
	}
	s.Send(esc)
	typed(s.Fixture, "/new")
	s.Send(enter)
	tuitest.Poll(t, func() bool { return s.calls.count("create") == 1 })
	if got := s.calls.get("create")[0]; got != (location{env: nil, cwd: "/work"}) {
		t.Fatalf("create = %v", got)
	}
	// The TUI chose the new session's ID and opened it at once.
	session := ""
	s.Do(func() { session = s.Runtime.Session })
	if created := s.calls.get("created"); len(created) != 1 || session != created[0] {
		t.Fatalf("session = %q, created %v", session, created)
	}
}

func TestChatControlsCompactEscapeAbortsCompactionNewContextAltUpRestoresOwnQueuedMessages(t *testing.T) {
	compacting := delta.JsonObjectOf("compactions", []any{delta.JsonObjectOf("taskId", float64(9), "trigger", "manual", "blocking", false, "attempt", float64(0))})
	s := newP4Chat(t, p4ChatOptions{live: compacting})
	pollContains(t, s.Fixture, "Compacting context... (escape to cancel)")
	typed(s.Fixture, "/compact keep the API notes")
	s.Send(enter)
	tuitest.Poll(t, func() bool { return s.calls.count("compact") == 1 })
	if got := asJSON(t, s.calls.get("compact")[0]); got != `{"instructions":"keep the API notes"}` {
		t.Fatalf("compact = %s", got)
	}
	s.Send(esc)
	tuitest.Poll(t, func() bool { return s.calls.count("abortCompaction") == 1 })
	if s.calls.count("stop") != 0 {
		t.Fatal("only the compaction is aborted when no run is busy")
	}
	typed(s.Fixture, "/new-context Continue with the parser")
	s.Send(enter)
	tuitest.Poll(t, func() bool { return s.calls.count("reset") == 1 })
	if got := asJSON(t, s.calls.get("reset")[0]); got != `{"handoff":"Continue with the parser"}` {
		t.Fatalf("reset = %s", got)
	}
	typed(s.Fixture, "draft")
	s.Send(altUp)
	s.Poll(func() bool { return s.Runtime.Drafts["s#1"] == "first queued\n\nsecond queued\n\ndraft" })
	tuitest.Poll(t, anyNotice(s.Fixture, "1 queued message was already sent"))
}

func TestAReloadWhileAnOverlayIsOpenClosesItCleanly(t *testing.T) {
	s := newP4Chat(t, p4ChatOptions{})
	pollContains(t, s.Fixture, "(faux) faux-1")
	s.Send(ctrlL)
	pollContains(t, s.Fixture, "Model Name")
	var snapshot tui.Snapshot
	s.Do(func() { snapshot = tui.SnapshotRuntime(s.Runtime) })
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var carried tui.Snapshot
	if err := json.Unmarshal(encoded, &carried); err != nil {
		t.Fatal(err)
	}
	s.Do(func() { s.Runtime.Dispose() })
	next := tuitest.OnHostWith(t, s.Client, tuiruntime.RoleOwner, 100, 24, func(rt *tuiruntime.TuiRuntime) { tui.RestoreRuntime(rt, carried) })
	next.Poll(next.Runtime.SlotsReady)
	pollContains(t, next, "(faux) faux-1")
	if strings.Contains(next.Screen(), "Model Name") {
		t.Fatalf("a stale overlay after the reload:\n%s", next.Screen())
	}
	identity := ""
	next.Do(func() { identity = next.Runtime.Identity() })
	if identity != "s#1" {
		t.Fatalf("identity = %q", identity)
	}
	next.Send("x")
	next.Poll(func() bool { return next.Runtime.Drafts["s#1"] == "x" })
}

// portsContext is what the card renders with: the dark theme, no key hints.
type portsContext struct{ theme *theme.ThemeRef }

func (c *portsContext) Theme() *theme.ThemeRef                             { return c.theme }
func (c *portsContext) KeyText(string) string                              { return "" }
func (c *portsContext) RequestRender()                                     {}
func (c *portsContext) MarkdownTransformers() []render.MarkdownTransformer { return nil }
func (c *portsContext) Settings() *prefs.TuiSettings                       { return nil }
func (c *portsContext) OutputPad() int                                     { return 1 }
func (c *portsContext) Cwd() string                                        { return "" }

var _ = tools.ToolRenderers{}

func TestPortsCardActionPortAndNameRadiusForwardAddressesOnly(t *testing.T) {
	dark, _ := theme.BuiltinTheme("dark")
	built, err := theme.CreateTheme(dark, pitui.TerminalColorModeTrueColor, theme.TerminalState{})
	if err != nil {
		t.Fatal(err)
	}
	url := "https://fwd--" + strings.Repeat("a", 32) + "--x.example.test/"
	card := components.NewToolExecutionComponent(transcript.ToolView{
		ID: "p", Name: "ports", Status: "done", Output: "Forwarding port 3000.",
		Args: jsvalue.NewObject().Set("action", "open").Set("port", float64(3000)).Set("name", "web").Set("public", true),
		Details: jsvalue.NewObject().Set("action", "open").Set("forward", jsvalue.NewObject().
			Set("url", url).Set("publicUrl", "https://evil.test/").Set("name", "web").Set("port", float64(3000))),
	}, portstui.Renderers, &portsContext{theme: theme.NewThemeRef(built)}, true)
	lines := card.Render(100)
	for i, line := range lines {
		lines[i] = strings.TrimRight(widthx.StripTerminalSequences(line), " ")
	}
	shown := strings.Join(lines, "\n")
	if !strings.Contains(shown, "ports open · 3000 · web · public") {
		t.Fatalf("card:\n%s", shown)
	}
	if !regexp.MustCompile(`Forwarding port 3000\.\n +Open: https://fwd--a{32}--x\.example\.test/`).MatchString(shown) {
		t.Fatalf("card:\n%s", shown)
	}
	if strings.Contains(shown, "evil.test") {
		t.Fatalf("only Radius forward addresses:\n%s", shown)
	}
}

func TestARefusedHoldIsReportedOnceAndNotRetriedInALoop(t *testing.T) {
	refusing, err := contracts.Remote("p2.test-refusing", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{}, HostAddress: p2runtime.ServerHost,
		Variants: []contracts.VariantEntry{contracts.Var("reader", contracts.VariantDeclaration{State: []contracts.Property{p2runtime.StateProp("value", schema.Num())}})},
	})
	if err != nil {
		t.Fatal(err)
	}
	attempts := &calls{}
	s := newP4Chat(t, p4ChatOptions{
		facets: []host.Facet{tui.CoreFacet},
		extra: []p2test.Stub{{Contract: refusing, Factory: func(*delta.JsonObject, p2runtime.Principal, p2test.StubSelf) p2test.StubMembers {
			attempts.record("attempt", nil)
			return p2test.StubMembers{Err: p2runtime.Forbidden("refused for this principal")}
		}}},
	})
	scope := s.Runtime.Ctx.Scope()
	t.Cleanup(scope.Dispose)
	held, err := scope.Hold(refusing, delta.JsonObjectOf())
	if err != nil {
		t.Fatal(err)
	}
	tuitest.Poll(t, anyNotice(s.Fixture, "refused for this principal"))
	// Forbidden is permanent: the reference is broken, with no retry time.
	if state := held.State().Value(); state.Kind != host.RefStateFailed || state.Cause.Kind != data.FailureBroken || state.RetryAt != nil {
		t.Fatalf("the refused reference is %+v: no automatic retry follows", state)
	}
	if attempts.count("attempt") != 1 {
		t.Fatalf("attempts = %d", attempts.count("attempt"))
	}
	var counts []int
	s.Do(func() {
		for _, notice := range s.Runtime.Notices {
			if strings.Contains(notice.Text, "refused") {
				counts = append(counts, notice.Count)
			}
		}
	})
	if len(counts) != 1 || counts[0] != 1 {
		t.Fatalf("notice counts = %v", counts)
	}
}
