//go:build !pig_strip_node_extensions && (linux || darwin)

package coding

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
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
}
