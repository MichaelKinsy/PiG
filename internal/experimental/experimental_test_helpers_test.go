package experimental

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/fsnotify/fsnotify"

	"github.com/MichaelKinsy/PiG/agent/harness/env"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/client"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:153-166. The spy covers only the static Client.connect constructor; instance Connect in discovery and activation remains real. Call from a non-parallel test and join its runtime before cleanup.
func rejectNextExperimentalClientConnect(t *testing.T) {
	t.Helper()
	previous := connectRuntimeClient
	var rejected atomic.Bool
	connectRuntimeClient = func(ctx context.Context, options client.ClientOptions) (*client.Client, error) {
		if rejected.CompareAndSwap(false, true) {
			return nil, &client.ServerError{Code: "version", Message: "stale server"}
		}
		return previous(ctx, options)
	}
	t.Cleanup(func() {
		connectRuntimeClient = previous
		if !rejected.Load() {
			t.Error("static client constructor interception was not exercised")
		}
	})
}

// isolateExperimentalTest resolves compiler/runtime/cache paths before replacing every effective agent/home root. The returned directory is the isolated agent directory.
func isolateExperimentalTest(t *testing.T) string {
	t.Helper()
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.CommandContext(t.Context(), goBinary, "env", "-json", "GOROOT", "GOPATH", "GOMODCACHE", "GOCACHE").Output()
	if err != nil {
		t.Fatalf("resolve Go caches: %v", err)
	}
	var cache map[string]string
	if err := json.Unmarshal(output, &cache); err != nil {
		t.Fatal(err)
	}
	goRoot := cache["GOROOT"]
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	nodeCommand := exec.CommandContext(t.Context(), node, "-p", "process.execPath")
	nodeCommand.Env = append(os.Environ(), "NODE_OPTIONS=")
	output, err = nodeCommand.Output()
	if err != nil {
		t.Fatalf("resolve Node executable: %v", err)
	}
	node = strings.TrimSpace(string(output))
	path := os.Getenv("PATH")
	sdk := os.Getenv("PIG_SDK_GO_ROOT")
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "PI_") || strings.HasPrefix(name, "PIG_") {
			t.Setenv(name, "")
			if err := os.Unsetenv(name); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Upstream remote-runtime fixtures use /tmp explicitly. Named testing directories exceed sun_path once backend UUID filenames are appended.
	var root string
	if runtime.GOOS == "windows" {
		root = t.TempDir()
	} else {
		root, err = os.MkdirTemp("/tmp", "pe")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.RemoveAll(root); err != nil {
				t.Error(err)
			}
		})
	}
	agentDir := filepath.Join(root, "agent")
	for _, directory := range []string{agentDir, filepath.Join(root, "pig"), filepath.Join(root, "config"), filepath.Join(root, "cache"), filepath.Join(root, "data"), filepath.Join(root, "state"), filepath.Join(root, "run"), filepath.Join(root, "tmp"), filepath.Join(root, "appdata"), filepath.Join(root, "localappdata")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// D2: shared-directory opt-in exercises the original PI_* fixture namespace inside this fresh home, never the worker's directories.
	for key, value := range map[string]string{"HOME": root, "USERPROFILE": root, "PIG_HOME": filepath.Join(root, "pig"), "PI_HOME": filepath.Join(root, "pi"), "PIG_CODING_AGENT_DIR": agentDir, "PI_CODING_AGENT_DIR": agentDir, "PIG_USE_PI_DIRS": "1", "XDG_CONFIG_HOME": filepath.Join(root, "config"), "XDG_CACHE_HOME": filepath.Join(root, "cache"), "XDG_DATA_HOME": filepath.Join(root, "data"), "XDG_STATE_HOME": filepath.Join(root, "state"), "XDG_RUNTIME_DIR": filepath.Join(root, "run"), "TMPDIR": filepath.Join(root, "tmp"), "TEMP": filepath.Join(root, "tmp"), "TMP": filepath.Join(root, "tmp"), "APPDATA": filepath.Join(root, "appdata"), "LOCALAPPDATA": filepath.Join(root, "localappdata"), "NODE_OPTIONS": "", "GOROOT": goRoot, "GOENV": "off", "GOTOOLCHAIN": "local", "PATH": filepath.Join(goRoot, "bin") + string(os.PathListSeparator) + filepath.Dir(node) + string(os.PathListSeparator) + path} {
		t.Setenv(key, value)
	}
	// Pinned coding-agent/vitest.config.ts:12 runs these tests offline unless a case explicitly opts into network access.
	t.Setenv("PI_OFFLINE", "1")
	for key, value := range cache {
		t.Setenv(key, value)
	}
	if sdk != "" {
		t.Setenv("PIG_SDK_GO_ROOT", sdk)
	}
	return agentDir
}

// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:39-45.
func setupExperimentalRemoteTest(t *testing.T) string {
	t.Helper()
	agentDir := isolateExperimentalTest(t)
	t.Setenv(experimentalTestEntryEnv, "1")
	configureExperimentalWorkerModel(t, agentDir)
	createExperimentalSessions(t, filepath.Join(agentDir, "experimental", "sessions"), []string{"demo-1", "demo-2"})
	return agentDir
}

// upstream: packages/coding-agent/test/experimental-session-support.ts:33-38.
func configureExperimentalWorkerModel(t *testing.T, agentDir string) {
	t.Helper()
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "auth.json"), []byte(`{"anthropic":{"type":"api_key","key":"test-key"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

// upstream: packages/coding-agent/test/experimental-session-support.ts:12-31. This helper uses the actual durable JSONL repository, not the stable SessionManager format.
func createExperimentalSessions(t *testing.T, sessionsRoot string, ids []string, cwdOverride ...string) []session.SessionMetadata {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if len(cwdOverride) > 0 {
		cwd = cwdOverride[0]
	}
	fileSystem := env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: cwd})
	repo := session.NewJsonlSessionRepo(session.JsonlSessionRepoOptions{FileSystem: fileSystem, SessionsRoot: sessionsRoot})
	ctx := context.Background()
	defer func() {
		if err := repo.Close(ctx); err != nil {
			t.Error(err)
		}
		fileSystem.Cleanup(ctx)
	}()
	metadata := make([]session.SessionMetadata, 0, len(ids))
	for _, id := range ids {
		opened, err := repo.Create(ctx, session.SessionCreateOptions{ID: id, Cwd: cwd})
		if err != nil {
			t.Fatal(err)
		}
		metadata = append(metadata, opened.Metadata())
		if err := opened.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return metadata
}

type experimentalSessionState struct {
	Branch      []session.Entry
	Model       *session.ModelRef
	ActiveTools []string
}

// upstream: packages/coding-agent/test/experimental-session-support.ts:40-74.
func readExperimentalSessionState(t *testing.T, sessionsRoot, sessionId string) experimentalSessionState {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	fileSystem := env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: cwd})
	repo := session.NewJsonlSessionRepo(session.JsonlSessionRepoOptions{FileSystem: fileSystem, SessionsRoot: sessionsRoot})
	var opened session.Session
	defer func() {
		if opened != nil {
			if err := opened.Close(ctx); err != nil {
				t.Error(err)
			}
		}
		if err := repo.Close(ctx); err != nil {
			t.Error(err)
		}
		fileSystem.Cleanup(ctx)
	}()
	metadata, err := repo.List(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	matches := slices.DeleteFunc(metadata, func(value session.SessionMetadata) bool { return value.ID != sessionId })
	if len(matches) != 1 {
		t.Fatalf("Expected one Session %s, found %d", sessionId, len(matches))
	}
	opened, err = repo.Open(ctx, matches[0])
	if err != nil {
		t.Fatal(err)
	}
	main, err := opened.Branch(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if main == nil {
		t.Fatal("Expected Session main Branch")
	}
	var branch []session.Entry
	var configuration *session.StoredValue[session.LaneConfiguration]
	var branchErr, configurationErr error
	var reads sync.WaitGroup
	reads.Go(func() {
		branch, branchErr = main.FindEntries(ctx, &session.BranchScan{Order: session.OrderOldestFirst})
	})
	reads.Go(func() { configuration, configurationErr = session.GetValue(ctx, opened, session.LaneConfig("main")) })
	reads.Wait()
	if branchErr != nil {
		t.Fatal(branchErr)
	}
	if configurationErr != nil {
		t.Fatal(configurationErr)
	}
	result := experimentalSessionState{Branch: branch, ActiveTools: []string{}}
	if configuration != nil {
		result.Model = new(configuration.Value.Model)
		result.ActiveTools = append([]string{}, configuration.Value.ActiveToolNames...)
	}
	return result
}

// The upstream fixture owns identity sets and closes every client before servers and directories. One per-test registry preserves those phases even when resources are created concurrently or interleaved.
var experimentalTrackedResources sync.Map

type experimentalTestResources struct {
	mu          sync.Mutex
	closeOnce   sync.Once
	clients     []*client.Client
	servers     []*RunningServer
	children    []*InternalProcess
	fauxWorker  bool
	directories []string
}

func experimentalResourcesFor(t *testing.T) *experimentalTestResources {
	t.Helper()
	created := &experimentalTestResources{}
	value, loaded := experimentalTrackedResources.LoadOrStore(t, created)
	resources := value.(*experimentalTestResources)
	if !loaded {
		t.Cleanup(func() {
			resources.close(t)
			experimentalTrackedResources.Delete(t)
		})
	}
	return resources
}

func (resources *experimentalTestResources) close(t *testing.T) {
	t.Helper()
	resources.closeOnce.Do(func() {
		resources.mu.Lock()
		clients, servers, directories := slices.Clone(resources.clients), slices.Clone(resources.servers), slices.Clone(resources.directories)
		resources.mu.Unlock()
		clientClosers := make([]func() error, len(clients))
		for index, peer := range clients {
			clientClosers[index] = func() error { return disposeServerClient(context.Background(), peer) }
		}
		serverClosers := make([]func() error, len(servers))
		for index, server := range servers {
			serverClosers[index] = server.Close
		}
		directoryClosers := make([]func() error, len(directories))
		for index, directory := range directories {
			directoryClosers[index] = func() error { return os.RemoveAll(directory) }
		}
		joinWorkers := func() error {
			resources.mu.Lock()
			children := slices.Clone(resources.children)
			resources.mu.Unlock()
			for _, child := range children {
				<-child.Done()
			}
			return nil
		}
		for _, phase := range [][]func() error{clientClosers, serverClosers, {joinWorkers}, directoryClosers} {
			for _, err := range settleClientCleanup(phase) {
				if err != nil {
					t.Error(err)
				}
			}
		}
	})
}

func trackExperimentalClient(t *testing.T, peer *client.Client) {
	t.Helper()
	resources := experimentalResourcesFor(t)
	resources.mu.Lock()
	defer resources.mu.Unlock()
	if !slices.Contains(resources.clients, peer) {
		resources.clients = append(resources.clients, peer)
	}
}

func trackExperimentalServer(t *testing.T, server *RunningServer) {
	t.Helper()
	resources := experimentalResourcesFor(t)
	resources.mu.Lock()
	defer resources.mu.Unlock()
	if !slices.Contains(resources.servers, server) {
		resources.servers = append(resources.servers, server)
		// Tests enroll a server before attaching clients. Retain the real child exit authority even when a replacement manager adopts its PID.
		spawn := server.workers.spawn
		server.workers.spawn = func(role InternalProcessRole, args []string, options InternalProcessSpawnOptions) (*InternalProcess, error) {
			resources.mu.Lock()
			faux := resources.fauxWorker
			resources.mu.Unlock()
			if faux && role == "session-worker" {
				options.Env = maps.Clone(options.Env)
				if options.Env == nil {
					options.Env = make(map[string]string)
				}
				options.Env[experimentalFauxWorkerEnv] = "1"
			}
			child, err := spawn(role, args, options)
			if child != nil {
				resources.mu.Lock()
				resources.children = append(resources.children, child)
				resources.mu.Unlock()
			}
			return child, err
		}
	}
}

// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:34,47-53. The caller first runs setupExperimentalRemoteTest; this helper uses its isolated roots and real durable sessions.
func makeExperimentalServer(t *testing.T) (string, *RunningServer) {
	t.Helper()
	directory, err := os.MkdirTemp("", "pes-")
	if err != nil {
		t.Fatal(err)
	}
	resources := experimentalResourcesFor(t)
	resources.mu.Lock()
	resources.directories = append(resources.directories, directory)
	resources.mu.Unlock()
	server, err := StartServer(t.Context(), StartServerOptions{Directory: &directory, Provider: new("anthropic"), Model: new("claude-sonnet-4-5")})
	if err != nil {
		t.Fatal(err)
	}
	trackExperimentalServer(t, server)
	return directory, server
}

// connectAndAttachExperimentalClient is the error-returning full upstream attachClient path. A caller can join several complete connect+attach operations concurrently without invoking Fatal on worker goroutines. The workflow context owns temporary connection work; service methods retain Background Context.
func connectAndAttachExperimentalClient(ctx context.Context, server *RunningServer, id string) (peer *client.Client, err error) {
	factory, err := client.CreateUnixTransportFactory(client.UnixTransportOptions{Path: server.SocketPath})
	if err != nil {
		return nil, err
	}
	peer, err = connectRuntimeClient(ctx, client.ClientOptions{ServerId: server.ServerId, TransportFactory: factory})
	if err != nil {
		return nil, err
	}
	connected := peer
	cancelled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = connected.Dispose(); close(cancelled) })
	defer func() {
		if !stop() {
			<-cancelled
		}
		if err == nil && ctx.Err() != nil {
			err = context.Cause(ctx)
		}
		if err != nil {
			if cleanup := disposeServerClient(context.Background(), connected); cleanup != nil {
				err = errors.Join(err, cleanup)
			}
			peer = nil
		}
	}()
	source, err := NewClientServerServiceSource(connected, ClientServiceSourceOptions{})
	if err != nil {
		return nil, err
	}
	defer func() {
		if cleanup := source.Dispose(context.Background()); cleanup != nil {
			err = cleanup
		}
	}()
	scope, err := source.Open(chord.RemoteServiceSourceOpenOptions{Services: []string{services.SessionManagementDefinition.Id()}})
	if err != nil {
		return nil, err
	}
	management, err := chord.UseRemoteClient(scope, services.SessionManagementDefinition)
	if err != nil {
		return nil, err
	}
	if err := scope.Ready(context.Background()); err != nil {
		return nil, err
	}
	if err := management.Attach(context.Background(), id); err != nil {
		return nil, err
	}
	return connected, nil
}

// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:68-76.
func attachExperimentalClient(t *testing.T, server *RunningServer, id string) *client.Client {
	t.Helper()
	peer, err := connectAndAttachExperimentalClient(t.Context(), server, id)
	if err != nil {
		t.Fatal(err)
	}
	trackExperimentalClient(t, peer)
	return peer
}

// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:675-693. Both connection establishment and management attachment run concurrently; this is not sequential setup followed by parallel Attach.
func attachExperimentalClients(t *testing.T, server *RunningServer, ids []string) []*client.Client {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	peers := make([]*client.Client, len(ids))
	var group sync.WaitGroup
	var first sync.Once
	var failure error
	for index, id := range ids {
		group.Go(func() {
			peer, err := connectAndAttachExperimentalClient(ctx, server, id)
			peers[index] = peer
			if err != nil {
				first.Do(func() { failure = err; cancel() })
			}
		})
	}
	group.Wait()
	for _, peer := range peers {
		if peer != nil {
			trackExperimentalClient(t, peer)
		}
	}
	if failure != nil {
		t.Fatal(failure)
	}
	return peers
}

// waitExperimentalWorkerRetired observes removal from THIS manager's PID map, not operating-system process exit. A replaced manager may release an adopted worker while that same process stays alive.
func waitExperimentalWorkerRetired(t *testing.T, server *RunningServer, id string) {
	t.Helper()
	for {
		manager := server.workers
		manager.mu.Lock()
		pid, present := manager.workerPids[id]
		if !present {
			manager.mu.Unlock()
			return
		}
		var terminated <-chan struct{}
		for _, worker := range manager.workerOrder {
			if worker.metadata.ID == id && worker.pid == pid {
				terminated = worker.terminated
				break
			}
		}
		manager.mu.Unlock()
		if terminated == nil {
			t.Fatalf("worker PID %s=%d has no owned record", id, pid)
		}
		select {
		case <-terminated:
		case <-t.Context().Done():
			t.Fatalf("wait for manager to retire %s: %v", id, context.Cause(t.Context()))
		}
	}
}

// waitExperimentalWorkerExit waits on the spawned child's own exit authority. Manager removal follows control-socket disconnection, which can precede process exit and reaping.
func waitExperimentalWorkerExit(t *testing.T, pid int) {
	t.Helper()
	resources := experimentalResourcesFor(t)
	resources.mu.Lock()
	var exited *InternalProcess
	for _, child := range resources.children {
		if child.PID() == pid {
			exited = child
			break
		}
	}
	resources.mu.Unlock()
	if exited == nil {
		t.Fatalf("worker PID %d was not spawned through the tracked server", pid)
	}
	select {
	case <-exited.Done():
	case <-t.Context().Done():
		t.Fatalf("wait for worker %d to exit: %v", pid, context.Cause(t.Context()))
	}
}

type experimentalChanges struct{ changed chan struct{} }

// watchExperimentalChanges owns real subscriptions. Coalesced notifications only wake a fresh predicate evaluation; no state or rendered frame is cached or replayed.
func watchExperimentalChanges(t *testing.T, subscribe ...func(func()) (func(), error)) *experimentalChanges {
	t.Helper()
	changes := &experimentalChanges{changed: make(chan struct{}, 1)}
	notify := func() {
		select {
		case changes.changed <- struct{}{}:
		default:
		}
	}
	for _, open := range subscribe {
		remove, err := open(notify)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(remove)
	}
	return changes
}

func (changes *experimentalChanges) Wait(t *testing.T, predicate func() bool) {
	t.Helper()
	for !predicate() {
		select {
		case <-changes.changed:
		case <-t.Context().Done():
			t.Fatalf("wait for experimental state: %v", context.Cause(t.Context()))
		}
	}
}

// waitExperimentalPathRemoved checks the same lstat predicate as upstream while filesystem notifications own the wait. A rename/replacement is rechecked rather than assumed to remove the path.
func waitExperimentalPathRemoved(t *testing.T, path string) {
	t.Helper()
	if err := awaitExperimentalPathRemoved(t.Context(), path); err != nil {
		t.Fatal(err)
	}
}

func awaitExperimentalPathRemoved(ctx context.Context, path string) (err error) {
	missing := func() (bool, error) {
		_, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, err
	}
	if gone, err := missing(); gone || err != nil {
		return err
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer func() {
		if failure := watcher.Close(); failure != nil {
			err = errors.Join(err, failure)
			return
		}
		for range watcher.Events {
		}
		for range watcher.Errors {
		}
	}()
	if err := watcher.Add(filepath.Dir(path)); err != nil {
		if gone, check := missing(); gone || check != nil {
			return check
		}
		return err
	}
	for {
		if gone, err := missing(); gone || err != nil {
			return err
		}
		select {
		case _, open := <-watcher.Events:
			if !open {
				return fmt.Errorf("filesystem watcher closed before %s was removed", path)
			}
		case failure, open := <-watcher.Errors:
			if !open {
				return fmt.Errorf("filesystem watcher errors closed before %s was removed", path)
			}
			return failure
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
}

func experimentalExamplePluginPath(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate experimental fixture source")
	}
	path := filepath.Join(filepath.Dir(source), "..", "..", ".upstream", "current", "packages", "coding-agent", "examples", "plugins", "pi-example-plugin")
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return path
}
