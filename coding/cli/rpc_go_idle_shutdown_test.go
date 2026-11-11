//go:build unix

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// idleShutdownFactory is a conventional Go factory (a packed runtime cell member, D20). The primary member requests shutdown from a goroutine while the session is idle; every member logs its session_shutdown.
const idleShutdownFactory = `package %[1]s

import (
	"os"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func logLine(s string) {
	f, err := os.OpenFile(os.Getenv("SP_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		_, _ = f.WriteString(s + "\n")
		_ = f.Close()
	}
}

func Extension() *sdk.Extension {
	e := sdk.New(%[1]q)
	// returned closes once ctx.Shutdown() returned and "after" was logged. The primary member's session_shutdown handler waits for it before it logs, because the SDK answers the call on one goroutine and runs the handler on another; a host that blocks the call until the handler ran never closes it, and the 5 s bound ends the wait (a hang detector, not a delay).
	returned := make(chan struct{})
	if %[2]v {
		e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
			go func() {
				time.Sleep(300 * time.Millisecond)
				logLine("before")
				ctx.Shutdown()
				logLine("after")
				close(returned)
			}()
			return nil, nil
		})
	}
	e.OnSessionShutdown(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if %[2]v {
			select {
			case <-returned:
			case <-time.After(5 * time.Second):
			}
		}
		logLine(%[1]q + ":session_shutdown")
		return nil, nil
	})
	return e
}
`

func writeIdleShutdownFactory(t *testing.T, name string, requests bool) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	mod := "module example.test/" + name + "\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(mod), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "extension.go"), []byte(fmt.Sprintf(idleShutdownFactory, name, requests)), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// Pi 1.5.0 and Pi main: ctx.shutdown() returns at once and the check runs on the next tick, so the code after the call runs before session_shutdown (rpc-mode.ts shutdownHandler, setImmediate). A Go SDK's ctx.Shutdown() is a synchronous host call: the host must not run the shutdown check on the caller's goroutine, or the call blocks until the session_shutdown handlers ran and the process exits first (D231, Pi main 1708b4788).
func TestRPCGoSDKIdleShutdownReturnsBeforeSessionShutdown(t *testing.T) {
	for _, layout := range []struct {
		name    string
		args    func(t *testing.T) []string
		wantEnd []string
	}{
		{"isolated", func(t *testing.T) []string { return []string{"-e", buildGoSDKFixture(t, "rpc-idle-shutdown-go")} }, []string{"session_shutdown"}},
		{"packed one member", func(t *testing.T) []string { return []string{"-e", writeIdleShutdownFactory(t, "idleprimary", true)} }, []string{"idleprimary:session_shutdown"}},
		{"packed two members", func(t *testing.T) []string {
			return []string{"-e", writeIdleShutdownFactory(t, "idleprimary", true), "-e", writeIdleShutdownFactory(t, "idlesecond", false)}
		}, []string{"idleprimary:session_shutdown", "idlesecond:session_shutdown"}},
	} {
		t.Run(layout.name, func(t *testing.T) {
			logPath := filepath.Join(t.TempDir(), "log.txt")
			home := t.TempDir()
			env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "SP_LOG=" + logPath}
			args := append([]string{"--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "--model", "test-faux/faux-1"}, layout.args(t)...)
			p := startRPCProcessAt(t, t.TempDir(), env, args...)
			awaitReady(p)
			drainRPCOutput(p)
			p.waitForExit("after an idle ctx.Shutdown() with stdin open")
			data, _ := os.ReadFile(logPath)
			got := strings.Fields(string(data))
			for _, line := range layout.wantEnd {
				if !slices.Contains(got, line) {
					t.Fatalf("extension log = %v, want %q", got, line)
				}
			}
			if len(got) < 2 || got[0] != "before" || got[1] != "after" {
				t.Fatalf("extension log = %v, want the statement after ctx.Shutdown() to run before any session_shutdown", got)
			}
		})
	}
}
