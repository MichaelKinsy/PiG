package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
)

func bashSingleQuotedArg(value string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(value, `\`, "/"), "'", `'"'"'`) + "'"
}

func inheritedStdioCommand(pidFile string) string {
	const script = "const fs=require('fs');const {spawn}=require('child_process');const child=spawn(process.execPath,['-e','setTimeout(()=>{},60000)'],{stdio:'inherit',detached:true});fs.writeFileSync(process.argv[1],String(child.pid));child.unref();console.log('child-exiting');"
	return "node -e " + bashSingleQuotedArg(script) + " " + bashSingleQuotedArg(pidFile)
}

func cleanupInheritedChild(t *testing.T, pidFile string) {
	t.Helper()
	data, err := os.ReadFile(pidFile)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Error(err)
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		t.Errorf("invalid owned child PID %q", data)
		return
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Error(err)
		return
	}
	defer func() {
		if err := process.Release(); err != nil {
			t.Error(err)
		}
	}()
	if err := killProcessGroup(process); err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		t.Errorf("cleanup owned child %d: %v", pid, err)
	}
}

func inheritedCloseWithinDeadline[T any](t *testing.T, cleanup func(), execute func(context.Context) (T, error)) T {
	t.Helper()
	type outcome struct {
		value T
		err   error
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan outcome, 1)
	go func() { value, err := execute(ctx); done <- outcome{value, err} }()
	timer := time.NewTimer(3000 * time.Millisecond)
	defer timer.Stop()
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		return result.value
	case <-timer.C:
		cancel()
		cleanup()
		<-done
		t.Fatal("Timed out after 3000ms")
		var zero T
		return zero
	}
}

func TestChildProcessCloseWithInheritedStdioPort(t *testing.T) {
	// The upstream file is Windows-only because that is where the hang was
	// reported. The same Node inheritance fixture also checks Go's shared wait
	// path on Unix; the Windows build runs these identical cases natively.
	dir := t.TempDir()
	// .upstream/v0.87.1/packages/coding-agent/test/bash-close-hang-windows.test.ts:84
	t.Run("executeBash resolves after the shell exits even if inherited stdio handles stay open", func(t *testing.T) {
		pidFile := filepath.Join(dir, "executor-grandchild.pid")
		cleanup := sync.OnceFunc(func() { cleanupInheritedChild(t, pidFile) })
		t.Cleanup(cleanup)
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		result := inheritedCloseWithinDeadline(t, cleanup, func(ctx context.Context) (BashResult, error) {
			return ExecuteBashWithOperations(ctx, inheritedStdioCommand(pidFile), cwd, NewLocalBashOperations(nil, ""), BashExecOptions{})
		})
		if !strings.Contains(result.Output, "child-exiting") || result.ExitCode == nil || *result.ExitCode != 0 || result.Cancelled {
			t.Fatalf("result = %+v", result)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/bash-close-hang-windows.test.ts:109
	t.Run("bash tool resolves after the shell exits even if inherited stdio handles stay open", func(t *testing.T) {
		pidFile := filepath.Join(dir, "tool-grandchild.pid")
		cleanup := sync.OnceFunc(func() { cleanupInheritedChild(t, pidFile) })
		t.Cleanup(cleanup)
		args, err := json.Marshal(map[string]string{"command": inheritedStdioCommand(pidFile)})
		if err != nil {
			t.Fatal(err)
		}
		result := inheritedCloseWithinDeadline(t, cleanup, func(ctx context.Context) (agent.AgentToolResult, error) {
			return (&BashTool{CWD: dir}).Execute(ctx, "test-call", args, nil)
		})
		if result.IsError || !strings.Contains(result.Text(), "child-exiting") {
			t.Fatalf("result = %+v", result)
		}
	})
}
