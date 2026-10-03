package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// print-mode.ts:97-99 and rpc-mode.ts:341-343 bind the command context's reload to session.reload() (agent-session.ts:3575-3625 in 0.99.2), so
// `await ctx.reload()` works in every mode. These tests run the real binary in print, JSON and RPC mode with a Node and a Go extension whose command
// rewrites the settings, a context file, a prompt template and a skill, then reloads. The extension's log shows the order Pi produces: the old instance gets session_shutdown
// (reason "reload"), every factory runs again, and the new instance's session_start (reason "reload") reads the reloaded settings, system prompt,
// active tools and command catalog.

const reloadProbeNode = `import { appendFileSync, mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
export default function (pi) {
  const log = (line) => appendFileSync(process.env.RELOAD_PROBE_LOG, line + "\n");
  log("factory");
  pi.on("session_start", async (event, ctx) => {
    const commands = pi.getCommands().map((command) => command.name).filter((name) => name.includes("reloaded")).sort();
    log("start:" + event.reason + " quiet=" + pi.getSettings().quietStartup + " marker=" + ctx.getSystemPrompt().includes("RELOAD_MARKER_CONTEXT") + " tools=" + pi.getActiveTools().join(",") + " commands=" + commands.join(","));
  });
  pi.on("session_shutdown", async (event) => log("shutdown:" + event.reason));
  pi.registerCommand("reloadme", {
    description: "Rewrite settings and a context file, then reload",
    handler: async (_args, ctx) => {
      writeFileSync(join(process.env.PIG_CODING_AGENT_DIR, "settings.json"), JSON.stringify({ quietStartup: false, defaultTools: ["+grep"] }));
      writeFileSync(join(ctx.cwd, "AGENTS.md"), "RELOAD_MARKER_CONTEXT");
      mkdirSync(join(process.env.PIG_CODING_AGENT_DIR, "prompts"), { recursive: true });
      writeFileSync(join(process.env.PIG_CODING_AGENT_DIR, "prompts", "reloaded.md"), "---\ndescription: Added by the reload\n---\nreloaded template");
      mkdirSync(join(process.env.PIG_CODING_AGENT_DIR, "skills", "reloaded-skill"), { recursive: true });
      writeFileSync(join(process.env.PIG_CODING_AGENT_DIR, "skills", "reloaded-skill", "SKILL.md"), "---\nname: reloaded-skill\ndescription: Added by the reload\n---\nSkill body");
      await ctx.reload();
      log("returned");
    },
  });
}
`

const reloadProbeGo = `package reloadprobe

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func logLine(line string) {
	file, err := os.OpenFile(os.Getenv("RELOAD_PROBE_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	_, _ = file.WriteString(line + "\n")
}

func Extension() *sdk.Extension {
	logLine("factory")
	e := sdk.New("reloadprobe")
	e.OnSessionStart(func(ctx sdk.Context, event map[string]any) (any, error) {
		settings, _ := ctx.GetSettings()
		prompt, _ := ctx.GetSystemPrompt()
		tools, _ := ctx.GetActiveTools()
		var commands []string
		if infos, err := ctx.GetCommands(); err == nil {
			for _, info := range infos {
				if strings.Contains(info.Name, "reloaded") {
					commands = append(commands, info.Name)
				}
			}
		}
		sort.Strings(commands)
		logLine(fmt.Sprintf("start:%v quiet=%v marker=%v tools=%s commands=%s", event["reason"], settings["quietStartup"], strings.Contains(prompt, "RELOAD_MARKER_CONTEXT"), strings.Join(tools, ","), strings.Join(commands, ",")))
		return nil, nil
	})
	e.OnSessionShutdown(func(ctx sdk.Context, event map[string]any) (any, error) {
		logLine(fmt.Sprintf("shutdown:%v", event["reason"]))
		return nil, nil
	})
	e.Command("reloadme", "Rewrite settings and a context file, then reload", func(ctx sdk.Context, args string) error {
		if err := os.WriteFile(filepath.Join(os.Getenv("PIG_CODING_AGENT_DIR"), "settings.json"), []byte("{\"quietStartup\":false,\"defaultTools\":[\"+grep\"]}"), 0o600); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(ctx.Cwd(), "AGENTS.md"), []byte("RELOAD_MARKER_CONTEXT"), 0o600); err != nil {
			return err
		}
		agentDir := os.Getenv("PIG_CODING_AGENT_DIR")
		for path, content := range map[string]string{
			filepath.Join(agentDir, "prompts", "reloaded.md"):                      "---\ndescription: Added by the reload\n---\nreloaded template",
			filepath.Join(agentDir, "skills", "reloaded-skill", "SKILL.md"): "---\nname: reloaded-skill\ndescription: Added by the reload\n---\nSkill body",
		} {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				return err
			}
		}
		if err := ctx.Reload(); err != nil {
			return err
		}
		logLine("returned")
		return nil
	})
	return e
}
`

// wantReloadLog is the extension's log for one /reloadme and the quit that ends the run. grep joins the active tools because the settings the command wrote add it to defaultTools (agent-session.ts:3598-3609).
var wantReloadLog = []string{
	"factory",
	"start:startup quiet=true marker=false tools=read,bash,edit,write commands=",
	"shutdown:reload",
	"factory",
	"start:reload quiet=false marker=true tools=read,bash,edit,write,grep commands=reloaded,skill:reloaded-skill",
	"returned",
	"shutdown:quit",
}

func writeReloadProbe(t *testing.T, language string) string {
	t.Helper()
	dir := t.TempDir()
	switch language {
	case "node":
		path := filepath.Join(dir, "reload-probe.mjs")
		if err := os.WriteFile(path, []byte(reloadProbeNode), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	case "go":
		root := filepath.Join(dir, "reloadprobe")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, content := range map[string]string{
			"go.mod":       "module example.test/reloadprobe\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n",
			"extension.go": reloadProbeGo,
		} {
			if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return root
	}
	t.Fatalf("unknown language %q", language)
	return ""
}

func readReloadLog(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the extension logged nothing: %v", err)
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

func TestExtensionReloadIsWiredInEveryHeadlessMode(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and extensions")
	}
	binary := buildPigBinaryForSignalTest(t)
	for _, language := range []string{"node", "go"} {
		for _, mode := range []string{"print", "json", "rpc"} {
			t.Run(language+"/"+mode, func(t *testing.T) {
				extension := writeReloadProbe(t, language)
				home, cwd, sessionDir := t.TempDir(), t.TempDir(), t.TempDir()
				agentDir := filepath.Join(home, "pig")
				if err := os.MkdirAll(agentDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"quietStartup":true}`), 0o600); err != nil {
					t.Fatal(err)
				}
				logPath := filepath.Join(t.TempDir(), "reload.log")
				env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + agentDir, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_OFFLINE=1", "RELOAD_PROBE_LOG=" + logPath}
				args := []string{"--no-extensions", "--model", "test-faux/faux-1", "--session-dir", sessionDir, "-e", extension}
				switch mode {
				case "rpc":
					process := startRPCProcessAt(t, cwd, env, args...)
					process.sendJSON(map[string]any{"id": "reload", "type": "prompt", "message": "/reloadme"})
					process.await("the prompt response", func(r rpcRecord) bool { return isSuccessResponse(r, "reload") })
					deadline := time.Now().Add(testbudget.Wait(t))
					for !strings.Contains(string(mustReadFile(logPath)), "returned") {
						if time.Now().After(deadline) {
							t.Fatalf("the command did not return from ctx.reload(); log:\n%s", mustReadFile(logPath))
						}
						time.Sleep(25 * time.Millisecond)
					}
					process.closeAndWait("reload probe")
				default:
					if mode == "print" {
						args = append(args, "--print")
					} else {
						args = append(args, "--mode", "json")
					}
					ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
					defer cancel()
					cmd := exec.CommandContext(ctx, binary, append(args, "/reloadme")...)
					cmd.Dir, cmd.Env = cwd, append(os.Environ(), env...)
					if out, err := cmd.CombinedOutput(); err != nil || strings.Contains(string(out), "Extension error") {
						t.Fatalf("%s: %v\n%s", mode, err, out)
					}
				}
				got := readReloadLog(t, logPath)
				if strings.Join(got, "\n") != strings.Join(wantReloadLog, "\n") {
					t.Fatalf("extension log:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(wantReloadLog, "\n"))
				}
			})
		}
	}
}

func mustReadFile(path string) []byte {
	data, _ := os.ReadFile(path)
	return data
}

const reloadFailureProbeNode = `import { appendFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
export default function (pi) {
  const log = (line) => appendFileSync(process.env.RELOAD_PROBE_LOG, line + "\n");
  pi.on("session_shutdown", async (event) => log("shutdown:" + event.reason));
  pi.registerCommand("reloadfail", {
    description: "Reload with a settings file Pi's resource loader rejects",
    handler: async (_args, ctx) => {
      writeFileSync(join(process.env.PIG_CODING_AGENT_DIR, "settings.json"), JSON.stringify({ prompts: ["file:///a%2Fb"] }));
      try {
        await ctx.reload();
        log("resolved");
      } catch (error) {
        log("rejected:" + error.message);
      }
    },
  });
}
`

// agent-session.ts:3598 awaits resourceLoader.reload(), which throws for the file: URL below (resource-loader.ts); the throw rejects session.reload(), and with it the extension's `await ctx.reload()`.
func TestExtensionReloadFailureRejectsTheExtensionCall(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and extensions")
	}
	binary := buildPigBinaryForSignalTest(t)
	extension := filepath.Join(t.TempDir(), "reload-fail.mjs")
	if err := os.WriteFile(extension, []byte(reloadFailureProbeNode), 0o600); err != nil {
		t.Fatal(err)
	}
	home, cwd, sessionDir := t.TempDir(), t.TempDir(), t.TempDir()
	agentDir := filepath.Join(home, "pig")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "reload.log")
	env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + agentDir, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_OFFLINE=1", "RELOAD_PROBE_LOG=" + logPath}
	ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--no-extensions", "--model", "test-faux/faux-1", "--session-dir", sessionDir, "-e", extension, "--print", "/reloadfail")
	cmd.Dir, cmd.Env = cwd, append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil || strings.Contains(string(out), "Extension error") {
		t.Fatalf("print: %v\n%s", err, out)
	}
	got := strings.Join(readReloadLog(t, logPath), "\n")
	want := "shutdown:reload\nrejected:" + invalidFileURLMessage() + "\nshutdown:quit"
	if got != want {
		t.Fatalf("extension log:\n%s\nwant:\n%s", got, want)
	}
}

const reloadStaleProbeNode = `import { appendFileSync } from "node:fs";
export default function (pi) {
  const log = (line) => appendFileSync(process.env.RELOAD_PROBE_LOG, line + "\n");
  pi.registerCommand("reloadstale", {
    description: "Use the captured pi after the reload",
    handler: async (_args, ctx) => {
      await ctx.reload();
      log("returned");
      try {
        pi.getActiveTools();
        log("live");
      } catch (error) {
        log("stale:" + error.message);
      }
    },
  });
}
`

// agent-session.ts:3580 invalidates the old runner, so a pi the old instance captured throws runner.ts invalidate's message after `await ctx.reload()` (probed with Pi 0.99.2: "stale:This extension ctx is stale ..."). The old Node generation keeps running until its command returns; its synchronous host call must get that error, not block the command, and with it the run, forever.
func TestExtensionCallAfterReloadFailsAsStale(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and extensions")
	}
	binary := buildPigBinaryForSignalTest(t)
	extension := filepath.Join(t.TempDir(), "reload-stale.mjs")
	if err := os.WriteFile(extension, []byte(reloadStaleProbeNode), 0o600); err != nil {
		t.Fatal(err)
	}
	home, cwd, sessionDir := t.TempDir(), t.TempDir(), t.TempDir()
	agentDir := filepath.Join(home, "pig")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "reload.log")
	env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + agentDir, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_OFFLINE=1", "RELOAD_PROBE_LOG=" + logPath}
	ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--no-extensions", "--model", "test-faux/faux-1", "--session-dir", sessionDir, "-e", extension, "--print", "/reloadstale")
	cmd.Dir, cmd.Env = cwd, append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil || strings.Contains(string(out), "Extension error") {
		t.Fatalf("print: %v\n%s\nlog:\n%s", err, out, mustReadFile(logPath))
	}
	got := strings.Join(readReloadLog(t, logPath), "\n")
	want := "returned\nstale:" + (&inproc.StaleError{}).Error()
	if got != want {
		t.Fatalf("extension log:\n%s\nwant:\n%s", got, want)
	}
}

const reloadFailureStaleProbeNode = `import { appendFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
export default function (pi) {
  const log = (line) => appendFileSync(process.env.RELOAD_PROBE_LOG, line + "\n");
  pi.on("session_shutdown", async (event) => log("shutdown:" + event.reason));
  pi.registerCommand("reloadfail", {
    description: "Reload with a settings file Pi's resource loader rejects, then use the captured pi",
    handler: async (_args, ctx) => {
      writeFileSync(join(process.env.PIG_CODING_AGENT_DIR, "settings.json"), JSON.stringify({ prompts: ["file:///a%2Fb"] }));
      try {
        await ctx.reload();
        log("resolved");
      } catch (error) {
        log("rejected:" + error.message);
      }
      try {
        pi.getActiveTools();
        log("live");
      } catch (error) {
        log("stale:" + error.message);
      }
    },
  });
}
`

// agent-session.ts:3580 invalidates the old runner before the step that throws, and nothing replaces it, so after a rejected reload the old instance's captured pi throws the stale message while session_shutdown(quit) still reaches it. Probed with Pi 0.99.2 in print mode: "shutdown:reload", "rejected:File URL path must not include encoded / characters", "stale:This extension ctx is stale ...", "shutdown:quit".
func TestFailedExtensionReloadLeavesTheOldInstanceStale(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and extensions")
	}
	binary := buildPigBinaryForSignalTest(t)
	extension := filepath.Join(t.TempDir(), "reload-fail-stale.mjs")
	if err := os.WriteFile(extension, []byte(reloadFailureStaleProbeNode), 0o600); err != nil {
		t.Fatal(err)
	}
	home, cwd, sessionDir := t.TempDir(), t.TempDir(), t.TempDir()
	agentDir := filepath.Join(home, "pig")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "reload.log")
	env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + agentDir, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_OFFLINE=1", "RELOAD_PROBE_LOG=" + logPath}
	ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--no-extensions", "--model", "test-faux/faux-1", "--session-dir", sessionDir, "-e", extension, "--print", "/reloadfail")
	cmd.Dir, cmd.Env = cwd, append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil || strings.Contains(string(out), "Extension error") {
		t.Fatalf("print: %v\n%s", err, out)
	}
	got := strings.Join(readReloadLog(t, logPath), "\n")
	want := "shutdown:reload\nrejected:" + invalidFileURLMessage() + "\nstale:" + (&inproc.StaleError{}).Error() + "\nshutdown:quit"
	if got != want {
		t.Fatalf("extension log:\n%s\nwant:\n%s", got, want)
	}
}
