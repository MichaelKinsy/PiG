//go:build !pig_strip_node_extensions && (linux || darwin)

package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// A child session created with neither request.ResourceLoader nor request.Resources builds its own DefaultResourceLoader and Extension Host (D19).
// Closing the child must shut down the childs Extension Host while leaving the parents Extension Host alive and responsive until the parent closes.
func TestChildSessionNoResourcesCloseEndsChildExtensionHost(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "project")
	agentDir := filepath.Join(root, "agent")
	extDir := filepath.Join(agentDir, "extensions")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", root)
	t.Setenv("PIG_HOME", filepath.Join(root, "home"))
	t.Setenv("PI_HOME", filepath.Join(root, "pi-home"))
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)

	pidLog := filepath.Join(root, "pids.log")
	extSrc := fmt.Sprintf(
		"import { appendFileSync, writeFileSync } from \"node:fs\";\nexport default function (pi) {\n  appendFileSync(%s, String(process.pid) + \"\\n\");\n  pi.registerCommand(\"probe\", { description: \"probe\", handler: async (args) => { if (args) writeFileSync(args, String(process.pid)); } });\n}\n",
		strconv.Quote(pidLog),
	)
	if err := os.WriteFile(filepath.Join(extDir, "probe.js"), []byte(extSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	readPIDs := func() []int {
		t.Helper()
		raw, err := os.ReadFile(pidLog)
		if err != nil {
			t.Fatalf("read pid log: %v", err)
		}
		var pids []int
		for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
			if line == "" {
				continue
			}
			p, err := strconv.Atoi(strings.TrimSpace(line))
			if err != nil || p <= 0 {
				t.Fatalf("invalid pid line %q (%v)", line, err)
			}
			pids = append(pids, p)
		}
		return pids
	}
	processAlive := func(pid int) bool {
		proc, err := os.FindProcess(pid)
		if err != nil {
			return false
		}
		return proc.Signal(syscall.Signal(0)) == nil
	}
	waitGone := func(pid int, timeout time.Duration) bool {
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			if !processAlive(pid) {
				return true
			}
			time.Sleep(25 * time.Millisecond)
		}
		return !processAlive(pid)
	}

	trusted := true
	parentServices, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{
		CWD:            cwd,
		AgentDir:       agentDir,
		ProjectTrusted: &trusted,
	})
	if err != nil {
		t.Fatalf("CreateAgentSessionServices: %v", err)
	}
	t.Cleanup(parentServices.Close)
	parentResult, err := coding.CreateAgentSessionFromServices(coding.CreateAgentSessionFromServicesOptions{
		Services: parentServices,
	})
	if err != nil {
		t.Fatalf("CreateAgentSessionFromServices: %v", err)
	}
	parentSession := parentResult.Session
	t.Cleanup(func() { _ = parentSession.Close() })

	pidsAfterParent := readPIDs()
	if len(pidsAfterParent) != 1 {
		t.Fatalf("expected 1 parent extension pid, got %v", pidsAfterParent)
	}
	parentPID := pidsAfterParent[0]

	childHandle, err := newChildSession(context.Background(), parentServices, subprocess.ChildSessionRequest{InMemory: true})
	if err != nil {
		t.Fatalf("newChildSession: %v", err)
	}
	child := childHandle.(*childSession)
	t.Cleanup(child.Close)

	pidsAfterChild := readPIDs()
	if len(pidsAfterChild) != 2 {
		t.Fatalf("expected 2 extension pids (parent+child), got %v", pidsAfterChild)
	}
	childPID := pidsAfterChild[1]
	if childPID == parentPID {
		t.Fatalf("child extension host reused parent pid %d", parentPID)
	}
	if !processAlive(childPID) {
		t.Fatalf("child extension host pid %d not alive before Close", childPID)
	}

	child.Close()
	if !waitGone(childPID, 5*time.Second) {
		t.Fatalf("child extension host pid %d still alive after child.Close()", childPID)
	}
	if !processAlive(parentPID) {
		t.Fatalf("parent extension host pid %d died when child closed", parentPID)
	}

	marker := filepath.Join(root, "parent.probe")
	loader := parentSession.ResourceLoader()
	loaded := loader.GetExtensions()
	runner := coding.NewExtensionRunner(loaded.Extensions, loaded.Runtime, parentSession.CWD(), parentSession.Inner(), parentSession.ModelRegistry())
	defer runner.Invalidate("")
	if !runner.ExecuteCommand(t.Context(), "probe", marker) {
		t.Fatalf("parent probe command not registered after child.Close()")
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read parent probe marker: %v", err)
	}
	if got := strings.TrimSpace(string(raw)); got != strconv.Itoa(parentPID) {
		t.Fatalf("parent probe answered from pid %q, want %d", got, parentPID)
	}

	if err := parentSession.Close(); err != nil {
		t.Fatalf("parentSession.Close: %v", err)
	}
	if !waitGone(parentPID, 5*time.Second) {
		t.Fatalf("parent extension host pid %d still alive after parentSession.Close()", parentPID)
	}
}
