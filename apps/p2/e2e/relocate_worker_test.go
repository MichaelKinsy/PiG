// Ports test/p2/relocate-worker.test.ts (Pi main c23b9ec1): in-place relocation inside one worker host, without processes:
// the real Harness moves (`HarnessParts.Relocate`: switch the environments, commit the agent cwds, commit the location)
// and the plugins following the location react. A stub "remote" provider supplies controllable environments for other
// machines; the subagents plugin's placement places conversations with their own location (seeded into its registry).

package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/models"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/powershell"
	powershellcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/powershell/contract"
	subagentscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	workspacecontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/workspace/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	chordhost "github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/env/node"
	"github.com/MichaelKinsy/PiG/durable/harness"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/dev-tools"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/file-index"
)

// remoteMachine is a machine of the stub provider: the system its connection reports, and a gate its environments'
// `CanonicalPath` waits at (closed: open), with failures to drop the connection for first.
type remoteMachine struct {
	system string
	gate   chan struct{}
	// waiting gets a value each time a `CanonicalPath` waits at the closed gate.
	waiting chan struct{}

	mu       sync.Mutex
	failures int
}

func newMachine(system string) *remoteMachine {
	gate := make(chan struct{})
	close(gate)
	return &remoteMachine{system: system, gate: gate, waiting: make(chan struct{}, 16)}
}

// blockedMachine is a machine whose environments wait until open is called.
func blockedMachine(system string) (machine *remoteMachine, open func()) {
	machine = newMachine(system)
	machine.gate = make(chan struct{})
	var once sync.Once
	return machine, func() { once.Do(func() { close(machine.gate) }) }
}

func (m *remoteMachine) dropNext(count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failures = count
}

// testEnv is a "remote" environment that actually runs here (a NodeExecutionEnv), with a controllable `CanonicalPath` and
// the system its connection reports (as `RemoteExecutionEnv.connection.info()` does).
type testEnv struct {
	*node.NodeExecutionEnv
	machine *remoteMachine
}

var _ powershell.SystemReporter = (*testEnv)(nil)

func newTestEnv(cwd string, machine *remoteMachine) *testEnv {
	env := &testEnv{NodeExecutionEnv: node.NewNodeExecutionEnv(node.NodeExecutionEnvOptions{Cwd: cwd}), machine: machine}
	env.Self = env
	return env
}

func (e *testEnv) SystemInfo(context.Context) (string, error) { return e.machine.system, nil }

func (e *testEnv) CanonicalPath(ctx context.Context, path string) (string, error) {
	select {
	case <-e.machine.gate:
	default:
		select {
		case e.machine.waiting <- struct{}{}:
		default:
		}
		select {
		case <-e.machine.gate:
		case <-ctx.Done():
			return "", context.Cause(ctx)
		}
	}
	e.machine.mu.Lock()
	drop := e.machine.failures > 0
	if drop {
		e.machine.failures--
	}
	e.machine.mu.Unlock()
	if drop {
		return "", errors.New("connection lost")
	}
	return e.NodeExecutionEnv.CanonicalPath(ctx, path)
}

// machineAt is the stub provider's connection to a machine: environments at its directories, and the closing recorded.
type machineAt struct {
	machine *remoteMachine
	close   func()
}

func (m machineAt) At(cwd string) durableenv.ExecutionEnv { return newTestEnv(cwd, m.machine) }
func (m machineAt) Close()                                { m.close() }

// names is a list that a test and the worker add to and read from.
type names struct {
	mu   sync.Mutex
	list []string
}

func (n *names) add(name string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.list = append(n.list, name)
}

func (n *names) all() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.list)
}

// checkout is the p2 app directory (the workers' `app`).
func checkout(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// tempRoot is a new directory, resolved through links (a command's `pwd` prints the real path).
func tempRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// dirWith makes directory name in parent, with empty files.
func dirWith(t *testing.T, parent, name string, files ...string) string {
	t.Helper()
	path := directory(t, parent, name)
	for _, file := range files {
		writeFileAt(t, filepath.Join(path, file), "")
	}
	return path
}

// workerHost is a worker host at a location with the Harness, workspace, file index, dev-tools, PowerShell and the
// subagents plugin's placement; machines by environment name. It is the worker of hosts/worker.go without its process.
type workerHost struct {
	t        *testing.T
	root     string
	host     chordhost.Host
	location *p2runtime.SessionLocation
	parts    *p2runtime.HarnessParts
	// opened and closed are the machines opened and closed, by environment name.
	opened, closed *names
}

var testModel = durable.ModelRef{Provider: "faux", ModelId: "faux-1"}

func location(cwd string, env string) p2runtime.Location {
	if env == "" {
		return p2runtime.Location{Cwd: cwd}
	}
	return p2runtime.Location{Cwd: cwd, Env: &env}
}

func envValue(env *string) any {
	if env == nil {
		return nil
	}
	return *env
}

// newWorkerHost also loads the facets in extra, after the plugins.
func newWorkerHost(t *testing.T, start p2runtime.Location, machines map[string]*remoteMachine, extra ...chordhost.HostFacet) *workerHost {
	t.Helper()
	p2test.NoLeakedGoroutines(t)
	w := &workerHost{t: t, root: tempRoot(t), opened: &names{}, closed: &names{}}
	dataDir := filepath.Join(w.root, "data")
	if err := os.MkdirAll(filepath.Join(dataDir, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The stub provider: a machine by environment name.
	models := chordhost.DefineFacet(func(_ chordhost.FacetHost, self chordhost.FacetSelf) error {
		return p2runtime.HarnessConfig.Contribute(self, p2runtime.Entry("default", p2runtime.HarnessConfigEntry{
			Models:   func() durable.Models { return models.CreateP2Models(true, ai.CreateModelsOptions{}) },
			Settings: func() *harness.HarnessSettings { return &harness.HarnessSettings{} },
		}))
	})
	remote := chordhost.DefineFacet(func(_ chordhost.FacetHost, self chordhost.FacetSelf) error {
		return p2runtime.HarnessEnvironments.Contribute(self, p2runtime.Entry("remote", p2runtime.HarnessEnvironmentEntry{
			Open: func(_ context.Context, name string) (p2runtime.Machine, error) {
				machine := machines[name]
				if machine == nil {
					return nil, fmt.Errorf("Environment %q is not configured", name)
				}
				w.opened.add(name)
				return machineAt{machine: machine, close: func() { w.closed.add(name) }}, nil
			},
			Describe: func(_ context.Context, name string) (string, bool, error) {
				return "the test machine " + name, true, nil
			},
			Sandbox: func(string) bool { return false },
		}))
	})
	sessionDir, err := p2runtime.SessionDirectory(dataDir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	store, err := p2runtime.OpenHostStore(p2test.Background, sessionDir, false)
	if err != nil {
		t.Fatal(err)
	}
	address := p2runtime.SessionKind.Address(delta.JsonObjectOf("id", "s1"))
	worker, err := p2runtime.NewHost(chordhost.HostOptions{
		Address:   &address,
		Principal: p2runtime.ToChord(p2runtime.Host),
		Store:     store,
		OnError:   func(failure chordhost.HostError) { fmt.Fprintf(os.Stderr, "worker host: %v\n", failure) },
	})
	if err != nil {
		t.Fatal(err)
	}
	w.host = worker
	liveness := p2runtime.NewLiveness()
	init := p2runtime.WorkerInit{
		App: checkout(t), DataDir: dataDir, Fake: true, Generation: 1,
		Values: delta.JsonObjectOf("session", delta.JsonObjectOf(
			"id", "s1", "cwd", start.Cwd, "env", envValue(start.Env),
			"model", delta.JsonObjectOf("provider", testModel.Provider, "modelId", testModel.ModelId), "thinkingLevel", "off")),
	}
	session, err := init.Session()
	if err != nil {
		t.Fatal(err)
	}
	w.location = p2runtime.NewSessionLocation(start)
	root := func(fn func(ctx chordhost.Ctx) (any, error), ctx context.Context) (any, error) {
		return worker.Root(fn, ctx, chordhost.RootOptions{})
	}
	selection, err := hosts.SelectPlugins([]string{"workspace", "file-index", "dev-tools", "powershell", "subagents"})
	if err != nil {
		t.Fatal(err)
	}
	facets := []chordhost.HostFacet{
		{Name: "@p2/core", Facet: p2runtime.WorkerCore(init, session, liveness, w.location, store, root)},
		{Name: "models", Facet: models},
		{Name: "remote", Facet: remote},
	}
	for _, plugin := range selection {
		if plugin.Worker != nil {
			facets = append(facets, chordhost.HostFacet{Name: plugin.Name, Facet: *plugin.Worker})
		}
	}
	facets = append(facets, extra...)
	report, err := worker.Load(facets, p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Failed) > 0 {
		t.Fatal(report.Failed[0].Error)
	}
	harnessHold, err := worker.Hold(p2test.Background, p2runtime.HarnessHost, delta.NewJsonObject(0), chordhost.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		harnessHold.Release()
		_ = worker.Close(p2test.Background)
		_ = store.Close(p2test.Background)
		liveness.Stop()
	})
	if err := <-whenLive(harnessHold); err != nil {
		t.Fatal(err)
	}
	source, err := worker.Registry(p2test.Background, p2runtime.HarnessNative, chordhost.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	w.parts, _ = source.Value().(*p2runtime.HarnessParts)
	if w.parts == nil {
		t.Fatal("the Harness is not open")
	}
	return w
}

// whenLive reports, once, when ref is live (a transient failure keeps waiting: the reference retries), or why it ended or
// failed broken.
func whenLive(ref *chordhost.Reference) <-chan error {
	ready := make(chan error, 1)
	go func() { ready <- p2runtime.WhenLive(p2test.Background, ref) }()
	return ready
}

// relocate moves the session in place (the Harness's move) and reports its failure.
func (w *workerHost) relocate(next p2runtime.Location) error {
	w.t.Helper()
	_, err := w.host.Root(func(ctx chordhost.Ctx) (any, error) { return nil, w.parts.Relocate(ctx, next, nil) }, p2test.Background, chordhost.RootOptions{})
	return err
}

func (w *workerHost) mustRelocate(next p2runtime.Location) {
	w.t.Helper()
	if err := w.relocate(next); err != nil {
		w.t.Fatal(err)
	}
}

func (w *workerHost) durable() harness.Harness {
	w.t.Helper()
	opened, err := w.parts.Durable()
	if err != nil {
		w.t.Fatal(err)
	}
	return opened
}

// subagent is a subagent conversation working at own (its own location), else following the session.
func (w *workerHost) subagent(name string, own *p2runtime.Location, tools []string) durable.ConversationId {
	w.t.Helper()
	opened := w.durable()
	conversation, err := opened.CreateConversation(p2test.Background, harness.ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
	if err != nil {
		w.t.Fatal(err)
	}
	id := conversation.Id()
	cwd := w.location.Value().Cwd
	if own != nil {
		cwd = own.Cwd
	}
	if _, err := opened.Commit(p2test.Background, func(tx durable.Tx) (any, error) {
		if err := harness.Configure(tx, id, harness.AgentChange{Model: harness.SetTo(testModel), Cwd: harness.SetTo(cwd)}); err != nil {
			return nil, err
		}
		if tools != nil {
			draft, err := durable.TxDoc[harness.AgentState](tx, harness.AgentDoc, id)
			if err != nil {
				return nil, err
			}
			listed := make([]any, len(tools))
			for i, tool := range tools {
				listed[i] = tool
			}
			if err := draft.Set("tools", listed); err != nil {
				return nil, err
			}
		}
		registry, err := durable.TxDoc[subagentscontract.SubagentState](tx, subagentscontract.SubagentsDoc, durable.ROOT_CONVERSATION_ID)
		if err != nil {
			return nil, err
		}
		entry := delta.JsonObjectOf(
			"conversationId", float64(id), "model", "faux/faux-1", "thinkingLevel", "off", "tools", []any{}, "reported", []any{},
		)
		if own != nil {
			entry.Set("location", delta.JsonObjectOf("cwd", own.Cwd, "env", envValue(own.Env)))
		}
		return nil, registry.Object("agents").Set(name, entry)
	}); err != nil {
		w.t.Fatal(err)
	}
	return id
}

// environment is where conversation id works and its environment, as Durable gets it.
func (w *workerHost) environment(id durable.ConversationId) (p2runtime.Location, durableenv.ExecutionEnv) {
	w.t.Helper()
	environments, err := w.parts.Environments()
	if err != nil {
		w.t.Fatal(err)
	}
	where, executionEnv, err := p2runtime.ConversationEnvironment(p2test.Background, environments, id, nil)
	if err != nil {
		w.t.Fatal(err)
	}
	return where, executionEnv
}

// agentState is what Durable stores as conversation id's agent choices.
func (w *workerHost) agentState(id durable.ConversationId) *harness.AgentState {
	w.t.Helper()
	state, err := durable.Snapshot(p2test.Background, w.durable(), harness.AgentDoc, id)
	if err != nil {
		w.t.Fatal(err)
	}
	return state
}

// agentCwd is the agent cwd Durable stores for conversation id.
func (w *workerHost) agentCwd(id durable.ConversationId) string {
	w.t.Helper()
	state := w.agentState(id)
	if state == nil || state.Cwd == nil {
		return ""
	}
	return *state.Cwd
}

// run runs command in executionEnv and returns its output.
func run(t *testing.T, ctx context.Context, executionEnv durableenv.ExecutionEnv, command string) string {
	t.Helper()
	var output strings.Builder
	result, err := executionEnv.Exec(ctx, command, &durableenv.ShellExecOptions{
		OnOutput: func(_ context.Context, text string, _ durableenv.ShellOutputInfo) { output.WriteString(text) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("%q exited %d: %s", command, result.ExitCode, output.String())
	}
	return strings.TrimSpace(output.String())
}

func (w *workerHost) get(contract *contracts.Contract, params *delta.JsonObject) *chordhost.Reference {
	w.t.Helper()
	ref, err := w.host.Get(p2test.Background, contract, params, chordhost.GetOptions{})
	if err != nil {
		w.t.Fatal(err)
	}
	return ref
}

// listNames lists a conversation's workspace root through its backend held as ref.
func listNames(ref *chordhost.Reference) ([]string, error) {
	answer, err := ref.Call("query", delta.JsonObjectOf("kind", "list", "path", ""), p2test.Background)
	if err != nil {
		return nil, err
	}
	if object(answer).Value("kind") != "list" {
		return nil, fmt.Errorf("answer = %v", answer)
	}
	var found []string
	for _, entry := range list(object(answer).Value("entries")) {
		name, _ := object(entry).Value("name").(string)
		found = append(found, name)
	}
	return found, nil
}

// devToolsOffered is whether the dev-tools extension is switched on now.
func devToolsOffered(t *testing.T, switches interface{ Current() (any, error) }) bool {
	t.Helper()
	value, err := switches.Current()
	if err != nil {
		t.Fatal(err)
	}
	table, ok := value.(*p2runtime.KeyedTable[*bool])
	if !ok {
		t.Fatalf("extension switches = %T", value)
	}
	on, _ := table.Get("p2-dev-tools")
	return on != nil && *on
}

func TestDevToolsAndPowerShellDetectAgainAfterARealMove(t *testing.T) {
	t.Setenv("P2_DEV", "1")
	windows := runtime.GOOS == "windows"
	w := newWorkerHost(t, p2runtime.Location{Cwd: checkout(t)}, map[string]*remoteMachine{"win": newMachine("windows")})
	elsewhere := directory(t, w.root, "elsewhere")
	ref := w.get(powershellcontract.PowerShell, delta.NewJsonObject(0))
	defer ref.Release()
	switches, err := w.host.Registry(p2test.Background, p2runtime.ExtensionSwitches.Registry, chordhost.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	switchedTo := func(on bool) {
		t.Helper()
		if _, err := p2test.UntilNative[any](switches, func(any) bool { return devToolsOffered(t, switches) == on }, 0); err != nil {
			t.Fatalf("the dev-tools extension did not become %v: %v", on, err)
		}
	}
	offeredTo := func(want bool) {
		t.Helper()
		if _, err := p2test.Until(ref.Member("offered"), func(v delta.JsonValue) bool { return object(v).Value("1") == want }, 0); err != nil {
			t.Fatalf("PowerShell was not offered to the main agent = %v: %v", want, err)
		}
	}
	switchedTo(true)
	offeredTo(windows)
	w.mustRelocate(location(elsewhere, ""))
	switchedTo(false)
	w.mustRelocate(location(elsewhere, "win"))
	offeredTo(true)
	if devToolsOffered(t, switches) {
		t.Fatal("dev tools are offered on a remote environment")
	}
	w.mustRelocate(location(checkout(t), ""))
	offeredTo(windows)
	switchedTo(true)
}

// The location lags behind the environments during a move: the private data directory is denied by what the
// environment is (here a "remote" location whose environment runs on this machine), never by the location's name.
func TestThePrivateDataDirectoryIsDeniedByTheEnvironmentNotTheLocation(t *testing.T) {
	parent := tempRoot(t)
	w := newWorkerHost(t, p2runtime.Location{Cwd: parent}, map[string]*remoteMachine{"box": newMachine("linux")})
	// The session works in the directory holding p2's data directory, on a location named as another machine.
	w.mustRelocate(location(w.root, "box"))
	backend := w.get(workspacecontract.WorkspaceBackend, delta.JsonObjectOf("conversation", float64(1)))
	defer backend.Release()
	if _, err := backend.Call("query", delta.JsonObjectOf("kind", "list", "path", "data"), p2test.Background); err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("listing the data directory: %v, want a refusal naming it private", err)
	}
	index := w.get(p2runtime.FileIndexCache, delta.JsonObjectOf("conversation", float64(1)))
	defer index.Release()
	for _, path := range completePaths(t, index, delta.JsonObjectOf("query", "", "limit", float64(100))) {
		if path == "data" || strings.HasPrefix(path, "data/") {
			t.Fatalf("the file index lists the private data directory: %q", path)
		}
	}
}

func TestAWorkspaceFollowsAMoveMadeWhileItActivatesAndRetriesAFailedReopen(t *testing.T) {
	blocked, open := blockedMachine("linux")
	flaky := newMachine("linux")
	machines := map[string]*remoteMachine{"blocked": blocked, "other": newMachine("linux"), "flaky": flaky}
	parent := tempRoot(t)
	first := dirWith(t, parent, "first", "first.txt")
	second := dirWith(t, parent, "second", "second.txt")
	third := dirWith(t, parent, "third", "third.txt")
	w := newWorkerHost(t, location(first, "blocked"), machines)
	// The workspace activates on the blocked machine; the session moves meanwhile. Held, not awaited: its activation waits
	// for the blocked machine (it has reached it once the machine says an environment waits there).
	backend, err := w.host.Hold(p2test.Background, workspacecontract.WorkspaceBackend, delta.JsonObjectOf("conversation", float64(1)), chordhost.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Release()
	ready := whenLive(backend)
	<-blocked.waiting
	w.mustRelocate(location(second, "other"))
	open()
	if err := <-ready; err != nil {
		t.Fatal(err)
	}
	if got, err := listNames(backend); err != nil || !slices.Equal(got, []string{"second.txt"}) {
		t.Fatalf("listing after the move = %v, %v; want [second.txt]", got, err)
	}
	// One failed reopen (a dropped connection) is not kept: the next use opens again.
	flaky.dropNext(1)
	w.mustRelocate(location(third, "flaky"))
	if err := p2test.Poll(func() (bool, error) {
		got, err := listNames(backend)
		return err == nil && slices.Contains(got, "third.txt"), nil
	}, 0, "the reopened workspace"); err != nil {
		t.Fatal(err)
	}
}

// The owner's case: the main conversation moves to the laptop; a subagent spawned with its own directory on the phone
// stays there and can still run tools; a subagent spawned without one follows the session.
func TestTheSessionMovesToAnotherMachineASubagentWithItsOwnLocationStaysOnItsOwn(t *testing.T) {
	parent := tempRoot(t)
	start := dirWith(t, parent, "start", "start.txt")
	own := dirWith(t, parent, "own", "own.txt")
	remote := dirWith(t, parent, "remote", "remote.txt")
	w := newWorkerHost(t, location(start, ""), map[string]*remoteMachine{"laptop": newMachine("linux")})
	ownLocation := location(own, "")
	mine := w.subagent("mine", &ownLocation, nil)
	follower := w.subagent("follower", nil, nil)

	w.mustRelocate(location(remote, "laptop"))
	mainWhere, mainEnv := w.environment(durable.ROOT_CONVERSATION_ID)
	keptWhere, keptEnv := w.environment(mine)
	followedWhere, followedEnv := w.environment(follower)
	if !p2runtime.SameLocation(mainWhere, location(remote, "laptop")) {
		t.Fatalf("main works at %+v", mainWhere)
	}
	if _, ok := mainEnv.(*testEnv); !ok {
		t.Fatalf("the main conversation runs on %T, want the laptop", mainEnv)
	}
	if !p2runtime.SameLocation(followedWhere, location(remote, "laptop")) {
		t.Fatalf("the follower works at %+v", followedWhere)
	}
	if followedEnv != mainEnv {
		t.Fatal("a follower does not share the session's environment")
	}
	if !p2runtime.SameLocation(keptWhere, location(own, "")) {
		t.Fatalf("the subagent works at %+v", keptWhere)
	}
	if _, ok := keptEnv.(*testEnv); ok {
		t.Fatal("the subagent left this machine")
	}
	if got := run(t, p2test.Background, keptEnv, "pwd"); got != own {
		t.Fatalf("the subagent's pwd = %q, want %q", got, own)
	}
	if got := run(t, p2test.Background, followedEnv, "pwd"); got != remote {
		t.Fatalf("the follower's pwd = %q, want %q", got, remote)
	}
	if got := w.agentCwd(follower); got != remote {
		t.Fatalf("the follower's agent cwd = %q, want %q", got, remote)
	}
	if got := w.agentCwd(mine); got != own {
		t.Fatalf("the subagent's agent cwd = %q, want %q", got, own)
	}
	if got := w.opened.all(); !slices.Equal(got, []string{"laptop"}) {
		t.Fatalf("machines opened = %v, want [laptop]", got)
	}

	// The workspace and the file index of each conversation show where it works.
	namesOf := func(conversation durable.ConversationId) []string {
		t.Helper()
		backend := w.get(workspacecontract.WorkspaceBackend, delta.JsonObjectOf("conversation", float64(conversation)))
		defer backend.Release()
		got, err := listNames(backend)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	for conversation, want := range map[durable.ConversationId]string{mine: "own.txt", follower: "remote.txt", 1: "remote.txt"} {
		if got := namesOf(conversation); !slices.Equal(got, []string{want}) {
			t.Fatalf("conversation %d lists %v, want [%s]", conversation, got, want)
		}
	}
	index := w.get(p2runtime.FileIndexCache, delta.JsonObjectOf("conversation", float64(mine)))
	if got := completePaths(t, index, delta.JsonObjectOf("query", "", "limit", float64(10))); !slices.Equal(got, []string{"own.txt"}) {
		t.Fatalf("the subagent's file index = %v, want [own.txt]", got)
	}
	index.Release()

	// Back on this machine: nothing works on the laptop any more, so its connection closes.
	w.mustRelocate(location(start, ""))
	if got := w.closed.all(); !slices.Equal(got, []string{"laptop"}) {
		t.Fatalf("machines closed = %v, want [laptop]", got)
	}
	_, followedEnv = w.environment(follower)
	if got := run(t, p2test.Background, followedEnv, "pwd"); got != start {
		t.Fatalf("the follower's pwd = %q, want %q", got, start)
	}
	_, keptEnv = w.environment(mine)
	if got := run(t, p2test.Background, keptEnv, "pwd"); got != own {
		t.Fatalf("the subagent's pwd = %q, want %q", got, own)
	}
}

func TestConversationsOnDifferentMachinesRunConcurrentlyOneConnectionPerMachine(t *testing.T) {
	parent := tempRoot(t)
	session := dirWith(t, parent, "session")
	phone := dirWith(t, parent, "phone")
	worktree := dirWith(t, parent, "worktree")
	next := dirWith(t, parent, "next")
	w := newWorkerHost(t, location(session, "laptop"), map[string]*remoteMachine{"laptop": newMachine("linux")})
	ownPhone, ownWorktree := location(phone, ""), location(worktree, "laptop")
	local := w.subagent("local", &ownPhone, nil)
	remoteAgent := w.subagent("remote", &ownWorktree, nil)

	// The three commands run at the same time when each waits for the others to have started (Pi's: each sleeps 0.6 s and the
	// whole takes under 1.5 s): run one after the other, the first never sees the others and fails at the bound.
	barrier := directory(t, parent, "barrier")
	ids := []durable.ConversationId{1, local, remoteAgent}
	outputs := make([]string, len(ids))
	var group sync.WaitGroup
	for i, id := range ids {
		group.Add(1)
		go func() {
			defer group.Done()
			_, executionEnv := w.environment(id)
			command := fmt.Sprintf(`touch %q/%d; while [ "$(ls %q | wc -l)" -lt %d ]; do sleep 0.01; done; pwd`, barrier, i, barrier, len(ids))
			bounded, cancel := context.WithTimeout(p2test.Background, 20*time.Second)
			defer cancel()
			var output strings.Builder
			result, err := executionEnv.Exec(bounded, command, &durableenv.ShellExecOptions{
				OnOutput: func(_ context.Context, text string, _ durableenv.ShellOutputInfo) { output.WriteString(text) },
			})
			if err != nil || result.ExitCode != 0 {
				t.Errorf("conversation %d: %v, exit %d, %q (the commands did not run at the same time)", id, err, result.ExitCode, output.String())
				return
			}
			outputs[i] = strings.TrimSpace(output.String())
		}()
	}
	group.Wait()
	if want := []string{session, phone, worktree}; !slices.Equal(outputs, want) {
		t.Fatalf("outputs = %v, want %v", outputs, want)
	}
	if got := w.opened.all(); !slices.Equal(got, []string{"laptop"}) {
		t.Fatalf("machines opened = %v: both laptop conversations share one connection", got)
	}
	_, mainEnv := w.environment(1)
	_, otherEnv := w.environment(remoteAgent)
	if mainEnv == otherEnv {
		t.Fatal("one environment per directory: the conversations share one")
	}

	// The session leaves the laptop; the subagent working there keeps its connection.
	w.mustRelocate(location(next, ""))
	if got := w.closed.all(); len(got) != 0 {
		t.Fatalf("machines closed = %v, want none", got)
	}
	_, otherEnv = w.environment(remoteAgent)
	if got := run(t, p2test.Background, otherEnv, "pwd"); got != worktree {
		t.Fatalf("the subagent's pwd = %q, want %q", got, worktree)
	}
	_, mainEnv = w.environment(1)
	if got := run(t, p2test.Background, mainEnv, "pwd"); got != next {
		t.Fatalf("the main conversation's pwd = %q, want %q", got, next)
	}
}

// PowerShell follows each agent's machine (Pi Mobile's offerPowerShell, per agent): the main agent and each subagent are
// offered it where they work on Windows and have the shell tool, independently, again after every move.
func TestPowerShellIsOfferedToEachAgentWorkingOnWindowsIndependently(t *testing.T) {
	parent := tempRoot(t)
	start := dirWith(t, parent, "start")
	windowsDir := dirWith(t, parent, "windows")
	w := newWorkerHost(t, location(start, ""), map[string]*remoteMachine{"win": newMachine("windows")})
	onWindows := location(windowsDir, "win")
	there := w.subagent("there", &onWindows, nil)
	noShell := w.subagent("no-shell", &onWindows, []string{"read"})
	follower := w.subagent("follower", nil, nil)
	local := runtime.GOOS == "windows"
	ref := w.get(powershellcontract.PowerShell, delta.NewJsonObject(0))
	defer ref.Release()
	id := func(conversation durable.ConversationId) string { return fmt.Sprint(int64(conversation)) }
	expect := func(main bool) {
		t.Helper()
		offered, err := p2test.Until(ref.Member("offered"), func(v delta.JsonValue) bool {
			return object(v).Value("1") == main && object(v).Value(id(follower)) == main && object(v).Value(id(there)) == true
		}, 0)
		if err != nil {
			t.Fatalf("PowerShell was not offered as the main agent = %v: %v", main, err)
		}
		if object(offered).Value(id(noShell)) != false {
			t.Fatal("PowerShell is offered to an agent without the shell tool")
		}
		for conversation, want := range map[durable.ConversationId]bool{1: main, follower: main, there: true, noShell: false} {
			if got := powershell.OffersPowerShell(w.agentState(conversation)); got != want {
				t.Fatalf("conversation %d is offered PowerShell = %v, want %v", conversation, got, want)
			}
		}
	}
	expect(local)
	// The session moves to Windows: the main agent and its follower get PowerShell; the others keep theirs.
	w.mustRelocate(onWindows)
	expect(true)
	w.mustRelocate(location(start, ""))
	expect(local)
}
