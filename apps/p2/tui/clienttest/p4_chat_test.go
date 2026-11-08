package clienttest

// test/p2/tui-p4.test.ts, the chat half: model and thinking selection (owner; guests read-only), scoped model cycling, /new, /name, /session, compaction
// controls, /new-context, Alt+Up restore-own, and closing overlays cleanly across a reload.

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	modelscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	modelstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/models/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	statustui "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/tui"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui"
	tuiruntime "github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tuitest"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/schema"

	chattui "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/tui"
)

const (
	ctrlS    = "\x13"
	shiftTab = "\x1b[Z"
	altUp    = "\x1b[1;3A"
	ctrlL    = "\x0c"
)

// p4Meta is the `META` of tui-p4.test.ts.
func p4Meta() *delta.JsonObject {
	return delta.JsonObjectOf("provider", "faux", "modelId", "faux-1", "name", "Faux one", "contextWindow", float64(1000), "reasoning", true, "thinkingLevels", []any{"off", "low", "high"})
}

// p4Models is the owner's Models of `stubs`: the catalog, the scope [faux-2:low, faux-1]; guests never get it.
func p4Models(s *p4Setup) p2test.Stub {
	choice := func(provider, id string, extra ...any) *delta.JsonObject {
		object := delta.JsonObjectOf("provider", provider, "modelId", id)
		for i := 0; i+1 < len(extra); i += 2 {
			object.Set(extra[i].(string), extra[i+1])
		}
		return object
	}
	return p2test.Stub{Contract: modelscontract.Models, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, _ p2test.StubSelf) p2test.StubMembers {
		s.calls.record("models", nil)
		return p2test.StubMembers{Variant: "owner", State: map[string]any{
			"models": []any{
				delta.JsonObjectOf("provider", "faux", "modelId", "faux-1", "name", "Faux one", "thinkingLevels", []any{"off", "low", "high"}, "contextWindow", float64(1000)),
				delta.JsonObjectOf("provider", "faux", "modelId", "faux-2", "name", "Faux two", "thinkingLevels", []any{"off"}, "contextWindow", float64(2000)),
			},
			"resources":     delta.JsonObjectOf("version", float64(1), "settings", delta.JsonObjectOf(), "credentials", []any{}, "providers", []any{}, "models", []any{}, "errors", []any{}),
			"defaultModel":  choice("faux", "faux-1", "thinkingLevel", "off"),
			"errors":        []any{},
			"enabledModels": []any{"faux-2:low", "faux-1"},
			"scopedModels":  []any{choice("faux", "faux-2", "thinkingLevel", "low"), choice("faux", "faux-1")},
		}, Methods: map[string]p2test.StubMethod{
			"setDefault": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
				s.calls.record("setDefault", args)
				return nil, nil
			},
			"setEnabledModels": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
				s.calls.record("setEnabledModels", args)
				return nil, nil
			},
		}}
	}}
}

// p4ChatWorker is the worker of `stubs`: a participant Chat that records its calls, and the drafts.
func p4ChatWorker(t *testing.T, view func() delta.JsonValue) func(s *p4Setup) []p2test.Stub {
	return func(s *p4Setup) []p2test.Stub {
		record := func(name string, result delta.JsonValue) p2test.StubMethod {
			return func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
				s.calls.record(name, args)
				return result, nil
			}
		}
		chat := tuitest.NewChatStub(t, tuitest.ChatStubOptions{
			Views: func(int) delta.JsonValue { return view() },
			Model: p4Meta(),
			Methods: map[string]p2test.StubMethod{
				"send":            record("send", delta.JsonObjectOf("submissionId", float64(1))),
				"stop":            record("stop", delta.JsonObjectOf("restored", []any{})),
				"configure":       record("configure", nil),
				"compact":         record("compact", delta.JsonObjectOf("taskId", float64(9))),
				"abortCompaction": record("abortCompaction", delta.JsonObjectOf("aborted", float64(1))),
				"reset":           record("reset", nil),
				"dequeueOwn":      record("dequeueOwn", delta.JsonObjectOf("restored", []any{"first queued", "second queued"}, "skipped", float64(1))),
			},
			Members: func(_ int, self p2test.StubSelf, members *p2test.StubMembers) {
				members.State["stats"] = self.Cell(delta.JsonObjectOf("contextTokens", float64(100)))
			},
		})
		return []p2test.Stub{chat.Stub, tuitest.DraftsStub()}
	}
}

func p4View(t *testing.T, extra ...any) func() delta.JsonValue {
	return func() delta.JsonValue {
		docs := []any{"pi.agent", map[string]any{"model": map[string]any{"provider": "faux", "modelId": "faux-1"}, "thinkingLevel": "off"}}
		return tuitest.View(t, 1, nil, tuitest.DocsOf(t, append(docs, extra...)...), 0)
	}
}

// newP4Chat is `setup` of the chat tests: the chat screen of session "s", the owner (or a guest) over the models and chat facets.
func newP4Chat(t *testing.T, guest bool, view func() delta.JsonValue) *p4Setup {
	t.Helper()
	s := newP4Chat0(t, guest, view)
	if !guest {
		// The owner's catalog is a held reference: the selectors open once it is live (before, the model is shown read-only).
		tuitest.Poll(t, func() bool {
			ready := false
			s.Do(func() {
				state, _ := s.Runtime.Surface(modelstui.StateSlot).(*modelstui.ModelState)
				ready = state != nil && state.Ready()
			})
			return ready
		})
	}
	return s
}

func newP4Chat0(t *testing.T, guest bool, view func() delta.JsonValue) *p4Setup {
	t.Helper()
	if view == nil {
		view = p4View(t)
	}
	s := newP4With(t, []contract.Session{p4Session("s", func(each *contract.Session) { each.Title = "Title s" })}, "chat", p4Options{
		guest: guest, worker: p4ChatWorker(t, view), facets: []host.Facet{chattui.NewFacet(), modelstui.Facet, statustui.Facet}, noNavigate: false,
		models: !guest,
	})
	return s
}

func (s *p4Setup) waitCount(t *testing.T, name string, count int) {
	t.Helper()
	tuitest.Poll(t, func() bool { return s.calls.count(name) >= count })
}

func (s *p4Setup) lastCall(name string) *delta.JsonObject {
	list := s.calls.get(name)
	return list[len(list)-1].(*delta.JsonObject)
}

func TestModelSelectorConfiguresTheConversationCtrlSSavesTheDefaultModelProviderIDSwitches(t *testing.T) {
	s := newP4Chat(t, false, nil)
	pollContains(t, s.Fixture, "(faux) faux-1")
	s.Send(ctrlL) // Ctrl+L: app.model.select
	pollScreen(t, s.Fixture, func(screen string) bool {
		return strings.Contains(screen, "Scope: all | scoped") || strings.Contains(screen, "Scope:")
	})
	mustMatch(t, `→ ✓ faux-1 \[faux\]|faux-2 \[faux\]`, s.Screen())
	// The scope exists, so it shows first: [faux-2, faux-1] with the current faux-1 selected; Down wraps to faux-2.
	s.Send(down)
	s.Send(enter)
	s.waitCount(t, "configure", 1)
	if got := fmtJSON(s.lastCall("configure")); got != "map[model:map[modelId:faux-2 provider:faux]]" {
		t.Fatalf("configure = %s", got)
	}
	s.Send(ctrlL)
	pollContains(t, s.Fixture, "Model Name")
	s.Send(ctrlS)
	s.waitCount(t, "setDefault", 1)
	if got := s.lastCall("setDefault").Value("modelId"); got != "faux-1" {
		t.Fatalf("the current model is preselected: %v", got)
	}
	typed(s.Fixture, "/model faux/faux-1")
	// The argument's completion is offered; Enter takes it (Pi's editor does not submit on an argument completion), the next Enter switches.
	pollContains(t, s.Fixture, "→ faux/faux-1")
	s.Send(enter)
	s.Send(enter)
	s.waitCount(t, "configure", 3)
	if got := fmtJSON(s.lastCall("configure")); got != "map[model:map[modelId:faux-1 provider:faux]]" {
		t.Fatalf("configure = %s", got)
	}
}

func TestShiftTabCyclesTheModelsThinkingLevelsCtrlPCyclesTheScopeWithItsLevelThinkingSelects(t *testing.T) {
	s := newP4Chat(t, false, nil)
	pollContains(t, s.Fixture, "(faux) faux-1")
	s.Send(shiftTab)
	s.waitCount(t, "configure", 1)
	if got := fmtJSON(s.lastCall("configure")); got != "map[thinkingLevel:low]" {
		t.Fatalf("configure = %s", got)
	}
	s.Send(ctrlP)
	s.waitCount(t, "configure", 2)
	// The scope is [faux-2:low, faux-1]; the current model is faux-1, so the next is faux-2 with its level.
	if got := fmtJSON(s.lastCall("configure")); got != "map[model:map[modelId:faux-2 provider:faux] thinkingLevel:low]" {
		t.Fatalf("configure = %s", got)
	}
	typed(s.Fixture, "/thinking")
	s.Send(enter)
	pollContains(t, s.Fixture, "Thinking Level")
	s.Send(down)
	s.Send(down)
	s.Send(enter)
	s.waitCount(t, "configure", 3)
	if got := fmtJSON(s.lastCall("configure")); got != "map[thinkingLevel:high]" {
		t.Fatalf("configure = %s", got)
	}
	typed(s.Fixture, "/scoped-models")
	s.Send(enter)
	pollContains(t, s.Fixture, "Scoped models")
	s.Send(enter) // toggles the first model
	s.Send(ctrlS)
	s.waitCount(t, "setEnabledModels", 1)
	if got := fmtJSON(s.lastCall("setEnabledModels")); got != "map[patterns:[faux/faux-2]]" {
		t.Fatalf("setEnabledModels = %s", got)
	}
}

func TestGuestsSeeTheModelReadOnlyNoSelectorNoCyclingNeverTheOwnersCatalog(t *testing.T) {
	s := newP4Chat(t, true, nil)
	pollContains(t, s.Fixture, "faux-1")
	// No surface or command asks for the owner-only Models (the footer uses Chat.model).
	s.Do(func() {})
	if s.calls.count("models") != 0 {
		t.Fatal("guests acquire Models")
	}
	s.Do(func() {
		for _, notice := range s.Runtime.Notices {
			if strings.Contains(notice.Text, "owner-only") {
				t.Errorf("a guest saw %q", notice.Text)
			}
		}
	})
	s.Send(ctrlL)
	tuitest.Poll(t, noticeContaining(s.Fixture, "only the owner changes it"))
	s.Send(shiftTab)
	s.Send(ctrlP)
	s.Do(func() {})
	if s.calls.count("configure") != 0 {
		t.Fatal("a guest configured the conversation")
	}
	// A known command that is not theirs is refused, not sent as a message.
	typed(s.Fixture, "/new-context x")
	s.Send(enter)
	tuitest.Poll(t, noticeContaining(s.Fixture, "/new-context is not available here"))
	if s.calls.count("send") != 0 || s.calls.count("reset") != 0 {
		t.Fatal("the refused command was sent")
	}
	// The footer's denominator comes from the guest-safe model metadata.
	mustMatch(t, `\d+\.\d%/1\.0k`, s.Screen())
}

func TestNewNameAndSession(t *testing.T) {
	s := newP4Chat(t, false, nil)
	pollContains(t, s.Fixture, "(faux) faux-1")
	typed(s.Fixture, "/name Better title")
	s.Send(enter)
	s.waitCount(t, "rename", 1)
	if got := s.calls.get("rename")[0]; got != [2]any{"s", "Better title"} {
		t.Fatalf("rename = %v", got)
	}
	typed(s.Fixture, "/session")
	s.Send(enter)
	pollContains(t, s.Fixture, "Whole session (children and tools)")
	pollScreen(t, s.Fixture, matches(`Viewers +2`))
	s.Send(esc)
	typed(s.Fixture, "/new")
	s.Send(enter)
	s.waitCount(t, "create", 1)
	if got := s.calls.get("create")[0].(location); got.cwd != "/work" || got.env != nil {
		t.Fatalf("create = %+v", got)
	}
	// The TUI chose the new session's ID and opened it at once.
	tuitest.Poll(t, func() bool {
		var session string
		s.Do(func() { session = s.Runtime.Session })
		created := s.calls.get("created")
		return len(created) == 1 && session == created[0]
	})
}

func TestChatControlsCompactEscapeAbortsCompactionNewContextAltUpRestoresOwnQueuedMessages(t *testing.T) {
	compacting := p4View(t, "pi.live", map[string]any{"compactions": []any{map[string]any{"taskId": 9, "trigger": "manual", "blocking": false, "attempt": 0}}})
	s := newP4Chat(t, false, compacting)
	pollContains(t, s.Fixture, "Compacting context... (escape to cancel)")
	typed(s.Fixture, "/compact keep the API notes")
	s.Send(enter)
	s.waitCount(t, "compact", 1)
	if got := fmtJSON(s.lastCall("compact")); got != "map[instructions:keep the API notes]" {
		t.Fatalf("compact = %s", got)
	}
	s.Send(esc)
	s.waitCount(t, "abortCompaction", 1)
	if s.calls.count("stop") != 0 {
		t.Fatal("only the compaction is aborted when no run is busy")
	}
	typed(s.Fixture, "/new-context Continue with the parser")
	s.Send(enter)
	s.waitCount(t, "reset", 1)
	if got := fmtJSON(s.lastCall("reset")); got != "map[handoff:Continue with the parser]" {
		t.Fatalf("reset = %s", got)
	}
	typed(s.Fixture, "draft")
	s.Send(altUp)
	tuitest.Poll(t, func() bool {
		var draft string
		s.Do(func() { draft = s.Runtime.Drafts["s#1"] })
		return draft == "first queued\n\nsecond queued\n\ndraft"
	})
	tuitest.Poll(t, noticeMatching(s.Fixture, `1 queued message was already sent`))
}

func noticeMatching(f *tuitest.Fixture, pattern string) func() bool {
	re := regexp.MustCompile(pattern)
	return func() bool {
		found := false
		f.Do(func() {
			for _, notice := range f.Runtime.Notices {
				if re.MatchString(notice.Text) {
					found = true
				}
			}
		})
		return found
	}
}

func TestAReloadWhileAnOverlayIsOpenClosesItCleanly(t *testing.T) {
	s := newP4Chat(t, false, nil)
	pollContains(t, s.Fixture, "(faux) faux-1")
	s.Send(ctrlL)
	pollContains(t, s.Fixture, "Model Name")
	var carried tui.Snapshot
	s.Do(func() {
		encoded, err := json.Marshal(tui.SnapshotRuntime(s.Runtime))
		if err != nil {
			t.Error(err)
		}
		if err := json.Unmarshal(encoded, &carried); err != nil {
			t.Error(err)
		}
		s.Runtime.Dispose()
	})
	next := tuitest.Attach(t, s.Client, 100, func(rt *tuiruntime.TuiRuntime) {
		rt.Role = tuiruntime.RoleOwner
		rt.Connected = true
		tui.RestoreRuntime(rt, carried)
	})
	pollContains(t, next, "(faux) faux-1")
	if strings.Contains(next.Screen(), "Model Name") {
		t.Fatal("a stale overlay after the reload")
	}
	var identity string
	next.Do(func() { identity = next.Runtime.Identity() })
	if identity != "s#1" {
		t.Fatalf("identity = %q", identity)
	}
	next.Send("x")
	tuitest.Poll(t, func() bool {
		var draft string
		next.Do(func() { draft = next.Runtime.Drafts["s#1"] })
		return draft == "x"
	})
}

var _ = harness.AgentState{}

func TestARefusedHoldIsReportedOnceAndNotRetriedInALoop(t *testing.T) {
	refusing, err := contracts.Remote("p2.test-refusing", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{}, HostAddress: p2runtime.ServerHost,
		Variants: []contracts.VariantEntry{contracts.Var("reader", contracts.VariantDeclaration{State: []contracts.Property{p2runtime.StateProp("value", schema.Num())}})},
	})
	if err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	s := newP4With(t, []contract.Session{p4Session("s", nil)}, "chat", p4Options{noNavigate: true, server: []p2test.Stub{{
		Contract: refusing,
		Refuse: func(*delta.JsonObject, p2runtime.Principal) error {
			attempts.Add(1)
			return p2runtime.Forbidden("refused for this principal")
		},
		Factory: func(*delta.JsonObject, p2runtime.Principal, p2test.StubSelf) p2test.StubMembers {
			return p2test.StubMembers{Variant: "reader", State: map[string]any{"value": float64(1)}}
		},
	}}})
	s.Do(func() {
		scope := s.Runtime.Ctx.Scope()
		t.Cleanup(func() { s.Do(scope.Dispose) })
		if _, err := s.Runtime.Watch(scope, refusing, delta.JsonObjectOf(), nil); err != nil {
			t.Error(err)
		}
	})
	tuitest.Poll(t, noticeMatching(s.Fixture, `refused for this principal`))
	// Forbidden is permanent: a re-acquire would show as another attempt. Settle the host's own work, then count.
	s.Do(func() {})
	if got := attempts.Load(); got != 1 {
		t.Fatalf("forbidden is permanent: %d attempts", got)
	}
	var counts []int
	s.Do(func() {
		for _, notice := range s.Runtime.Notices {
			if strings.Contains(notice.Text, "refused") {
				counts = append(counts, notice.Count)
			}
		}
	})
	if len(counts) != 1 || counts[0] > 1 {
		t.Fatalf("the refusal is reported %v times", counts)
	}
}
