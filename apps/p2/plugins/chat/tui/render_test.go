package tui_test

// The chat screen on a bare host (P0c: identities, layout, reading position, snapshot, Ctrl+C/Ctrl+D; P2: plugin contributions), with the stub topology of
// test/p2/tui-fixture.ts: a server host, a session worker below it and the TUI's client host on a virtual terminal.
//
// pi: apps/p2/test/p2/tui-render.test.ts

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chattui "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/tui"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	p2tui "github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tools"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tuitest"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/vterm"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

const (
	ctrlC    = "\x03"
	ctrlD    = "\x04"
	ctrlEnd  = "\x1b[1;5F"
	ctrlHome = "\x1b[1;5H"
	pageUp   = "\x1b[5~"
	left     = "\x1b[D"
	enter    = "\r"
	escape   = "\x1b"
	up       = "\x1b[A"
)

func typed(f *tuitest.Fixture, text string) {
	for _, char := range text {
		f.Send(string(char))
	}
}

func contains(f *tuitest.Fixture, text string) func() bool {
	return func() bool { return strings.Contains(f.Screen(), text) }
}

// waitFor polls until the screen shows text.
func waitFor(t *testing.T, f *tuitest.Fixture, text string) {
	t.Helper()
	tuitest.Poll(t, contains(f, text))
}

func waitGone(t *testing.T, f *tuitest.Fixture, text string) {
	t.Helper()
	tuitest.Poll(t, func() bool { return !strings.Contains(f.Screen(), text) })
}

func answer(id int, text string) durable.EntryRecord { return tuitest.AnswerEntry(id, text, int64(id)) }

func longAnswer(count int, prefix string) string {
	lines := make([]string, count)
	for i := range lines {
		lines[i] = prefix + "-" + itoa(i)
	}
	return strings.Join(lines, "\n\n")
}

func itoa(n int) string {
	encoded, _ := json.Marshal(n)
	return string(encoded)
}

type setup struct {
	*tuitest.Topology
	Chat *tuitest.ChatStub
}

type setupOptions struct {
	entries func(query tuitest.EntriesQuery) ([]durable.EntryRecord, bool)
	facets  []host.Facet
	ready   <-chan struct{}
}

// chatRuntime is a runtime whose chat surface talks to a participant Chat stub (and Drafts) per conversation of session "s".
func chatRuntime(t *testing.T, views func(conversation int) delta.JsonValue, options setupOptions) *setup {
	t.Helper()
	chat := tuitest.NewChatStub(t, tuitest.ChatStubOptions{Views: views, Entries: options.entries})
	facets := append([]host.Facet{p2tui.CoreFacet, chattui.Facet}, options.facets...)
	top := tuitest.NewTopology(t, tuitest.TopologyOptions{
		Server: []p2test.Stub{tuitest.SessionsStub(tuitest.Session)}, Worker: []p2test.Stub{chat.Stub, tuitest.DraftsStub()},
		Facets: facets, Ready: options.ready,
	})
	return &setup{Topology: top, Chat: chat}
}

// reload reloads the TUI host with facets (the names of its start).
func (s *setup) reload(t *testing.T, facets ...host.Facet) {
	t.Helper()
	list := make([]host.HostFacet, len(facets))
	for i, facet := range facets {
		list[i] = host.HostFacet{Name: "tui-" + string(rune('0'+i)), Facet: facet}
	}
	if _, err := s.Client.Reload(list, context.Background(), host.ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
}

// surfaceNow is the chat surface; call it on the owner loop (inside Do).
func (s *setup) surfaceNow() *chattui.ChatSurface {
	surface, _ := s.Runtime.Surface("chat").(*chattui.ChatSurface)
	return surface
}

func (s *setup) surface() (surface *chattui.ChatSurface) {
	s.Do(func() { surface = s.surfaceNow() })
	return surface
}

func (s *setup) draft(identity string) (text string) {
	s.Do(func() { text = s.Runtime.Drafts[identity] })
	return text
}

func (s *setup) navigate(conversation int) {
	s.Do(func() { s.Runtime.Navigate("chat", "s", true, &conversation) })
}

func (s *setup) editorText() (text string) {
	s.Do(func() { text = s.surfaceNow().Editor.Text() })
	return text
}

func TestReadingPositionHeldWhileOutputGrowsKeptAcrossFacetReloadCtrlEndFollowsAgain(t *testing.T) {
	s := chatRuntime(t, func(int) delta.JsonValue {
		return tuitest.View(t, 1, []durable.EntryRecord{answer(1, longAnswer(60, "line"))}, tuitest.DocsOf(t), 0)
	}, setupOptions{})
	waitFor(t, s.Fixture, "line-59")
	s.Send(pageUp)
	waitGone(t, s.Fixture, "line-59")
	before := s.Screen()
	// Entries never change once committed: the output grows by a new one.
	s.Chat.SetView(1, tuitest.View(t, 1, []durable.EntryRecord{answer(1, longAnswer(60, "line")), answer(2, longAnswer(10, "appended"))}, tuitest.DocsOf(t), 0))
	time.Sleep(50 * time.Millisecond)
	if after := s.Screen(); after != before {
		t.Fatalf("the reading position moved:\n%s\n--\n%s", before, after)
	}
	// A facet reload recreates the chat surface: the anchor stored in the runtime restores the same lines. A copy of the facet is a new facet value: its plugin's
	// objects and slot are replaced.
	s.reload(t, p2tui.CoreFacet, chattui.NewFacet())
	tuitest.Poll(t, func() bool { return s.Screen() == before })
	s.Send(ctrlEnd)
	waitFor(t, s.Fixture, "appended-9")
}

func TestHistoryPrependKeepsTheVisibleContentCtrlHomeReachesTheConversationStart(t *testing.T) {
	// The window starts at a compaction marker; older entries come from earlier pages.
	older := make([]durable.EntryRecord, 20)
	for i := range older {
		older[i] = tuitest.AnswerEntry(20-i, "older-"+itoa(19-i), int64(20-i))
	}
	var pages atomic.Int32
	head := durable.EntryId(21)
	marker := tuitest.Entry(30, "pi.compaction", ai.UserMessage{Content: ai.UserText("S"), Timestamp: 30})
	marker.Head = &head
	window := tuitest.View(t, 1, []durable.EntryRecord{marker, tuitest.AnswerEntry(31, longAnswer(40, "recent"), 31)}, tuitest.DocsOf(t), 20)
	s := chatRuntime(t, func(int) delta.JsonValue { return window }, setupOptions{entries: func(tuitest.EntriesQuery) ([]durable.EntryRecord, bool) {
		pages.Add(1)
		return older, false
	}})
	waitFor(t, s.Fixture, "recent-39")
	s.Send(pageUp)
	waitGone(t, s.Fixture, "recent-39")
	s.Send(ctrlHome)
	// At the top the page loads; what was visible stays put while older entries arrive above it.
	tuitest.Poll(t, func() bool { return pages.Load() > 0 })
	time.Sleep(50 * time.Millisecond)
	after := strings.Split(s.Screen(), "\n")
	// The marker stays on the row it had below the header; older entries fill the rows above it.
	index := -1
	for i, line := range after {
		if strings.Contains(line, "[compaction]") {
			index = i
			break
		}
	}
	if index != 6 {
		t.Fatalf("the marker is on row %d, want 6:\n%s", index, strings.Join(after, "\n"))
	}
	if !strings.Contains(strings.Join(after[:6], "\n"), "older-19") {
		t.Fatalf("no older-19 above the marker:\n%s", strings.Join(after, "\n"))
	}
	s.Send(ctrlHome)
	waitFor(t, s.Fixture, "older-0")
}

func TestAGapInViewFillsItsMissingEntriesItsLoadMoreLineGoes(t *testing.T) {
	s := chatRuntime(t, func(int) delta.JsonValue {
		return tuitest.View(t, 1, []durable.EntryRecord{answer(1, "first answer")}, tuitest.DocsOf(t), 0)
	}, setupOptions{entries: func(tuitest.EntriesQuery) ([]durable.EntryRecord, bool) {
		return []durable.EntryRecord{answer(5, "filled answer")}, false
	}})
	waitFor(t, s.Fixture, "first answer")
	// The window moved on while entries 2 to 9 were never seen: a gap.
	s.Chat.SetView(1, tuitest.View(t, 1, []durable.EntryRecord{answer(10, "later answer")}, tuitest.DocsOf(t), 9))
	waitFor(t, s.Fixture, "filled answer")
	queries := s.Chat.Queries()
	two, nine := 2, 9
	if len(queries) != 1 || queries[0].Min == nil || *queries[0].Min != two || queries[0].Max == nil || *queries[0].Max != nine || queries[0].Order != "ascending" || queries[0].Limit != 100 {
		t.Fatalf("queries = %+v", queries)
	}
	if strings.Contains(s.Screen(), "Load more") {
		t.Fatal("the Load more line stays")
	}
}

func TestIdentityConversationsKeepTheirOwnHandlesDraftsSendIDsAndReadingPositionsSnapshotRoundTrip(t *testing.T) {
	s := chatRuntime(t, func(conversation int) delta.JsonValue {
		return tuitest.View(t, conversation, []durable.EntryRecord{answer(1, "conversation "+itoa(conversation)+" answer")}, tuitest.DocsOf(t), 0)
	}, setupOptions{})
	waitFor(t, s.Fixture, "conversation 1 answer")
	typed(s.Fixture, "draft one")
	s.navigate(2)
	waitFor(t, s.Fixture, "conversation 2 answer")
	if strings.Contains(s.Screen(), "draft one") {
		t.Fatal("the draft of conversation 1 shows in conversation 2")
	}
	typed(s.Fixture, "second")
	s.Send(enter)
	tuitest.Poll(t, func() bool { return len(s.Chat.Sent(2)) == 1 })
	if s.Chat.Sent(2)[0].Conversation != 2 || len(s.Chat.Sent(1)) != 0 {
		t.Fatalf("sent: %+v %+v", s.Chat.Sent(2), s.Chat.Sent(1))
	}
	s.navigate(1)
	waitFor(t, s.Fixture, "draft one")
	if got := s.draft("s#1"); got != "draft one" {
		t.Fatalf("draft = %q", got)
	}
	// The reload snapshot carries identity, drafts, pending send IDs and anchors; version 1 drafts migrate.
	var snapshot p2tui.Snapshot
	s.Do(func() {
		s.Runtime.Submissions["s#1"] = runtime.Submission{Text: "pending", ID: "send-1", Mode: "steer"}
		s.Runtime.Anchors["s#2"] = components.TranscriptAnchor{Key: "1", EntryID: new(1), Offset: 3}
		snapshot = p2tui.SnapshotRuntime(s.Runtime)
	})
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var carried p2tui.Snapshot
	if err := json.Unmarshal(encoded, &carried); err != nil {
		t.Fatal(err)
	}
	fresh := func() *runtime.TuiRuntime {
		return runtime.NewTuiRuntime(s.Client, pitui.NewTuiAltScreen(vterm.New(80, 24), nil, t.TempDir()), nil)
	}
	restored := fresh()
	p2tui.RestoreRuntime(restored, carried)
	if restored.Identity() != "s#1" || restored.Drafts["s#1"] != "draft one" {
		t.Fatalf("restored identity %q drafts %v", restored.Identity(), restored.Drafts)
	}
	if got := restored.Submissions["s#1"]; got != (runtime.Submission{Text: "pending", ID: "send-1", Mode: "steer"}) {
		t.Fatalf("submission = %+v", got)
	}
	if got, _ := restored.Anchors["s#2"].(components.TranscriptAnchor); got.Key != "1" || got.EntryID == nil || *got.EntryID != 1 || got.Offset != 3 {
		t.Fatalf("anchor = %+v", restored.Anchors["s#2"])
	}
	legacy := fresh()
	p2tui.RestoreRuntime(legacy, p2tui.Snapshot{
		HasSession: true, Session: "s", Conversation: 1, Screen: "chat",
		Drafts: []p2tui.SnapshotPair[string]{{Key: "s", Value: "old"}},
	})
	if legacy.Drafts["s#1"] != "old" {
		t.Fatalf("the version 1 draft did not migrate: %v", legacy.Drafts)
	}
	// What a snapshot cannot say is refused: an unknown screen, an anchor without an item.
	p2tui.RestoreRuntime(legacy, p2tui.Snapshot{Screen: "evil", Anchors: []p2tui.SnapshotPair[components.TranscriptAnchor]{{Key: "x"}}})
	if legacy.Screen != "chat" || len(legacy.Anchors) != 0 {
		t.Fatalf("screen %q anchors %v", legacy.Screen, legacy.Anchors)
	}
}

func TestWhileTheConversationConnectsTypedKeysStayInTheEditorAndEnterSendsOnceItIsLive(t *testing.T) {
	open := make(chan struct{})
	s := chatRuntime(t, func(int) delta.JsonValue {
		return tuitest.View(t, 1, []durable.EntryRecord{answer(1, "ready")}, tuitest.DocsOf(t), 0)
	}, setupOptions{ready: open})
	typed(s.Fixture, "typed early")
	s.Send(enter)
	// Shown at once (no transcript yet: in the pending area) and out of the editor; sent once the conversation is live.
	tuitest.Poll(t, func() bool { return s.draft("s#1") == "" })
	waitFor(t, s.Fixture, "Sending: typed early")
	if len(s.Chat.Sent(1)) != 0 {
		t.Fatal("sent before the conversation was live")
	}
	close(open)
	tuitest.Poll(t, func() bool { return len(s.Chat.Sent(1)) == 1 })
	if got := s.Chat.Sent(1)[0].Text; got != "typed early" {
		t.Fatalf("sent %q", got)
	}
}

func TestCtrlDExitsOnlyOnAnEmptyEditorCtrlCClearsTwiceExits(t *testing.T) {
	s := chatRuntime(t, func(int) delta.JsonValue {
		return tuitest.View(t, 1, []durable.EntryRecord{answer(1, "ready")}, tuitest.DocsOf(t), 0)
	}, setupOptions{})
	var exits atomic.Int32
	s.Do(func() { s.Runtime.OnExit = func() { exits.Add(1) } })
	waitFor(t, s.Fixture, "ready")
	typed(s.Fixture, "abc")
	s.Send(left)
	s.Send(ctrlD)
	tuitest.Poll(t, func() bool { return s.draft("s#1") == "ab" })
	if exits.Load() != 0 {
		t.Fatal("Ctrl+D with text deletes forward")
	}
	s.Send(ctrlC)
	tuitest.Poll(t, func() bool { return s.draft("s#1") == "" })
	if exits.Load() != 0 {
		t.Fatal("the first Ctrl+C only clears")
	}
	s.Send(ctrlC)
	if exits.Load() != 1 {
		t.Fatalf("a second Ctrl+C within 500 ms exits: %d", exits.Load())
	}
	time.Sleep(600 * time.Millisecond)
	s.Send(ctrlD)
	if exits.Load() != 2 {
		t.Fatalf("Ctrl+D on an empty editor exits: %d", exits.Load())
	}
	if s.surface() == nil {
		t.Fatal("no chat surface")
	}
}

func TestPluginContributionsToolRenderersEntryRenderersAndMarkdownTransformers(t *testing.T) {
	contributions := host.DefineFacet(func(_ host.FacetHost, self host.FacetSelf) error {
		if err := runtime.TuiToolRenderers.Contribute(self, p2runtime.Entry("artifact", &tools.ToolRenderers{
			RenderCall: func(args tools.ToolArgs, t *theme.Theme, _ *tools.ToolRenderContext) pitui.Component {
				name, _ := args.Lookup("name").(string)
				return pitui.NewPaddedText(t.Fg("accent", "artifact card "+name), 0, 0, nil)
			},
		})); err != nil {
			return err
		}
		if err := runtime.TuiEntryRenderers.Contribute(self, p2runtime.Entry("test.note", &components.EntryRenderer{
			Render: func(entry durable.EntryRecord, _ components.EntryRenderContext) pitui.Component {
				data, _ := json.Marshal(entry.Data)
				return pitui.NewPaddedText("note entry "+string(data), 1, 0, nil)
			},
		})); err != nil {
			return err
		}
		return runtime.TuiMarkdownTransformers.Contribute(self, p2runtime.Entry("10:test", render.MarkdownTransformer(func(markdown string, _ render.MarkdownTransformContext) string {
			return strings.ReplaceAll(markdown, "TOKEN", "transformed")
		})))
	})
	call := ai.ToolCall{ID: "a1", Name: "artifact", Arguments: ai.JsonObject{"name": "report"}}
	note := tuitest.Entry(2, "test.note")
	note.Data = delta.JsonObjectOf("n", float64(1))
	s := chatRuntime(t, func(int) delta.JsonValue {
		return tuitest.View(t, 1, []durable.EntryRecord{
			answer(1, "answer with TOKEN"), note,
			tuitest.Entry(3, "pi.assistant", tuitest.Assistant(ai.StopReasonToolUse, 3, call)),
		}, tuitest.DocsOf(t), 0)
	}, setupOptions{facets: []host.Facet{contributions}})
	waitFor(t, s.Fixture, "artifact card report")
	shown := s.Screen()
	if !strings.Contains(shown, "answer with transformed") || !strings.Contains(shown, `note entry {"n":1}`) {
		t.Fatalf("screen:\n%s", shown)
	}
}
