package host

// Ports packages/chord/src/loader.ts, the Node facet loader `createHost({ loader: nodeLoader })`, and the nodeLoader
// export of packages/chord/src/node.ts.
//
// Pi's nodeLoader imports facet modules into the running Node process. A Go host has no in-process JavaScript, so
// this loader runs them as the isolated-Node loader the owner chose (chord C1(b)): every Load forks one Node child
// (ForkDriver under a HostManager, loader_child.mjs) that imports the modules, installs their facets on a Chord host
// of its own and reports what they serve. Each returned Facet serves the same contracts on the Go host; its methods
// open, call and release the child's objects over the control channel. Fresh copies on every Load (Pi's cache-busting
// import query) are a fresh process, so the copies a reload replaces are not kept: a child stops when the facets it
// backs are no longer referenced, or at Close.
//
// Promise mapping: Load blocks until the child is ready (the import and install settled) and returns its rejection
// as the error. A call blocks until the child answers, the child exits (unavailable) or the call's root is aborted:
// the child's root is aborted, the call returns aborted at once, and a later answer is dropped. Replies match
// requests by id, so concurrent calls complete in any order, as concurrent promises do.
//
// What crosses: contracts whose members are methods (a remote contract with one variant). State, opaque members,
// several variants, accepts adapters and state migrations need the served object itself on the host, so a facet
// that serves them is refused at Load. The facets of one Load see each other (one child host); a facet cannot reach
// contracts the Go host serves.

import (
	"context"
	_ "embed" // loader_child.mjs
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

//go:embed loader_child.mjs
var nodeLoaderChild []byte

// NodeLoaderOptions configure a NodeLoaderImpl.
type NodeLoaderOptions struct {
	// Chord is the Chord package the child imports createHost from: its directory, its entry file or a file URL. Empty
	// finds @earendil-works/chord installed above the first module.
	Chord string
	// ExecArgv are Node flags for the child (default: none).
	ExecArgv []string
	// ReadyTimeout is how long Load waits for the child to import and install the modules (default 30s).
	ReadyTimeout *time.Duration
}

// NodeLoaderImpl is a Node facet loader: its Load is a FacetLoader.
type NodeLoaderImpl struct {
	options NodeLoaderOptions

	mu    sync.Mutex
	hosts map[*nodeFacetHost]struct{}
}

// NewNodeLoader makes a Node facet loader.
// pig divergence (D178): the loader owns isolated Node children, not in-process module copies.
func NewNodeLoader(options NodeLoaderOptions) *NodeLoaderImpl {
	return &NodeLoaderImpl{options: options, hosts: map[*nodeFacetHost]struct{}{}}
}

var defaultNodeLoader = NewNodeLoader(NodeLoaderOptions{})

// NodeLoader is loader.ts nodeLoader: it imports fresh copies of facet modules (default export: a FacetDefinition,
// from DefineFacet's JavaScript twin defineFacet); each facet is named by its URL. It is NewNodeLoader's Load with no
// options.
func NodeLoader(modules []string) ([]Facet, error) { return defaultNodeLoader.Load(modules) }

// Close stops every child this loader started.
func (loader *NodeLoaderImpl) Close() error {
	loader.mu.Lock()
	hosts := make([]*nodeFacetHost, 0, len(loader.hosts))
	for host := range loader.hosts {
		hosts = append(hosts, host)
	}
	loader.mu.Unlock()
	var errs []error
	for _, host := range hosts {
		if err := host.stop(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// nodeLoaderEntry writes loader_child.mjs into a directory only this load can reach and returns its path and the
// function that removes the directory. The child reads its entry once, at start, so the caller removes the directory as
// soon as the start has settled. Nothing is shared between loads or processes: a process that ends or cleans up
// cannot take another's entry away, and no other user can swap the code the child runs.
func nodeLoaderEntry() (path string, remove func(), err error) {
	dir, err := os.MkdirTemp("", "chord-node-loader-")
	if err != nil {
		return "", nil, err
	}
	remove = func() {
		// Best effort: the directory holds only this copy, and a failed removal changes no result.
		_ = os.RemoveAll(dir)
	}
	path = filepath.Join(dir, "loader_child.mjs")
	if err := os.WriteFile(path, nodeLoaderChild, 0o600); err != nil {
		remove()
		return "", nil, err
	}
	return path, remove, nil
}

// Load imports the facet modules in a child process and returns their facets, named by module URL, in module order.
// A module whose default export is not a facet fails the load with a TypeError, as loader.ts does.
// pig divergence (D178): Load runs the facet modules in an isolated Node child, not in this process.
func (loader *NodeLoaderImpl) Load(modules []string) ([]Facet, error) {
	if len(modules) == 0 {
		return []Facet{}, nil
	}
	entry, removeEntry, err := nodeLoaderEntry()
	if err != nil {
		return nil, err
	}
	defer removeEntry()
	names := make([]any, len(modules))
	for index, module := range modules {
		names[index] = module
	}
	host := &nodeFacetHost{loader: loader, pending: map[int]chan nodeReply{}, generation: int(nodeLoadGeneration.Add(1))}
	host.manager = CreateHostManager(HostManagerOptions{
		Driver: ForkDriver(ForkDriverOptions{
			Entry:     entry,
			ExecArgv:  loader.options.ExecArgv,
			Env:       map[string]string{"CHORD_LOADER_CHORD": loader.options.Chord},
			Init:      chordjson.ObjectOf("modules", names),
			HasInit:   true,
			OnMessage: host.tap,
		}),
		ReadyTimeout: loader.options.ReadyTimeout,
	})
	loader.mu.Lock()
	loader.hosts[host] = struct{}{}
	loader.mu.Unlock()
	instance, err := host.manager.Start()
	if err != nil {
		host.mu.Lock()
		failed := host.failed
		host.mu.Unlock()
		_ = host.stop()
		if failed != nil {
			return nil, failed
		}
		return nil, err
	}
	host.instance = instance
	host.mu.Lock()
	infos, failed := host.facets, host.failed
	host.mu.Unlock()
	if failed != nil {
		// The child reported a facet table this side cannot mirror.
		_ = host.stop()
		return nil, failed
	}
	owner := &nodeFacetOwner{host: host}
	// The child stops once nothing refers to the facets it backs: a reload that replaced them and the roots of the
	// code versions that used them are done. The cleanup holds the host, never the owner.
	runtime.AddCleanup(owner, func(host *nodeFacetHost) {
		//portlint:allow golifetime the stop is the cleanup's own work and ends with the child
		go func() { _ = host.stop() }()
	}, host)
	facets := make([]Facet, 0, len(infos))
	for _, info := range infos {
		facets = append(facets, owner.facet(info))
	}
	return facets, nil
}

// nodeServe is one contract a facet serves, as the child reported it.
type nodeServe struct {
	description Description
	linger      time.Duration
	hasLinger   bool
	lifetime    Lifetime
}

// nodeFacetInfo is one facet the child installed.
type nodeFacetInfo struct {
	name   string
	uses   []*Contract
	serves []nodeServe
}

type nodeReply struct {
	value any
	err   error
}

// nodeFacetHost is the parent's side of one child: the requests in flight and what the child reported. It never
// refers to the owner that keeps it alive.
type nodeFacetHost struct {
	loader   *NodeLoaderImpl
	manager  *HostManager
	instance Instance

	mu      sync.Mutex
	pending map[int]chan nodeReply
	next    int
	facets  []nodeFacetInfo
	failed  error
	stopped bool

	refs atomic.Int64
	// generation numbers the load that started this child: an object keeps its resources across a reload unless their
	// Rev changes, and the new code's object lives in the new child.
	generation int
}

var nodeLoadGeneration atomic.Int64

// tap reads the child's application messages ({ chord2loader }): the facet table, a failed load and replies.
func (host *nodeFacetHost) tap(message any) {
	object, ok := message.(*chordjson.Object)
	if !ok {
		return
	}
	inner, ok := object.Value("chord2loader").(*chordjson.Object)
	if !ok {
		return
	}
	switch kind, _ := inner.Value("type").(string); kind {
	case "facets":
		infos, err := parseNodeFacets(inner.Value("facets"))
		host.mu.Lock()
		host.facets = infos
		if err != nil {
			host.failed = err
		}
		host.mu.Unlock()
	case "failed":
		message, _ := inner.Value("message").(string)
		failure := error(errors.New(message))
		if name, _ := inner.Value("name").(string); name == "TypeError" {
			failure = &TypeError{Message: message}
		}
		host.mu.Lock()
		host.failed = failure
		host.mu.Unlock()
	case "reply":
		id, ok := intOf(inner.Value("id"))
		if !ok {
			return
		}
		reply := nodeReply{value: inner.Value("value")}
		if done, _ := inner.Value("ok").(bool); !done {
			reply.err = nodeErrorOf(inner.Value("error"))
		}
		host.mu.Lock()
		waiting := host.pending[id]
		delete(host.pending, id)
		host.mu.Unlock()
		if waiting != nil {
			waiting <- reply
		}
	}
}

func intOf(value any) (int, bool) {
	number, ok := value.(float64)
	//portlint:allow numbers request ids and reference numbers are small integers this package wrote; a malformed child value only fails the match
	return int(number), ok
}

func nodeErrorOf(value any) error {
	object, ok := value.(*chordjson.Object)
	if !ok {
		return errors.New("facet host: malformed error")
	}
	message, _ := object.Value("message").(string)
	if code, _ := object.Value("code").(string); code != "" {
		reason, _ := object.Value("reason").(string)
		for _, known := range chordErrorCodes {
			if string(known) == code {
				return &ChordError{
					Name: "ChordError", Code: known, Reason: reason, Message: message,
					Retryable: known == CodeSuperseded || known == CodeUpgradeConflict,
				}
			}
		}
	}
	if name, _ := object.Value("name").(string); name == "TypeError" {
		return &TypeError{Message: message}
	}
	return errors.New(message)
}

func parseNodeFacets(value any) ([]nodeFacetInfo, error) {
	list, ok := value.([]any)
	if !ok {
		return nil, errors.New("facet host: malformed facet table")
	}
	infos := make([]nodeFacetInfo, 0, len(list))
	for _, entry := range list {
		object, ok := entry.(*chordjson.Object)
		if !ok {
			return nil, errors.New("facet host: malformed facet")
		}
		info := nodeFacetInfo{}
		info.name, _ = object.Value("name").(string)
		uses, _ := object.Value("uses").([]any)
		for _, token := range uses {
			description, err := nodeDescription(token)
			if err != nil {
				return nil, err
			}
			contract, err := contractFromDescription(description)
			if err != nil {
				return nil, err
			}
			info.uses = append(info.uses, contract)
		}
		serves, _ := object.Value("serves").([]any)
		for _, token := range serves {
			serve, err := parseNodeServe(token)
			if err != nil {
				return nil, err
			}
			info.serves = append(info.serves, serve)
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func nodeDescription(value any) (Description, error) {
	text, err := json.Marshal(value)
	if err != nil {
		return Description{}, err
	}
	var description Description
	//perf:ok once per served contract when a facet module loads, not per call or message
	return description, description.UnmarshalJSON(text)
}

func parseNodeServe(value any) (nodeServe, error) {
	object, ok := value.(*chordjson.Object)
	if !ok {
		return nodeServe{}, errors.New("facet host: malformed served contract")
	}
	description, err := nodeDescription(object.Value("description"))
	if err != nil {
		return nodeServe{}, err
	}
	serve := nodeServe{description: description}
	if linger, ok := object.Value("linger").(float64); ok {
		//portlint:allow numbers linger is the milliseconds the facet passed to serve, a JS number
		serve.linger = time.Duration(linger * float64(time.Millisecond))
		serve.hasLinger = true
	}
	if lifetime, ok := object.Value("lifetime").(string); ok {
		serve.lifetime = Lifetime(lifetime)
	}
	return serve, nil
}

// stop shuts the child down (graceful, then the process tree is killed) and forgets it.
func (host *nodeFacetHost) stop() error {
	host.mu.Lock()
	already := host.stopped
	host.stopped = true
	host.mu.Unlock()
	if already {
		return nil
	}
	host.loader.mu.Lock()
	delete(host.loader.hosts, host)
	host.loader.mu.Unlock()
	return host.manager.Stop()
}

// request sends one request to the child and waits for its answer. An aborted ctx aborts the child's call and returns
// aborted at once; the answer then has no one to go to.
func (host *nodeFacetHost) request(ctx context.Context, what string, fields ...any) (any, error) {
	waiting := make(chan nodeReply, 1)
	host.mu.Lock()
	host.next++
	id := host.next
	host.pending[id] = waiting
	host.mu.Unlock()
	drop := func() {
		host.mu.Lock()
		delete(host.pending, id)
		host.mu.Unlock()
	}
	host.send(append([]any{"type", what, "id", id}, fields...)...)
	select {
	case reply := <-waiting:
		return reply.value, reply.err
	case <-host.instance.Exited():
		select {
		case reply := <-waiting:
			return reply.value, reply.err
		default:
		}
		drop()
		return nil, NewChordError(CodeUnavailable, "node facet host exited ("+exitLabel(host.instance.Exit())+")", ChordErrorOptions{})
	case <-ctx.Done():
		drop()
		host.send("type", "cancel", "id", 0, "target", id)
		return nil, NewChordError(CodeAborted, what+" aborted", ChordErrorOptions{Cause: context.Cause(ctx)})
	}
}

// send hands the child a request: an init control message whose value is { chord2loader }, which the child's
// control channel delivers after its start request.
func (host *nodeFacetHost) send(pairs ...any) {
	host.instance.Send(ControlMessage{Type: ControlInit, Value: chordjson.ObjectOf("chord2loader", chordjson.ObjectOf(pairs...))})
}

// nodeFacetOwner keeps its host's child alive: the facets refer to it.
type nodeFacetOwner struct{ host *nodeFacetHost }

func (owner *nodeFacetOwner) facet(info nodeFacetInfo) Facet {
	return Facet{
		Name: info.name,
		FacetDefinition: FacetDefinition{
			Uses: info.uses,
			Setup: func(api FacetApi) error {
				for _, serve := range info.serves {
					contract := nodeContract(serve.description)
					options := ServeOptions{Linger: serve.linger, HasLinger: serve.hasLinger, Lifetime: serve.lifetime}
					if err := api.Serve(contract, owner.factory(contract, serve.description), options); err != nil {
						return err
					}
				}
				return nil
			},
		},
	}
}

// nodeContract is the contract a description names, with the definition Serve validates implementations against.
func nodeContract(description Description) *Contract {
	if description.Scope == ScopeLocal {
		members := Fields[any]{}
		for _, member := range description.Variants[0].Value {
			members = append(members, Field[any]{Name: member.Name, Value: Method(member.Value.Args, member.Value.Result)})
		}
		return Local(description.Name, LocalDef{Version: description.Version, Input: description.Input, Members: members})
	}
	variants := Fields[VariantDef]{}
	for _, variant := range description.Variants {
		methods := Fields[MethodSpec]{}
		for _, member := range variant.Value {
			methods = append(methods, Field[MethodSpec]{Name: member.Name, Value: Method(member.Value.Args, member.Value.Result)})
		}
		variants = append(variants, Field[VariantDef]{Name: variant.Name, Value: VariantDef{Methods: methods}})
	}
	return Remote(description.Name, RemoteDef{Version: description.Version, Input: description.Input, Host: description.Host, Variants: variants})
}

// factory serves one contract: the object holds the child's object for its input (a resource, opened before the
// object is live and released when it is disposed) and every member forwards its call to it.
func (owner *nodeFacetOwner) factory(contract *Contract, description Description) Factory {
	// pig divergence (D178): the principal is not carried to the child, and the facet cannot reach the Go host.
	return func(input any, _ Principal, self Self) (Implementation, error) {
		// The closures refer to the owner, not its host: the child stays up for as long as an implementation can call it.
		ref := owner.host.refs.Add(1)
		_, err := self.Resource("node-facet", ResourceDeclaration[any]{
			Version: owner.host.generation,
			Open: func(options ResourceOpenOptions) (any, error) {
				_, err := owner.host.request(options.Signal, "open", "handle", ref, "contract", contract.Name, "input", input)
				return nil, err
			},
			Close: func(any) error {
				//portlint:allow ctxbackground object.ts close(): a resource release has no caller to cancel it
				_, err := owner.host.request(context.Background(), "release", "handle", ref)
				if IsChordError(err, CodeUnavailable) {
					return nil // the child is gone and took the object with it
				}
				return err
			},
		})
		if err != nil {
			return nil, err
		}
		implementation := chordjson.NewObject(0)
		for _, variant := range description.Variants {
			if contract.Scope == ScopeRemote {
				implementation.Set("variant", variant.Name)
			}
			for _, member := range variant.Value {
				name := member.Name
				// pig divergence (D178): the call runs in the isolated child; only ctx.Signal crosses, and an error keeps its code, reason and message.
				implementation.Set(name, func(args any, ctx Ctx) (any, error) {
					//portlint:allow ctxbackground a method called outside a root has no ctx (Pi's ctx argument is optional)
					signal := context.Background()
					if ctx != nil {
						signal = ctx.Signal()
					}
					return owner.host.request(signal, "call", "handle", ref, "member", name, "args", args)
				})
			}
		}
		return implementation, nil
	}
}
