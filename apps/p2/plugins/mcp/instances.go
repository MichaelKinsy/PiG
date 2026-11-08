package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/mcp/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	pimcp "github.com/MichaelKinsy/PiG/mcp"
	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// Ports apps/p2/plugins/mcp/instances.ts: the MCP supervisor, a native of the manager: one client per instance the placement
// needs, started when a lease first needs it and closed when nothing needs it any more (a shared instance stays while its
// server is configured). A lost connection (a crashed server, or an environment's connection whose daemon killed it)
// reconnects with backoff, keeping its tools listed; a server's tool-list change refreshes its tools.

// ClientInfo is how the p2 server names itself to MCP servers (`initialize`'s `clientInfo`).
type ClientInfo struct{ Name, Version string }

// Backoff is the reconnect waits: the first, the cap of their doubling, and how long a connection lasts to start over at the
// first.
type Backoff struct{ First, Max, Stable time.Duration }

// Reconnect is the manager's backoff: 1 s doubling to 60 s, from 1 s again after a connection that was up for a minute.
var Reconnect = Backoff{First: time.Second, Max: time.Minute, Stable: time.Minute}

// Instance is one running (or failed) server process or connection, as the manager lists it.
type Instance struct {
	ID     string
	Server string
	// Source is "owner" or "project".
	Source   string
	Location *p2runtime.Location
	RunsOn   RunOn
	// OAuth: an HTTP server that signs in with OAuth (no `Authorization` header configured).
	OAuth bool
	// Status is "starting", "connected", "sign-in" or "failed".
	Status string
	Error  string
	// Tools are kept while it reconnects.
	Tools []pimcp.Tool
}

// Instances are the running instances; subscribers hear of every change of what List returns.
type Instances struct {
	client  ClientInfo
	backoff Backoff
	// seed is the tools last listed per instance ID (a reload's previous supervisor): listed until the server answers again.
	seed map[string][]pimcp.Tool
	envs *EnvConnections
	auth AuthProviders

	mu        sync.Mutex
	running   map[string]*connection
	listeners map[int]func()
	next      int
	closed    bool
	closing   sync.WaitGroup
}

// NewInstances is a supervisor that opens environments' connections through connect and gets the auth provider of an
// OAuth HTTP server from auth.
func NewInstances(client ClientInfo, backoff Backoff, seed []Instance, connect ConnectEnv, auth AuthProviders) *Instances {
	tools := map[string][]pimcp.Tool{}
	for _, instance := range seed {
		tools[instance.ID] = instance.Tools
	}
	return &Instances{
		client: client, backoff: backoff, seed: tools, envs: NewEnvConnections(connect), auth: auth,
		running: map[string]*connection{}, listeners: map[int]func(){},
	}
}

// Subscribe has listener called after every change of List; the result stops it.
func (i *Instances) Subscribe(listener func()) (stop func()) {
	i.mu.Lock()
	defer i.mu.Unlock()
	id := i.next
	i.next++
	i.listeners[id] = listener
	return func() {
		i.mu.Lock()
		delete(i.listeners, id)
		i.mu.Unlock()
	}
}

// Reconcile starts the instances placement's leases use and closes the others, except shared ones still configured.
func (i *Instances) Reconcile(placement Placement) {
	needed := map[string]bool{}
	for _, lease := range placement.Leases {
		for _, use := range lease.Servers {
			needed[use.ID] = true
		}
	}
	i.mu.Lock()
	if i.closed {
		i.mu.Unlock()
		return
	}
	changed := false
	for id := range placement.Kept {
		if _, ok := i.running[id]; ok {
			needed[id] = true
		}
	}
	for id, running := range i.running {
		if !needed[id] {
			delete(i.running, id)
			i.closing.Add(1)
			go func() {
				defer i.closing.Done()
				running.close()
			}()
			changed = true
		}
	}
	var started []*connection
	for id := range needed {
		if _, ok := i.running[id]; ok {
			continue
		}
		// Every leased ID comes from the same placement's instances.
		spec := placement.Instances[id]
		created := newConnection(spec, TransportsFor(spec.Connect, i.envs, i.auth), i.client, i.backoff, i.seed[id], i.notify)
		i.running[id] = created
		started = append(started, created)
		changed = true
	}
	i.mu.Unlock()
	for _, created := range started {
		created.start()
	}
	if changed {
		i.notify()
	}
}

// List is the instances as the manager shows them, by server name, then ID.
func (i *Instances) List() []Instance {
	i.mu.Lock()
	connections := make([]*connection, 0, len(i.running))
	for _, running := range i.running {
		connections = append(connections, running)
	}
	i.mu.Unlock()
	list := make([]Instance, len(connections))
	for index, running := range connections {
		list[index] = running.view()
	}
	slices.SortFunc(list, func(a, b Instance) int {
		if a.Server == b.Server {
			return strings.Compare(a.ID, b.ID)
		}
		return strings.Compare(a.Server, b.Server)
	})
	return list
}

// Call calls tool on instance id; it fails while it is not connected.
func (i *Instances) Call(ctx context.Context, id, tool string, args any) (*pimcp.CallToolResult, error) {
	i.mu.Lock()
	running := i.running[id]
	i.mu.Unlock()
	if running == nil {
		return nil, errors.New("This MCP server is not running")
	}
	return running.call(ctx, tool, args)
}

// Restart connects instance id again now, its backoff reset (a connect under way is closed first).
func (i *Instances) Restart(id string) error {
	i.mu.Lock()
	running := i.running[id]
	i.mu.Unlock()
	if running == nil {
		return data.NewPluginError("not_found", "No MCP server instance "+id)
	}
	running.restart()
	return nil
}

// ReconnectServer connects every HTTP instance of owner server server again now (its credentials changed).
func (i *Instances) ReconnectServer(server string) {
	i.mu.Lock()
	var matching []*connection
	for _, running := range i.running {
		if running.isHTTP(server) {
			matching = append(matching, running)
		}
	}
	i.mu.Unlock()
	for _, running := range matching {
		running.restart()
	}
}

// Close closes every instance (stdio servers get MCP's shutdown: stdin closed, then killed) and so every connection.
func (i *Instances) Close() {
	i.mu.Lock()
	i.closed = true
	connections := make([]*connection, 0, len(i.running))
	for _, running := range i.running {
		connections = append(connections, running)
	}
	i.running = map[string]*connection{}
	i.mu.Unlock()
	var group sync.WaitGroup
	for _, running := range connections {
		group.Add(1)
		go func() {
			defer group.Done()
			running.close()
		}()
	}
	group.Wait()
	i.closing.Wait()
}

func (i *Instances) notify() {
	i.mu.Lock()
	listeners := make([]func(), 0, len(i.listeners))
	for _, listener := range i.listeners {
		listeners = append(listeners, listener)
	}
	i.mu.Unlock()
	for _, listener := range listeners {
		listener()
	}
}

// stderrTailChars is how much of a stdio server's stderr the owner's error shows.
const stderrTailChars = 300

type stateKind int

// Where one connection is: not trying (before its start, unplaced, failed for good, waiting for a sign-in), opening its
// transport (an environment's connection may start first), connecting, connected, waiting to reconnect, or closed.
const (
	stateIdle stateKind = iota
	stateOpening
	stateConnecting
	stateConnected
	stateWaiting
	stateClosed
)

// connState is one state of a connection; identity tells a state from a later one.
type connState struct {
	kind   stateKind
	client *pimcp.Client
	since  time.Time
	timer  *time.Timer
	// cancel ends the open and the connect under way.
	cancel context.CancelFunc
	// ctx is the context cancel ends (the open's and the connect's).
	ctx context.Context
}

// failure is why the last attempt failed: the message for everybody, the stderr tail for the owner only.
type failure struct{ message, stderr string }

// signInNeeded is the status message of an instance waiting for a sign-in.
var signInNeeded = &failure{message: "This server needs a sign-in"}

// connection is one instance: its connection state, and its status, failure and tools as listed.
type connection struct {
	spec       InstanceSpec
	transports Transports
	info       ClientInfo
	backoff    Backoff
	changed    func()
	root       context.Context
	rootCancel context.CancelFunc
	routines   sync.WaitGroup

	mu      sync.Mutex
	state   *connState
	status  string
	failure *failure
	tools   []pimcp.Tool
	delay   time.Duration
}

func newConnection(spec InstanceSpec, transports Transports, info ClientInfo, backoff Backoff, tools []pimcp.Tool, changed func()) *connection {
	//portlint:allow ctxbackground the connection owns this context for its life: close cancels it
	root, cancel := context.WithCancel(context.Background())
	return &connection{
		spec: spec, transports: transports, info: info, backoff: backoff, changed: changed, root: root, rootCancel: cancel,
		state: &connState{kind: stateIdle}, status: "starting", tools: tools, delay: backoff.First,
	}
}

func (c *connection) start() {
	if c.spec.Connect.Kind == ConnectUnplaced {
		c.show("failed", &failure{message: c.spec.Connect.Reason})
		return
	}
	c.spawn(c.connect)
}

// spawn runs fn on a routine close waits for; a closed connection runs nothing.
func (c *connection) spawn(fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.kind == stateClosed {
		return
	}
	c.routines.Add(1)
	go func() {
		defer c.routines.Done()
		fn()
	}()
}

// isHTTP is whether this is an instance of HTTP server server.
func (c *connection) isHTTP(server string) bool {
	return c.spec.Server == server && c.spec.Connect.Kind == ConnectHTTP
}

func (c *connection) view() Instance {
	c.mu.Lock()
	defer c.mu.Unlock()
	instance := Instance{
		ID: c.spec.ID, Server: c.spec.Server, Source: c.spec.Source, Location: c.spec.Location, RunsOn: c.spec.RunsOn,
		OAuth: c.spec.Connect.Kind == ConnectHTTP && c.spec.Connect.OAuth != nil, Status: c.status, Tools: slices.Clone(c.tools),
	}
	if c.failure != nil {
		instance.Error = c.failure.message
		if c.failure.stderr != "" {
			instance.Error = c.failure.message + " (stderr: " + c.failure.stderr + ")"
		}
	}
	return instance
}

// call calls a tool. The error says why it is not connected, never the server's stderr (the model reads it); for a sign-in,
// where the user signs in. Tokens refused for good during a call (revoked, say) make it wait for a sign-in.
func (c *connection) call(ctx context.Context, tool string, args any) (*pimcp.CallToolResult, error) {
	server := c.spec.Server
	needed := func() error {
		return fmt.Errorf(`MCP server "%s" needs a sign-in: ask the user to sign in from the MCP panel in p2's sidebar (or at %s on p2)`, server, contract.SignInLink(server))
	}
	c.mu.Lock()
	if c.status == "sign-in" {
		c.mu.Unlock()
		return nil, needed()
	}
	state := c.state
	if state.kind != stateConnected {
		why := ""
		if c.failure != nil {
			why = ": " + c.failure.message
		}
		c.mu.Unlock()
		return nil, fmt.Errorf(`MCP server "%s" is not connected%s`, server, why)
	}
	c.mu.Unlock()
	options := pimcp.RequestOptions{}
	if c.spec.TimeoutMs != nil {
		options.TimeoutMs = int(*c.spec.TimeoutMs)
	}
	result, err := state.client.CallTool(ctx, tool, args, options)
	if err == nil {
		return result, nil
	}
	// A restart meanwhile (a sign-in that just finished) decides on its own.
	c.mu.Lock()
	same := c.state == state
	c.mu.Unlock()
	if !c.needsSignIn(err) || !same {
		return nil, err
	}
	c.stopAndClose()
	c.show("sign-in", signInNeeded)
	return nil, needed()
}

func (c *connection) restart() {
	c.mu.Lock()
	if c.spec.Connect.Kind == ConnectUnplaced || c.state.kind == stateClosed {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	c.stopAndClose()
	c.mu.Lock()
	c.delay = c.backoff.First
	c.mu.Unlock()
	if opening := c.begin(); opening != nil {
		c.spawn(func() { c.open(opening) })
	}
}

// close closes the client, then lets go of what its transports held (an environment's connection, maybe its last user).
func (c *connection) close() {
	c.mu.Lock()
	old := c.state
	c.state = &connState{kind: stateClosed}
	c.mu.Unlock()
	closing := endState(old)
	c.rootCancel()
	if closing != nil {
		_ = closing.Close()
	}
	c.routines.Wait()
	c.transports.Release()
}

// endState ends what old holds (a connect under way, a timer) and gives the client to close, if any.
func endState(old *connState) *pimcp.Client {
	if old.cancel != nil {
		old.cancel()
	}
	if old.timer != nil {
		old.timer.Stop()
	}
	if old.kind == stateConnecting || old.kind == stateConnected {
		return old.client
	}
	return nil
}

// stopAndClose ends what the state holds and goes idle, closing the client.
func (c *connection) stopAndClose() {
	c.mu.Lock()
	old := c.state
	if old.kind == stateClosed {
		c.mu.Unlock()
		return
	}
	c.state = &connState{kind: stateIdle}
	c.mu.Unlock()
	if client := endState(old); client != nil {
		_ = client.Close()
	}
}

// cancelCurrent ends the context of the state's open and connect (the attempt is over).
func (c *connection) cancelCurrent() {
	c.mu.Lock()
	cancel := c.state.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *connection) isState(state *connState) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state == state
}

func (c *connection) connect() { c.open(c.begin()) }

// begin is the synchronous start of a connect (Pi's `connect` runs to its first await in its caller's step, so a restart
// answers with the instance already starting): the opening state, shown. nil: the connection is closed.
func (c *connection) begin() *connState {
	ctx, cancel := context.WithCancel(c.root)
	opening := &connState{kind: stateOpening, cancel: cancel, ctx: ctx}
	c.mu.Lock()
	if c.state.kind == stateClosed {
		c.mu.Unlock()
		cancel()
		return nil
	}
	c.state = opening
	c.status = "starting"
	c.mu.Unlock()
	c.changed()
	return opening
}

// open opens the transports of an opening state and initializes the client on them.
func (c *connection) open(opening *connState) {
	if opening == nil {
		return
	}
	ctx := opening.ctx
	opened, err := c.transports.Open(ctx)
	if err != nil {
		if c.isState(opening) {
			c.failed(err, nil)
		}
		return
	}
	// Closed or restarted meanwhile: the transport never started, so there is nothing to close.
	if c.isState(opening) {
		c.initialize(ctx, opening, opened)
	}
}

func (c *connection) initialize(ctx context.Context, opening *connState, opened Opened) {
	options := pimcp.ClientOptions{Implementation: pimcp.Implementation{Name: c.info.Name, Version: c.info.Version}}
	if c.spec.TimeoutMs != nil {
		options.RequestTimeoutMs = int(*c.spec.TimeoutMs)
	}
	if len(opened.Roots) > 0 {
		options.Roots = make([]pimcp.Root, len(opened.Roots))
		for i, uri := range opened.Roots {
			options.Roots[i] = pimcp.Root{URI: uri}
		}
	}
	client := pimcp.NewClient(options)
	state := &connState{kind: stateConnecting, client: client, cancel: opening.cancel}
	c.mu.Lock()
	if c.state != opening {
		c.mu.Unlock()
		return
	}
	c.state = state
	c.mu.Unlock()
	// Only a connection that was up is lost; a failed connect closes its client too.
	client.OnClose(func() {
		c.mu.Lock()
		lost := c.state.kind == stateConnected && c.state.client == client
		c.mu.Unlock()
		if lost {
			c.lost(opened.Transport)
		}
	})
	client.OnNotification("notifications/tools/list_changed", func(json.RawMessage) {
		c.spawn(func() { c.refresh(client) })
	})
	_, err := client.Connect(ctx, opened.Transport)
	var tools []pimcp.Tool
	if err == nil {
		tools, err = client.ListTools(ctx, pimcp.RequestOptions{})
	}
	if err != nil {
		_ = client.Close()
		if c.isState(state) {
			c.failed(err, opened.Transport)
		}
		return
	}
	c.mu.Lock()
	// Closed or restarted meanwhile: that already closed this client.
	if c.state != state {
		c.mu.Unlock()
		return
	}
	c.state = &connState{kind: stateConnected, client: client, since: time.Now(), cancel: state.cancel}
	c.tools = toolsOf(tools)
	// The status changes with the state, in one step: a restart between them would be shown over.
	c.status, c.failure = "connected", nil
	c.mu.Unlock()
	c.changed()
}

// failed: a connect failed: wait for a sign-in (an OAuth server without usable tokens), stay failed (a daemon too old), or try
// again after the backoff (a server refusing its configured headers too).
func (c *connection) failed(err error, transport pimcp.Transport) {
	c.cancelCurrent()
	if c.needsSignIn(err) {
		c.mu.Lock()
		c.state = &connState{kind: stateIdle}
		c.status, c.failure = "sign-in", signInNeeded
		c.mu.Unlock()
		c.changed()
		return
	}
	var tooOld *DaemonTooOldError
	if errors.As(err, &tooOld) {
		c.mu.Lock()
		c.state = &connState{kind: stateIdle}
		c.status, c.failure = "failed", &failure{message: tooOld.Error()}
		c.mu.Unlock()
		c.changed()
		return
	}
	c.show("failed", failureOf(err, transport))
	c.wait()
}

// needsSignIn is whether err means the user has to sign in: an OAuth server refusing the request with the tokens it has.
func (c *connection) needsSignIn(err error) bool {
	if c.spec.Connect.Kind != ConnectHTTP || c.spec.Connect.OAuth == nil {
		return false
	}
	var required *pimcp.McpAuthRequiredError
	var authorization *oauth.McpOAuthAuthorizationRequiredError
	return errors.As(err, &required) || errors.As(err, &authorization)
}

// lost: the connection dropped (a stdio server exited, say): reconnect after the backoff, tools still listed.
func (c *connection) lost(transport pimcp.Transport) {
	c.mu.Lock()
	if c.state.kind == stateConnected && time.Since(c.state.since) >= c.backoff.Stable {
		c.delay = c.backoff.First
	}
	c.mu.Unlock()
	c.cancelCurrent()
	c.show("starting", failureOf(errors.New("the connection was lost"), transport))
	c.wait()
}

func (c *connection) wait() {
	c.mu.Lock()
	if c.state.kind == stateClosed {
		c.mu.Unlock()
		return
	}
	delay := c.delay
	waiting := &connState{kind: stateWaiting}
	waiting.timer = time.AfterFunc(delay, func() {
		if c.isState(waiting) {
			c.spawn(c.connect)
		}
	})
	c.state = waiting
	c.delay = min(delay*2, c.backoff.Max)
	c.mu.Unlock()
}

func (c *connection) refresh(client *pimcp.Client) {
	tools, err := client.ListTools(c.root, pimcp.RequestOptions{})
	if err != nil {
		if c.root.Err() == nil {
			log.Printf("p2 mcp: refreshing the tools of %q failed: %v", c.spec.Server, err)
		}
		return
	}
	c.mu.Lock()
	if c.state.kind != stateConnected || c.state.client != client {
		c.mu.Unlock()
		return
	}
	c.tools = toolsOf(tools)
	c.mu.Unlock()
	c.changed()
}

func (c *connection) show(status string, why *failure) {
	c.mu.Lock()
	c.status = status
	c.failure = why
	c.mu.Unlock()
	c.changed()
}

// toolsOf is the tools as instances list them: plain JSON, without the fields p2 does not use.
func toolsOf(tools []pimcp.Tool) []pimcp.Tool {
	out := make([]pimcp.Tool, len(tools))
	for i, tool := range tools {
		tool.Execution = nil
		tool.Meta = nil
		out[i] = tool
	}
	return out
}

// failureOf is err's message, and the end of a stdio server's stderr (on the p2 host or an environment).
func failureOf(err error, transport pimcp.Transport) *failure {
	var stderr string
	switch typed := transport.(type) {
	case *pimcp.StdioTransport:
		stderr = typed.Stderr()
	case *pimcp.PipeTransport:
		stderr = typed.Stderr()
	}
	stderr = lastUnits(strings.TrimSpace(stderr), stderrTailChars)
	return &failure{message: err.Error(), stderr: stderr}
}

// lastUnits is JavaScript's `text.slice(-count)`: the last count UTF-16 code units.
func lastUnits(text string, count int) string {
	units := utf16.Encode([]rune(text))
	if len(units) > count {
		units = units[len(units)-count:]
	}
	return string(utf16.Decode(units))
}
