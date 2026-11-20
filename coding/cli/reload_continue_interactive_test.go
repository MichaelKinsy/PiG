//go:build linux

package cli

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

const interactiveReloadContinueGo = `package reloadcontinue
import (
 "fmt"
 "os"
 sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)
const version = "old"
func log(line string) {
 f, err := os.OpenFile(os.Getenv("RELOAD_PROBE_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
 if err != nil { panic(err) }
 defer f.Close()
 if _, err := fmt.Fprintln(f, line); err != nil { panic(err) }
}
func Extension() *sdk.Extension {
 e := sdk.New("reloadcontinue")
 e.OnEvent("session_start", func(_ sdk.Context, _ map[string]any)(any,error) { log(fmt.Sprintf("start:%s:%d",version,os.Getpid())); return nil,nil })
 e.OnEvent("input",func(_ sdk.Context,data map[string]any)(any,error) { log(fmt.Sprintf("input:%s:%s",version,data["text"])); return nil,nil })
 e.Tool("reload_and_continue", "Reload then continue", sdk.Schema{"type":"object"}, func(ctx sdk.Context,_ map[string]any)(any,error) {
  if err := ctx.ReloadWithOptions(sdk.ReloadOptions{ContinueWith:"Run: ext tool version {}"}); err != nil { return nil,err }
  log("tool-returned:"+version)
  return sdk.ToolResult{Content:"reload scheduled after this turn"},nil
 })
 e.Tool("version", "Report the loaded code version", sdk.Schema{"type":"object"},func(_ sdk.Context,_ map[string]any)(any,error) { log("version:"+version); return sdk.ToolResult{Content:version},nil })
 return e
}
`

// A changed source must run in a new process before the continuation's tool runs.
// The print/RPC probe alone cannot detect interactive's streaming reload refusal.
func TestGoReloadContinuationRestartsChangedCodeInInteractiveMode(t *testing.T) {
	runInteractiveReloadContinuation(t, false, false)
}

func TestGoReloadContinuationFailureStillResumesInteractiveMode(t *testing.T) {
	runInteractiveReloadContinuation(t, true, false)
}

func TestGoMultipleReloadContinuationsWaitForEachInteractiveTurn(t *testing.T) {
	runInteractiveReloadContinuation(t, false, true)
}

func runInteractiveReloadContinuation(t *testing.T, failReload, repeat bool) {
	t.Helper()
	if testing.Short() {
		t.Skip("builds the pig binary and a Go extension")
	}
	binary := buildPigBinaryForSignalTest(t)
	root := filepath.Join(t.TempDir(), "reloadcontinue")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	source := interactiveReloadContinueGo
	if repeat {
		source = strings.Replace(source, `  log("tool-returned:"+version)`, `  if err := ctx.ReloadWithOptions(sdk.ReloadOptions{ContinueWith:"Run: ext tool version {}"}); err != nil { return nil,err }
  log("tool-returned:"+version)`, 1)
	}
	for name, content := range map[string]string{
		"go.mod":       "module example.test/reloadcontinue\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n",
		"extension.go": source,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	home, cwd, sessionDir := t.TempDir(), t.TempDir(), t.TempDir()
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"quietStartup":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "reload.log")
	env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, "pig"), "PI_HOME=" + filepath.Join(home, "pi"), "PIG_CODING_AGENT_DIR=" + agentDir, "PI_CODING_AGENT_DIR=" + agentDir, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_OFFLINE=1", "RELOAD_PROBE_LOG=" + logPath, "TERM=xterm-256color"}
	master, slave := openPTY(t, 40, 160)
	ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--no-extensions", "--model", "test-faux/faux-1", "-e", root, "--session-dir", sessionDir, "--approve")
	cmd.Dir, cmd.Env = cwd, append(os.Environ(), env...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	output := &ptyOutput{}
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, err := master.Read(buffer)
			if n > 0 {
				_, _ = output.Write(buffer[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	done := make(chan struct{})
	go func() { defer close(done); _ = cmd.Wait() }()
	t.Cleanup(func() { cancel(); <-done; _ = master.Close() })
	output.waitQuiet(0, []byte("faux-1"), 200*time.Millisecond, testbudget.Wait(t))
	changed := strings.Replace(source, `const version = "old"`, `const version = "new"`, 1)
	if failReload {
		changed += "\nthis is invalid Go source\n"
	}
	if err := os.WriteFile(filepath.Join(root, "extension.go"), []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := master.WriteString("Run: ext tool reload_and_continue {}\r"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(testbudget.Wait(t))
	for {
		data, _ := os.ReadFile(logPath)
		log := string(data)
		if failReload && strings.Contains(log, "tool-returned:old\n") {
			var continuation string
			_ = filepath.WalkDir(sessionDir, func(path string, entry fs.DirEntry, err error) error {
				if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".jsonl") {
					return nil
				}
				data, _ := os.ReadFile(path)
				for line := range strings.SplitSeq(string(data), "\n") {
					var record struct {
						Message struct {
							Role    string `json:"role"`
							Content []struct {
								Text string `json:"text"`
							} `json:"content"`
						} `json:"message"`
					}
					if json.Unmarshal([]byte(line), &record) != nil || record.Message.Role != "user" {
						continue
					}
					for _, block := range record.Message.Content {
						if strings.HasPrefix(block.Text, "[reload failed: ") {
							continuation = block.Text
						}
					}
				}
				return nil
			})
			if continuation != "" {
				lines := strings.Split(continuation, "\n")
				if len(lines) != 2 || !strings.HasSuffix(lines[0], "]") || lines[1] != "Run: ext tool version {}" {
					t.Fatalf("failure must prefix one line and preserve continuation: %q", continuation)
				}
				t.Log("failed reload still delivered the prefixed continuation")
				return
			}
		}
		if strings.Contains(log, "version:old\n") {
			t.Fatalf("continuation ran old code instead of restarting the changed extension: %s", log)
		}
		if repeat && strings.Contains(log, "input:new:[reload failed:") {
			t.Fatalf("later scheduled reload ran while the first continuation was still active: %s", log)
		}
		if strings.Contains(log, "version:new\n") && (!repeat || strings.Count(log, "version:new\n") >= 2) {
			markers := []string{"tool-returned:old\n", "start:new:", "input:new:Run: ext tool version {}\n", "version:new\n"}
			previous := -1
			for _, marker := range markers {
				index := strings.Index(log, marker)
				if index <= previous {
					t.Fatalf("reload/continuation order is wrong: %s", log)
				}
				previous = index
			}
			pids := map[string]int{}
			for line := range strings.SplitSeq(log, "\n") {
				parts := strings.Split(line, ":")
				if len(parts) == 3 && parts[0] == "start" {
					pids[parts[1]], _ = strconv.Atoi(parts[2])
				}
			}
			if pids["old"] == 0 || pids["new"] == 0 || pids["old"] == pids["new"] {
				t.Fatalf("extension process did not restart: %s", log)
			}
			if repeat {
				afterFirstTurn := log[strings.Index(log, "version:new\n")+len("version:new\n"):]
				if start, input := strings.Index(afterFirstTurn, "start:new:"), strings.Index(afterFirstTurn, "input:new:Run: ext tool version {}\n"); start < 0 || input <= start {
					t.Fatalf("the second scheduled operation must reload after the first continuation turn: %s", log)
				}
			}
			t.Logf("loaded new code in a replacement process before continuation: %s", log)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("new code never ran; log:\n%s\nscreen:\n%s", log, output.since(0))
		}
		select {
		case <-done:
			t.Fatalf("pig exited before the new code ran; log:\n%s\nscreen:\n%s", log, output.since(0))
		case <-time.After(50 * time.Millisecond):
		}
	}
}
