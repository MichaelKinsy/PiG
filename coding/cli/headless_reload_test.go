package cli

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
// `await ctx.reload()` works in every mode. These tests run the real binary in print, JSON and RPC mode with a Node, Go, Python, Rust and Lisp extension whose command
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

const reloadProbePython = `import json
import os

import pig_sdk


def log(line):
    path = os.environ.get("RELOAD_PROBE_LOG")
    if not path:
        return  # the build that loads the module to find its factory has no log
    with open(path, "a", encoding="utf-8", newline="\n") as file:
        file.write(line + "\n")


def new_extension() -> pig_sdk.Extension:
    log("factory")
    ext = pig_sdk.Extension("reloadprobe")

    def session_start(ctx, event):
        commands = sorted(info["name"] for info in ctx.get_commands() if "reloaded" in info["name"])
        quiet = json.dumps(ctx.get_settings().get("quietStartup"))
        marker = json.dumps("RELOAD_MARKER_CONTEXT" in ctx.get_system_prompt())
        log(f"start:{event['reason']} quiet={quiet} marker={marker} tools={','.join(ctx.get_active_tools())} commands={','.join(commands)}")

    def session_shutdown(ctx, event):
        log("shutdown:" + event["reason"])

    def reloadme(ctx, _args):
        agent_dir = os.environ["PIG_CODING_AGENT_DIR"]
        files = {
            os.path.join(agent_dir, "settings.json"): '{"quietStartup":false,"defaultTools":["+grep"]}',
            os.path.join(ctx.cwd, "AGENTS.md"): "RELOAD_MARKER_CONTEXT",
            os.path.join(agent_dir, "prompts", "reloaded.md"): "---\ndescription: Added by the reload\n---\nreloaded template",
            os.path.join(agent_dir, "skills", "reloaded-skill", "SKILL.md"): "---\nname: reloaded-skill\ndescription: Added by the reload\n---\nSkill body",
        }
        for path, content in files.items():
            os.makedirs(os.path.dirname(path), exist_ok=True)
            with open(path, "w", encoding="utf-8") as file:
                file.write(content)
        ctx.reload()
        log("returned")

    ext.on_event("session_start", session_start)
    ext.on_event("session_shutdown", session_shutdown)
    ext.command("reloadme", "Rewrite settings and a context file, then reload", reloadme)
    return ext
`

const reloadProbeRustManifest = `[package]
name = "reloadprobe"
version = "0.1.0"
edition = "2024"
publish = false

[dependencies]
pig-sdk = "0.1.0"
serde_json = "1"
`

const reloadProbeRust = `use pig_sdk::{CommandResult, Extension};
use serde_json::Value;
use std::io::Write;
use std::path::Path;

fn log_line(line: &str) {
    let path = std::env::var("RELOAD_PROBE_LOG").unwrap_or_default();
    if let Ok(mut file) = std::fs::OpenOptions::new().append(true).create(true).open(path) {
        let _ = writeln!(file, "{line}");
    }
}

pub fn new_extension() -> Extension {
    log_line("factory");
    let mut ext = Extension::new("reloadprobe");
    ext.on_event("session_start", |ctx, event| {
        let settings = ctx.get_settings().unwrap_or(Value::Null);
        let prompt = ctx.get_system_prompt().unwrap_or_default();
        let tools = ctx.get_active_tools().unwrap_or_default();
        let mut commands: Vec<String> = ctx
            .get_commands()
            .unwrap_or_default()
            .into_iter()
            .map(|info| info.name)
            .filter(|name| name.contains("reloaded"))
            .collect();
        commands.sort();
        log_line(&format!(
            "start:{} quiet={} marker={} tools={} commands={}",
            event["reason"].as_str().unwrap_or(""),
            settings["quietStartup"],
            prompt.contains("RELOAD_MARKER_CONTEXT"),
            tools.join(","),
            commands.join(",")
        ));
        None
    });
    ext.on_event("session_shutdown", |_ctx, event| {
        log_line(&format!("shutdown:{}", event["reason"].as_str().unwrap_or("")));
        None
    });
    ext.command(
        "reloadme",
        "Rewrite settings and a context file, then reload",
        |ctx, _args| {
            let agent_dir = std::env::var("PIG_CODING_AGENT_DIR").unwrap_or_default();
            let agent = Path::new(&agent_dir);
            let files = [
                (agent.join("settings.json"), "{\"quietStartup\":false,\"defaultTools\":[\"+grep\"]}"),
                (Path::new(ctx.cwd()).join("AGENTS.md"), "RELOAD_MARKER_CONTEXT"),
                (agent.join("prompts").join("reloaded.md"), "---\ndescription: Added by the reload\n---\nreloaded template"),
                (agent.join("skills").join("reloaded-skill").join("SKILL.md"), "---\nname: reloaded-skill\ndescription: Added by the reload\n---\nSkill body"),
            ];
            for (path, content) in files {
                if let Some(parent) = path.parent() {
                    if let Err(err) = std::fs::create_dir_all(parent) {
                        return CommandResult::Error(err.to_string());
                    }
                }
                if let Err(err) = std::fs::write(&path, content) {
                    return CommandResult::Error(err.to_string());
                }
            }
            if let Err(err) = ctx.reload() {
                return CommandResult::Error(err.to_string());
            }
            log_line("returned");
            CommandResult::Ok
        },
    );
    ext
}
`

const reloadProbeLisp = `(defpackage "reload-probe" (:use "COMMON-LISP"))
(in-package "reload-probe")

(defun env (name) (sb-posix:getenv name))

(defun log-line (line)
  (with-open-file (out (sb-ext:parse-native-namestring (env "RELOAD_PROBE_LOG")) :direction :output :if-exists :append :if-does-not-exist :create :external-format :utf-8)
    (write-line line out)))

(defun write-file (path content)
  (let ((path (sb-ext:parse-native-namestring path)))
    (ensure-directories-exist path)
    (with-open-file (out path :direction :output :if-exists :supersede :external-format :utf-8)
      (write-string content out))))

(defun bool-text (value) (cond ((eq value t) "true") ((eq value :false) "false") (t (princ-to-string value))))

(defun new-extension ()
  (log-line "factory")
  (let ((ext (pig:make-extension "reload-probe")))
    (pig:on ext "session_start"
            (lambda (ctx event)
              (let ((commands (sort (loop for info in (coerce (pig:ctx-get-commands ctx) 'list)
                                          when (search "reloaded" (pig:jget info "name")) collect (pig:jget info "name"))
                                    #'string<)))
                (log-line (format nil "start:~A quiet=~A marker=~A tools=~{~A~^,~} commands=~{~A~^,~}"
                                  (pig:jget event "reason")
                                  (bool-text (pig:jget (pig:ctx-get-settings ctx) "quietStartup"))
                                  (if (search "RELOAD_MARKER_CONTEXT" (pig:ctx-get-system-prompt ctx)) "true" "false")
                                  (coerce (pig:ctx-get-active-tools ctx) 'list)
                                  commands)))))
    (pig:on ext "session_shutdown"
            (lambda (ctx event)
              (declare (ignore ctx))
              (log-line (format nil "shutdown:~A" (pig:jget event "reason")))))
    (pig:command ext "reloadme" "Rewrite settings and a context file, then reload"
                 (lambda (ctx args)
                   (declare (ignore args))
                   (let ((agent-dir (env "PIG_CODING_AGENT_DIR")))
                     (write-file (format nil "~A/settings.json" agent-dir) "{\"quietStartup\":false,\"defaultTools\":[\"+grep\"]}")
                     (write-file (format nil "~A/AGENTS.md" (pig:ctx-cwd ctx)) "RELOAD_MARKER_CONTEXT")
                     (write-file (format nil "~A/prompts/reloaded.md" agent-dir) (format nil "---~%description: Added by the reload~%---~%reloaded template"))
                     (write-file (format nil "~A/skills/reloaded-skill/SKILL.md" agent-dir)
                                 (format nil "---~%name: reloaded-skill~%description: Added by the reload~%---~%Skill body")))
                   (pig:ctx-reload ctx)
                   (log-line "returned")))
    ext))
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
	case "python":
		path := filepath.Join(dir, "reloadprobe.py")
		if err := os.WriteFile(path, []byte(reloadProbePython), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	case "rust":
		root := filepath.Join(dir, "reloadprobe")
		if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		for name, content := range map[string]string{
			"Cargo.toml": reloadProbeRustManifest,
			"src/lib.rs": reloadProbeRust,
		} {
			if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return root
	case "lisp":
		requireSDKFixtureToolchain(t, language)
		path := filepath.Join(dir, "reload-probe.lisp")
		if err := os.WriteFile(path, []byte(reloadProbeLisp), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
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
	for _, language := range []string{"node", "go", "python", "rust", "lisp"} {
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

// agent-session.ts:3663-3664 invalidates the old runner before the step that throws, and nothing replaces it, so after a rejected reload the old instance's captured pi throws the stale message while session_shutdown(quit) still reaches it. Probed with Pi 0.99.2 in print mode and Pi 1.1.0 in RPC mode (rpc-mode.ts:341-343 binds the same session.reload()): "shutdown:reload", "rejected:File URL path must not include encoded / characters", "stale:This extension ctx is stale ...", "shutdown:quit".
// mutation-checked: dropping the extension host invalidation after a failed reload in RPC mode (rpc_mode.go SetReload) logs "live" instead of the stale message.
func TestFailedExtensionReloadLeavesTheOldInstanceStale(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and extensions")
	}
	binary := buildPigBinaryForSignalTest(t)
	extension := filepath.Join(t.TempDir(), "reload-fail-stale.mjs")
	if err := os.WriteFile(extension, []byte(reloadFailureStaleProbeNode), 0o600); err != nil {
		t.Fatal(err)
	}
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
			if mode == "rpc" {
				p := startRPCProcessAt(t, cwd, env, args...)
				p.send(`{"id":"reload","type":"prompt","message":"/reloadfail"}`)
				p.await("the reload command's response", func(record rpcRecord) bool {
					if record["type"] == "extension_error" {
						t.Fatalf("extension_error: %v", record)
					}
					return record["type"] == "response" && record["id"] == "reload"
				})
				p.closeAndWait("the reload command answered")
			} else {
				ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
				defer cancel()
				cmd := exec.CommandContext(ctx, binary, append(args, "--print", "/reloadfail")...)
				cmd.Dir, cmd.Env = cwd, append(os.Environ(), env...)
				if out, err := cmd.CombinedOutput(); err != nil || strings.Contains(string(out), "Extension error") {
					t.Fatalf("print: %v\n%s", err, out)
				}
			}
			got := strings.Join(readReloadLog(t, logPath), "\n")
			want := "shutdown:reload\nrejected:" + invalidFileURLMessage() + "\nstale:" + (&inproc.StaleError{}).Error() + "\nshutdown:quit"
			if got != want {
				t.Fatalf("extension log:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

const reloadBusyProbeNode = `import { appendFileSync, existsSync, writeFileSync } from "node:fs";
import { join } from "node:path";
const dir = process.env.RELOAD_BUSY_DIR;
const log = (line) => appendFileSync(join(dir, "log"), line + "\n");
const until = async (name) => { while (!existsSync(join(dir, name))) await new Promise((resolve) => setTimeout(resolve, 20)); };
export default function (pi) {
  pi.on("session_shutdown", async (event) => log("shutdown:" + event.reason));
  pi.registerTool({
    name: "wait_then_succeed", label: "Wait then succeed", description: "Use the tool context after waiting",
    parameters: { type: "object", properties: {} },
    async execute(_id, _params, _signal, _update, ctx) {
      writeFileSync(join(dir, "started"), "");
      await until("release");
      try {
        ctx.getSystemPrompt();
        log("tool:ctx live");
      } catch (error) {
        log("tool:ctx threw:" + error.message);
      }
      writeFileSync(join(dir, "done"), "");
      return { content: [{ type: "text", text: "tool succeeded" }], details: {} };
    },
  });
  pi.registerCommand("reloadbusy", {
    description: "Start the tool, then reload while it runs",
    handler: async (_args, ctx) => {
      pi.sendUserMessage("Run: ext tool wait_then_succeed");
      await until("started");
      try {
        await ctx.reload();
        log("resolved");
      } catch (error) {
        log("rejected:" + error.message);
      }
      try {
        pi.getActiveTools();
        log("command pi live");
      } catch (error) {
        log("command pi threw:" + error.message);
      }
      writeFileSync(join(dir, "release"), "");
      await until("done");
    },
  });
}
`

// Ports the "command" case of packages/coding-agent/test/suite/regressions/9221-reload-during-tool.test.ts (Pi c3b938e3e): a reload refused while a tool
// runs must leave the tool's context and the command's captured pi live and the tool's result successful. The refusal touched nothing, so the print and RPC
// reload closures must not invalidate the extension host.
func TestRefusedReloadDuringARunningToolKeepsTheExtensionHostLive(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and extensions")
	}
	binary := buildPigBinaryForSignalTest(t)
	extension := filepath.Join(t.TempDir(), "reload-busy.mjs")
	if err := os.WriteFile(extension, []byte(reloadBusyProbeNode), 0o600); err != nil {
		t.Fatal(err)
	}
	const refusal = "Wait for the current response to finish before reloading."
	want := "rejected:" + refusal + "\ncommand pi live\ntool:ctx live\nshutdown:quit"
	for _, mode := range []string{"print", "rpc"} {
		t.Run(mode, func(t *testing.T) {
			home, cwd, sessionDir, busyDir := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
			agentDir := filepath.Join(home, "pig")
			if err := os.MkdirAll(agentDir, 0o755); err != nil {
				t.Fatal(err)
			}
			env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + agentDir, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_OFFLINE=1", "RELOAD_BUSY_DIR=" + busyDir}
			args := []string{"--no-extensions", "--model", "test-faux/faux-1", "--session-dir", sessionDir, "-e", extension}
			if mode == "rpc" {
				p := startRPCProcessAt(t, cwd, env, args...)
				p.send(`{"id":"busy","type":"prompt","message":"/reloadbusy"}`)
				toolFailed := false
				p.await("the tool's result", func(record rpcRecord) bool {
					if record["type"] == "extension_error" {
						t.Fatalf("extension_error: %v", record)
					}
					if record["type"] == "tool_execution_end" && record["isError"] == true {
						toolFailed = true
					}
					return record["type"] == "agent_end"
				})
				if toolFailed {
					t.Fatal("the tool result is an error")
				}
				p.closeAndWait("the tool finished")
			} else {
				ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
				defer cancel()
				cmd := exec.CommandContext(ctx, binary, append(args, "--print", "/reloadbusy")...)
				cmd.Dir, cmd.Env = cwd, append(os.Environ(), env...)
				if out, err := cmd.CombinedOutput(); err != nil || strings.Contains(string(out), "Extension error") || strings.Contains(string(out), "stale") {
					t.Fatalf("print: %v\n%s\nlog:\n%s", err, out, mustReadFile(filepath.Join(busyDir, "log")))
				}
			}
			if got := strings.Join(readReloadLog(t, filepath.Join(busyDir, "log")), "\n"); got != want {
				t.Fatalf("extension log:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}
