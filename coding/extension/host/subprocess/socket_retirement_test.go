package subprocess

import (
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// socketFiles lists the socket files in the Host's private socket directory, which also holds the files a runtime writes there (harness-argv.json).
func socketFiles(t *testing.T, h *Host) []string {
	t.Helper()
	if h.sockRuntimeDir == "" {
		t.Fatal("the Host has no socket directory yet")
	}
	entries, err := os.ReadDir(h.sockRuntimeDir)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), socketLeafExtension) {
			names = append(names, entry.Name())
		}
	}
	return names
}

// leftSocketFile leaves a socket file at path that no listener holds, as a generation does that is stopped between its bind and its connection.
func leftSocketFile(t *testing.T, path string) {
	t.Helper()
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	_ = listener.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the socket file was not left behind: %v", err)
	}
}

// retiredGenerationHost is a Host whose extension "busy" runs a generation with a command in flight, in the given subprocess mode, and the channel that reports which providers a stop unregistered.
func retiredGenerationHost(t *testing.T, packed bool) (*Host, *managedExt, chan string) {
	t.Helper()
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test complete") })
	stopped := make(chan string, 64)
	h.SetProviderCallbacks(func(string, extension.ProviderConfig) error { return nil }, func(name string) { stopped <- name })
	return h, runningGeneration(t, h, packed, 1), stopped
}

// runningGeneration registers the generation of "busy" that starts as the host's current one, on a socket path of its own, with a command in flight.
func runningGeneration(t *testing.T, h *Host, packed bool, generation int) *managedExt {
	t.Helper()
	path, err := h.newSocketPath()
	if err != nil {
		t.Fatal(err)
	}
	gen := &managedExt{config: ExtConfig{Name: "busy"}, providerNames: []string{"old-busy"}, sockPath: path}
	if packed {
		gen.packedProcess = &packedProcessState{key: "cell", generation: generation}
		h.packedCellGeneration = map[string]int{"cell": generation + 1}
	}
	h.mu.Lock()
	h.exts["busy"] = gen
	h.mu.Unlock()
	gen.commands.begin()
	return gen
}

// A stop must have unregistered the retired generation's provider, and nothing else.
func requireStopped(t *testing.T, stopped chan string) {
	t.Helper()
	select {
	case got := <-stopped:
		if got != "old-busy" {
			t.Fatalf("stopped generation = %q, want old-busy", got)
		}
	default:
		t.Fatal("the generation was not stopped after its command returned")
	}
}

func eachSubprocessMode(t *testing.T, run func(t *testing.T, packed bool)) {
	t.Helper()
	for _, packed := range []bool{false, true} {
		name := "isolated"
		if packed {
			name = "packed"
		}
		t.Run(name, func(t *testing.T) { run(t, packed) })
	}
}

// A command that awaits `ctx.reload()` keeps its generation running until it returns (retireGeneration), which can be after the next reload staged a successor and began to listen: the stop of the old generation must leave that listener's file where it is. Before each generation had a socket path of its own, they all listened on the path their extension's name mapped to, the stop removed it, and the successor's process failed to connect (ENOENT): the reload that staged it waited for a connection that never came, so the prompt that asked for the reload never answered.
func TestADeferredRetirementLeavesTheSocketOfTheGenerationStagedAfterIt(t *testing.T) {
	eachSubprocessMode(t, func(t *testing.T, packed bool) {
		h, old, stopped := retiredGenerationHost(t, packed)
		// The reload the command awaits commits the replacement; the old generation keeps running until the command returns.
		h.commitStaged([]stagedManagedExt{{name: "busy", me: &managedExt{config: ExtConfig{Name: "busy"}}}}, nil, "reload replaced")
		// The next reload stages the generation after that one and listens before the first command returned, as staging does.
		nextPath, err := h.newSocketPath()
		if err != nil {
			t.Fatal(err)
		}
		listener, address, err := ListenExtension(nextPath, false)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = listener.Close() }()

		old.commands.end()
		h.retirementTasks.Wait()
		requireStopped(t, stopped)
		conn, err := net.Dial("unix", address)
		if err != nil {
			t.Fatalf("the generation staged after the retired one cannot connect to its socket: %v", err)
		}
		_ = conn.Close()
	})
}

// A generation's stop removes the file of its own path, one that its listener did not get to remove because the generation stopped before it connected, and no other. The Host's directory holds a file exactly while a listener holds it or a generation left it: twenty reloads each awaited by a command that outlives it leave nothing behind, an extension a reload removes leaves nothing, and Shutdown removes the directory with whatever a staged generation still listens on.
func TestAStopRemovesTheSocketFileOfItsOwnGenerationOnly(t *testing.T) {
	eachSubprocessMode(t, func(t *testing.T, packed bool) {
		h, old, stopped := retiredGenerationHost(t, packed)
		for round := range 20 {
			// The retiring generation never connected: its file is still there when it stops.
			leftSocketFile(t, old.sockPath)
			ownLeaf := filepath.Base(old.sockPath)
			h.commitStaged([]stagedManagedExt{{name: "busy", me: &managedExt{config: ExtConfig{Name: "busy"}}}}, nil, "reload replaced")
			nextPath, err := h.newSocketPath()
			if err != nil {
				t.Fatal(err)
			}
			listener, _, err := ListenExtension(nextPath, false)
			if err != nil {
				t.Fatalf("round %d: %v", round, err)
			}
			old.commands.end()
			h.retirementTasks.Wait()
			requireStopped(t, stopped)
			if got := socketFiles(t, h); !slices.Equal(got, []string{filepath.Base(nextPath)}) {
				t.Fatalf("round %d: socket directory holds %q after the stop of %q, want only the successor's %q", round, got, ownLeaf, filepath.Base(nextPath))
			}
			if err := listener.Close(); err != nil {
				t.Fatal(err)
			}
			if got := socketFiles(t, h); len(got) != 0 {
				t.Fatalf("round %d: socket directory holds %q after the listener closed", round, got)
			}
			// The successor is the generation the next round retires: its command is running when its own successor commits.
			old = &managedExt{config: ExtConfig{Name: "busy"}, providerNames: []string{"old-busy"}, sockPath: nextPath}
			if packed {
				old.packedProcess = &packedProcessState{key: "cell", generation: round + 2}
				h.packedCellGeneration["cell"] = round + 3
			}
			h.mu.Lock()
			h.exts["busy"] = old
			h.mu.Unlock()
			old.commands.begin()
		}
		// A reload that removes the extension: its generation's stop runs with no successor, once its command returns. This generation keeps no provider (a removed generation that keeps one lingers instead of stopping), so the stop's work is the removal of its own file.
		old.providerNames = nil
		leftSocketFile(t, old.sockPath)
		h.commitStaged(nil, []*managedExt{old}, "removed")
		if got := socketFiles(t, h); !slices.Equal(got, []string{filepath.Base(old.sockPath)}) {
			t.Fatalf("socket directory holds %q while the removed generation's command runs, want its own %q", got, filepath.Base(old.sockPath))
		}
		old.commands.end()
		h.retirementTasks.Wait()
		if got := socketFiles(t, h); len(got) != 0 {
			t.Fatalf("socket directory holds %q after the extension was removed", got)
		}
		// A generation staged and listening when the session ends: Shutdown removes the directory and its file.
		path, err := h.newSocketPath()
		if err != nil {
			t.Fatal(err)
		}
		staged, _, err := ListenExtension(path, false)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = staged.Close() }()
		dir := h.sockRuntimeDir
		if got := socketFiles(t, h); !slices.Equal(got, []string{filepath.Base(path)}) {
			t.Fatalf("socket directory holds %q before Shutdown, want the staged generation's %q", got, filepath.Base(path))
		}
		h.Shutdown("session ended")
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("socket directory survived Shutdown: %v", err)
		}
	})
}

// A retired generation that a running command holds until the session ends (a command awaiting a surface call that nothing answers) owns its socket path for as long as it is held: the generations that later reloads stage each bind and close a path of their own and none of them removes the held one's file, and Shutdown runs the held generation's stop and removes the directory.
func TestAHeldRetiredGenerationOwnsItsSocketFileUntilTheSessionEnds(t *testing.T) {
	eachSubprocessMode(t, func(t *testing.T, packed bool) {
		h, held, stopped := retiredGenerationHost(t, packed)
		leftSocketFile(t, held.sockPath)
		heldLeaf := filepath.Base(held.sockPath)
		h.commitStaged([]stagedManagedExt{{name: "busy", me: &managedExt{config: ExtConfig{Name: "busy"}}}}, nil, "reload replaced")
		// Five later generations each bind a path of their own and close their listener once connected; the command of the first is still running.
		for round := range 5 {
			path, err := h.newSocketPath()
			if err != nil {
				t.Fatal(err)
			}
			listener, _, err := ListenExtension(path, false)
			if err != nil {
				t.Fatalf("round %d: %v", round, err)
			}
			if err := listener.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if got := socketFiles(t, h); !slices.Equal(got, []string{heldLeaf}) {
			t.Fatalf("socket directory holds %q while the generation is held, want only its own %q", got, heldLeaf)
		}
		select {
		case got := <-stopped:
			t.Fatalf("the held generation %q was stopped before the session ended", got)
		default:
		}
		dir := h.sockRuntimeDir
		h.Shutdown("session ended")
		requireStopped(t, stopped)
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("socket directory survived Shutdown: %v", err)
		}
	})
}
