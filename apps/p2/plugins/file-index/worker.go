package fileindex

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	workspacecontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/workspace/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// Ports apps/p2/plugins/file-index/worker.ts: `FileIndexCache` (one conversation's index, shared by its viewers, built from
// the environment's own listing or the workspace's git/walk index, rebuilt when the workspace changes) and `FileIndex`, the
// participants' view of it.

// rebuildAfter is how often a rebuild happens at most while files keep changing: completion may lag a burst of writes by
// this much.
const rebuildAfter = time.Second

// linger keeps the index while typing pauses; switching back to a conversation soon reuses it.
var linger = 60 * time.Second

var outOfScopePattern = regexp.MustCompile(`^(?:/|~|\.\.(?:/|$))`)

// OutOfScope is whether a query's prefix leaves the session directory: completion is cwd-relative.
func OutOfScope(query string) bool { return outOfScopePattern.MatchString(query) }

func objectOf(value any) *delta.JsonObject {
	object, _ := value.(*delta.JsonObject)
	return object
}

func stringOf(value any) string {
	text, _ := value.(string)
	return text
}

// cached keeps the first success of a native getter: the registry value behind it may be read only inside a section of
// the host, so the first call is made in one (a handler's synchronous part) and later ones, off the loop, take the kept
// value.
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

// index belongs to a location version: after a move neither the cached index nor a build in flight is used.
type index struct {
	version   int
	changes   delta.JsonValue
	items     []FileItem
	truncated bool
	at        time.Time
}

type building struct {
	version int
	done    chan struct{}
	index   *index
	err     error
}

type cache struct {
	conversation durable.ConversationId
	harness      func() (*p2runtime.HarnessParts, error)
	worker       func() (*p2runtime.WorkerParts, error)
	backend      *host.Reference

	mu sync.Mutex
	// unsupported is the environment found unable to list (no daemon `listFiles`): the workspace's index while it is in
	// use. A move brings other environments, which are tried again.
	unsupported durableenv.ExecutionEnv
	index       *index
	building    *building
}

// listed is the environment's own listing, with the workspace's confinement (workspace/worker.ts: the conversation's
// location, canonical root, p2's private data directory for local sessions). nil falls back to the workspace's index,
// which also reports a private or unavailable working directory.
func (c *cache) listed(ctx context.Context) *Listing {
	var result *Listing
	workerParts, err := c.worker()
	if err != nil {
		return nil
	}
	// A failed listing (a lost connection, an abort) is retried by the workspace's index, which reports it.
	_ = workerParts.Liveness.Call(func() error {
		harnessParts, err := c.harness()
		if err != nil {
			return err
		}
		session, err := harnessParts.Durable()
		if err != nil {
			return err
		}
		handle, err := session.Conversation(ctx, c.conversation)
		if err != nil || handle == nil {
			return err
		}
		environments, err := harnessParts.Environments()
		if err != nil {
			return err
		}
		_, env, err := p2runtime.ConversationEnvironment(ctx, environments, handle.Id(), session)
		if err != nil {
			return err
		}
		c.mu.Lock()
		skip := c.unsupported == env
		c.mu.Unlock()
		if skip {
			return nil
		}
		root, err := env.CanonicalPath(ctx, env.Cwd())
		if err != nil {
			return nil
		}
		// Decided from the environment, not the location, which lags behind it during a move.
		var denied []string
		if p2runtime.IsLocalEnvironment(env) {
			if private, err := env.CanonicalPath(ctx, workerParts.Init.DataDir); err == nil {
				denied = []string{private}
			} else {
				denied = []string{workerParts.Init.DataDir}
			}
		}
		for _, directory := range denied {
			if inside(directory, root) {
				return nil
			}
		}
		listing, err := ListIndex(ctx, env, root, denied)
		if err != nil {
			return err
		}
		if listing == nil {
			c.mu.Lock()
			c.unsupported = env
			c.mu.Unlock()
		}
		result = listing
		return nil
	})
	return result
}

// keep keeps built unless a move made it older meanwhile (it still answers the request it was built for).
func (c *cache) keep(built *index) *index {
	if workerParts, err := c.worker(); err == nil && built.version == workerParts.Location.Version() {
		c.mu.Lock()
		c.index = built
		c.mu.Unlock()
	}
	return built
}

func (c *cache) build(ctx context.Context, version int, changes delta.JsonValue) (*index, error) {
	if own := c.listed(ctx); own != nil {
		return c.keep(&index{version: version, changes: changes, items: own.Items, truncated: own.Truncated, at: time.Now()}), nil
	}
	// Tracked and untracked files without ignored ones (git), else a bounded walk; directories derive from the file paths
	// (workspace/environment.ts `files`).
	result, err := c.backend.Call("query", delta.JsonObjectOf("kind", "files"), ctx)
	if err != nil {
		return nil, err
	}
	files := objectOf(result)
	if files == nil || files.Value("kind") != "files" {
		return nil, errors.New("Unexpected workspace result")
	}
	var paths []string
	for _, each := range files.Value("paths").([]any) {
		paths = append(paths, stringOf(each))
	}
	truncated, _ := files.Value("truncated").(bool)
	return c.keep(&index{version: version, changes: changes, items: WithDirectories(paths), truncated: truncated, at: time.Now()}), nil
}

// current is the index of the location's current version: the cached one while the workspace did not change (or not
// for a second), else a rebuild that requests for the version share.
func (c *cache) current(ctx context.Context, version int, changes delta.JsonValue) (*index, error) {
	c.mu.Lock()
	if current := c.index; current != nil && current.version == version && (current.changes == changes || time.Since(current.at) < rebuildAfter) {
		c.mu.Unlock()
		return current, nil
	}
	if pending := c.building; pending != nil && pending.version == version {
		c.mu.Unlock()
		select {
		case <-pending.done:
			return pending.index, pending.err
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
	mine := &building{version: version, done: make(chan struct{})}
	c.building = mine
	c.mu.Unlock()
	mine.index, mine.err = c.build(ctx, version, changes)
	c.mu.Lock()
	// A build for this version is the only one: requests for it share it.
	if c.building == mine {
		c.building = nil
	}
	c.mu.Unlock()
	close(mine.done)
	return mine.index, mine.err
}

func itemsJSON(items []FileItem) []any {
	list := make([]any, len(items))
	for at, item := range items {
		list[at] = item.json()
	}
	return list
}

func serveCache(facet host.FacetHost) error {
	return facet.Serve(p2runtime.FileIndexCache, host.LocalFactory(func(params *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		conversation, _ := params.Value("conversation").(float64)
		// The workspace's environment, confinement, private-path exclusion and watch (its `changes` counter).
		backend := self.Get(workspacecontract.WorkspaceBackend, delta.JsonObjectOf("conversation", conversation))
		c := &cache{conversation: durable.ConversationId(conversation), harness: cached(p2runtime.HarnessOf(self)), worker: cached(p2runtime.WorkerOf(self)), backend: backend}
		// What a handler reads in its synchronous part: the natives, the location's version and the workspace's changes.
		state := func() (version int, changes delta.JsonValue, err error) {
			workerParts, err := c.worker()
			if err != nil {
				return 0, nil, err
			}
			if _, err := c.harness(); err != nil {
				return 0, nil, err
			}
			changes, _ = backend.Member("changes").Value()
			return workerParts.Location.Version(), changes, nil
		}
		return contracts.Implementation{Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{
				"changes": self.Derive([]any{backend.Member("changes")}, func(values []any) (delta.JsonValue, error) { return values[0], nil }),
			},
			Methods: map[string]contracts.MethodFunction{
				"complete": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					in := objectOf(args)
					query := stringOf(in.Value("query"))
					if OutOfScope(query) {
						return delta.JsonObjectOf("items", []any{}, "truncated", false), nil
					}
					limit := 20
					if given, ok := in.Value("limit").(float64); ok {
						limit = int(given)
					}
					kind := KindAny
					if given, ok := in.Value("kind").(string); ok {
						kind = Kind(given)
					}
					version, changes, err := state()
					if err != nil {
						return nil, err
					}
					chordctx.Suspend(ctx)
					built, err := c.current(ctx, version, changes)
					if err != nil {
						return nil, err
					}
					return delta.JsonObjectOf("items", itemsJSON(RankFiles(built.items, query, limit, kind)), "truncated", built.truncated), nil
				},
				"list": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					relative := strings.TrimRight(stringOf(objectOf(args).Value("path")), "/")
					if relative != "" {
						if _, valid := workspacecontract.WorkspacePath(relative); !valid {
							return nil, errors.New("Invalid path")
						}
					}
					result, err := backend.Call("query", delta.JsonObjectOf("kind", "list", "path", relative), ctx)
					if err != nil {
						return nil, err
					}
					listing := objectOf(result)
					if listing == nil || listing.Value("kind") != "list" {
						return nil, errors.New("Unexpected workspace result")
					}
					entries, _ := listing.Value("entries").([]any)
					listed := make([]any, len(entries))
					for at, each := range entries {
						entry := objectOf(each)
						listed[at] = delta.JsonObjectOf("name", entry.Value("name"), "dir", entry.Value("kind") == "directory")
					}
					return delta.JsonObjectOf("entries", listed, "truncated", listing.Value("truncated")), nil
				},
			},
		}}, nil
	}), host.ServeOptions{Linger: &linger})
}

func serveIndex(facet host.FacetHost) error {
	return facet.Serve(p2runtime.FileIndex, host.RemoteFactory(func(params *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		// Participants only, as the workspace (canSend): view-only guests get no file names.
		who, err := p2runtime.FromChord(principal)
		if err != nil || !p2runtime.CanSend(who, stringOf(params.Value("session"))) {
			return contracts.Implementation{}, p2runtime.Forbidden("participants only")
		}
		cache := self.Get(p2runtime.FileIndexCache, delta.JsonObjectOf("conversation", params.Value("conversation")))
		return contracts.Implementation{Variant: "reader", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{
				"changes": self.Derive([]any{cache.Member("changes")}, func(values []any) (delta.JsonValue, error) { return values[0], nil }),
			},
			Methods: map[string]contracts.MethodFunction{
				"complete": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return cache.Call("complete", args, ctx)
				},
				"list": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return cache.Call("list", args, ctx)
				},
			},
		}}, nil
	}), host.ServeOptions{})
}

// WorkerFacet serves `FileIndexCache` and `FileIndex`.
var WorkerFacet = p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	if err := serveCache(facet); err != nil {
		return err
	}
	return serveIndex(facet)
})
