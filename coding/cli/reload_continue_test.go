package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const reloadContinueGo = `package reloadcontinue
import (
 "fmt"
 "os"
 "sync/atomic"
 "strings"
 sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)
var generation atomic.Int64
func log(line string) {
 f, err := os.OpenFile(os.Getenv("RELOAD_PROBE_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
 if err != nil { panic(err) }
 defer f.Close()
 if _, err := fmt.Fprintln(f, line); err != nil { panic(err) }
}
func Extension() *sdk.Extension {
 g := generation.Add(1)
 e := sdk.New("reloadcontinue")
 e.OnEvent("session_start",func(_ sdk.Context, data map[string]any)(any,error) { log(fmt.Sprintf("start:%d:%s",g,data["reason"])); return nil,nil })
 e.OnEvent("input",func(_ sdk.Context,data map[string]any)(any,error) { log(fmt.Sprintf("input:%d:%s",g,data["text"])); return nil,nil })
 reload := func(ctx sdk.Context) error {
  if err := ctx.Reload(sdk.ReloadOptions{ContinueWith:"continuation marker"}); err != nil { return err }
  err := ctx.SendUserMessage("must not arrive", "followUp")
  if err == nil { return fmt.Errorf("old context was accepted") }
  if !strings.Contains(err.Error(),"stale") { return err }
  log("old:stale")
  return nil
 }
 e.Command("reloadcontinue", "Reload and continue", func(ctx sdk.Context,_ string)error { return reload(ctx) })
 e.Tool("reload_and_continue", "Reload and continue",sdk.Schema{"type":"object"},func(ctx sdk.Context,_ map[string]any)(any,error) { return map[string]any{"content":"reloaded"},reload(ctx) })
 return e
}
`

// Exercise the real Go SDK, retained factory, bridge, session queue and provider.
// A new generation must receive exactly one continuation as the next user turn.
func TestGoReloadContinuationIsNextUserTurn(t *testing.T) {
	if testing.Short() {
		t.Skip("builds pig and a Go extension")
	}
	_ = buildPigBinaryForSignalTest(t)
	for _, trigger := range []struct{ name, prompt string }{
		{"command", "/reloadcontinue"},
		{"tool", "Run: ext tool reload_and_continue {}"},
	} {
		t.Run(trigger.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "reloadcontinue")
			if err := os.MkdirAll(root, 0755); err != nil {
				t.Fatal(err)
			}
			for name, content := range map[string]string{
				"go.mod":       "module example.test/reloadcontinue\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n",
				"extension.go": reloadContinueGo,
			} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			home, cwd := t.TempDir(), t.TempDir()
			agentDir := filepath.Join(home, "agent")
			logPath := filepath.Join(t.TempDir(), "reload.log")
			env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, "pig"), "PI_HOME=" + filepath.Join(home, "pi"), "PIG_CODING_AGENT_DIR=" + agentDir, "PI_CODING_AGENT_DIR=" + agentDir, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_OFFLINE=1", "RELOAD_PROBE_LOG=" + logPath}
			process := startRPCProcessAt(t, cwd, env, "--no-extensions", "--approve", "--no-session", "--model", "test-faux/faux-1", "-e", root)
			process.sendJSON(map[string]any{"id": "reload", "type": "prompt", "message": trigger.prompt})
			var users []string
			var settled, answered, toolFinished bool
			process.await("the continuation run to settle", func(record rpcRecord) bool {
				if record["type"] == "extension_error" {
					t.Fatalf("extension error: %v", record)
				}
				if record["type"] == "tool_execution_end" {
					if record["isError"] == true {
						t.Fatalf("tool failed: %v", record)
					}
					toolFinished = true
				}
				if record["type"] == "message_end" {
					message, _ := record["message"].(map[string]any)
					if message["role"] == "user" {
						content, _ := message["content"].([]any)
						for _, block := range content {
							if text, ok := block.(map[string]any); ok && text["type"] == "text" {
								value := text["text"].(string)
								if value == "continuation marker" && trigger.name == "tool" && !toolFinished {
									t.Fatal("continuation interrupted the tool")
								}
								users = append(users, value)
							}
						}
					}
				}
				settled = settled || record["type"] == "agent_settled"
				answered = answered || isSuccessResponse(record, "reload")
				return settled && answered
			})
			process.closeAndWait("continuation completed")
			wantUsers := []string{"continuation marker"}
			if trigger.name == "tool" {
				wantUsers = append([]string{trigger.prompt}, wantUsers...)
			}
			if strings.Join(users, "\n") != strings.Join(wantUsers, "\n") {
				t.Fatalf("user turns=%q, want %q", users, wantUsers)
			}
			log := string(mustReadFile(logPath))
			t.Logf("extension log:\n%s", log)
			turns := []string{}
			for line := range strings.SplitSeq(strings.TrimSpace(log), "\n") {
				if strings.HasPrefix(line, "input:") {
					turns = append(turns, line)
				}
			}
			want := "input:2:continuation marker"
			if trigger.name == "tool" {
				want = "input:1:" + trigger.prompt + "\n" + want
			}
			if strings.Join(turns, "\n") != want || strings.Count(log, "old:stale\n") != 1 || !strings.Contains(log, "start:2:reload\n") {
				t.Fatalf("log:\n%s\nwant inputs:\n%s", log, want)
			}
		})
	}
}
