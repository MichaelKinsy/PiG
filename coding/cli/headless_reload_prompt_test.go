package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// reloadPromptHead opens a generation-tagged extension: the counter file gives each instance the factory builds a number, so a log line says which instance wrote it.
const reloadPromptHead = `import { appendFileSync, existsSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
export default function (pi) {
  const logPath = process.env.RELOAD_PROBE_LOG;
  const countPath = logPath + ".count";
  const n = (existsSync(countPath) ? Number(readFileSync(countPath, "utf8")) : 0) + 1;
  writeFileSync(countPath, String(n));
  const g = "g" + n;
  const log = (line) => appendFileSync(logPath, g + " " + line + "\n");
  const settingsPath = join(process.env.PIG_CODING_AGENT_DIR, "settings.json");
`

// reloadPromptHandlers registers, for input, before_agent_start, tool_call and a tool's execute, one handler that does not touch its ctx and one that does (an instance Pi has
// invalidated throws the stale message there), and the tool a prompt runs by name ("Run: ext tool <name> {}").
const reloadPromptHandlers = `  log("load");
  const tool = (name, execute) => pi.registerTool({ name, label: name, description: name, parameters: { type: "object", properties: {} }, execute });
  tool("b14_plain", async () => {
    log("plain tool executed");
    return { content: [{ type: "text", text: "plain tool ran " + g }] };
  });
  tool("b14_ctx", async (_id, _params, _signal, _update, ctx) => {
    log("ctx tool executing");
    const cwd = ctx.cwd;
    log("ctx tool read cwd");
    return { content: [{ type: "text", text: "ctx tool ran " + g + " " + typeof cwd }] };
  });
  tool("b14_throw", async () => {
    log("throw tool executed");
    return { content: [{ type: "text", text: "throw tool ran " + g }] };
  });
  pi.on("session_start", async (event) => log("start:" + event.reason));
  pi.on("session_shutdown", async (event) => log("shutdown:" + event.reason));
  pi.on("input", async (event) => {
    log("input plain " + JSON.stringify(event.text));
    return { action: "transform", text: event.text + "\nplain-input-ran " + g };
  });
  pi.on("input", async (_event, ctx) => {
    log("input ctx");
    const cwd = ctx.cwd;
    log("input ctx read " + typeof cwd);
  });
  pi.on("before_agent_start", async (event) => {
    log("before_agent_start plain");
    return { message: { customType: "b14", content: "plain before_agent_start ran " + g + "\n" + event.prompt, display: true } };
  });
  pi.on("before_agent_start", async (_event, ctx) => {
    log("before_agent_start ctx");
    const cwd = ctx.cwd;
    log("before_agent_start ctx read " + typeof cwd);
  });
  pi.on("tool_call", async (event) => log("tool_call plain " + event.toolName));
  pi.on("tool_call", async (event, ctx) => {
    if (event.toolName !== "b14_throw") return;
    log("tool_call ctx " + event.toolName);
    const cwd = ctx.cwd;
    log("tool_call ctx read " + typeof cwd);
  });
  pi.on("tool_result", async (event) => log("tool_result plain " + event.toolName));
`

const reloadPromptHeadlessNode = reloadPromptHead + reloadPromptHandlers + `  const reload = async (ctx) => {
    try {
      await ctx.reload();
      log("reload resolved");
    } catch (error) {
      log("reload rejected " + error.message);
    }
  };
  pi.registerCommand("reloadfail", {
    description: "reload with a settings entry the resource loader rejects",
    handler: async (_args, ctx) => {
      writeFileSync(settingsPath, JSON.stringify({ quietStartup: true, prompts: ["file:///a%2Fb"] }));
      await reload(ctx);
    },
  });
  pi.registerCommand("reloadok", { description: "reload that loads", handler: async (_args, ctx) => reload(ctx) });
}
`

var reloadPromptTools = []string{"b14_plain", "b14_ctx", "b14_throw"}

// reloadPromptInstanceLog is what one instance logs for the three prompts: Pi 1.5.2 probed with the extension above, after a failed /reload (the old instance, stale) and after
// a /reload that loaded (the new one, live). A stale instance's handler that reads its ctx throws (the runner reports it as an extension error and the prompt goes on); a
// tool_call handler that throws blocks that call, which fails with the stale message (runner.ts emitToolCall has no catch; agent-session.ts:733-739 rethrows).
func reloadPromptInstanceLog(g string, live bool) []string {
	var lines []string
	add := func(line string) { lines = append(lines, g+" "+line) }
	for _, name := range reloadPromptTools {
		add(fmt.Sprintf("input plain %q", "Run: ext tool "+name+" {}"))
		add("input ctx")
		if live {
			add("input ctx read string")
		}
		add("before_agent_start plain")
		add("before_agent_start ctx")
		if live {
			add("before_agent_start ctx read string")
		}
		add("tool_call plain " + name)
		switch name {
		case "b14_plain":
			add("plain tool executed")
		case "b14_ctx":
			add("ctx tool executing")
			if live {
				add("ctx tool read cwd")
			}
		case "b14_throw":
			add("tool_call ctx b14_throw")
			if live {
				add("tool_call ctx read string")
				add("throw tool executed")
			}
		}
		if name != "b14_throw" || live {
			add("tool_result plain " + name)
		}
	}
	return lines
}

// A reload that throws leaves the session on the runner session.reload() invalidated (agent-session.ts:3825-3826, :3844), and no emit of Pi's runner looks at staleness
// (runner.ts:1135-1560): the next prompts still run the old instance's input, before_agent_start, tool_call and tool_result handlers and its tools, and the handlers that read their ctx
// fail with the stale message, reported as extension errors while the prompt goes on. A reload that loaded replaces the runner, so the next prompts reach only the new instance.
// Probed with Pi 1.5.2 in print and RPC mode: the logs, the six extension errors and the final answers below are Pi's.
//
// mutation-checked: restoring the assertActive entry guard of EmitInput (coding/extension/host/inproc/runner.go) drops every old-instance line after the failed reload and answers
// the first prompt with the stale error; dispatching on the old runner after a loaded reload puts g1 lines into the second test's log.
func TestPromptsAfterAFailedReloadRunOnTheOldInstance(t *testing.T) {
	want := append([]string{"g1 load", "g1 start:startup", "g1 shutdown:reload", "g1 reload rejected " + invalidFileURLMessage()}, reloadPromptInstanceLog("g1", false)...)
	want = append(want, "g1 shutdown:quit")
	// Each prompt's input and before_agent_start handlers that read ctx fail; the final prompt's tool_call handler blocks the call, whose result is the stale message.
	failed := []string{"input", "before_agent_start", "input", "before_agent_start", "input", "before_agent_start"}
	runReloadPromptProbe(t, "/reloadfail", want, failed, "ext-tool-error: "+(&inproc.StaleError{}).Error())
}

func TestPromptsAfterASuccessfulReloadRunOnTheNewInstanceOnly(t *testing.T) {
	want := []string{"g1 load", "g1 start:startup", "g1 shutdown:reload", "g2 load", "g2 start:reload", "g1 reload resolved"}
	want = append(want, reloadPromptInstanceLog("g2", true)...)
	want = append(want, "g2 shutdown:quit")
	runReloadPromptProbe(t, "/reloadok", want, nil, "ext-tool-done: throw tool ran g2")
}

// runReloadPromptProbe runs the extension through print mode and RPC mode: the reload command, then one prompt per tool. wantErrors are the events whose handlers the runner reported
// as extension errors, each with the stale message (RPC names the event; print mode's "Extension error (<path>): <message>" line does not, so it is counted); wantAnswer is the final
// text print mode writes.
func runReloadPromptProbe(t *testing.T, command string, want, wantErrors []string, wantAnswer string) {
	t.Helper()
	if testing.Short() {
		t.Skip("builds the pig binary and extensions")
	}
	binary := buildPigBinaryForSignalTest(t)
	extension := filepath.Join(t.TempDir(), "reload-prompt.mjs")
	if err := os.WriteFile(extension, []byte(reloadPromptHeadlessNode), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := (&inproc.StaleError{}).Error()
	for _, mode := range []string{"print", "rpc"} {
		t.Run(mode, func(t *testing.T) {
			home, cwd, sessionDir := t.TempDir(), t.TempDir(), t.TempDir()
			agentDir := filepath.Join(home, "pig")
			if err := os.MkdirAll(agentDir, 0o755); err != nil {
				t.Fatal(err)
			}
			logPath := filepath.Join(t.TempDir(), "reload.log")
			env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + agentDir, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_OFFLINE=1", "RELOAD_PROBE_LOG=" + logPath}
			args := []string{"--no-extensions", "--model", "test-faux/faux-1", "--session-dir", sessionDir, "-e", extension}
			prompts := make([]string, 0, len(reloadPromptTools))
			for _, name := range reloadPromptTools {
				prompts = append(prompts, "Run: ext tool "+name+" {}")
			}
			if mode == "print" {
				ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
				defer cancel()
				cmd := exec.CommandContext(ctx, binary, append(append(args, "--print", command), prompts...)...)
				var stdout, stderr bytes.Buffer
				cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = cwd, append(os.Environ(), env...), &stdout, &stderr
				if err := cmd.Run(); err != nil {
					t.Fatalf("print: %v\n%s", err, stderr.String())
				}
				var reported int
				for line := range strings.SplitSeq(stderr.String(), "\n") {
					if line == "" {
						continue
					}
					if rest, ok := strings.CutPrefix(line, "Extension error ("); !ok || !strings.HasSuffix(rest, "): "+stale) {
						t.Fatalf("stderr line %q is not an extension error carrying the stale message", line)
					}
					reported++
				}
				if reported != len(wantErrors) {
					t.Fatalf("print mode reported %d extension errors, want %d\n%s", reported, len(wantErrors), stderr.String())
				}
				if got := strings.TrimSpace(stdout.String()); got != wantAnswer {
					t.Fatalf("print output %q, want %q", got, wantAnswer)
				}
			} else {
				var reported []string
				p := startRPCProcessAt(t, cwd, env, args...)
				collect := func(record rpcRecord) {
					if record["type"] != "extension_error" {
						return
					}
					if record["error"] != stale {
						t.Fatalf("extension_error carries %v, want the stale message", record["error"])
					}
					reported = append(reported, fmt.Sprint(record["event"]))
				}
				p.sendJSON(map[string]any{"id": "reload", "type": "prompt", "message": command})
				p.await("the reload command's response", func(record rpcRecord) bool {
					collect(record)
					return isSuccessResponse(record, "reload")
				})
				// A prompt command answers before the extension command it starts has ended (rpc-mode.ts does not await session.prompt), so the log says when the reload is over.
				awaitReloadLogLine(t, logPath, "g1 reload ")
				for i, name := range reloadPromptTools {
					p.sendJSON(map[string]any{"id": name, "type": "prompt", "message": prompts[i]})
					p.await("the run of "+name, func(record rpcRecord) bool {
						collect(record)
						return record["type"] == "agent_settled"
					})
				}
				p.closeAndWait("the prompts settled")
				if strings.Join(reported, ",") != strings.Join(wantErrors, ",") {
					t.Fatalf("extension errors for events %v, want %v", reported, wantErrors)
				}
			}
			if got := strings.Join(readReloadLog(t, logPath), "\n"); got != strings.Join(want, "\n") {
				t.Fatalf("extension log:\n%s\nwant (Pi):\n%s", got, strings.Join(want, "\n"))
			}
		})
	}
}

// awaitReloadLogLine waits until the extension's log has a line starting with prefix; testbudget.Wait only bounds how long a missing line takes to fail.
func awaitReloadLogLine(t *testing.T, path, prefix string) {
	t.Helper()
	deadline := time.NewTimer(testbudget.Wait(t))
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		data, _ := os.ReadFile(path)
		for line := range strings.SplitSeq(string(data), "\n") {
			if strings.HasPrefix(line, prefix) {
				return
			}
		}
		select {
		case <-deadline.C:
			t.Fatalf("the extension never logged a line starting %q:\n%s", prefix, data)
		case <-tick.C:
		}
	}
}
