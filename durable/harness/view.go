// Ports packages/durable/src/harness/view.ts.

package harness

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// ConversationView is the structural mount of one conversation's active transcript and built-in documents (spec §9.3).
type ConversationView struct {
	Conversation durable.ConversationRecord `json:"conversation"`
	// Entries are the raw active entries, as ContextView.Entries: the head marker, then the non-head entries from its head.
	Entries []durable.EntryRecord `json:"entries"`
	// Docs holds pi.agent, pi.live, pi.inbox, and pi.usage, keyed by kind; absent documents are absent.
	Docs map[string]durable.JsonObject `json:"docs"`
}

// ViewObserver receives each next revision of a mount, and the Session's close. Advance and Publication may be nil.
type ViewObserver struct {
	Advance func(ctx context.Context, value ConversationView, ops []durable.Op)
	// Publication receives every publication, after the mount took it; ops are the mount's, possibly none.
	Publication  func(ctx context.Context, before, after ConversationView, ops []durable.Op, publication durable.CommitPublication)
	CloseSession func()
}

// mountedDocs are the documents a view mounts, in mount order.
func mountedDocs() []durable.AnyDocToken {
	return []durable.AnyDocToken{AgentDoc, LiveDoc, InboxDoc, UsageDoc}
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

// viewMount is one conversation's mount: its current revision, the document incarnations it shows, and its observers.
type viewMount struct {
	value ConversationView
	// docs is the mounted incarnation and definition version per kind; another incarnation or version is set whole.
	docs      map[string]docIncarnation
	observers []*ViewObserver
}

// ConversationViews holds the Harness's conversation view mounts: at most one per conversation, built on the Session line by its first observer and dropped with its last. Each mount advances from the Session's commit publications, which are durable.
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
	sessionImpl.SubscribeCommits(func(ctx context.Context, publication durable.CommitPublication) {
		// Detach may run off the line, from a watch that ends on its delivery goroutine; read the observers under the lock.
		type advancing struct {
			id        durable.ConversationId
			mount     *viewMount
			observers []*ViewObserver
		}
		views.mu.Lock()
		mounts := make([]advancing, 0, len(views.order))
		for _, id := range views.order {
			mount := views.mounts[id]
			mounts = append(mounts, advancing{id, mount, slices.Clone(mount.observers)})
		}
		views.mu.Unlock()
		for _, entry := range mounts {
			advanceView(ctx, entry.id, entry.mount, entry.observers, publication)
		}
	})
	sessionImpl.SubscribeClose(func() {
		views.mu.Lock()
		views.closed = true
		var observers []*ViewObserver
		for _, id := range views.order {
			observers = append(observers, views.mounts[id].observers...)
		}
		views.mounts = map[durable.ConversationId]*viewMount{}
		views.order = nil
		views.mu.Unlock()
		for _, observer := range observers {
			observer.CloseSession()
		}
	})
	return views
}

// State returns a disposable read-only Chord state of the view.
func (views *ConversationViews) State(ctx context.Context, id durable.ConversationId) (durable.AttachedReplicatedState[ConversationView], error) {
	var source *session.CommittedStateSource[ConversationView]
	observer, detach, err := views.Attach(ctx, id, func(value ConversationView, release func(), _ durable.Storage) (*ViewObserver, error) {
		source = session.NewCommittedStateSource(views.session, value, release)
		return &ViewObserver{
			Advance:      func(ctx context.Context, value ConversationView, ops []durable.Op) { source.Advance(ctx, value, ops) },
			CloseSession: source.CloseSession,
		}, nil
	})
	if err != nil {
		return nil, err
	}
	_ = observer
	state, err := chord.AttachReplicatedStateSource[ConversationView](source, chord.ReplicatedStateSourceOptions{})
	if err != nil {
		detach()
		return nil, err
	}
	return state, nil
}

// Watch returns a serialized exact-frame watch of the view; cancelling ctx stops it.
func (views *ConversationViews) Watch(ctx context.Context, id durable.ConversationId) (ConversationWatch, error) {
	var watch *session.CommittedWatch[ConversationView]
	_, _, err := views.Attach(ctx, id, func(value ConversationView, release func(), _ durable.Storage) (*ViewObserver, error) {
		watch = session.NewCommittedWatch(views.session, value, release, nil)
		return &ViewObserver{
			Advance:      func(ctx context.Context, value ConversationView, ops []durable.Op) { watch.Advance(ctx, value, ops) },
			CloseSession: watch.CloseSession,
		}, nil
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

// Attach registers an observer created from the current revision, atomically on the Session line: it sees every later publication and nothing earlier. create may read committed Storage, still on the line. The returned detach drops the observer, and the mount with its last observer.
func (views *ConversationViews) Attach(ctx context.Context, id durable.ConversationId, create func(value ConversationView, release func(), storage durable.Storage) (*ViewObserver, error)) (*ViewObserver, func(), error) {
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
		var observer *ViewObserver
		detach := func() {
			views.mu.Lock()
			defer views.mu.Unlock()
			mount.observers = slices.DeleteFunc(mount.observers, func(candidate *ViewObserver) bool { return candidate == observer })
			if len(mount.observers) == 0 && views.mounts[id] == mount {
				delete(views.mounts, id)
				views.order = slices.DeleteFunc(views.order, func(candidate durable.ConversationId) bool { return candidate == id })
			}
		}
		created, err := create(mount.value, detach, views.storage)
		if err != nil {
			return nil, err
		}
		observer = created
		views.mu.Lock()
		defer views.mu.Unlock()
		// Close or cancellation may begin while the mount hydrates; register nothing then.
		if views.closed {
			return nil, closedError()
		}
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		if _, exists := views.mounts[id]; !exists {
			views.order = append(views.order, id)
		}
		views.mounts[id] = mount
		mount.observers = append(mount.observers, observer)
		return attached{observer: observer, detach: detach}, nil
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
	bounds, err := CaptureContextBounds(ctx, views.storage, id, nil)
	if err != nil {
		return nil, err
	}
	entries, err := ActiveEntries(ctx, views.storage, id, bounds)
	if err != nil {
		return nil, err
	}
	docs := map[string]durable.JsonObject{}
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
		docs[kind] = loaded.Value
		incarnations[kind] = docIncarnation{id: loaded.Record.Id, version: loaded.Version}
	}
	return &viewMount{value: ConversationView{Conversation: *conversation, Entries: entries, Docs: docs}, docs: incarnations}, nil
}

// advanceView derives the mount's operations from one publication, applies them, and hands the revision to every observer.
func advanceView(ctx context.Context, id durable.ConversationId, mount *viewMount, observers []*ViewObserver, publication durable.CommitPublication) {
	var docOps, entryOps []durable.Op
	entries := mount.value.Entries
	entriesChanged := false
	// Entry writes are published in ID order.
	for _, change := range publication.Changes {
		if write, ok := change.(durable.EntryWrite); ok && write.Value.ConversationId == id {
			entry := write.Value
			value, err := durable.ToJsonValue(entry)
			if err != nil {
				panic(err)
			}
			if entry.Head == nil {
				entryOps = append(entryOps, durable.Op{"p", []any{"entries"}, len(entries), 0, []any{value}})
				entries = append(slices.Clone(entries), entry)
				entriesChanged = true
				continue
			}
			// A head marker keeps the non-head entries from its head, which are always a suffix, and goes in front.
			target := *entry.Head
			kept := slices.IndexFunc(entries, func(candidate durable.EntryRecord) bool {
				return candidate.Head == nil && candidate.Id >= target
			})
			if kept < 0 {
				kept = len(entries)
			}
			entryOps = append(entryOps, durable.Op{"p", []any{"entries"}, 0, kept, []any{value}})
			entries = append([]durable.EntryRecord{entry}, entries[kept:]...)
			entriesChanged = true
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
		mounted, isMounted := mount.docs[kind]
		switch {
		case document.Value == nil:
			if !isMounted || mounted.id != document.Record.Id {
				continue
			}
			delete(mount.docs, kind)
			docOps = append(docOps, durable.Op{"d", path})
		case isMounted && mounted.id == document.Record.Id && document.Version != nil && mounted.version == *document.Version:
			for _, op := range document.Ops {
				docOps = append(docOps, prefixedOp(op, path))
			}
		default:
			mount.docs[kind] = docIncarnation{id: document.Record.Id, version: *document.Version}
			docOps = append(docOps, durable.Op{"s", path, map[string]any(document.Value)})
		}
	}
	before := mount.value
	ops := slices.Concat(docOps, entryOps)
	frameContext := context.WithoutCancel(ctx)
	if len(ops) > 0 {
		value := mount.value
		if len(docOps) > 0 {
			docs, err := applyDocOps(value.Docs, docOps)
			if err != nil {
				panic(err)
			}
			value.Docs = docs
		}
		if entriesChanged {
			value.Entries = entries
		}
		mount.value = value
		for _, observer := range observers {
			if observer.Advance != nil {
				observer.Advance(frameContext, mount.value, ops)
			}
		}
	}
	for _, observer := range observers {
		if observer.Publication != nil {
			observer.Publication(frameContext, before, mount.value, ops, publication)
		}
	}
}

// applyDocOps applies view operations under ["docs", ...] immutably: unchanged documents keep their identity.
func applyDocOps(docs map[string]durable.JsonObject, ops []durable.Op) (map[string]durable.JsonObject, error) {
	root := map[string]any{}
	tree := map[string]any{}
	for kind, value := range docs {
		tree[kind] = map[string]any(value)
	}
	root["docs"] = tree
	applied, err := delta.ApplyImmutable(root, ops)
	if err != nil {
		return nil, err
	}
	next := map[string]durable.JsonObject{}
	for kind, value := range applied.(map[string]any)["docs"].(map[string]any) {
		next[kind] = value.(map[string]any)
	}
	return next, nil
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
