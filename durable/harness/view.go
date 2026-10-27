// Ports packages/durable/src/harness/view.ts.

package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/internal/detach"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// ConversationView is the structural view of one conversation: a window over its transcript and its built-in documents (spec §9.3).
type ConversationView struct {
	Conversation durable.ConversationRecord `json:"conversation"`
	// Entries are a window over the transcript, its fork-aware history (Conversation.Entries), in entry ID order. Head markers
	// such as compaction summaries sit where they were written; their Head tells where the entries they summarize end.
	Entries []durable.EntryRecord `json:"entries"`
	// Before is the ID of the entry just before the window; nil when the window starts the transcript.
	Before *durable.EntryId `json:"before,omitempty"`
	// Docs holds the built-in conversation documents keyed by kind; absent documents are absent.
	Docs ViewDocs `json:"docs"`
}

// ConversationWindow says which entries a view's window holds (spec §9.3). The window holds between Min and Max entries: it
// starts with the newest Min entries, takes appends, and once it would hold more than Max, drops its oldest in one splice
// down to Min. With From, it starts at From instead when at most Max entries are at or after it. Every field is optional;
// a nil *ConversationWindow takes every default.
type ConversationWindow struct {
	// From is where the first value's window starts, inclusive, such as right after the newest entry a client has
	// (ConversationTranscript.Resume). It selects only that first value: every entry committed after the attachment is
	// appended, whatever its ID. A From past the newest entry starts the window empty, with Before the newest entry.
	From *durable.EntryId
	// Min defaults to 50; an integer, at least 1.
	Min *int
	// Max defaults to 2 * Min; an integer, at least Min.
	Max *int
}

// defaultWindowMin is the default ConversationWindow.Min.
const defaultWindowMin = 50

// viewScanPageSize is the page size of the scans that load a window.
const viewScanPageSize = 256

// ViewDocs is a view's read-only record of documents keyed by kind (Pi Readonly<Record<string, JsonObject>>). Its keys keep Pi's object
// order (view.ts:161-166): mounted documents in mount order, then documents set later in the order they arrive.
type ViewDocs struct{ object *delta.JsonObject }

// ViewDocsOf returns docs with document set at kind, last when kind is new; docs itself is unchanged.
func ViewDocsOf(docs ViewDocs, kind string, document durable.JsonObject) ViewDocs {
	object := delta.NewJsonObject(docs.Len() + 1)
	for name, value := range docs.object.All() {
		object.Set(name, value)
	}
	object.Set(kind, document)
	return ViewDocs{object}
}

// ViewDocsWithout returns docs without kind; docs itself is unchanged.
func ViewDocsWithout(docs ViewDocs, kind string) ViewDocs {
	object := delta.NewJsonObject(docs.Len())
	for name, value := range docs.object.All() {
		if name != kind {
			object.Set(name, value)
		}
	}
	return ViewDocs{object}
}

// Get returns the document of kind and whether it is present.
func (docs ViewDocs) Get(kind string) (durable.JsonObject, bool) {
	value, present := docs.object.Get(kind)
	document, _ := value.(durable.JsonObject)
	return document, present
}

// Has reports whether a document of kind is present.
func (docs ViewDocs) Has(kind string) bool { return docs.object.Has(kind) }

// Len returns the number of documents.
func (docs ViewDocs) Len() int { return docs.object.Len() }

// Keys returns the kinds in order.
func (docs ViewDocs) Keys() []string { return docs.object.Keys() }

// All iterates the kinds and documents in order.
func (docs ViewDocs) All() iter.Seq2[string, durable.JsonObject] {
	return func(yield func(string, durable.JsonObject) bool) {
		for kind, value := range docs.object.All() {
			document, _ := value.(durable.JsonObject)
			if !yield(kind, document) {
				return
			}
		}
	}
}

// MarshalJSON writes the documents as one JSON object in order.
func (docs ViewDocs) MarshalJSON() ([]byte, error) {
	if docs.object == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(docs.object)
}

// UnmarshalJSON reads a JSON object of JSON objects, keeping the text's key order.
func (docs *ViewDocs) UnmarshalJSON(data []byte) error {
	object := delta.NewJsonObject(0)
	if err := json.Unmarshal(data, object); err != nil {
		return err
	}
	for kind, value := range object.All() {
		if _, isObject := value.(durable.JsonObject); !isObject {
			return fmt.Errorf("view document %q is not a JSON object", kind)
		}
	}
	docs.object = object
	return nil
}

// ViewObserver receives each next revision of one observer's window, and the Session's close.
type ViewObserver struct {
	// Advance receives each next revision.
	Advance func(ctx context.Context, change chord.WatchChange[ConversationView])
	// End receives the Session's failure, nil when it closed.
	End func(failure error)
}

// mountedDocs are the documents a view mounts, in mount order.
func mountedDocs() []durable.AnyDocToken {
	return []durable.AnyDocToken{AgentDoc, LiveDoc, InboxDoc, ProviderDoc, UsageDoc}
}

func mountedKind(kind string) bool {
	for _, token := range mountedDocs() {
		if token.AnyDefinition().Kind == kind {
			return true
		}
	}
	return false
}

type docIncarnation struct {
	id      durable.DocumentId
	version int
}

// windowedObserver is one observer of a mount and its window: the bounds and the revision it was last given.
type windowedObserver struct {
	min, max int
	value    ConversationView
	observer *ViewObserver
}

// viewMount is one conversation's mount: the documents it shows, shared by every observer, and the observers, each with
// its own window over the entries.
type viewMount struct {
	conversation durable.ConversationRecord
	docs         ViewDocs
	// incarnations is the mounted incarnation and definition version per kind; another incarnation or version is set whole.
	incarnations map[string]docIncarnation
	observers    []*windowedObserver
}

// ConversationViews holds the Harness's conversation view mounts: at most one per conversation, built on the Session line by its first observer and dropped with its last. Each mount advances from the Session's commit publications, which are durable, and hands each observer the next revision of its own window.
type ConversationViews struct {
	session *session.SessionImpl
	storage durable.Storage
	mu      sync.Mutex
	mounts  map[durable.ConversationId]*viewMount
	order   []durable.ConversationId
	closed  bool
}

// NewConversationViews subscribes the mounts to the Session's commits and close.
func NewConversationViews(sessionImpl *session.SessionImpl, storage durable.Storage) *ConversationViews {
	views := &ConversationViews{session: sessionImpl, storage: storage, mounts: map[durable.ConversationId]*viewMount{}}
	sessionImpl.ObserveCommits(func(ctx context.Context, publication session.ObservedCommit) {
		// Detach may run off the line, from a watch that ends on its delivery goroutine; read the observers under the lock.
		type advancing struct {
			id        durable.ConversationId
			mount     *viewMount
			observers []*windowedObserver
		}
		views.mu.Lock()
		mounts := make([]advancing, 0, len(views.order))
		for _, id := range views.order {
			mount := views.mounts[id]
			mounts = append(mounts, advancing{id, mount, slices.Clone(mount.observers)})
		}
		views.mu.Unlock()
		for _, entry := range mounts {
			advanceView(ctx, entry.id, entry.mount, entry.observers, publication, views.session.Report)
		}
	})
	sessionImpl.SubscribeClose(func() {
		failure := views.session.Failure()
		views.mu.Lock()
		views.closed = true
		var observers []*windowedObserver
		for _, id := range views.order {
			observers = append(observers, views.mounts[id].observers...)
		}
		views.mounts = map[durable.ConversationId]*viewMount{}
		views.order = nil
		views.mu.Unlock()
		for _, windowed := range observers {
			session.Contained(func() { windowed.observer.End(failure) }, views.session.Report)
		}
	})
	return views
}

// Observe attaches a synchronous observer of the view (Harness.ObserveConversation), behind session.GuardObserver; ctx governs
// only the attachment.
func (views *ConversationViews) Observe(ctx context.Context, id durable.ConversationId, observer durable.Observer[ConversationView], window *ConversationWindow) (durable.Observation[ConversationView], error) {
	var value ConversationView
	var guarded *session.GuardedObserver[ConversationView]
	_, detach, err := views.Attach(ctx, id, window, func(current ConversationView, release func()) *ViewObserver {
		value = current
		guarded = session.GuardObserver(observer, release, views.session.Report)
		return &ViewObserver{Advance: guarded.Advance, End: guarded.End}
	})
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		detach()
		return nil, context.Cause(ctx)
	}
	return session.NewObservation(value, guarded), nil
}

// Watch returns a serialized watch of the view, an observer of its mount; cancelling ctx stops it.
func (views *ConversationViews) Watch(ctx context.Context, id durable.ConversationId, window *ConversationWindow) (ConversationWatch, error) {
	var watch *chord.Watch[ConversationView]
	_, _, err := views.Attach(ctx, id, window, func(value ConversationView, release func()) *ViewObserver {
		watch = session.NewSessionWatch(views.session, value, release)
		return &ViewObserver{
			Advance: watch.Advance,
			End:     watch.SourceEnded,
		}
	})
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		watch.Cancel()
		return nil, context.Cause(ctx)
	}
	if ctx.Done() != nil {
		watch.ObserveCancellation(ctx)
	}
	return watch, nil
}

// windowBounds is a ConversationWindow with its defaults applied.
type windowBounds struct {
	from     *durable.EntryId
	min, max int
}

// resolveWindow returns window with its defaults; it fails for a Min below 1 and for a Max below Min.
func resolveWindow(window *ConversationWindow) (windowBounds, error) {
	bounds := windowBounds{min: defaultWindowMin}
	if window != nil {
		bounds.from = window.From
		if window.Min != nil {
			bounds.min = *window.Min
		}
	}
	if bounds.min < 1 {
		return windowBounds{}, fmt.Errorf("Window min must be an integer >= 1, got %d", bounds.min)
	}
	bounds.max = 2 * bounds.min
	if window != nil && window.Max != nil {
		bounds.max = *window.Max
	}
	if bounds.max < bounds.min {
		return windowBounds{}, fmt.Errorf("Window max must be an integer >= min (%d), got %d", bounds.min, bounds.max)
	}
	return bounds, nil
}

// Attach registers an observer created from its window's first revision, atomically on the Session line: it sees every later publication and nothing earlier. The returned detach drops the observer, and the mount with its last observer.
func (views *ConversationViews) Attach(ctx context.Context, id durable.ConversationId, window *ConversationWindow, create func(value ConversationView, release func()) *ViewObserver) (*ViewObserver, func(), error) {
	bounds, err := resolveWindow(window)
	if err != nil {
		return nil, nil, err
	}
	type attached struct {
		observer *ViewObserver
		detach   func()
	}
	result, err := views.session.ReadOnLine(func() (any, error) {
		views.mu.Lock()
		mount := views.mounts[id]
		views.mu.Unlock()
		if mount == nil {
			built, err := views.build(ctx, id)
			if err != nil {
				return nil, err
			}
			mount = built
		}
		entries, before, err := loadWindow(ctx, views.storage, id, bounds)
		if err != nil {
			return nil, err
		}
		windowed := &windowedObserver{min: bounds.min, max: bounds.max, value: viewOf(mount.conversation, entries, before, mount.docs)}
		detach := func() {
			views.mu.Lock()
			defer views.mu.Unlock()
			mount.observers = slices.DeleteFunc(mount.observers, func(candidate *windowedObserver) bool { return candidate == windowed })
			if len(mount.observers) == 0 && views.mounts[id] == mount {
				delete(views.mounts, id)
				views.order = slices.DeleteFunc(views.order, func(candidate durable.ConversationId) bool { return candidate == id })
			}
		}
		windowed.observer = create(windowed.value, detach)
		views.mu.Lock()
		defer views.mu.Unlock()
		// Close or cancellation may begin while the mount hydrates; register nothing then.
		if views.closed {
			return nil, closedError(views.session)
		}
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		if _, exists := views.mounts[id]; !exists {
			views.order = append(views.order, id)
		}
		views.mounts[id] = mount
		mount.observers = append(mount.observers, windowed)
		return attached{observer: windowed.observer, detach: detach}, nil
	})
	if err != nil {
		return nil, nil, err
	}
	registered := result.(attached)
	return registered.observer, registered.detach, nil
}

func (views *ConversationViews) build(ctx context.Context, id durable.ConversationId) (*viewMount, error) {
	conversation, err := views.storage.Conversation(ctx, id)
	if err != nil {
		return nil, err
	}
	if conversation == nil {
		return nil, fmt.Errorf("Conversation %d does not exist", id)
	}
	docs := ViewDocs{delta.NewJsonObject(0)}
	incarnations := map[string]docIncarnation{}
	for _, token := range mountedDocs() {
		loaded, err := views.session.ConversationDocumentOnLine(ctx, token, id)
		if err != nil {
			return nil, err
		}
		if loaded == nil {
			continue
		}
		kind := token.AnyDefinition().Kind
		docs.object.Set(kind, loaded.Value)
		incarnations[kind] = docIncarnation{id: loaded.Record.Id, version: loaded.Version}
	}
	return &viewMount{conversation: *conversation, docs: docs, incarnations: incarnations}, nil
}

// viewOf is a view revision; Before is set only when there is an entry before the window.
func viewOf(conversation durable.ConversationRecord, entries []durable.EntryRecord, before *durable.EntryId, docs ViewDocs) ConversationView {
	return ConversationView{Conversation: conversation, Entries: entries, Before: before, Docs: docs}
}

// loadWindow reads a new observer's window on the Session line with bounded scans: the entries from bounds.from when at most
// bounds.max are at or after it, otherwise the newest bounds.min, and the entry before them. The entries are the observer's own.
func loadWindow(ctx context.Context, storage durable.Storage, conversationId durable.ConversationId, bounds windowBounds) ([]durable.EntryRecord, *durable.EntryId, error) {
	if bounds.from != nil {
		since, err := scanUpTo(ctx, storage, durable.EntryQuery{ConversationId: conversationId, MinEntryId: bounds.from, Order: new(durable.ScanAscending)}, bounds.max+1)
		if err != nil {
			return nil, nil, err
		}
		if len(since) <= bounds.max {
			page, err := storage.ScanEntries(ctx, durable.EntryQuery{ConversationId: conversationId, MaxEntryId: new(*bounds.from - 1)}, 1, nil)
			if err != nil {
				return nil, nil, err
			}
			var before *durable.EntryId
			if len(page.Items) > 0 {
				before = new(page.Items[0].Id)
			}
			return ownedEntries(storage, since), before, nil
		}
	}
	newest, err := scanUpTo(ctx, storage, durable.EntryQuery{ConversationId: conversationId}, bounds.min+1)
	if err != nil {
		return nil, nil, err
	}
	var before *durable.EntryId
	if len(newest) > bounds.min {
		before = new(newest[bounds.min].Id)
		newest = newest[:bounds.min]
	}
	slices.Reverse(newest)
	return ownedEntries(storage, newest), before, nil
}

// scanUpTo returns up to limit entries of query, following cursors past short pages.
func scanUpTo(ctx context.Context, storage durable.Storage, query durable.EntryQuery, limit int) ([]durable.EntryRecord, error) {
	entries := make([]durable.EntryRecord, 0, min(limit, viewScanPageSize))
	var cursor durable.Cursor
	for {
		page, err := storage.ScanEntries(ctx, query, min(viewScanPageSize, limit-len(entries)), cursor)
		if err != nil {
			return nil, err
		}
		entries = append(entries, page.Items...)
		if page.Next == nil || len(entries) >= limit {
			return entries, nil
		}
		cursor = *page.Next
	}
}

// ownedEntries detaches entries a storage shares with its readers: the view's state is the observers' own.
func ownedEntries(storage durable.Storage, entries []durable.EntryRecord) []durable.EntryRecord {
	if !sharesEntries(storage) {
		return entries
	}
	for index := range entries {
		entries[index] = detach.Entry(entries[index])
	}
	return entries
}

// advanceView derives the mount's operations from one publication and hands each observer the next revision of its window; one
// that panics is reported through report, and the others still get theirs. Documents are applied once and shared; each window
// takes the appended entries and, past its max, drops its oldest down to its min in one splice.
func advanceView(ctx context.Context, id durable.ConversationId, mount *viewMount, observers []*windowedObserver, publication session.ObservedCommit, report func(error)) {
	var docOps []durable.Op
	// Entry writes are published in ID order.
	var appended []durable.EntryRecord
	for _, change := range publication.Changes {
		if write, ok := change.(durable.EntryWrite); ok && write.Value.ConversationId == id {
			appended = append(appended, write.Value)
			continue
		}
		document, ok := change.(durable.DocumentChange)
		if !ok || document.ConversationId == nil || *document.ConversationId != id {
			continue
		}
		kind := document.Record.Kind
		if !mountedKind(kind) || document.Record.Key != nil {
			continue
		}
		path := []any{"docs", kind}
		mounted, isMounted := mount.incarnations[kind]
		switch {
		case document.Value == nil:
			if !isMounted || mounted.id != document.Record.Id {
				continue
			}
			delete(mount.incarnations, kind)
			docOps = append(docOps, durable.Op{"d", path})
		case isMounted && mounted.id == document.Record.Id && document.Version != nil && mounted.version == *document.Version:
			for _, op := range document.Ops {
				docOps = append(docOps, prefixedOp(op, path))
			}
		default:
			mount.incarnations[kind] = docIncarnation{id: document.Record.Id, version: *document.Version}
			docOps = append(docOps, durable.Op{"s", path, document.Value})
		}
	}
	if len(docOps) == 0 && len(appended) == 0 {
		return
	}
	if len(docOps) > 0 {
		docs, err := applyDocOps(mount.docs, docOps)
		if err != nil {
			panic(err)
		}
		mount.docs = docs
	}
	frameContext := context.WithoutCancel(ctx)
	var inserted []any
	for _, entry := range appended {
		value, err := durable.ToJsonValue(entry)
		if err != nil {
			panic(err)
		}
		inserted = append(inserted, value)
	}
	for _, windowed := range observers {
		previous := windowed.value
		entries := previous.Entries
		before := previous.Before
		ops := slices.Clone(docOps)
		if len(appended) > 0 {
			ops = append(ops, durable.Op{"p", []any{"entries"}, len(entries), 0, inserted})
			entries = slices.Concat(entries, appended)
			if len(entries) > windowed.max {
				drop := len(entries) - windowed.min
				before = new(entries[drop-1].Id)
				entries = entries[drop:]
				ops = append(ops, durable.Op{"p", []any{"entries"}, 0, drop, []any{}}, durable.Op{"s", []any{"before"}, float64(*before)})
			}
		}
		windowed.value = viewOf(mount.conversation, entries, before, mount.docs)
		change := chord.WatchChange[ConversationView]{Value: windowed.value, Ops: ops}
		session.Contained(func() { windowed.observer.Advance(frameContext, change) }, report)
	}
}

// applyDocOps applies view operations under ["docs", ...] immutably: unchanged documents keep their identity.
func applyDocOps(docs ViewDocs, ops []durable.Op) (ViewDocs, error) {
	object := docs.object
	if object == nil {
		object = delta.NewJsonObject(0)
	}
	applied, err := delta.ApplyImmutable(delta.JsonObjectOf("docs", object), ops)
	if err != nil {
		return ViewDocs{}, err
	}
	return ViewDocs{applied.(*delta.JsonObject).Value("docs").(*delta.JsonObject)}, nil
}

// viewDoc returns the mounted document of kind, or nil when it is absent.
func viewDoc(view ConversationView, kind string) durable.JsonObject {
	document, _ := view.Docs.Get(kind)
	return document
}

// prefixedOp moves op under prefix; a root replacement becomes a set of the prefix.
func prefixedOp(op durable.Op, prefix []any) durable.Op {
	at := func(path any) []any { return slices.Concat(prefix, path.([]any)) }
	switch op[0] {
	case "r":
		return durable.Op{"s", prefix, op[1]}
	case "p":
		return durable.Op{"p", at(op[1]), op[2], op[3], op[4]}
	case "m", "s", "a", "t":
		return durable.Op{op[0], at(op[1]), op[2]}
	case "d":
		return durable.Op{"d", at(op[1])}
	}
	panic(fmt.Sprintf("unknown operation %v", op[0]))
}
