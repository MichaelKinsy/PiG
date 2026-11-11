package workspace

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	locationscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/locations/contract"
	subagentscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/workspace/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	durableharness "github.com/MichaelKinsy/PiG/durable/harness"
)

// Ports apps/p2/plugins/workspace/worker.ts: the worker serves a conversation's workspace (`WorkspaceBackend`: the
// environment where it works now and a watch reporting its changes), the session's upload writes (`WorkspaceWrites`), and
// to people (`Workspace`, participants only) and the server's HTTP transport (`WorkspaceTransfer`, server only) the views
// of that backend.
//
// Forced Go differences. Pi keeps the workspaces of `WorkspaceWrites` in a WeakMap by environment, so those of environments
// a move closed go with them; here the most recent writeWorkspaces are kept (an evicted one is built again at its next
// write, which only reads the private directory's canonical path). An awaited promise is a channel closed when it settles.

// writeWorkspaces is how many environments' workspaces WorkspaceWrites keeps.
const writeWorkspaces = 8

// watchDebounce is how long a workspace's changes are gathered into one notification.
const watchDebounce = 100 * time.Millisecond

func objectOf(value any) *delta.JsonObject {
	object, _ := value.(*delta.JsonObject)
	return object
}

func stringOf(value any) string {
	text, _ := value.(string)
	return text
}

func integerOf(value any) *int64 {
	number, ok := value.(float64)
	if !ok {
		return nil
	}
	whole := int64(number)
	return &whole
}

// deniedDirectories are the directories a workspace over env hides: p2's private data directory on this machine only,
// as its canonical path when that resolves (else as given).
func deniedDirectories(ctx context.Context, env durableenv.ExecutionEnv, dataDir string) string {
	if !p2runtime.IsLocalEnvironment(env) {
		return ""
	}
	if canonical, err := env.CanonicalPath(ctx, dataDir); err == nil {
		return canonical
	}
	return dataDir
}

func deniedList(denied string) []string {
	if denied == "" {
		return nil
	}
	return []string{denied}
}

// cached keeps the first success of a native getter (HarnessOf, WorkerOf): the natives do not change once open, and the
// registry value behind them may be read only inside a section of the host, so the first call is made in one (a handler's
// synchronous part, a factory) and later ones, off the loop, take the kept value.
func cached[T any](get func() (*T, error)) func() (*T, error) {
	var mu sync.Mutex
	var kept *T
	return func() (*T, error) {
		mu.Lock()
		defer mu.Unlock()
		if kept != nil {
			return kept, nil
		}
		value, err := get()
		if err == nil {
			kept = value
		}
		return value, err
	}
}

func parts(self host.Self) (worker func() (*p2runtime.WorkerParts, error), harness func() (*p2runtime.HarnessParts, error)) {
	worker, harness = cached(p2runtime.WorkerOf(self)), cached(p2runtime.HarnessOf(self))
	return worker, harness
}

// writer is `WorkspaceWrites`: uploads of every conversation one at a time, each through the environment at its location.
type writer struct {
	harness func() (*p2runtime.HarnessParts, error)
	worker  func() (*p2runtime.WorkerParts, error)
	turn    chan struct{}

	mu         sync.Mutex
	workspaces map[durableenv.ExecutionEnv]*list.Element
	recent     *list.List
}

type writeEntry struct {
	env       durableenv.ExecutionEnv
	workspace *Workspace
}

func (w *writer) workspaceAt(ctx context.Context, location p2runtime.Location) (*Workspace, error) {
	harness, err := w.harness()
	if err != nil {
		return nil, err
	}
	environments, err := harness.Environments()
	if err != nil {
		return nil, err
	}
	env, err := environments.At(ctx, location)
	if err != nil {
		return nil, err
	}
	w.mu.Lock()
	if found, ok := w.workspaces[env]; ok {
		w.recent.MoveToFront(found)
		workspace := found.Value.(writeEntry).workspace
		w.mu.Unlock()
		return workspace, nil
	}
	w.mu.Unlock()
	worker, err := w.worker()
	if err != nil {
		return nil, err
	}
	workspace := NewWorkspace(env, deniedList(deniedDirectories(ctx, env, worker.Init.DataDir)))
	w.mu.Lock()
	defer w.mu.Unlock()
	if found, ok := w.workspaces[env]; ok {
		return found.Value.(writeEntry).workspace, nil
	}
	w.workspaces[env] = w.recent.PushFront(writeEntry{env: env, workspace: workspace})
	for w.recent.Len() > writeWorkspaces {
		oldest := w.recent.Back()
		w.recent.Remove(oldest)
		delete(w.workspaces, oldest.Value.(writeEntry).env)
	}
	return workspace, nil
}

func serveWrites(facet host.FacetHost) error {
	return facet.Serve(contract.WorkspaceWrites, host.LocalFactory(func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		worker, harness := parts(self)
		w := &writer{harness: harness, worker: worker, turn: make(chan struct{}, 1), workspaces: map[durableenv.ExecutionEnv]*list.Element{}, recent: list.New()}
		return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
			"write": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				in := objectOf(args)
				location, err := p2runtime.ParseLocationJSON(in.Value("location"))
				if err != nil {
					return nil, err
				}
				// The first read of the natives is made here, in the handler's synchronous part.
				if _, err := w.harness(); err != nil {
					return nil, err
				}
				if _, err := w.worker(); err != nil {
					return nil, err
				}
				chordctx.Suspend(ctx)
				select {
				case w.turn <- struct{}{}:
				case <-ctx.Done():
					return nil, context.Cause(ctx)
				}
				defer func() { <-w.turn }()
				if err := ctx.Err(); err != nil {
					return nil, context.Cause(ctx)
				}
				workspace, err := w.workspaceAt(ctx, location)
				if err != nil {
					return nil, err
				}
				return nil, workspace.Write(ctx, objectOf(in.Value("upload")))
			},
		}}}, nil
	}), host.ServeOptions{})
}

// opened is a workspace as the conversation works now: the view, where that is, and its watch (which starts after the
// workspace answers: walking a large tree takes many seconds).
type opened struct {
	workspace *Workspace
	location  p2runtime.Location
	// watch delivers the watcher once its start settles (nil when it failed or the environment cannot watch).
	watch chan durableenv.FileWatcher
	// privateRelative is p2's private data directory relative to the root, when it lies inside it.
	privateRelative string
}

// opening is an open in progress.
type opening struct {
	done  chan struct{}
	value *opened
	err   error
}

func (o *opening) wait(ctx context.Context) (*opened, error) {
	select {
	case <-o.done:
		return o.value, o.err
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

// slot is the open of one version of the conversation's place.
type slot struct {
	version string
	opening *opening
	abandon context.CancelFunc
}

// backend is the viewed conversation's workspace where it works now (`WorkspaceBackend`'s resource).
type backend struct {
	conversation durable.ConversationId
	harness      func() (*p2runtime.HarnessParts, error)
	worker       *p2runtime.WorkerParts
	durable      durableharness.Harness
	environments p2runtime.ExecutionEnvironments

	closing func()
	// closed ends every open.
	closedContext context.Context

	mu        sync.Mutex
	listeners map[int]func()
	nextID    int
	pending   *time.Timer
	// version counts the workspace's changes (at most one per watchDebounce): viewers read again at each.
	version int
	// moves counts moves of the conversation's own location (a subagent moved by its main agent or by itself), which the
	// session's location does not see.
	moves int
	slot  *slot
	ended bool

	stopSession func()
	commits     durable.CommitObservation
}

func (b *backend) subscribe(changed func()) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	id := b.nextID
	b.listeners[id] = changed
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.listeners, id)
	}
}

func (b *backend) currentVersion() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.version
}

// notify tells viewers to read again, at most once per watchDebounce.
func (b *backend) notify() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pending != nil || b.ended {
		return
	}
	b.pending = time.AfterFunc(watchDebounce, func() {
		b.mu.Lock()
		b.pending = nil
		if b.ended {
			b.mu.Unlock()
			return
		}
		b.version++
		listeners := make([]func(), 0, len(b.listeners))
		for _, listener := range b.listeners {
			listeners = append(listeners, listener)
		}
		b.mu.Unlock()
		for _, listener := range listeners {
			listener()
		}
	})
}

// openWorkspace is the viewed conversation's workspace where it works now, and the watch reporting its changes.
func (b *backend) openWorkspace(ctx context.Context) (*opened, error) {
	handle, err := b.durable.Conversation(ctx, b.conversation)
	if err != nil {
		return nil, err
	}
	if handle == nil {
		return nil, errors.New("Unknown conversation")
	}
	// Where the viewed conversation works: a subagent may have its own location (e.g. a git worktree, on another machine
	// than the session now).
	location, env, err := p2runtime.ConversationEnvironment(ctx, b.environments, handle.Id(), b.durable)
	if err != nil {
		return nil, err
	}
	// p2's private data directory is denied on this machine, decided from the environment (not the location, which lags
	// behind it during a move).
	denied := deniedDirectories(ctx, env, b.worker.Init.DataDir)
	canonical, err := env.CanonicalPath(ctx, env.Cwd())
	if err != nil {
		return nil, errors.New("Working directory unavailable")
	}
	root := canonical
	if denied != "" && (root == denied || strings.HasPrefix(root, denied+"/")) {
		return nil, errors.New("Workspace is private")
	}
	base := strings.TrimRight(root, "/")
	privateRelative := ""
	if denied != "" && strings.HasPrefix(denied, base+"/") {
		privateRelative = denied[len(base)+1:]
	}
	workspace := NewWorkspace(env, deniedList(denied))
	// The workspace answers before its watch is up: walking a large tree (a home folder) takes many seconds. A failed
	// watch leaves the panel without live updates; once one is up, viewers read again (a change made before it would be
	// missed otherwise).
	watch := make(chan durableenv.FileWatcher, 1)
	go func() {
		watcher, err := workspace.Watch(ctx, b.notify)
		if err != nil {
			if ctx.Err() == nil {
				fmt.Fprintln(os.Stderr, "p2 workspace: watching failed:", err.Error())
			}
			watch <- nil
			return
		}
		b.notify()
		watch <- watcher
	}()
	return &opened{workspace: workspace, location: location, watch: watch, privateRelative: privateRelative}, nil
}

// release abandons a superseded open (a watch still walking its tree stops) and closes its watch.
func release(previous *slot) {
	previous.abandon()
	go func() {
		<-previous.opening.done
		if previous.opening.err != nil {
			return
		}
		if watcher := <-previous.opening.value.watch; watcher != nil {
			_ = watcher.Stop(context.Background())
		}
	}()
}

// current is the workspace of the location's current version. A move makes it older: the next use (or the move's listener)
// opens it again where the conversation works now and closes the superseded watch. A failed open is not kept: the next
// use tries again.
func (b *backend) current() *opening {
	b.mu.Lock()
	defer b.mu.Unlock()
	version := fmt.Sprintf("%d:%d", b.worker.Location.Version(), b.moves)
	if b.slot != nil && b.slot.version == version {
		return b.slot.opening
	}
	previous := b.slot
	ctx, abandon := context.WithCancel(b.closedContext)
	started := &opening{done: make(chan struct{})}
	mine := &slot{version: version, opening: started, abandon: abandon}
	b.slot = mine
	go func() {
		started.value, started.err = b.openWorkspace(ctx)
		close(started.done)
		if started.err != nil {
			b.mu.Lock()
			if b.slot == mine {
				b.slot = nil
			}
			b.mu.Unlock()
			abandon()
		}
	}()
	if previous != nil {
		release(previous)
	}
	return started
}

// workspaceNow is the current workspace, waiting for its open.
func (b *backend) workspaceNow(ctx context.Context) (*opened, error) {
	return b.current().wait(ctx)
}

// reopenIfMoved opens the workspace again when the conversation's own location moved (its agent changed).
func (b *backend) reopenIfMoved() {
	b.mu.Lock()
	var previous *opening
	if b.slot != nil {
		previous = b.slot.opening
	}
	b.mu.Unlock()
	var was *opened
	if previous != nil {
		was, _ = previous.wait(b.closedContext)
	}
	now, err := b.environments.Locate(b.closedContext, b.conversation, b.durable)
	if err != nil || was == nil || p2runtime.SameLocation(was.location, now) {
		return
	}
	b.mu.Lock()
	b.moves++
	b.mu.Unlock()
	_, _ = b.current().wait(b.closedContext)
	b.notify()
}

type commitObserver struct{ backend *backend }

func (o commitObserver) Advance(_ context.Context, publication durable.CommitPublication) {
	moveChanged, agentChanged := false, false
	for _, change := range publication.Changes {
		typed, ok := change.(durable.DocumentChange)
		if !ok || typed.Record.Scope.Kind != durable.ScopeConversation {
			continue
		}
		// The session's move waits, or failed: viewers show it with the workspace's place.
		if typed.Record.Kind == locationscontract.MoveKind && typed.Record.Scope.ConversationId == durable.ROOT_CONVERSATION_ID {
			moveChanged = true
		}
		// The conversation's agent changed (an own location's move configures its cwd): open again if it moved.
		if typed.Record.Kind == durableharness.AgentDoc.AnyDefinition().Kind && typed.Record.Scope.ConversationId == o.backend.conversation {
			agentChanged = true
		}
	}
	if moveChanged {
		o.backend.notify()
	}
	if agentChanged {
		go o.backend.reopenIfMoved()
	}
}

// End is the Session's close or failure: the worker's Harness, and this resource with it, ends.
func (commitObserver) End(chord.WatchEnd) {}

// openBackend is the conversation's workspace resource: opened before the resource is ready, so that its failure
// (an unknown conversation, a private directory) fails the activation with its cause.
func openBackend(ctx context.Context, conversation durable.ConversationId, harness func() (*p2runtime.HarnessParts, error), worker *p2runtime.WorkerParts) (*backend, error) {
	harnessParts, err := harness()
	if err != nil {
		return nil, err
	}
	session, err := harnessParts.Durable()
	if err != nil {
		return nil, err
	}
	environments, err := harnessParts.Environments()
	if err != nil {
		return nil, err
	}
	closedContext, closing := context.WithCancel(context.Background())
	b := &backend{
		conversation: conversation, harness: harness, worker: worker, durable: session, environments: environments,
		closing: closing, closedContext: closedContext, listeners: map[int]func(){},
	}
	// An abandoned activation aborts its open.
	stopAbandon := context.AfterFunc(ctx, closing)
	// Subscribed before the first open: a move while it activates is not missed. After a move, viewers are told to read
	// again (the conversation's directory or machine changed).
	b.stopSession = worker.Location.Subscribe(func(p2runtime.Location) {
		go func() {
			_, _ = b.current().wait(b.closedContext)
			b.notify()
		}()
	})
	b.commits = session.ObserveCommits(commitObserver{backend: b})
	_, err = b.current().wait(ctx)
	stopAbandon()
	if err != nil {
		b.close()
		return nil, err
	}
	return b, nil
}

func (b *backend) close() {
	b.stopSession()
	b.commits.Detach()
	b.closing()
	b.mu.Lock()
	b.ended = true
	if b.pending != nil {
		b.pending.Stop()
		b.pending = nil
	}
	previous := b.slot
	b.slot = nil
	b.mu.Unlock()
	if previous != nil {
		release(previous)
	}
}

func locationJSON(location *subagentscontract.LocationValue) *delta.JsonObject {
	var env any
	if location.Env != nil {
		env = *location.Env
	}
	return delta.JsonObjectOf("cwd", location.Cwd, "env", env)
}

func runtimeLocationJSON(location p2runtime.Location) *delta.JsonObject {
	var env any
	if location.Env != nil {
		env = *location.Env
	}
	return delta.JsonObjectOf("env", env, "cwd", location.Cwd)
}

// query answers one query: a place query with the session's waiting or failed move, the rest through the workspace with
// p2's private directory kept out.
func (b *backend) query(ctx context.Context, query *delta.JsonObject) (delta.JsonValue, error) {
	if path, ok := query.Value("path").(string); ok {
		if _, valid := contract.WorkspacePath(path); !valid {
			return nil, errors.New("Invalid workspace path")
		}
	}
	kind := stringOf(query.Value("kind"))
	if kind == "search" {
		if _, valid := contract.WorkspaceSearchText(stringOf(query.Value("text"))); !valid {
			return nil, errors.New("Invalid search text")
		}
	}
	current, err := b.workspaceNow(ctx)
	if err != nil {
		return nil, err
	}
	if kind == "place" {
		move, err := durable.Snapshot(ctx, b.durable, locationscontract.LocationMoveDoc, durable.ROOT_CONVERSATION_ID)
		if err != nil {
			return nil, err
		}
		place := delta.JsonObjectOf("kind", "place", "location", runtimeLocationJSON(current.location))
		if move != nil && move.Pending != nil {
			place.Set("pending", locationJSON(move.Pending))
		}
		if move != nil && move.Failed != nil {
			place.Set("failed", delta.JsonObjectOf("to", locationJSON(&move.Failed.To), "error", move.Failed.Error))
		}
		return place, nil
	}
	if private := current.privateRelative; private != "" {
		if path, ok := query.Value("path").(string); ok && (path == private || strings.HasPrefix(path, private+"/")) {
			return nil, errors.New("Path is private")
		}
		if path, _ := query.Value("path").(string); kind == "search" && (path == "" || strings.HasPrefix(private, path+"/")) {
			relative := private
			if path != "" {
				relative = private[len(path)+1:]
			}
			exclude := stringOf(query.Value("exclude"))
			if exclude != "" {
				exclude += ","
			}
			excluded := delta.NewJsonObject(0)
			for _, key := range query.Keys() {
				excluded.Set(key, query.Value(key))
			}
			excluded.Set("exclude", exclude+"./"+relative+"/")
			query = excluded
		}
	}
	return current.workspace.Query(ctx, query)
}

func (b *backend) read(ctx context.Context, args *delta.JsonObject) (delta.JsonValue, error) {
	current, err := b.workspaceNow(ctx)
	if err != nil {
		return nil, err
	}
	return current.workspace.Read(ctx, stringOf(args.Value("path")), ReadRange{Start: integerOf(args.Value("start")), End: integerOf(args.Value("end")), Suffix: integerOf(args.Value("suffix"))})
}

func (b *backend) write(ctx context.Context, writes *host.Reference, upload *delta.JsonObject) error {
	current, err := b.workspaceNow(ctx)
	if err != nil {
		return err
	}
	_, err = writes.Call("write", delta.JsonObjectOf("location", runtimeLocationJSON(current.location), "upload", upload), ctx)
	return err
}

func serveBackend(facet host.FacetHost) error {
	return facet.Serve(contract.WorkspaceBackend, host.LocalFactory(func(params *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		conversation, _ := params.Value("conversation").(float64)
		worker, harness := parts(self)
		writes := self.Get(contract.WorkspaceWrites, delta.NewJsonObject(0))
		resource := self.Open(
			func(ctx context.Context) (any, error) {
				workerParts, err := worker()
				if err != nil {
					return nil, err
				}
				return openBackend(ctx, durable.ConversationId(conversation), harness, workerParts)
			},
			func(value any, _ host.NativeCloseReason, _ context.Context) error {
				value.(*backend).close()
				return nil
			},
		)
		current := func() (*backend, error) {
			value, err := resource.Value()
			if err != nil {
				return nil, err
			}
			return value.(*backend), nil
		}
		// What a handler does with the worker's liveness: the call counts as active and is refused during a relocation.
		live := func(ctx context.Context, run func() (delta.JsonValue, error)) (delta.JsonValue, error) {
			workerParts, err := worker()
			if err != nil {
				return nil, err
			}
			chordctx.Suspend(ctx)
			var result delta.JsonValue
			err = workerParts.Liveness.Call(func() error {
				var runErr error
				result, runErr = run()
				return runErr
			})
			return result, err
		}
		changes := p2runtime.FollowNative(self,
			func(changed func()) func() {
				view, err := current()
				if err != nil {
					panic(err)
				}
				return view.subscribe(changed)
			},
			func() (delta.JsonValue, error) {
				view, err := current()
				if err != nil {
					return nil, err
				}
				return float64(view.currentVersion()), nil
			},
		)
		return contracts.Implementation{Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"changes": changes},
			Methods: map[string]contracts.MethodFunction{
				"query": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					view, err := current()
					if err != nil {
						return nil, err
					}
					return live(ctx, func() (delta.JsonValue, error) { return view.query(ctx, objectOf(args)) })
				},
				"read": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					view, err := current()
					if err != nil {
						return nil, err
					}
					return live(ctx, func() (delta.JsonValue, error) { return view.read(ctx, objectOf(args)) })
				},
				"write": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					view, err := current()
					if err != nil {
						return nil, err
					}
					_, err = live(ctx, func() (delta.JsonValue, error) { return nil, view.write(ctx, writes, objectOf(args)) })
					return nil, err
				},
			},
		}}, nil
	}), host.ServeOptions{})
}

func serveWorkspace(facet host.FacetHost) error {
	return facet.Serve(contract.Workspace, host.RemoteFactory(func(params *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		who, err := p2runtime.FromChord(principal)
		if err != nil || !p2runtime.CanSend(who, stringOf(params.Value("session"))) {
			return contracts.Implementation{}, p2runtime.Forbidden("participants only")
		}
		backend := self.Get(contract.WorkspaceBackend, delta.JsonObjectOf("conversation", params.Value("conversation")))
		return contracts.Implementation{Variant: "reader", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{
				"changes": self.Derive([]any{backend.Member("changes")}, func(values []any) (delta.JsonValue, error) { return values[0], nil }),
			},
			Methods: map[string]contracts.MethodFunction{
				"query": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return backend.Call("query", args, ctx)
				},
			},
		}}, nil
	}), host.ServeOptions{})
}

func serveTransfer(facet host.FacetHost) error {
	return facet.Serve(contract.WorkspaceTransfer, host.RemoteFactory(func(params *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		if who, err := p2runtime.FromChord(principal); err != nil || who != (p2runtime.ServerPrincipal{}) {
			return contracts.Implementation{}, p2runtime.Forbidden("server only")
		}
		backend := self.Get(contract.WorkspaceBackend, delta.JsonObjectOf("conversation", params.Value("conversation")))
		return contracts.Implementation{Variant: "server", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
			"read": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return backend.Call("read", args, ctx)
			},
			"write": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return backend.Call("write", args, ctx)
			},
		}}}, nil
	}), host.ServeOptions{})
}

// WorkerFacet serves the worker's parts: `WorkspaceWrites`, `WorkspaceBackend`, `Workspace` and `WorkspaceTransfer`.
var WorkerFacet = p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	for _, serve := range []func(host.FacetHost) error{serveWrites, serveBackend, serveWorkspace, serveTransfer} {
		if err := serve(facet); err != nil {
			return err
		}
	}
	return nil
})
