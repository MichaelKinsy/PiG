//go:build !pig_strip_node_extensions && (linux || darwin)

package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// A Session constructed without a supplied ResourceLoader builds its own DefaultResourceLoader and starts an Extension Host for discovered extensions (D19).
// Closing that Session must shut down the loader's Extension Host and reap its child processes; repeated Close must stay safe, a supplied or borrowed loader must stay live when the Session closes, a Clone must not close its source's owned loader, and a failed NewSession must not leak the loader it built.
func TestSessionCloseShutsDownOwnedResourceLoader(t *testing.T) {
	writePidExt := func(t *testing.T, extPath, pidFile string) {
		t.Helper()
		src := fmt.Sprintf(
			"import { writeFileSync } from \"node:fs\";\nexport default function (pi) {\n  writeFileSync(%s, String(process.pid));\n  pi.registerCommand(\"probe\", { description: \"probe\", handler: async () => {} });\n}\n",
			strconv.Quote(pidFile),
		)
		writeExtensionFiles(t, map[string]string{extPath: src})
	}
	writeProbePidExt := func(t *testing.T, extPath, pidFile string) {
		t.Helper()
		src := fmt.Sprintf(
			"import { writeFileSync } from \"node:fs\";\nexport default function (pi) {\n  writeFileSync(%s, String(process.pid));\n  pi.registerCommand(\"probe\", { description: \"probe\", handler: async (args) => { if (args) writeFileSync(args, String(process.pid)); } });\n  pi.registerTool({ name: \"pid_probe\", label: \"PID Probe\", description: \"Report extension host pid\", parameters: { type: \"object\", properties: {} }, async execute() { return { content: [{ type: \"text\", text: String(process.pid) }], details: {} }; } });\n}\n",
			strconv.Quote(pidFile),
		)
		writeExtensionFiles(t, map[string]string{extPath: src})
	}
	readPID := func(t *testing.T, pidFile string) int {
		t.Helper()
		raw, err := os.ReadFile(pidFile)
		if err != nil {
			t.Fatalf("read extension pid file: %v", err)
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil || pid <= 0 {
			t.Fatalf("invalid pid %q (%v)", string(raw), err)
		}
		return pid
	}
	processAlive := func(pid int) bool {
		proc, err := os.FindProcess(pid)
		if err != nil {
			return false
		}
		return proc.Signal(syscall.Signal(0)) == nil
	}
	assertExtensionResponsive := func(t *testing.T, s *Session, markerFile string, wantPID int) {
		t.Helper()
		_ = os.Remove(markerFile)
		loader := s.ResourceLoader()
		loaded := loader.GetExtensions()
		runner := NewExtensionRunner(loaded.Extensions, loaded.Runtime, s.CWD(), s.Inner(), s.ModelRegistry())
		defer runner.Invalidate("")
		if !runner.ExecuteCommand(t.Context(), "probe", markerFile) {
			t.Fatalf("probe command not registered on loader")
		}
		if gotPID := readPID(t, markerFile); gotPID != wantPID {
			t.Fatalf("probe command answered from pid %d, want %d", gotPID, wantPID)
		}
	}
	assertExtensionToolRunsInPID := func(t *testing.T, s *Session, wantPID int) {
		t.Helper()
		var matches []agent.AgentTool
		for _, tool := range s.Tools() {
			if tool.Name() == "pid_probe" {
				matches = append(matches, tool)
			}
		}
		if len(matches) != 1 {
			t.Fatalf("session.Tools() has %d pid_probe entries, want 1 (active: %v)", len(matches), s.ActiveToolNames())
		}
		res, err := matches[0].Execute(t.Context(), "call-pid-probe", json.RawMessage(`{}`), nil)
		if err != nil {
			t.Fatalf("pid_probe.Execute failed: %v", err)
		}
		gotPID, err := strconv.Atoi(strings.TrimSpace(res.Text()))
		if err != nil {
			t.Fatalf("pid_probe returned non-integer %q (%v)", res.Text(), err)
		}
		if gotPID != wantPID {
			t.Fatalf("pid_probe ran in pid %d, want %d", gotPID, wantPID)
		}
	}
	newOwnedSession := func(t *testing.T) (string, string, *Session) {
		t.Helper()
		root := t.TempDir()
		cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		pidFile := filepath.Join(root, "ext.pid")
		writeProbePidExt(t, filepath.Join(agentDir, "extensions", "probe.js"), pidFile)
		trusted := true
		services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir, ProjectTrusted: &trusted})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(services.Close)
		result, err := CreateAgentSessionFromServices(CreateAgentSessionFromServicesOptions{Services: services, Model: fakeModel()})
		if err != nil {
			t.Fatal(err)
		}
		session := result.Session
		t.Cleanup(func() { _ = session.Close() })
		if _, err := session.Inner().AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: "hello"}}}}); err != nil {
			t.Fatal(err)
		}
		return root, pidFile, session
	}

	t.Run("owned loader closes on Session.Close and survives Reload", func(t *testing.T) {
		root := t.TempDir()
		cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		pidFile := filepath.Join(root, "ext.pid")
		writePidExt(t, filepath.Join(agentDir, "extensions", "probe.js"), pidFile)

		trusted := true
		services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir, ProjectTrusted: &trusted})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(services.Close)

		result, err := CreateAgentSessionFromServices(CreateAgentSessionFromServicesOptions{Services: services, Model: fakeModel()})
		if err != nil {
			t.Fatal(err)
		}
		session := result.Session
		firstPID := readPID(t, pidFile)
		if !processAlive(firstPID) {
			t.Fatalf("extension process %d not alive before Close", firstPID)
		}

		// Reload spawns a fresh Extension Host generation under the same owned loader.
		if err := session.Reload(t.Context()); err != nil {
			t.Fatal(err)
		}
		reloadedPID := readPID(t, pidFile)
		if !processAlive(reloadedPID) {
			t.Fatalf("reloaded extension process %d not alive before Close", reloadedPID)
		}

		if err := session.Close(); err != nil {
			t.Fatalf("first Close: %v", err)
		}
		// Second Close must be idempotent.
		if err := session.Close(); err != nil {
			t.Fatalf("second Close: %v", err)
		}

		if processAlive(firstPID) {
			t.Errorf("initial extension process %d still alive after Session.Close", firstPID)
		}
		if processAlive(reloadedPID) {
			t.Errorf("reloaded extension process %d still alive after Session.Close", reloadedPID)
		}
	})

	t.Run("supplied loader stays live on Session.Close", func(t *testing.T) {
		root := t.TempDir()
		cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		pidFile := filepath.Join(root, "ext.pid")
		writePidExt(t, filepath.Join(agentDir, "extensions", "probe.js"), pidFile)

		loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: cwd, AgentDir: agentDir})
		t.Cleanup(loader.Close)
		if err := loader.Reload(); err != nil {
			t.Fatal(err)
		}
		pid := readPID(t, pidFile)
		if !processAlive(pid) {
			t.Fatalf("supplied loader extension process %d not alive", pid)
		}

		result, err := CreateAgentSession(CreateAgentSessionOptions{CWD: cwd, AgentDir: agentDir, ResourceLoader: loader, Model: fakeModel()})
		if err != nil {
			t.Fatal(err)
		}
		if err := result.Session.Close(); err != nil {
			t.Fatal(err)
		}
		if !processAlive(pid) {
			t.Fatalf("Session.Close shut down caller-supplied loader process %d", pid)
		}
		loader.Close()
		if processAlive(pid) {
			t.Fatalf("loader.Close did not shut down process %d", pid)
		}
	})

	t.Run("clone Close does not close source owned loader", func(t *testing.T) {
		root := t.TempDir()
		cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		pidFile := filepath.Join(root, "ext.pid")
		writePidExt(t, filepath.Join(agentDir, "extensions", "probe.js"), pidFile)

		trusted := true
		services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir, ProjectTrusted: &trusted})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(services.Close)

		result, err := CreateAgentSessionFromServices(CreateAgentSessionFromServicesOptions{Services: services, Model: fakeModel()})
		if err != nil {
			t.Fatal(err)
		}
		session := result.Session
		t.Cleanup(func() { _ = session.Close() })
		pid := readPID(t, pidFile)

		if _, err := session.Inner().AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: "hello"}}}}); err != nil {
			t.Fatal(err)
		}
		clone, err := session.Clone()
		if err != nil {
			t.Fatal(err)
		}
		if err := clone.Close(); err != nil {
			t.Fatal(err)
		}
		if !processAlive(pid) {
			t.Fatalf("Clone.Close shut down source session's owned loader process %d", pid)
		}
		if err := session.Close(); err != nil {
			t.Fatal(err)
		}
		if processAlive(pid) {
			t.Fatalf("source Session.Close did not shut down owned loader process %d", pid)
		}
	})

	t.Run("failed NewSession closes the loader it constructed", func(t *testing.T) {
		root := t.TempDir()
		cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		pidFile := filepath.Join(root, "ext.pid")
		writePidExt(t, filepath.Join(agentDir, "extensions", "probe.js"), pidFile)

		corrupt := filepath.Join(root, "corrupt.jsonl")
		if err := os.WriteFile(corrupt, []byte("not-a-valid-session-header\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		trusted := true
		services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir, ProjectTrusted: &trusted})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(services.Close)

		// Corrupt ResumePath fails NewSession AFTER resolveResourceLoader has constructed and reloaded the loader.
		_, err = NewSession(services, SessionOptions{ResumePath: corrupt})
		if err == nil {
			t.Fatal("NewSession with corrupt ResumePath unexpectedly succeeded")
		}
		pid := readPID(t, pidFile)
		if processAlive(pid) {
			t.Fatalf("failed NewSession leaked owned loader extension process %d", pid)
		}
	})

	t.Run("source Close while clone lives keeps owned loader alive until clone closes", func(t *testing.T) {
		root, pidFile, source := newOwnedSession(t)
		pid := readPID(t, pidFile)
		clone, err := source.Clone()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = clone.Close() })

		if err := source.Close(); err != nil {
			t.Fatalf("source.Close: %v", err)
		}
		if !processAlive(pid) {
			t.Fatalf("extension process %d not alive after source.Close while clone lives", pid)
		}
		assertExtensionResponsive(t, clone, filepath.Join(root, "clone.probe"), pid)
		assertExtensionToolRunsInPID(t, clone, pid)

		if err := clone.Close(); err != nil {
			t.Fatalf("clone.Close: %v", err)
		}
		if processAlive(pid) {
			t.Fatalf("extension process %d still alive after last sharer (clone) closed", pid)
		}
	})

	t.Run("closeSuperseded keeps shared owned loader alive until successor closes", func(t *testing.T) {
		root, pidFile, outgoing := newOwnedSession(t)
		pid := readPID(t, pidFile)
		successor, err := outgoing.Clone()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = successor.Close() })

		if err := outgoing.closeSuperseded(successor); err != nil {
			t.Fatalf("closeSuperseded: %v", err)
		}
		if !processAlive(pid) {
			t.Fatalf("extension process %d not alive after outgoing.closeSuperseded(successor)", pid)
		}
		assertExtensionResponsive(t, successor, filepath.Join(root, "successor.probe"), pid)

		if err := successor.Close(); err != nil {
			t.Fatalf("successor.Close: %v", err)
		}
		if processAlive(pid) {
			t.Fatalf("extension process %d still alive after successor.Close", pid)
		}
	})

	t.Run("three sharers closed in mixed order end owned loader only after last", func(t *testing.T) {
		root, pidFile, source := newOwnedSession(t)
		pid := readPID(t, pidFile)
		cloneA, err := source.Clone()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cloneA.Close() })
		cloneB, err := cloneA.Clone()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cloneB.Close() })

		if err := cloneA.Close(); err != nil {
			t.Fatal(err)
		}
		if !processAlive(pid) {
			t.Fatalf("extension process %d not alive after first of three sharers closed", pid)
		}
		if err := source.Close(); err != nil {
			t.Fatal(err)
		}
		if !processAlive(pid) {
			t.Fatalf("extension process %d not alive after second of three sharers closed", pid)
		}
		assertExtensionResponsive(t, cloneB, filepath.Join(root, "cloneB.probe"), pid)

		if err := cloneB.Close(); err != nil {
			t.Fatal(err)
		}
		if processAlive(pid) {
			t.Fatalf("extension process %d still alive after last of three sharers closed", pid)
		}
	})

	t.Run("repeated Close on source and clone is idempotent without early shutdown", func(t *testing.T) {
		root, pidFile, source := newOwnedSession(t)
		pid := readPID(t, pidFile)
		clone, err := source.Clone()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = clone.Close() })

		if err := source.Close(); err != nil {
			t.Fatalf("source.Close #1: %v", err)
		}
		if err := source.Close(); err != nil {
			t.Fatalf("source.Close #2: %v", err)
		}
		if !processAlive(pid) {
			t.Fatalf("extension process %d not alive after source closed twice while clone lives", pid)
		}
		assertExtensionResponsive(t, clone, filepath.Join(root, "clone-idem.probe"), pid)

		if err := clone.Close(); err != nil {
			t.Fatalf("clone.Close #1: %v", err)
		}
		if err := clone.Close(); err != nil {
			t.Fatalf("clone.Close #2: %v", err)
		}
		if processAlive(pid) {
			t.Fatalf("extension process %d still alive after clone closed twice", pid)
		}
	})

	t.Run("Reload on source while clone lives closes reloaded generation only on last sharer", func(t *testing.T) {
		root, pidFile, source := newOwnedSession(t)
		initialPID := readPID(t, pidFile)
		clone, err := source.Clone()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = clone.Close() })

		// Reload mutates the shared DefaultResourceLoader in place (replacing its Extension Host generation).
		if err := source.Reload(t.Context()); err != nil {
			t.Fatalf("source.Reload: %v", err)
		}
		reloadedPID := readPID(t, pidFile)
		if !processAlive(reloadedPID) {
			t.Fatalf("reloaded extension process %d not alive", reloadedPID)
		}
		if reloadedPID != initialPID && processAlive(initialPID) {
			t.Fatalf("superseded extension process %d still alive after in-place Reload", initialPID)
		}

		if err := source.Close(); err != nil {
			t.Fatal(err)
		}
		if !processAlive(reloadedPID) {
			t.Fatalf("reloaded extension process %d not alive after source.Close while clone lives", reloadedPID)
		}
		assertExtensionResponsive(t, clone, filepath.Join(root, "reloaded.probe"), reloadedPID)

		if err := clone.Close(); err != nil {
			t.Fatal(err)
		}
		if processAlive(reloadedPID) {
			t.Fatalf("reloaded extension process %d still alive after clone.Close", reloadedPID)
		}
	})

	t.Run("concurrent Clone and Close never leak or leave a dead host on a returned clone", func(t *testing.T) {
		root, pidFile, source := newOwnedSession(t)
		sourcePID := readPID(t, pidFile)

		var wg sync.WaitGroup
		start := make(chan struct{})
		var clone *Session
		var cloneErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			clone, cloneErr = source.Clone()
		}()
		go func() {
			defer wg.Done()
			<-start
			_ = source.Close()
		}()
		close(start)
		wg.Wait()

		if cloneErr != nil {
			t.Fatalf("concurrent Clone failed: %v", cloneErr)
		}
		clonePID := readPID(t, pidFile)
		if !processAlive(clonePID) {
			t.Fatalf("concurrent Clone succeeded but extension process %d is already dead", clonePID)
		}
		assertExtensionResponsive(t, clone, filepath.Join(root, "race.probe"), clonePID)
		if err := clone.Close(); err != nil {
			t.Fatalf("clone.Close: %v", err)
		}
		if processAlive(sourcePID) {
			t.Fatalf("source extension process %d leaked after concurrent Clone and Close", sourcePID)
		}
		if processAlive(clonePID) {
			t.Fatalf("clone extension process %d leaked after clone.Close", clonePID)
		}
	})

	t.Run("Clone after source Close constructs a fresh owned loader", func(t *testing.T) {
		root, pidFile, source := newOwnedSession(t)
		oldPID := readPID(t, pidFile)
		if err := source.Close(); err != nil {
			t.Fatal(err)
		}
		if processAlive(oldPID) {
			t.Fatalf("extension process %d still alive after source.Close", oldPID)
		}
		cloned, err := source.Clone()
		if err != nil {
			t.Fatalf("Clone on closed Session: %v", err)
		}
		newPID := readPID(t, pidFile)
		if newPID == oldPID {
			t.Fatalf("Clone on closed Session reused old pid %d", oldPID)
		}
		if processAlive(oldPID) {
			t.Fatalf("old extension process %d unexpectedly alive", oldPID)
		}
		if !processAlive(newPID) {
			t.Fatalf("clone extension process %d not alive", newPID)
		}
		assertExtensionResponsive(t, cloned, filepath.Join(root, "closed-clone.probe"), newPID)
		if err := cloned.Close(); err != nil {
			t.Fatalf("cloned.Close: %v", err)
		}
		if processAlive(newPID) {
			t.Fatalf("clone extension process %d still alive after cloned.Close", newPID)
		}
		if processAlive(oldPID) {
			t.Fatalf("old extension process %d unexpectedly alive after cloned.Close", oldPID)
		}
	})

	t.Run("Clone after source Close lists and executes extension tool on fresh host", func(t *testing.T) {
		_, pidFile, source := newOwnedSession(t)
		oldPID := readPID(t, pidFile)
		if err := source.Close(); err != nil {
			t.Fatal(err)
		}
		if processAlive(oldPID) {
			t.Fatalf("extension process %d still alive after source.Close", oldPID)
		}
		cloned, err := source.Clone()
		if err != nil {
			t.Fatalf("Clone on closed Session: %v", err)
		}
		t.Cleanup(func() { _ = cloned.Close() })
		newPID := readPID(t, pidFile)
		if newPID == oldPID {
			t.Fatalf("Clone on closed Session reused old pid %d", oldPID)
		}
		if processAlive(oldPID) {
			t.Fatalf("old extension process %d unexpectedly alive", oldPID)
		}
		if !processAlive(newPID) {
			t.Fatalf("clone extension process %d not alive", newPID)
		}
		assertExtensionToolRunsInPID(t, cloned, newPID)
		if err := cloned.Close(); err != nil {
			t.Fatalf("cloned.Close: %v", err)
		}
		if processAlive(newPID) {
			t.Fatalf("clone extension process %d still alive after cloned.Close", newPID)
		}
	})

	t.Run("Runtime replacement closes outgoing loader when factory builds a new one", func(t *testing.T) {
		root := t.TempDir()
		cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		pidFile := filepath.Join(root, "ext.pid")
		writeProbePidExt(t, filepath.Join(agentDir, "extensions", "probe.js"), pidFile)
		trusted := true
		var pids []int
		factory := func(_ context.Context, options CreateAgentSessionRuntimeOptions) (CreateAgentSessionRuntimeResult, error) {
			services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: options.CWD, AgentDir: options.AgentDir, ProjectTrusted: &trusted})
			if err != nil {
				return CreateAgentSessionRuntimeResult{}, err
			}
			res, err := CreateAgentSessionFromServices(CreateAgentSessionFromServicesOptions{Services: services, SessionManager: options.SessionManager, Model: fakeModel()})
			if err != nil {
				services.Close()
				return CreateAgentSessionRuntimeResult{}, err
			}
			pids = append(pids, readPID(t, pidFile))
			return CreateAgentSessionRuntimeResult{Session: res.Session, Services: services, ExtensionsResult: res.ExtensionsResult, Dispose: func(string) { services.Close() }}, nil
		}
		mgr, err := NewInMemorySessionManager(cwd)
		if err != nil {
			t.Fatal(err)
		}
		rt, err := CreateAgentSessionRuntime(t.Context(), factory, CreateAgentSessionRuntimeOptions{CWD: cwd, AgentDir: agentDir, SessionManager: mgr})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = rt.Close() })
		firstPID := pids[0]
		if !processAlive(firstPID) {
			t.Fatalf("first runtime session process %d not alive", firstPID)
		}
		if _, err := rt.NewSession(t.Context(), &extension.NewSessionOptions{}); err != nil {
			t.Fatalf("rt.NewSession: %v", err)
		}
		secondPID := pids[1]
		if processAlive(firstPID) {
			t.Fatalf("outgoing session process %d still alive after Runtime.NewSession replacement", firstPID)
		}
		if !processAlive(secondPID) {
			t.Fatalf("replacement session process %d not alive", secondPID)
		}
		assertExtensionResponsive(t, rt.Session(), filepath.Join(root, "rt.probe"), secondPID)
		if err := rt.Close(); err != nil {
			t.Fatalf("rt.Close: %v", err)
		}
		if processAlive(secondPID) {
			t.Fatalf("replacement session process %d still alive after rt.Close", secondPID)
		}
	})
}
