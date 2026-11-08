// Package tui is the chat screen in the TUI: coding-agent's fullscreen chat layout (chat-viewport.ts, DurableTui) over a p2 conversation. The transcript is
// incremental components in a ScrollView (pi-tui's scroll and search keys), the dock holds the pending area and pi's editor with the working indicator in
// its border. Everything the reader leaves behind is kept per viewed conversation, (session, conversation): references, drafts, pending send IDs and the
// reading position (P0c).
//
// Forced Go differences: every method runs on the TUI's owner loop; a call to the server blocks, so it runs on a goroutine of its own and continues on the loop
// with runtime.Post. A watched value is read where it is used (its reader), not copied by a callback, and what Pi's bind callback does once a reference is
// live happens in update, which every change of a scope runs.
//
// Ports apps/p2/plugins/chat/tui.ts.
package tui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	draftscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/drafts/contract"
	filescontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/files/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	treecontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/tree/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	p2tui "github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tools"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	chordtui "github.com/MichaelKinsy/PiG/chord/next/tui"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	pitui "github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// outgoing is a message sent from here that the server does not show yet: in the transcript, or the queue while a run is busy.
type outgoing struct {
	id    string
	text  string
	mode  string
	files []LauncherUpload
}

// noOutgoing is no message in flight: one slice, so a transcript rebuild sees "unchanged".
var noOutgoing = []outgoing{}

// pi: a second Ctrl+C within this window exits; a second Escape runs `doubleEscapeAction`.
const (
	doubleCtrlCMs  = 500
	doubleEscapeMs = 500
)

func randomUUID() string {
	var id [16]byte
	_, _ = rand.Read(id[:])
	id[6], id[8] = id[6]&0x0f|0x40, id[8]&0x3f|0x80
	hexed := hex.EncodeToString(id[:])
	return hexed[0:8] + "-" + hexed[8:12] + "-" + hexed[12:16] + "-" + hexed[16:20] + "-" + hexed[20:]
}

// viewWatchable is a reference's `view` member as the transcript store follows it; comparable, so the store can tell another reference's.
type viewWatchable struct{ member *host.ReferenceMember }

func (v viewWatchable) Value() delta.JsonValue {
	value, _ := v.member.Value()
	return value
}

func (v viewWatchable) Watch(ctx context.Context) (chord.WatchHandle[delta.JsonValue], error) {
	return v.member.Watch(ctx)
}

// variantOf is the variant a reference is bound to, "" before its first binding.
func variantOf(ref *host.Reference) string {
	variant, _, _ := ref.Variant()
	return variant
}

// liveIn is ref when it is live in one of variants, else nil.
func liveIn(ref *host.Reference, variants ...string) *host.Reference {
	if ref == nil || p2runtime.StatusOf(ref) != host.RefStateLive || !slices.Contains(variants, variantOf(ref)) {
		return nil
	}
	return ref
}

// entriesOf pages a conversation's entries through `Chat.entries` of ref.
func entriesOf(ref *host.Reference) harness.EntryLoader {
	return func(ctx context.Context, query durable.EntryQuery, limit int) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
		args := delta.NewJsonObject(4)
		if query.MinEntryId != nil {
			args.Set("minEntryId", float64(*query.MinEntryId))
		}
		if query.MaxEntryId != nil {
			args.Set("maxEntryId", float64(*query.MaxEntryId))
		}
		if query.Order != nil {
			if *query.Order == durable.ScanAscending {
				args.Set("order", "ascending")
			} else {
				args.Set("order", "descending")
			}
		}
		args.Set("limit", float64(limit))
		result, err := ref.Call("entries", args, ctx)
		if err != nil {
			return durable.Page[durable.EntryRecord, durable.Cursor]{}, err
		}
		var page durable.Page[durable.EntryRecord, durable.Cursor]
		for _, each := range p2tui.JList(p2tui.JField(result, "items")) {
			record, err := durable.FromJsonValue[durable.EntryRecord](each)
			if err != nil {
				return page, err
			}
			page.Items = append(page.Items, record)
		}
		if p2tui.JBool(p2tui.JField(result, "more")) {
			page.Next = &durable.Cursor{}
		}
		return page, nil
	}
}

// fileIndex is a live FileIndex reader reference as the completion's file source.
type fileIndex struct{ ref *host.Reference }

func (f fileIndex) Complete(ctx context.Context, query string, limit int) ([]p2runtime.IndexedPath, bool, error) {
	result, err := f.ref.Call("complete", delta.JsonObjectOf("query", query, "limit", float64(limit)), ctx)
	if err != nil {
		return nil, false, err
	}
	var items []p2runtime.IndexedPath
	for _, each := range p2tui.JList(p2tui.JField(result, "items")) {
		items = append(items, p2runtime.IndexedPath{Path: p2tui.JStr(p2tui.JField(each, "path")), Dir: p2tui.JBool(p2tui.JField(each, "dir"))})
	}
	return items, p2tui.JBool(p2tui.JField(result, "truncated")), nil
}

func (f fileIndex) List(ctx context.Context, path string) ([]p2runtime.DirectoryEntry, bool, error) {
	result, err := f.ref.Call("list", delta.JsonObjectOf("path", path), ctx)
	if err != nil {
		return nil, false, err
	}
	var entries []p2runtime.DirectoryEntry
	for _, each := range p2tui.JList(p2tui.JField(result, "entries")) {
		entries = append(entries, p2runtime.DirectoryEntry{Name: p2tui.JStr(p2tui.JField(each, "name")), Dir: p2tui.JBool(p2tui.JField(each, "dir"))})
	}
	return entries, p2tui.JBool(p2tui.JField(result, "truncated")), nil
}

// shellOf is a `liveShell` value as the shell command it describes, nil for none.
func shellOf(value any) *chatcontract.LiveShell {
	if p2tui.JObj(value) == nil {
		return nil
	}
	shell := &chatcontract.LiveShell{
		ID: p2tui.JStr(p2tui.JField(value, "id")), Command: p2tui.JStr(p2tui.JField(value, "command")),
		IncludeInContext: p2tui.JBool(p2tui.JField(value, "includeInContext")), Output: p2tui.JStr(p2tui.JField(value, "output")),
		Truncated: p2tui.JBool(p2tui.JField(value, "truncated")), Running: p2tui.JBool(p2tui.JField(value, "running")),
		Cancelled: p2tui.JBool(p2tui.JField(value, "cancelled")),
	}
	author := p2tui.JField(value, "author")
	shell.Author = p2runtime.Author{Kind: p2runtime.AuthorKind(p2tui.JStr(p2tui.JField(author, "kind"))), ID: p2tui.JStr(p2tui.JField(author, "id")), Name: p2tui.JStr(p2tui.JField(author, "name"))}
	if code, ok := p2tui.JField(value, "exitCode").(float64); ok {
		shell.ExitCode = &code
	}
	if message, ok := p2tui.JField(value, "error").(string); ok {
		shell.Error = &message
	}
	return shell
}

// sameSlice is JavaScript's identity of two lists that are replaced, never changed in place.
func sameSlice[T any](a, b []T) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
}

// ChatSurface is the chat screen.
type ChatSurface struct {
	runtime *runtime.TuiRuntime
	ctx     context.Context
	cancel  context.CancelFunc
	// Editor is pi's editor; Focus is what takes the cursor.
	Editor     *components.CustomEditor
	transcript *components.TranscriptView
	scroll     *pitui.ScrollView
	pending    *pitui.Container
	status     *pitui.Container
	input      *pitui.Container
	dock       *pitui.VStack
	// chips is the composer bar's chips (`composer:<plugin>` slots, the web's SlotList "composer"), one line under the editor.
	chips pitui.Component
	// canReply: replies exist while a plugin allows them (chat-mode, in chat mode), as the web's `canReply`.
	canReply bool
	// mentionName is the agent's name to mention while chat mode is on (chat-mode's setting), offered first as `@name`.
	mentionName string
	// replyTo is the message the next send answers.
	replyTo  *runtime.ReplyTo
	readOnly *pitui.Text

	scope    *chordtui.TuiScope
	sessions *chordtui.TuiScope
	// identity is the viewed identity, `IdentityKey(session, conversation)`, "" for none.
	identity     string
	session      string
	hasSession   bool
	conversation int
	rowsRead     func() (delta.JsonValue, bool)
	catalog      *host.Reference
	ref          *host.Reference
	// stops end what the viewed identity follows (a stop per follow), at the next switch.
	stops []func()

	// outgoing: messages in flight (`send`) are shown at once, the editor already cleared; a refused one comes back to the editor.
	outgoing func() []outgoing
	sending  chordtui.OptimisticAdd[outgoing]
	// lastTurn is the finish of the latest action made with perform; turnMu guards it.
	turnMu   sync.Mutex
	lastTurn <-chan struct{}
	// starting: a `!cmd` in flight, its block shows as running before the server's state does.
	starting func() *chatcontract.LiveShell
	start    chordtui.OptimisticAdd[*chatcontract.LiveShell]
	// compacting: a `/compact` in flight, the compaction indicator shows before `pi.live` does.
	compacting func() bool
	compactNow chordtui.OptimisticAdd[bool]

	// tree is the viewed session's branches (`/tree`), held from its first use until another session is viewed: a switch of the main conversation changes the
	// view's identity but must not release it while `navigate` is in flight.
	tree *treeHold

	viewRead      func() (delta.JsonValue, bool)
	liveShellRead func() (delta.JsonValue, bool)
	view          delta.JsonValue
	viewLoaded    bool
	bound         bool
	// shellView is the running command's component (the transcript tail).
	shellView *shellView

	// store is the conversation's transcript: entries that leave the view's window stay. It lives in the runtime's stores, so switching back to a conversation
	// finds its pages loaded.
	store         *transcript.TranscriptStore
	unsubscribe   func()
	earlierFailed map[*transcript.TranscriptStore]bool
	gapsFailed    map[transcript.Gap]bool
	current       *transcript.Transcript

	synced        syncInputs
	disposed      bool
	lastCtrlC     time.Time
	lastEscape    time.Time
	indicator     *components.StatusIndicator
	indicatorKey  string
	toolRenderers map[string]*tools.ToolRenderers
	resolvers     []tools.NamedResolver
	entryRender   map[string]*components.EntryRenderer
	transformers  []render.MarkdownTransformer
	displays      map[string]func(data any) *chatcontract.EntryDisplay
	// the registries' tables as last applied, and their readers
	tables  registryTables
	readers registryReaders

	files   *host.Reference
	drafts  *host.Reference
	claimed bool

	// attachments are uploaded files (P9) waiting for the next send, by identity; added only by the launcher's uploads.
	attachments map[string][]LauncherUpload

	removeListener func()
	removeAnswers  func()
	pluginSettings string
	autocomplete   sync.WaitGroup
}

type treeHold struct {
	session string
	scope   *chordtui.TuiScope
	ref     *host.Reference
}

type shellView struct {
	id        string
	component *components.BashExecutionComponent
}

// syncInputs are what the transcript was last built from, compared by identity.
type syncInputs struct {
	pages        *transcript.Pages
	outgoing     []outgoing
	displays     map[string]func(data any) *chatcontract.EntryDisplay
	tools        map[string]*tools.ToolRenderers
	resolvers    []tools.NamedResolver
	entries      map[string]*components.EntryRenderer
	hasInputs    bool
	pluginsEpoch int
}

type registryTables struct {
	tools, resolvers, entries, transformers, displays any
}

type registryReaders struct {
	tools, resolvers, entries, transformers, displays func() (any, bool)
}

var _ runtime.ChatSurfaceAPI = (*ChatSurface)(nil)

// ChatSurfaceMarker brands the chat surface (Pi's CHAT_SURFACE).
func (s *ChatSurface) ChatSurfaceMarker() {}

// chatContext is what the transcript's components need of the surface.
type chatContext struct{ s *ChatSurface }

func (c chatContext) Theme() *theme.ThemeRef                             { return c.s.runtime.Theme() }
func (c chatContext) KeyText(id string) string                           { return c.s.runtime.Ctx.KeyText(id) }
func (c chatContext) RequestRender()                                     { c.s.runtime.TUI.RequestRender() }
func (c chatContext) Cwd() string                                        { return c.s.cwd() }
func (c chatContext) MarkdownTransformers() []render.MarkdownTransformer { return c.s.transformers }
func (c chatContext) Settings() *prefs.TuiSettings {
	settings := c.s.runtime.Appearance.Settings
	return &settings
}
func (c chatContext) OutputPad() int {
	if pad := c.s.runtime.Appearance.Settings.OutputPad; pad != nil {
		return *pad
	}
	return 1
}
func (c chatContext) ToolRenderers(name string) *tools.ToolRenderers {
	return tools.ResolveToolRenderers(name, c.s.resolvers, c.s.toolRenderers, c.s.runtime.Appearance.Settings)
}
func (c chatContext) EntryRenderers() map[string]*components.EntryRenderer { return c.s.entryRender }

// pluginSettingsKey is the plugin settings as text: resolvers read them, so a change draws every card again.
func (s *ChatSurface) pluginSettingsKey() string {
	encoded, _ := json.Marshal(s.runtime.Appearance.Settings.Plugins)
	return string(encoded)
}

// NewChatSurface is the chat surface of rt.
func NewChatSurface(rt *runtime.TuiRuntime) *ChatSurface {
	s := &ChatSurface{
		runtime: rt, conversation: 1, mentionName: "Pi", outgoing: func() []outgoing { return noOutgoing },
		starting: func() *chatcontract.LiveShell { return nil }, compacting: func() bool { return false },
		earlierFailed: map[*transcript.TranscriptStore]bool{}, gapsFailed: map[transcript.Gap]bool{},
		attachments: map[string][]LauncherUpload{}, readOnly: pitui.NewPaddedText("", 1, 0, nil),
		pending: pitui.NewContainer(), status: pitui.NewContainer(), input: pitui.NewContainer(),
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.chips = chipsLine{s}
	one := 1
	s.Editor = components.NewCustomEditor(rt.TUI, rt.Ctx, pitui.EditorOptions{PaddingX: &one})
	s.transcript = components.NewTranscriptView(chatContext{s}, components.ViewState{Expanded: rt.ExpandedTools, ShowThinking: rt.ShowThinking}, headerOf{rt})
	s.scroll = pitui.NewScrollView(s.transcript, pitui.ScrollViewOptions{
		Follow: "end", Primary: true, Overscroll: "chain", Scrollbar: pitui.ScrollbarAuto,
		ScrollbarTrackStyle: func(text string) string { return rt.Theme().Current().Fg("scrollbarTrack", text) },
		ScrollbarThumbStyle: func(text string) string { return rt.Theme().Current().Fg("scrollbarThumb", text) },
	})
	s.transcript.Attach(s.scroll)
	s.transcript.OnTop = s.loadEarlier
	s.transcript.OnVisibleGap = func(item transcript.GapItem) { s.fillGap(item.Gap) }
	s.transcript.OnMissingAnchor = s.missingAnchor
	s.Editor.OnChange = func(string) {
		if s.identity != "" {
			rt.Drafts[s.identity] = s.Editor.GetExpandedText()
		}
		s.syncBorder()
	}
	s.Editor.SetAutocomplete(p2tui.NewFileCompletionProvider(rt.Ctx.Commands().Autocomplete(), s.fileSource, s.hint, func() string {
		// Chat mode: `@pi` (or the agent's name there) mentions the agent; offered first while chat mode is on.
		if s.canReply {
			return s.mentionName
		}
		return ""
	}))
	// The editor's completion results apply on the owner loop.
	s.Editor.SetAsyncApply(rt.Post)
	s.Editor.SetAutocompleteTaskOwner(s.ctx, func(task func()) {
		s.autocomplete.Add(1)
		go func() { defer s.autocomplete.Done(); task() }()
	}, func(err error) {
		if err != nil && s.ctx.Err() == nil {
			rt.Post(func() { rt.Fail(err) })
		}
	})
	// Editor clears before onSubmit: restore until admission succeeds, including failed/offline sends.
	s.Editor.OnSubmit = s.submit
	s.input.AddChild(s.Editor)
	one0 := 1
	zero := 0
	s.dock = pitui.NewVStack([]pitui.StackChild{
		{Component: s.pending, StackEntryOptions: pitui.StackEntryOptions{Shrink: &one0, MinSize: &zero}},
		{Component: s.status, StackEntryOptions: pitui.StackEntryOptions{Shrink: &one0, MinSize: &zero}},
		{Component: s.input, StackEntryOptions: pitui.StackEntryOptions{Shrink: &one0, MinSize: &one0}},
		{Component: s.chips, StackEntryOptions: pitui.StackEntryOptions{Shrink: &one0, MinSize: &zero}},
	}, pitui.StackOptions{})
	// The session's cwd: tool paths display relative to it.
	s.sessions = rt.Ctx.Scope()
	s.readers = registryReaders{
		tools:        chordtui.Registry(s.sessions, runtime.TuiToolRenderers.Registry),
		resolvers:    chordtui.Registry(s.sessions, runtime.TuiToolRendererResolvers.Registry),
		entries:      chordtui.Registry(s.sessions, runtime.TuiEntryRenderers.Registry),
		transformers: chordtui.Registry(s.sessions, runtime.TuiMarkdownTransformers.Registry),
		displays:     chordtui.Registry(s.sessions, chatcontract.TranscriptEntries.Registry),
	}
	if catalog, err := s.sessions.Hold(sessionscontract.Sessions, delta.JsonObjectOf()); err == nil {
		s.catalog = catalog
		s.rowsRead = chordtui.WatchMember(s.sessions, catalog.Member("sessions"))
	}
	// Resolvers read plugin settings (`ToolRendererResolver`): a change there draws every card again.
	s.pluginSettings = s.pluginSettingsKey()
	s.removeListener = rt.AddListener(s.changed)
	// What the launcher may ask for under a grant (P5: Ctrl+X copy, Ctrl+G external editor).
	s.removeAnswers = s.registerLauncherAnswers()
	s.update()
	return s
}

// headerOf is the runtime's header slot (or the built-in one) at the top of the transcript.
type headerOf struct{ rt *runtime.TuiRuntime }

func (h headerOf) Render(width int) []string { return h.rt.Header().Render(width) }
func (h headerOf) Invalidate()               { h.rt.Header().Invalidate() }

// chipsLine is the composer bar's chips under the editor.
type chipsLine struct{ s *ChatSurface }

func (c chipsLine) Invalidate() {}
func (c chipsLine) Render(width int) []string {
	s := c.s
	current := s.runtime.Theme().Current()
	var parts []string
	if s.identity != "" {
		for _, file := range s.attachments[s.identity] {
			parts = append(parts, current.Fg("accent", fmt.Sprintf("[%s %s]", render.LineText(file.Name), FormatSize(file.Size))))
		}
	}
	for _, surface := range s.runtime.Slots("composer:") {
		parts = append(parts, surface.Render(width)...)
	}
	if len(parts) == 0 {
		return nil
	}
	return []string{widthx.TruncateToWidth(" "+strings.Join(parts, "  "), width, "...", false)}
}

// hint shows a status line of the completion, unless it is the last one shown.
func (s *ChatSurface) hint(text string) {
	s.runtime.Post(func() {
		if n := len(s.runtime.Notices); n == 0 || s.runtime.Notices[n-1].Text != text {
			s.runtime.Notify(text, runtime.NoticeInfo)
		}
	})
}

// cwd is where the viewed conversation works (its agent's cwd: a subagent's own, else the session's).
func (s *ChatSurface) cwd() string {
	if s.store != nil {
		if pages := s.store.Value(); pages != nil {
			if agent, ok := pages.View.Docs.Get("pi.agent"); ok && agent != nil {
				if cwd, ok := agent.Get("cwd"); ok {
					if text, ok := cwd.(string); ok {
						return text
					}
				}
			}
		}
	}
	for _, row := range s.rows() {
		if row.ID == s.session {
			return row.Cwd
		}
	}
	return ""
}

func (s *ChatSurface) rows() []sessionscontract.Session {
	if s.rowsRead == nil || liveIn(s.catalog, "owner", "guest") == nil {
		return nil
	}
	value, _ := s.rowsRead()
	var rows []sessionscontract.Session
	for _, each := range p2tui.JList(value) {
		rows = append(rows, sessionscontract.SessionFromJSON(each))
	}
	return rows
}

// changed is the runtime's change: registries and settings first, then the identity.
func (s *ChatSurface) changed() {
	if next := s.pluginSettingsKey(); next != s.pluginSettings {
		s.pluginSettings = next
		s.renderersChanged()
	}
	s.update()
}

// readTables follows the registries: a new table redraws every card.
func (s *ChatSurface) readTables() {
	changed := false
	read := func(reader func() (any, bool), last *any) (any, bool) {
		value, ok := reader()
		if !ok || value == *last {
			return nil, false
		}
		*last = value
		changed = true
		return value, true
	}
	if table, ok := read(s.readers.tools, &s.tables.tools); ok {
		s.toolRenderers = map[string]*tools.ToolRenderers{}
		if t, ok := table.(*p2runtime.KeyedTable[*tools.ToolRenderers]); ok {
			for _, key := range t.Keys() {
				s.toolRenderers[key], _ = t.Get(key)
			}
		}
	}
	if table, ok := read(s.readers.resolvers, &s.tables.resolvers); ok {
		s.resolvers = nil
		if t, ok := table.(*p2runtime.KeyedTable[tools.ToolRendererResolver]); ok {
			for _, key := range t.Keys() {
				resolver, _ := t.Get(key)
				s.resolvers = append(s.resolvers, tools.NamedResolver{Key: key, Resolver: resolver})
			}
		}
	}
	if table, ok := read(s.readers.entries, &s.tables.entries); ok {
		s.entryRender = map[string]*components.EntryRenderer{}
		if t, ok := table.(*p2runtime.KeyedTable[*components.EntryRenderer]); ok {
			for _, key := range t.Keys() {
				s.entryRender[key], _ = t.Get(key)
			}
		}
	}
	if table, ok := read(s.readers.transformers, &s.tables.transformers); ok {
		s.transformers = nil
		if t, ok := table.(*p2runtime.KeyedTable[render.MarkdownTransformer]); ok {
			s.transformers = runtime.Ordered(t)
		}
	}
	if table, ok := read(s.readers.displays, &s.tables.displays); ok {
		s.displays = map[string]func(data any) *chatcontract.EntryDisplay{}
		if t, ok := table.(*p2runtime.KeyedTable[func(data any) *chatcontract.EntryDisplay]); ok {
			for _, key := range t.Keys() {
				s.displays[key], _ = t.Get(key)
			}
		}
	}
	if changed {
		s.renderersChanged()
	}
}

// renderersChanged: renderer contributions changed (facet reload): rebuild every component with the new tables.
func (s *ChatSurface) renderersChanged() {
	s.transcript.Reset()
	s.synced = syncInputs{}
	s.sync()
	s.runtime.TUI.RequestRender()
}

// display is what a plugin adds to its entry kind's messages.
func (s *ChatSurface) display(kind string, data any) *transcript.ReplyDisplay {
	show := s.displays[kind]
	if show == nil {
		return nil
	}
	shown := show(data)
	if shown == nil {
		return nil
	}
	out := &transcript.ReplyDisplay{Note: shown.Note}
	if shown.ReplyTo != nil {
		out.ReplyTo = &transcript.ReplyQuote{EntryID: int(shown.ReplyTo.EntryID), Name: shown.ReplyTo.Name, Excerpt: shown.ReplyTo.Excerpt}
	}
	return out
}

// Layout is the fullscreen layout: the transcript fills the screen, the dock sits under it.
func (s *ChatSurface) Layout() *runtime.SurfaceLayout {
	return &runtime.SurfaceLayout{Body: s.scroll, Dock: s.dock}
}

// Focus is what takes the cursor: the editor.
func (s *ChatSurface) Focus() pitui.Component { return s.Editor }

// SaveState keeps the reading position of the viewed conversation in the runtime (identity switches and reloads).
func (s *ChatSurface) SaveState() {
	if s.identity == "" {
		return
	}
	if anchor := s.transcript.Anchor(); anchor != nil {
		s.runtime.Anchors[s.identity] = *anchor
	} else {
		delete(s.runtime.Anchors, s.identity)
	}
}

// update is a runtime state change: switch identity if needed, then sync transcript, dock and indicator.
func (s *ChatSurface) update() {
	if s.disposed {
		return
	}
	rt := s.runtime
	if identity := rt.Identity(); identity != s.identity {
		s.switchTo(identity)
	}
	s.readTables()
	s.bind()
	s.transcript.SetState(components.ViewState{Expanded: rt.ExpandedTools, ShowThinking: rt.ShowThinking})
	s.sync()
}

func (s *ChatSurface) stopFollows() {
	for _, stop := range s.stops {
		stop()
	}
	s.stops = nil
}

func (s *ChatSurface) switchTo(identity string) {
	rt := s.runtime
	s.SaveState()
	if s.unsubscribe != nil {
		s.unsubscribe()
		s.unsubscribe = nil
	}
	s.stopFollows()
	if s.scope != nil {
		s.scope.Dispose()
	}
	s.scope = nil
	s.ref = nil
	s.outgoing, s.sending = func() []outgoing { return noOutgoing }, nil
	s.starting, s.start = func() *chatcontract.LiveShell { return nil }, nil
	s.compacting, s.compactNow = func() bool { return false }, nil
	s.view, s.viewLoaded, s.bound = nil, false, false
	s.viewRead, s.liveShellRead = nil, nil
	s.store = nil
	s.files, s.drafts, s.claimed = nil, nil, false
	s.current = nil
	s.synced = syncInputs{}
	s.replyTo = nil
	s.transcript.Reset()
	s.identity = identity
	s.session, s.hasSession, s.conversation = rt.Session, rt.HasSession, rt.Conversation
	s.Editor.SetText(rt.Drafts[identity])
	if identity == "" {
		s.transcript.Restore(nil)
		return
	}
	if anchor, ok := rt.Anchors[identity].(components.TranscriptAnchor); ok {
		s.transcript.Restore(&anchor)
	} else {
		s.transcript.Restore(nil)
	}
	if !s.hasSession {
		return
	}
	scope := rt.Ctx.Scope()
	s.scope = scope
	s.outgoing, s.sending, _ = chordtui.Optimistic(scope, func() []outgoing { return noOutgoing }, func(list []outgoing, message outgoing) []outgoing {
		return append(slices.Clone(list), message)
	})
	s.starting, s.start, _ = chordtui.OptimisticLast(scope, func() *chatcontract.LiveShell { return nil })
	s.compacting, s.compactNow, _ = chordtui.OptimisticLast(scope, func() bool { return false })
	if ref, err := scope.HoldWith(chatcontract.Chat, delta.JsonObjectOf("session", s.session, "conversation", float64(s.conversation)), func(ref *host.Reference) {
		// The conversation stays held for a while after its screen is left (Pi's UI_LINGER): coming back finds it live.
		p2runtime.UILinger.Release(ref, chatcontract.Chat.Name)
	}); err == nil {
		s.ref = ref
		s.viewRead = chordtui.WatchMember(scope, ref.Member("view"))
		s.liveShellRead = chordtui.WatchMember(scope, ref.Member("liveShell"))
	} else {
		rt.Fail(err)
	}
	// Who may write is known before the conversation is live (the role, a guest's grant): no wait for it.
	if rt.Role == runtime.RoleOwner || rt.GuestMode() == "full" {
		s.holdWriterParts()
	}
}

// bind is what Pi does once the conversation's reference is live: every view is merged into the transcript (released with the reference), and a
// participant holds its writer parts.
func (s *ChatSurface) bind() {
	if s.ref == nil || s.bound || p2runtime.StatusOf(s.ref) != host.RefStateLive {
		return
	}
	s.bound = true
	rt := s.runtime
	store := transcript.StoresOf(rt).Of(s.identity, transcript.TranscriptSource{View: viewWatchable{s.ref.Member("view")}, Entries: entriesOf(s.ref)})
	s.store = store
	s.unsubscribe = store.Subscribe(func() { rt.Post(rt.Changed) })
	if variantOf(s.ref) == "participant" {
		s.holdWriterParts()
	}
}

// holdWriterParts is what a writer of the conversation holds besides it: its drafts withdrawn by others (claimed back into the editor) and its file index (`@`
// and Tab completion). Once per view.
func (s *ChatSurface) holdWriterParts() {
	if s.files != nil || s.scope == nil {
		return
	}
	session, conversation := s.session, s.conversation
	params := func() *delta.JsonObject {
		return delta.JsonObjectOf("session", session, "conversation", float64(conversation))
	}
	if drafts, err := s.scope.Hold(draftscontract.Drafts, params()); err == nil {
		s.drafts = drafts
	}
	if files, err := s.scope.Hold(p2runtime.FileIndex, params()); err == nil {
		s.files = files
	}
}

// followDrafts claims the drafts others withdrew once the reference is live in its author variant.
func (s *ChatSurface) followDrafts() {
	if s.claimed || liveIn(s.drafts, "author") == nil {
		return
	}
	s.claimed = true
	drafts := s.drafts
	identity := IdentityKey(s.session, s.conversation)
	s.stops = append(s.stops, p2tui.FollowMember(drafts.Member("drafts"), func(value delta.JsonValue) {
		list := p2tui.JList(value)
		if len(list) == 0 {
			return
		}
		ids := make([]any, 0, len(list))
		for _, item := range list {
			ids = append(ids, p2tui.JField(item, "id"))
		}
		result, err := drafts.Call("claim", delta.JsonObjectOf("ids", ids), context.Background())
		if err != nil {
			s.runtime.Post(func() { s.runtime.Fail(err) })
			return
		}
		var texts []string
		for _, text := range p2tui.JList(p2tui.JField(result, "texts")) {
			texts = append(texts, p2tui.JStr(text))
		}
		s.runtime.Post(func() { s.restore(identity, texts) })
	}))
}

// IdentityKey is a viewed conversation's key.
func IdentityKey(session string, conversation int) string {
	return runtime.IdentityKey(session, conversation)
}

// fileSource is the viewed conversation's file index once live (P7b).
func (s *ChatSurface) fileSource() p2tui.FileSource {
	if ref := liveIn(s.files, "reader"); ref != nil {
		return fileIndex{ref}
	}
	return nil
}

// ToolViews are the finished calls of a tool in the loaded transcript, oldest first (`/downloads` lists send_file's).
func (s *ChatSurface) ToolViews(name string) []transcript.ToolView {
	if s.current == nil {
		return nil
	}
	var views []transcript.ToolView
	for _, item := range s.current.Items {
		var candidates []transcript.ToolView
		switch item := item.(type) {
		case transcript.AssistantItem:
			candidates = item.Tools
		case transcript.ToolItem:
			candidates = []transcript.ToolView{item.Tool}
		}
		for _, tool := range candidates {
			if tool.Name == name && tool.Status == "done" {
				views = append(views, tool)
			}
		}
	}
	return views
}

// AddAttachment adds an uploaded attachment (P9) for the next send; false when it cannot be added.
func (s *ChatSurface) AddAttachment(file LauncherUpload) bool {
	if s.identity == "" || s.participant() == nil {
		return false
	}
	list := s.attachments[s.identity]
	if len(list) >= filescontract.MaxAttachments || slices.ContainsFunc(list, func(entry LauncherUpload) bool { return entry.ID == file.ID }) {
		return false
	}
	s.attachments[s.identity] = append(slices.Clone(list), file)
	s.runtime.Changed()
	return true
}

// Detach removes the last attachment, or all of them.
func (s *ChatSurface) Detach(all bool) {
	if s.identity == "" {
		return
	}
	list := s.attachments[s.identity]
	if all || len(list) == 0 {
		s.attachments[s.identity] = nil
	} else {
		s.attachments[s.identity] = slices.Clone(list[:len(list)-1])
	}
	s.runtime.Changed()
}

// LastAssistantText is the latest answer's text (pi's /copy and Ctrl+X): the last assistant message's text parts.
func (s *ChatSurface) LastAssistantText() string {
	if s.current == nil {
		return ""
	}
	for i := len(s.current.Items) - 1; i >= 0; i-- {
		item, ok := s.current.Items[i].(transcript.AssistantItem)
		if !ok || item.Streaming || item.Message == nil {
			continue
		}
		var parts []string
		for _, part := range item.Message.Content {
			if text, ok := part.(ai.TextContent); ok {
				parts = append(parts, text.Text)
			}
		}
		return jsstring.Trim(strings.Join(parts, "\n\n"))
	}
	return ""
}

// Editable is whether this visitor can edit the editor here (a live participant).
func (s *ChatSurface) Editable() bool { return s.participant() != nil }

func (s *ChatSurface) participant() *host.Reference { return liveIn(s.ref, "participant") }

// readOnlyView is whether the reference is live in a variant that cannot write (a viewer).
func (s *ChatSurface) readOnlyView() bool {
	return s.ref != nil && p2runtime.StatusOf(s.ref) == host.RefStateLive && variantOf(s.ref) != "participant"
}

// live is the viewed conversation's `pi.live`, nil for none.
func (s *ChatSurface) live() *harness.LiveState {
	if s.store == nil {
		return nil
	}
	pages := s.store.Value()
	if pages == nil {
		return nil
	}
	return transcript.DocOf[harness.LiveState](pages.View.Docs, "pi.live")
}

// shownShell is the running command as shown: the server's, else one just started from here.
func (s *ChatSurface) shownShell() *chatcontract.LiveShell {
	if s.liveShellRead != nil && s.bound {
		if value, ok := s.liveShellRead(); ok {
			if shell := shellOf(value); shell != nil {
				return shell
			}
		}
	}
	return s.starting()
}

// liveShell is the conversation's user shell command while it runs (`Chat.liveShell`).
func (s *ChatSurface) liveShell() *chatcontract.LiveShell {
	if s.liveShellRead == nil || !s.bound {
		return nil
	}
	value, _ := s.liveShellRead()
	return shellOf(value)
}

func (s *ChatSurface) sync() {
	if s.disposed {
		return
	}
	rt := s.runtime
	if s.ref != nil && !s.viewLoaded {
		if value, ok := s.viewRead(); ok && p2runtime.StatusOf(s.ref) == host.RefStateLive {
			s.view, s.viewLoaded = value, true
		}
	} else if s.viewLoaded && s.viewRead != nil {
		if value, ok := s.viewRead(); ok {
			s.view = value
		}
	}
	var pages *transcript.Pages
	if s.store != nil {
		pages = s.store.Value()
	}
	outgoing := s.outgoing()
	inputs := syncInputs{pages: pages, outgoing: outgoing, displays: s.displays, tools: s.toolRenderers, resolvers: s.resolvers, entries: s.entryRender, hasInputs: true}
	if !s.sameInputs(inputs) {
		s.synced = inputs
		s.current = nil
		var retained []durable.EntryRecord
		if pages != nil {
			filling := pages.Filling
			built := transcript.BuildTranscript(*pages.View, s.display, filling)
			built = s.withOutgoing(built)
			s.current = &built
			for _, item := range pages.View.Items {
				if entry, ok := item.(harness.TranscriptEntry); ok {
					retained = append(retained, entry.Entry)
				}
			}
		}
		s.transcript.Sync(s.current, retained)
	}
	switch {
	case !s.hasSession:
		s.transcript.Empty = "No session selected."
	case !s.viewLoaded:
		s.transcript.Empty = "Loading conversation…"
	case s.view == nil:
		s.transcript.Empty = "This conversation is not available."
	default:
		s.transcript.Empty = ""
	}
	participant := s.participant() != nil
	s.syncBorder()
	s.syncShell()
	s.syncPending(participant)
	s.syncIndicator(participant)
	s.followDrafts()
	s.input.Clear()
	if participant {
		s.input.AddChild(s.Editor)
		return
	}
	current := rt.Theme().Current()
	var message string
	switch {
	case !s.hasSession:
		keys := rt.Ctx.KeyText("p2.sessions")
		if keys == "" {
			keys = "/sessions"
		}
		message = keys + " to choose a session"
	case s.ref != nil && p2runtime.StatusOf(s.ref) == host.RefStateLive:
		// The web's read-only bar: a view-only guest sees under which name it joined.
		if rt.Role == runtime.RoleGuest && rt.Grant != nil {
			name := jsstring.Slice(render.LineText(rt.Grant.Name), 0, 40)
			message = "Joined as " + name + " (unverified) · read-only"
		} else {
			message = "Read-only conversation"
		}
	default:
		message = "Connecting conversation…"
	}
	s.readOnly.SetText(current.Fg("muted", message))
	s.input.AddChild(s.readOnly)
}

func (s *ChatSurface) sameInputs(next syncInputs) bool {
	last := s.synced
	return last.hasInputs && last.pages == next.pages && sameSlice(last.outgoing, next.outgoing) &&
		sameMap(last.displays, next.displays) && sameMap(last.tools, next.tools) && sameSlice(last.resolvers, next.resolvers) &&
		sameMap(last.entries, next.entries)
}

// sameMap is identity of two maps that are replaced, never changed in place.
func sameMap[K comparable, V any](a, b map[K]V) bool {
	return len(a) == len(b) && (len(a) == 0 || reflect.ValueOf(a).UnsafePointer() == reflect.ValueOf(b).UnsafePointer())
}

// syncBorder is pi's thinking level's border color, or the bash mode color while the text starts with `!`.
func (s *ChatSurface) syncBorder() {
	current := s.runtime.Theme().Current()
	level := "off"
	if s.current != nil && s.current.Agent.ThinkingLevel != "" {
		level = string(s.current.Agent.ThinkingLevel)
	}
	bash := s.participant() != nil && strings.HasPrefix(jsstring.TrimStart(s.Editor.Text()), "!")
	if bash {
		s.Editor.BorderColor = current.GetBashModeBorderColor()
	} else {
		s.Editor.BorderColor = current.GetThinkingBorderColor(level)
	}
}

// syncShell shows the running command as pi's bash execution component at the end of the transcript.
func (s *ChatSurface) syncShell() {
	live := s.shownShell()
	if live == nil {
		if s.shellView != nil {
			s.shellView.component.Dispose()
			s.shellView = nil
		}
		s.transcript.Tail = nil
		return
	}
	rt := s.runtime
	if s.shellView == nil || s.shellView.id != live.ID {
		if s.shellView != nil {
			s.shellView.component.Dispose()
		}
		options := components.BashExecutionOptions{ExcludeFromContext: !live.IncludeInContext, By: components.ShellAuthor(live.Author)}
		if live.Running {
			options.TUI = rt.TUI
		}
		s.shellView = &shellView{id: live.ID, component: components.NewBashExecutionComponent(live.Command, rt.Ctx, options)}
	}
	component := s.shellView.component
	component.SetExpanded(rt.ExpandedTools)
	component.SetOutput(live.Output, live.Truncated)
	if !live.Running {
		component.SetComplete(live.ExitCode, live.Cancelled, components.BashCompletion{Truncated: live.Truncated, Error: live.Error})
	}
	s.transcript.Tail = component
}

// withOutgoing is the transcript with the messages in flight: at its end while idle, else in the queue (syncPending).
func (s *ChatSurface) withOutgoing(built transcript.Transcript) transcript.Transcript {
	messages := s.outgoing()
	if len(messages) == 0 || built.Busy {
		return built
	}
	author := s.ownAuthor()
	items := slices.Clone(built.Items)
	for _, message := range messages {
		files := make([]string, len(message.files))
		for i, file := range message.files {
			files[i] = file.Name
		}
		self := author
		self.ID = ""
		items = append(items, transcript.UserItem{Key: "outgoing-" + message.id, Text: message.text, Files: files, Author: &self})
	}
	built.Items = items
	return built
}

// ownAuthor is who the server will record as this visitor (for messages and commands shown before it does).
func (s *ChatSurface) ownAuthor() p2runtime.Author {
	rt := s.runtime
	if rt.Role == runtime.RoleGuest {
		name := "Guest"
		if rt.Grant != nil {
			name = rt.Grant.Name
		}
		return p2runtime.Author{Kind: p2runtime.AuthorGuest, Name: name}
	}
	return p2runtime.Author{Kind: p2runtime.AuthorOwner, Name: "Owner"}
}

// performer orders what the surface sends to the server. Pi starts every call at the moment its key is handled, so two actions reach the server in the order the
// visitor made them (Alt+Enter's send, then Alt+Up's dequeue). Here each action runs off the owner loop, and a turn per action keeps the order of their starts:
// an action's first call waits for every earlier action to have started its own (or to have finished), and starts under the turn; the answers are awaited apart.
type turn struct {
	prev    <-chan struct{}
	done    chan struct{}
	waited  sync.Once
	release sync.Once
}

// noTurn is the turn before the first: already finished.
var noTurn = func() chan struct{} { done := make(chan struct{}); close(done); return done }()

// begin waits for the earlier actions to have started their calls.
func (t *turn) begin() { t.waited.Do(func() { <-t.prev }) }

// finish lets the next action start.
func (t *turn) finish() { t.release.Do(func() { close(t.done) }) }

// call starts a method of ref in this action's turn, then awaits its answer with no deadline.
func (t *turn) call(ref *host.Reference, method string, args *delta.JsonObject) (delta.JsonValue, error) {
	t.begin()
	pending, err := ref.Start(method, args, context.Background())
	t.finish()
	if err != nil {
		return nil, err
	}
	return pending.Wait(context.Background())
}

// perform runs an action off the loop, in the order actions were made, and shows its failure. It is called on the owner loop.
func (s *ChatSurface) perform(action func(t *turn) error) {
	s.turnMu.Lock()
	prev := s.lastTurn
	if prev == nil {
		prev = noTurn
	}
	t := &turn{prev: prev, done: make(chan struct{})}
	s.lastTurn = t.done
	s.turnMu.Unlock()
	go func() {
		defer t.finish()
		t.begin()
		if err := action(t); err != nil {
			s.runtime.Post(func() { s.runtime.Fail(err) })
		}
	}()
}

// call is a method of ref called with no deadline.
func call(ref *host.Reference, method string, args *delta.JsonObject) (delta.JsonValue, error) {
	return ref.Call(method, args, context.Background())
}

// Shell runs `!cmd`/`!!cmd` (`Chat.shell`); one at a time, as pi. Its block shows as running at once and the editor clears; a refused command comes back
// to the editor with the error.
func (s *ChatSurface) Shell(command string, includeInContext bool) {
	rt := s.runtime
	ref := s.participant()
	identity := s.identity
	if ref == nil || identity == "" || s.start == nil {
		if s.readOnlyView() {
			rt.Fail(fmt.Errorf("This conversation is read-only"))
		} else {
			rt.Fail(fmt.Errorf("Not connected yet"))
		}
		return
	}
	if shell := s.shownShell(); shell != nil && shell.Running {
		rt.Notify(chatcontract.ShellBusy, runtime.NoticeWarning)
		return
	}
	draft := s.Editor.GetExpandedText()
	s.Editor.AddToHistory(jsstring.Trim(draft))
	s.Editor.SetText("")
	rt.Drafts[identity] = ""
	rt.ClearNotices()
	s.scroll.ScrollToEnd()
	requestID := randomUUID()
	author := s.ownAuthor()
	guess := &chatcontract.LiveShell{ID: requestID, Command: command, Author: author, IncludeInContext: includeInContext, Running: true}
	start := s.start
	s.perform(func(t *turn) error {
		err := start(guess, func() error {
			_, err := t.call(ref, "shell", delta.JsonObjectOf("command", command, "includeInContext", includeInContext, "requestId", requestID))
			return err
		})
		if err != nil {
			rt.Post(func() { s.restore(identity, []string{draft}) })
		}
		return err
	})
}

// submit is the editor's Enter: a bash command, a slash command, a template, or a message.
func (s *ChatSurface) submit(text string) {
	rt := s.runtime
	trimmed := jsstring.TrimStart(text)
	// pi's bash mode: `!cmd` runs a command whose output joins the context, `!!cmd` one that stays out of it.
	if bang := strings.HasPrefix(trimmed, "!"); bang {
		rest := trimmed[1:]
		includeInContext := true
		if strings.HasPrefix(rest, "!") {
			rest, includeInContext = rest[1:], false
		}
		if command := jsstring.Trim(rest); command != "" {
			s.Editor.SetText(text)
			s.Shell(command, includeInContext)
			return
		}
	}
	if strings.HasPrefix(trimmed, "/") {
		if rt.Ctx.Commands().RunSlash(text) {
			return
		}
		// A known command that does not apply here (not connected yet, a guest, ...) is not sent as a message.
		name := slashName(trimmed)
		if name != "" {
			for _, item := range rt.Ctx.Commands().List(nil) {
				if item.Slash == name {
					s.Editor.SetText(text)
					rt.Notify("/"+name+" is not available here", runtime.NoticeWarning)
					return
				}
			}
			// A prompt template or /skill: (10.8) is expanded on the session worker, then sent as pi does.
			if rt.Ctx.Commands().Expandable(name) {
				s.Editor.SetText(text)
				go func() {
					expanded, err := rt.Ctx.Commands().Expand(trimmed)
					rt.Post(func() {
						if err != nil {
							rt.Fail(err)
							return
						}
						if s.Editor.Text() == text {
							s.Editor.SetText(expanded)
						}
						s.Send("steer")
					})
				}()
				return
			}
		}
	}
	s.Editor.SetText(text)
	s.Send("steer")
}

// slashName is the command name of `/name args`, "" for none.
func slashName(text string) string {
	rest := strings.TrimPrefix(text, "/")
	end := strings.IndexFunc(rest, jsstring.IsSpace)
	if end < 0 {
		end = len(rest)
	}
	return rest[:end]
}

// syncPending shows queued inputs (coding-agent's pending messages area).
func (s *ChatSurface) syncPending(participant bool) {
	current := s.runtime.Theme().Current()
	s.pending.Clear()
	var queued []transcript.Queued
	if s.current != nil {
		queued = s.current.Queued
	}
	// Messages in flight while a run is busy wait in the queue, as the server will place them; before the transcript arrived (the conversation still
	// connecting) they show here too.
	var inFlight []outgoing
	if s.current == nil || s.current.Busy {
		inFlight = s.outgoing()
	}
	var replying *runtime.ReplyTo
	if s.canReply {
		replying = s.replyTo
	}
	if len(queued) == 0 && len(inFlight) == 0 && replying == nil {
		return
	}
	s.pending.AddChild(pitui.NewPaddedText("", 0, 0, nil))
	if replying != nil {
		s.pending.AddChild(pitui.NewTruncatedText(current.Fg("accent", "↪ Replying to "+render.LineText(replying.Name)+" · /reply cancels"), 1, 0))
	}
	label := func(mode string) string {
		switch {
		case s.current == nil:
			return "Sending"
		case mode == "followUp":
			return "Follow-up"
		}
		return "Steering"
	}
	for _, input := range queued {
		s.pending.AddChild(pitui.NewTruncatedText(current.Fg("dim", label(input.Mode)+": "+render.LineText(input.Text)), 1, 0))
	}
	for _, input := range inFlight {
		s.pending.AddChild(pitui.NewTruncatedText(current.Fg("dim", label(input.mode)+": "+render.LineText(input.text)), 1, 0))
	}
	if len(queued) > 0 && participant {
		if hint := s.runtime.Ctx.KeyText("app.message.dequeue"); hint != "" {
			s.pending.AddChild(pitui.NewTruncatedText(current.Fg("dim", "↳ "+hint+" to edit your queued messages"), 1, 0))
		}
	}
}

// syncIndicator is DTUI `#syncStatus`: retry, compaction, running tool or working, from `pi.live`.
func (s *ChatSurface) syncIndicator(participant bool) {
	rt := s.runtime
	live := s.live()
	var generation *harness.LiveGeneration
	var run *harness.LiveRun
	var running *harness.ToolSlot
	var compaction *harness.CompactionStatus
	if live != nil {
		generation, run = live.Generation, live.Run
		if len(live.Compactions) > 0 {
			compaction = &live.Compactions[0]
		}
		for i := range live.Tools {
			if live.Tools[i].Status == harness.ToolSlotRunning {
				running = &live.Tools[i]
				break
			}
		}
	}
	// A `/compact` in flight shows as a manual compaction until `pi.live` has it.
	if compaction == nil && s.compacting() {
		compaction = &harness.CompactionStatus{Trigger: "manual", Attempt: 1}
	}
	abort := "(" + rt.Ctx.KeyText("app.interrupt") + " to abort)"
	key := ""
	var create func() *components.StatusIndicator
	switch {
	case generation != nil && generation.Retry != nil:
		retry, attempt := *generation.Retry, generation.Attempt
		key = fmt.Sprintf("retry:%d:%s", attempt, jsNumber(retry.At))
		create = func() *components.StatusIndicator {
			return components.NewRetryStatusIndicator(s.ctx, rt.TUI, rt.Ctx, attempt, int64(retry.At), render.LineText(retry.Error))
		}
	case generation != nil && generation.Deferred != nil:
		key = "deferred"
		create = func() *components.StatusIndicator {
			return components.NewWorkingStatusIndicator(rt.TUI, rt.Ctx, "Waiting for deferred response...", nil)
		}
	case compaction != nil:
		trigger := string(compaction.Trigger)
		attempt := ""
		var retry *int
		if compaction.Retry != nil {
			attempt = fmt.Sprint(compaction.Attempt)
			retry = &compaction.Attempt
		}
		key = "compaction:" + trigger + ":" + attempt
		create = func() *components.StatusIndicator {
			return components.NewCompactionStatusIndicator(rt.TUI, rt.Ctx, trigger, retry)
		}
	case running != nil:
		name := running.Name
		key = "tool:" + name
		create = func() *components.StatusIndicator {
			return components.NewWorkingStatusIndicator(rt.TUI, rt.Ctx, "Running "+render.LineText(name)+"... "+abort, nil)
		}
	case run != nil:
		key = "working"
		create = func() *components.StatusIndicator {
			return components.NewWorkingStatusIndicator(rt.TUI, rt.Ctx, "Working... "+abort, nil)
		}
	}
	key = fmt.Sprintf("%s:%t", key, participant)
	if key != s.indicatorKey {
		s.indicatorKey = key
		if s.indicator != nil {
			s.indicator.Dispose()
			s.indicator = nil
		}
		if create != nil {
			s.indicator = create()
		}
	}
	if participant {
		s.Editor.SetWorkingStatusIndicator(s.indicator)
	} else {
		s.Editor.SetWorkingStatusIndicator(nil)
	}
	s.status.Clear()
	// Viewers have no editor border: the indicator gets its own line.
	if !participant && s.indicator != nil {
		s.status.AddChild(s.indicator)
	}
}

// jsNumber is a number as JavaScript prints it in a template.
func jsNumber(value float64) string {
	if value == float64(int64(value)) {
		return fmt.Sprint(int64(value))
	}
	return fmt.Sprint(value)
}

// Send sends the editor's message the way the web's outbox does: it leaves the editor and shows in the transcript (the queue while a run is busy) at once, and
// is delivered once the conversation is live (a reload, a reconnect). A refused message comes back to the editor with the error; a retry of the same text
// reuses its request ID, so it is admitted once.
func (s *ChatSurface) Send(mode string) {
	rt := s.runtime
	text := jsstring.Trim(s.Editor.GetExpandedText())
	identity := s.identity
	var files []LauncherUpload
	if identity != "" {
		files = s.attachments[identity]
	}
	ref := s.ref
	if (text == "" && len(files) == 0) || identity == "" || ref == nil || s.sending == nil {
		return
	}
	if s.readOnlyView() {
		rt.Fail(fmt.Errorf("This conversation is read-only"))
		return
	}
	draft := s.Editor.GetExpandedText()
	submission, found := rt.Submissions[identity]
	if !found || submission.Text != text || submission.Mode != mode {
		submission = runtime.Submission{Text: text, Mode: mode, ID: randomUUID()}
		rt.Submissions[identity] = submission
	}
	var replyTo *runtime.ReplyTo
	if s.canReply {
		replyTo = s.replyTo
	}
	s.attachments[identity] = nil
	s.replyTo = nil
	s.Editor.AddToHistory(text)
	s.Editor.SetText("")
	rt.Drafts[identity] = ""
	rt.ClearNotices()
	s.scroll.ScrollToEnd()
	sent := submission
	sending := s.sending
	message := outgoing{id: sent.ID, text: text, mode: mode, files: files}
	s.perform(func(t *turn) error {
		return sending(message, func() error {
			// Waits through a connecting conversation; calls made while it reconnects wait for it too.
			err := p2runtime.WhenLive(context.Background(), ref)
			var writer *host.Reference
			if err == nil {
				if writer = liveIn(ref, "participant"); writer == nil {
					err = fmt.Errorf("This conversation is read-only")
				}
			}
			if err == nil {
				args := delta.JsonObjectOf("text", text, "requestId", sent.ID, "whenBusy", mode)
				if len(files) > 0 {
					ids := make([]any, len(files))
					for i, file := range files {
						ids[i] = file.ID
					}
					args.Set("attachments", ids)
				}
				if replyTo != nil {
					args.Set("replyTo", float64(replyTo.EntryID))
				}
				_, err = t.call(writer, "send", args)
			}
			rt.Post(func() {
				if err == nil {
					if rt.Submissions[identity] == sent {
						delete(rt.Submissions, identity)
					}
					return
				}
				s.restore(identity, []string{draft})
				s.attachments[identity] = append(slices.Clone(files), s.attachments[identity]...)
				if identity == s.identity && s.replyTo == nil {
					s.replyTo = replyTo
				}
			})
			return err
		})
	})
}

// restore puts texts back in front of the identity's draft.
func (s *ChatSurface) restore(identity string, texts []string) {
	if len(texts) == 0 {
		return
	}
	rt := s.runtime
	var parts []string
	for _, text := range append(slices.Clone(texts), rt.Drafts[identity]) {
		if text != "" {
			parts = append(parts, text)
		}
	}
	draft := strings.Join(parts, "\n\n")
	rt.Drafts[identity] = draft
	if !s.disposed && s.identity == identity {
		s.Editor.SetText(draft)
	}
	rt.Changed()
}

// missingAnchor is a position in older history: page towards it while there is more.
func (s *ChatSurface) missingAnchor(anchor components.TranscriptAnchor) bool {
	var pages *transcript.Pages
	if s.store != nil {
		pages = s.store.Value()
	}
	if pages == nil {
		// Not loaded yet (a reload's new surface, a reference still connecting): the position waits for the conversation. Upstream's reference lingers, so its
		// pages are there at the first render.
		return s.ref != nil
	}
	items := pages.View.Items
	older := true
	for _, item := range items {
		if entry, ok := item.(harness.TranscriptEntry); ok {
			older = anchor.EntryID == nil || *anchor.EntryID < int(entry.Entry.Id)
			break
		}
	}
	_, earlier := firstItem(items).(harness.TranscriptEarlier)
	if anchor.EntryID == nil || !earlier || !older {
		return pages.LoadingEarlier
	}
	s.loadEarlier()
	return true
}

func firstItem(items []harness.TranscriptItem) harness.TranscriptItem {
	if len(items) == 0 {
		return nil
	}
	return items[0]
}

func (s *ChatSurface) loadEarlier() {
	store := s.store
	if s.ref == nil || p2runtime.StatusOf(s.ref) != host.RefStateLive || store == nil || s.earlierFailed[store] {
		return
	}
	go func() {
		err := store.LoadEarlier()
		s.runtime.Post(func() {
			if err != nil {
				// No retry loop from the top of the transcript: scrolling there again does not ask again.
				s.earlierFailed[store] = true
				s.runtime.Fail(err)
			}
			s.runtime.Changed()
		})
	}()
}

// fillGap loads a gap's missing entries while it is in view; a failed load is not retried for that gap.
func (s *ChatSurface) fillGap(gap transcript.Gap) {
	store := s.store
	if s.ref == nil || p2runtime.StatusOf(s.ref) != host.RefStateLive || store == nil || s.gapsFailed[gap] {
		return
	}
	if pages := store.Value(); pages != nil && pages.Filling[transcript.GapKey(gap)] {
		return
	}
	go func() {
		err := store.FillGap(gap)
		s.runtime.Post(func() {
			if err != nil {
				s.gapsFailed[gap] = true
				s.runtime.Fail(err)
			}
			s.runtime.Changed()
		})
	}()
}

// Interrupt is `app.interrupt` (pi's Escape): aborts a running compaction and stops the generation; the caller's own queued texts come back to the editor.
func (s *ChatSurface) Interrupt() {
	ref := s.participant()
	identity := s.identity
	if ref == nil || identity == "" {
		return
	}
	live := s.live()
	shell := s.liveShell()
	running := live != nil && live.Run != nil
	compacting := live != nil && len(live.Compactions) > 0
	// pi's order: a running agent, a running command, bash mode's text, then the double Escape on an empty editor.
	if !running && !compacting {
		if shell != nil && shell.Running {
			id := shell.ID
			s.perform(func(t *turn) error {
				_, err := t.call(ref, "abortShell", delta.JsonObjectOf("shellId", id))
				return err
			})
			return
		}
		if strings.HasPrefix(jsstring.TrimStart(s.Editor.Text()), "!") {
			s.Editor.SetText("")
			s.runtime.Drafts[identity] = ""
			s.syncBorder()
			return
		}
		if jsstring.Trim(s.Editor.Text()) == "" {
			s.doubleEscape()
		}
	}
	if compacting {
		s.perform(func(t *turn) error { _, err := t.call(ref, "abortCompaction", nil); return err })
	}
	if running || !compacting {
		s.perform(func(t *turn) error {
			result, err := t.call(ref, "stop", nil)
			if err != nil {
				return err
			}
			var restored []string
			for _, text := range p2tui.JList(p2tui.JField(result, "restored")) {
				restored = append(restored, p2tui.JStr(text))
			}
			s.runtime.Post(func() { s.restore(identity, restored) })
			return nil
		})
	}
}

// doubleEscape is pi's `doubleEscapeAction` (default "tree"): "tree" opens `/tree`, "fork" `/fork`'s picker.
func (s *ChatSurface) doubleEscape() {
	action := "tree"
	if setting := s.runtime.Appearance.Settings.DoubleEscapeAction; setting != nil {
		action = *setting
	}
	if action == "none" || s.runtime.Role != runtime.RoleOwner {
		return
	}
	now := time.Now()
	if now.Sub(s.lastEscape) < doubleEscapeMs*time.Millisecond {
		s.lastEscape = time.Time{}
		if action == "fork" {
			s.OpenFork()
		} else {
			s.OpenTree()
		}
	} else {
		s.lastEscape = now
	}
}

// OpenTree is pi's `/tree` (docs/tree.md): the session's branches as one tree; Enter continues the session at the chosen message (`SessionTree.navigate`), a
// user message waiting in the editor of the conversation it switched to. The label key renames the selected message's branch (`SessionTree.rename`); a null
// label shows the session title.
func (s *ChatSurface) OpenTree() {
	rt := s.runtime
	session := s.session
	if !s.hasSession {
		return
	}
	title := func() string {
		for _, row := range s.rows() {
			if row.ID == session {
				return row.Title
			}
		}
		return ""
	}
	// One reference per viewed session: opening `/tree` again and each action reuse it.
	if s.tree == nil || s.tree.session != session {
		if s.tree != nil {
			s.tree.scope.Dispose()
			s.tree = nil
		}
		scope := rt.Ctx.Scope()
		ref, err := scope.Hold(treecontract.SessionTree, delta.JsonObjectOf("session", session))
		if err != nil {
			scope.Dispose()
			rt.Fail(err)
			return
		}
		s.tree = &treeHold{session: session, scope: scope, ref: ref}
	}
	held := s.tree.ref
	titleNow := title()
	var selector *components.TreeSelectorComponent
	var overlay runtime.TuiOverlayHandle
	go func() {
		fail := func(err error) { rt.Post(func() { rt.Fail(err) }) }
		if err := p2runtime.WhenLive(context.Background(), held); err != nil {
			fail(err)
			return
		}
		owner := liveIn(held, "owner")
		if owner == nil {
			fail(fmt.Errorf("Only the owner switches branches"))
			return
		}
		result, err := call(owner, "tree", nil)
		if err != nil {
			fail(err)
			return
		}
		var nodes []components.TreeSelectorNode
		for _, each := range p2tui.JList(p2tui.JField(result, "nodes")) {
			node := components.TreeSelectorNode{
				Entry: int(p2tui.JNum(p2tui.JField(each, "entry"))), Conversation: int(p2tui.JNum(p2tui.JField(each, "conversation"))),
				Role: p2tui.JStr(p2tui.JField(each, "role")), Text: p2tui.JStr(p2tui.JField(each, "text")),
			}
			if parent, ok := p2tui.JField(each, "parent").(float64); ok {
				id := int(parent)
				node.Parent = &id
			}
			nodes = append(nodes, node)
		}
		var current *int
		if value, ok := p2tui.JField(result, "current").(float64); ok {
			id := int(value)
			current = &id
		}
		labels := map[int]string{}
		branchLabels := map[int]*string{}
		for _, branch := range p2tui.JList(p2tui.JField(result, "branches")) {
			conversation := int(p2tui.JNum(p2tui.JField(branch, "conversation")))
			label, hasLabel := p2tui.JField(branch, "label").(string)
			if hasLabel {
				branchLabels[conversation] = &label
				labels[conversation] = label
			} else {
				labels[conversation] = titleNow
			}
		}
		rt.Post(func() {
			selector = components.NewTreeSelector(runtime.Selectors(rt.Ctx), nodes, current, labels,
				func(entry int) {
					overlay.Close()
					s.perform(func(t *turn) error {
						answer, err := t.call(owner, "navigate", delta.JsonObjectOf("entry", float64(entry)))
						if err != nil {
							return err
						}
						conversation := int(p2tui.JNum(p2tui.JField(answer, "conversation")))
						text, hasText := p2tui.JField(answer, "text").(string)
						rt.Post(func() {
							identity := IdentityKey(session, conversation)
							if hasText {
								rt.Drafts[identity] = text
							}
							rt.Navigate("chat", session, true, &conversation)
							// Already shown (the session continues in the same conversation): the editor takes the text now.
							if hasText && !s.disposed && s.identity == identity {
								s.Editor.SetText(text)
							}
						})
						return nil
					})
				},
				func() { overlay.Close() },
				// The label shows at once; a refused rename puts the old one back.
				func(conversation int, label string) {
					before := titleNow
					if previous := branchLabels[conversation]; previous != nil {
						before = *previous
					}
					shown := label
					if shown == "" {
						shown = title()
					}
					selector.SetLabel(conversation, shown)
					args := delta.JsonObjectOf("conversation", float64(conversation), "label", nil)
					if label != "" {
						args.Set("label", label)
					}
					go func() {
						if _, err := call(owner, "rename", args); err != nil {
							rt.Post(func() {
								selector.SetLabel(conversation, before)
								rt.Fail(err)
							})
						}
					}()
				})
			overlay = rt.Ctx.Overlay("tree", selector, pitui.OverlaySpec{Width: p2tui.Percent(100), Anchor: "bottom-center"})
		})
	}()
}

// OpenFork is pi's `/fork`: pick a person's message of the loaded transcript; a new session holds what came before it and the message waits in its editor
// (`Sessions.fork`). The main conversation only.
func (s *ChatSurface) OpenFork() {
	rt := s.runtime
	session := s.session
	if !s.hasSession {
		return
	}
	if s.conversation != rt.MainOf(session) {
		rt.Notify("Forking a subagent conversation is not supported yet", runtime.NoticeWarning)
		return
	}
	var messages []components.UserMessageItem
	if s.current != nil {
		for _, item := range s.current.Items {
			if user, ok := item.(transcript.UserItem); ok && user.EntryID != nil && jsstring.Trim(user.Text) != "" {
				messages = append(messages, components.UserMessageItem{ID: *user.EntryID, Text: user.Text})
			}
		}
	}
	if len(messages) == 0 {
		rt.Notify("No messages to fork from", runtime.NoticeInfo)
		return
	}
	last := messages[len(messages)-1].ID
	var overlay runtime.TuiOverlayHandle
	conversation := s.conversation
	selector := components.NewUserMessageSelector(runtime.Selectors(rt.Ctx), messages,
		func(id int) {
			overlay.Close()
			text := ""
			for _, message := range messages {
				if message.ID == id {
					text = message.Text
					break
				}
			}
			catalog := liveIn(s.catalog, "owner")
			if catalog == nil {
				return
			}
			// The forked message waits in the new session's editor.
			s.openCopy(session, func(copy string) error {
				_, err := call(catalog, "fork", delta.JsonObjectOf("id", session, "conversation", float64(conversation), "before", float64(id), "copy", copy))
				return err
			}, &text)
		},
		func() { overlay.Close() }, &last)
	overlay = rt.Ctx.Overlay("fork", selector, pitui.OverlaySpec{Width: p2tui.Percent(100), Anchor: "bottom-center"})
}

// CloneSession is pi's `/clone`: the whole session as a new one (`Sessions.clone`), opened at once.
func (s *ChatSurface) CloneSession() {
	session := s.session
	catalog := liveIn(s.catalog, "owner")
	if !s.hasSession || catalog == nil {
		return
	}
	s.openCopy(session, func(copy string) error {
		_, err := call(catalog, "clone", delta.JsonObjectOf("id", session, "copy", copy))
		return err
	}, nil)
}

// openCopy opens a copy of session (a clone, a fork) at once: this TUI chooses its ID (copy), the new conversation shows loading while its files are copied
// (a job; its worker starts after), with draft in its editor. A refused copy returns to session with the error.
func (s *ChatSurface) openCopy(session string, run func(copy string) error, draft *string) {
	rt := s.runtime
	copy := randomUUID()
	if draft != nil {
		rt.Drafts[IdentityKey(copy, 1)] = *draft
	}
	one := 1
	rt.Navigate("chat", copy, true, &one)
	go func() {
		if err := run(copy); err != nil {
			rt.Post(func() {
				if rt.Session == copy {
					rt.Navigate("chat", session, true, nil)
				}
				rt.Fail(err)
			})
		}
	}()
}

// Compact is `/compact [instructions]`: summarize earlier context now (Escape aborts it); its indicator shows at once.
func (s *ChatSurface) Compact(instructions string) {
	ref := s.participant()
	if ref == nil || s.compactNow == nil {
		return
	}
	args := delta.NewJsonObject(1)
	if trimmed := jsstring.Trim(instructions); trimmed != "" {
		args.Set("instructions", trimmed)
	}
	compactNow := s.compactNow
	s.perform(func(t *turn) error {
		return compactNow(true, func() error { _, err := t.call(ref, "compact", args); return err })
	})
}

// NewContext is `/new-context [handoff]`: a new context, starting with the handoff text when given (owner).
func (s *ChatSurface) NewContext(handoff string) {
	ref := s.participant()
	if ref == nil {
		return
	}
	args := delta.NewJsonObject(1)
	if trimmed := jsstring.Trim(handoff); trimmed != "" {
		args.Set("handoff", trimmed)
	}
	s.perform(func(t *turn) error {
		if _, err := t.call(ref, "reset", args); err != nil {
			return err
		}
		s.runtime.Post(func() { s.runtime.Notify("Started a new context", runtime.NoticeInfo) })
		return nil
	})
}

// Clear is `app.clear` (pi's Ctrl+C): clears the editor; a second press within 500 ms exits.
func (s *ChatSurface) Clear() {
	now := time.Now()
	if now.Sub(s.lastCtrlC) < doubleCtrlCMs*time.Millisecond {
		s.lastCtrlC = time.Time{}
		s.runtime.OnExit()
		return
	}
	s.lastCtrlC = now
	s.Editor.SetText("")
	if s.identity != "" {
		s.runtime.Drafts[s.identity] = ""
	}
}

// DequeueOwn is `app.message.dequeue` (pi's Alt+Up): every queued input of the caller back to the editor, in order, before the current text; other people's
// stay queued (`Chat.dequeueOwn`).
func (s *ChatSurface) DequeueOwn() {
	ref := s.participant()
	identity := s.identity
	if ref == nil || identity == "" {
		return
	}
	rt := s.runtime
	s.perform(func(t *turn) error {
		result, err := t.call(ref, "dequeueOwn", nil)
		if err != nil {
			return err
		}
		var restored []string
		for _, text := range p2tui.JList(p2tui.JField(result, "restored")) {
			restored = append(restored, p2tui.JStr(text))
		}
		skipped := int(p2tui.JNum(p2tui.JField(result, "skipped")))
		rt.Post(func() {
			s.restore(identity, restored)
			switch {
			case skipped > 0:
				was := "were"
				plural := "s"
				if skipped == 1 {
					was, plural = "was", ""
				}
				rt.Notify(fmt.Sprintf("%d queued message%s %s already sent", skipped, plural, was), runtime.NoticeWarning)
			case len(restored) == 0:
				rt.Notify("No queued messages of yours", runtime.NoticeInfo)
			}
		})
		return nil
	})
}

// SetCanReply is chat mode switching replies on or off (chat-mode's composer chip).
func (s *ChatSurface) SetCanReply(enabled bool, mentionName string) {
	if mentionName == "" {
		mentionName = "Pi"
	}
	if enabled == s.canReply && mentionName == s.mentionName {
		return
	}
	s.canReply = enabled
	s.mentionName = mentionName
	if !enabled {
		s.replyTo = nil
	}
	s.runtime.Changed()
}

// ReplyTo is the message the next send answers, nil for none.
func (s *ChatSurface) ReplyTo() *runtime.ReplyTo { return s.replyTo }

// SetReplyTo makes the next send answer this entry (chat mode); nil cancels.
func (s *ChatSurface) SetReplyTo(target *runtime.ReplyTo) {
	s.replyTo = target
	s.runtime.Changed()
}

// ReplyTargets are the messages a reply can answer: people's and Pi's committed messages, newest first.
func (s *ChatSurface) ReplyTargets() []runtime.ReplyTarget {
	if s.current == nil {
		return nil
	}
	var targets []runtime.ReplyTarget
	for _, item := range s.current.Items {
		switch item := item.(type) {
		case transcript.UserItem:
			if item.EntryID == nil {
				continue
			}
			name := "You"
			if item.Author != nil {
				name = item.Author.Name
			} else if item.Peer != nil {
				name = item.Peer.Title
			}
			targets = append(targets, runtime.ReplyTarget{EntryID: *item.EntryID, Name: name, Excerpt: item.Text})
		case transcript.AssistantItem:
			if item.EntryID == nil || item.Message == nil {
				continue
			}
			var parts []string
			for _, part := range item.Message.Content {
				if text, ok := part.(ai.TextContent); ok {
					parts = append(parts, text.Text)
				}
			}
			targets = append(targets, runtime.ReplyTarget{EntryID: *item.EntryID, Name: "Pi", Excerpt: strings.Join(parts, " ")})
		}
	}
	slices.Reverse(targets)
	return targets[:min(len(targets), 50)]
}

// Contexts are the context terms for command `when` expressions (section 6.3).
func (s *ChatSurface) Contexts() []string {
	var terms []string
	if s.current != nil && s.current.Working {
		terms = append(terms, "busy")
	}
	if s.current != nil && len(s.current.Compactions) > 0 {
		terms = append(terms, "compacting")
	}
	if s.participant() == nil {
		return terms
	}
	terms = append(terms, "participant", "editorFocused")
	if s.Editor.Text() == "" {
		terms = append(terms, "editorEmpty")
	}
	if s.Editor.AutocompleteOpen() {
		terms = append(terms, "autocomplete")
	}
	if s.canReply {
		terms = append(terms, "canReply")
	}
	if s.identity != "" && len(s.attachments[s.identity]) > 0 {
		terms = append(terms, "attachments")
	}
	return terms
}

// HandleInput: the editor keeps input while the conversation connects (a reload, a reconnect); read-only views drop it.
func (s *ChatSurface) HandleInput(data string) {
	if !s.readOnlyView() {
		s.Editor.HandleInput(data)
	}
}

// Render is the surface without the fullscreen layout (tests): transcript, dock.
func (s *ChatSurface) Render(width int) []string {
	return append(s.transcript.Render(width), s.dock.Render(width)...)
}

// Invalidate drops cached renders.
func (s *ChatSurface) Invalidate() {
	s.transcript.Invalidate()
	s.Editor.Invalidate()
}

// Dispose releases everything the surface holds.
func (s *ChatSurface) Dispose() {
	s.SaveState()
	s.disposed = true
	s.removeListener()
	s.removeAnswers()
	s.cancel()
	if s.unsubscribe != nil {
		s.unsubscribe()
	}
	s.stopFollows()
	if s.indicator != nil {
		s.indicator.Dispose()
	}
	if s.shellView != nil {
		s.shellView.component.Dispose()
	}
	s.transcript.Dispose()
	s.sessions.Dispose()
	if s.tree != nil {
		s.tree.scope.Dispose()
	}
	if s.scope != nil {
		s.scope.Dispose()
	}
	s.autocomplete.Wait()
}

// NewFacet is a chat plugin TUI facet: the chat slot and its commands. The plugin defines the transcript's entry displays (chat-mode adds its own): served on
// the TUI too. A new facet value replaces the loaded one on a reload.
func NewFacet() host.Facet {
	return runtime.TuiFacet("chat", func(rt *runtime.TuiRuntime) runtime.TuiSurface { return NewChatSurface(rt) }, Commands(),
		func(facet host.FacetHost) error { return chatcontract.TranscriptEntries.Serve(facet) })
}

// Facet is the chat plugin's TUI facet.
var Facet = NewFacet()
