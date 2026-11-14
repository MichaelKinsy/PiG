//go:build linux

package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

const abortProbeNode = `import { appendFileSync } from "node:fs";
export default function (pi) {
  const log = (line) => appendFileSync(process.env.ABORT_PROBE_LOG, line + "\n");
  const state = (signal) => (signal === undefined ? "none" : signal.aborted ? "aborted" : "live");
  let fired = false;
  let again = false;
  pi.on("message_update", async (_event, ctx) => {
    if (fired) return;
    fired = true;
    const held = ctx.signal;
    log("abort:" + state(held));
    ctx.abort();
    log("returned:" + state(held) + ":" + state(ctx.signal));
  });
  // A second handler of the same event runs after the first one's call returned: the run's signal is aborted for it too, and its own abort is a no-op.
  pi.on("message_update", async (_event, ctx) => {
    if (!fired || again) return;
    again = true;
    log("second:" + state(ctx.signal));
    ctx.abort();
    log("second again:" + state(ctx.signal));
  });
  pi.on("agent_end", async (event) => {
    const last = [...event.messages].reverse().find((message) => message.role === "assistant");
    log("agent_end:" + (last?.stopReason ?? ""));
  });
}
`

// abortProbeGo is a conventional Go factory (a packed runtime cell member); testdata/ctx-abort-go is the same extension as a program of its own (an isolated cell), and testdata/ctx-abort-rs is the Rust one.
const abortProbeGo = `package abortprobe

import (
	"context"
	"os"
	"sync/atomic"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

var fired, again atomic.Bool

func logLine(line string) {
	file, err := os.OpenFile(os.Getenv("ABORT_PROBE_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	_, _ = file.WriteString(line + "\n")
}

func state(signal context.Context) string {
	switch {
	case signal == nil:
		return "none"
	case signal.Err() != nil:
		return "aborted"
	}
	return "live"
}

func Extension() *sdk.Extension {
	e := sdk.New("abortprobe")
	e.OnEvent("message_update", func(ctx sdk.Context, _ map[string]any) (any, error) {
		if fired.CompareAndSwap(false, true) {
			held := ctx.Signal()
			logLine("abort:" + state(held))
			ctx.Abort()
			logLine("returned:" + state(held) + ":" + state(ctx.Signal()))
		}
		return nil, nil
	})
	e.OnEvent("message_update", func(ctx sdk.Context, _ map[string]any) (any, error) {
		if fired.Load() && again.CompareAndSwap(false, true) {
			logLine("second:" + state(ctx.Signal()))
			ctx.Abort()
			logLine("second again:" + state(ctx.Signal()))
		}
		return nil, nil
	})
	e.OnEvent("agent_end", func(_ sdk.Context, event map[string]any) (any, error) {
		reason := ""
		messages, _ := event["messages"].([]any)
		for i := len(messages) - 1; i >= 0; i-- {
			if message, ok := messages[i].(map[string]any); ok && message["role"] == "assistant" {
				reason, _ = message["stopReason"].(string)
				break
			}
		}
		logLine("agent_end:" + reason)
		return nil, nil
	})
	return e
}
`

const abortProbePython = `import os

import pig_sdk

fired = []
again = []


def log(line):
    path = os.environ.get("ABORT_PROBE_LOG")
    if not path:
        return  # the build that loads the module to find its factory has no log
    with open(path, "a", encoding="utf-8", newline="\n") as file:
        file.write(line + "\n")


def state(signal):
    return "none" if signal is None else "aborted" if signal.is_set() else "live"


def new_extension() -> pig_sdk.Extension:
    ext = pig_sdk.Extension("abortprobe")

    def message_update(ctx, event):
        if fired:
            return
        fired.append(True)
        held = ctx.signal
        log("abort:" + state(held))
        ctx.abort()
        log("returned:" + state(held) + ":" + state(ctx.signal))

    def second_update(ctx, event):
        if not fired or again:
            return
        again.append(True)
        log("second:" + state(ctx.signal))
        ctx.abort()
        log("second again:" + state(ctx.signal))

    def agent_end(ctx, event):
        reason = ""
        for message in reversed(event.get("messages") or []):
            if message.get("role") == "assistant":
                reason = message.get("stopReason") or ""
                break
        log("agent_end:" + reason)

    ext.on_event("message_update", message_update)
    ext.on_event("message_update", second_update)
    ext.on_event("agent_end", agent_end)
    return ext
`

const abortProbeRustManifest = `[package]
name = "abortprobe"
version = "0.1.0"
edition = "2024"
publish = false

[dependencies]
pig-sdk = "0.1.0"
serde_json = "1"
`

const abortProbeRust = `use pig_sdk::Extension;
use std::io::Write;
use std::sync::atomic::{AtomicBool, Ordering};

static FIRED: AtomicBool = AtomicBool::new(false);
static AGAIN: AtomicBool = AtomicBool::new(false);

fn log_line(line: &str) {
    let path = std::env::var("ABORT_PROBE_LOG").unwrap_or_default();
    if let Ok(mut file) = std::fs::OpenOptions::new().append(true).create(true).open(path) {
        let _ = writeln!(file, "{line}");
    }
}

fn state(cancelled: Option<bool>) -> &'static str {
    match cancelled {
        None => "none",
        Some(true) => "aborted",
        Some(false) => "live",
    }
}

pub fn new_extension() -> Extension {
    let mut ext = Extension::new("abortprobe");
    ext.on_event("message_update", |ctx, _event| {
        if !FIRED.swap(true, Ordering::SeqCst) {
            let held = ctx.signal();
            log_line(&format!("abort:{}", state(held.as_ref().map(|signal| signal.is_cancelled()))));
            ctx.abort();
            log_line(&format!(
                "returned:{}:{}",
                state(held.as_ref().map(|signal| signal.is_cancelled())),
                state(ctx.signal().map(|signal| signal.is_cancelled()))
            ));
        }
        None
    });
    ext.on_event("message_update", |ctx, _event| {
        if FIRED.load(Ordering::SeqCst) && !AGAIN.swap(true, Ordering::SeqCst) {
            log_line(&format!("second:{}", state(ctx.signal().map(|signal| signal.is_cancelled()))));
            ctx.abort();
            log_line(&format!("second again:{}", state(ctx.signal().map(|signal| signal.is_cancelled()))));
        }
        None
    });
    ext.on_event("agent_end", |_ctx, event| {
        let reason = event["messages"]
            .as_array()
            .and_then(|messages| messages.iter().rev().find(|message| message["role"] == "assistant"))
            .and_then(|message| message["stopReason"].as_str())
            .unwrap_or("")
            .to_string();
        log_line(&format!("agent_end:{reason}"));
        None
    });
    ext
}
`

const abortProbeLisp = `(defpackage "abortprobe" (:use "COMMON-LISP"))
(in-package "abortprobe")

(defvar *fired* nil)
(defvar *again* nil)

(defun signal-state (signal)
  (cond ((null signal) "none") ((pig:abort-signal-set-p signal) "aborted") (t "live")))

(defun log-line (line)
  (with-open-file (out (sb-posix:getenv "ABORT_PROBE_LOG") :direction :output :if-exists :append :if-does-not-exist :create :external-format :utf-8)
    (write-line line out))
  nil)

(defun last-stop-reason (messages)
  (let ((messages (coerce (or messages #()) 'vector)))
    (loop for i from (1- (length messages)) downto 0
          for message = (aref messages i)
          when (equal (pig:jget message "role") "assistant")
            return (or (pig:jget message "stopReason") "")
          finally (return ""))))

(defun new-extension ()
  (let ((ext (pig:make-extension "abortprobe")))
    (pig:on ext "message_update"
            (lambda (ctx data)
              (declare (ignore data))
              (unless *fired*
                (setf *fired* t)
                (let ((held (pig:ctx-signal ctx)))
                  (log-line (concatenate 'string "abort:" (signal-state held)))
                  (pig:ctx-abort ctx)
                  (log-line (concatenate 'string "returned:" (signal-state held) ":" (signal-state (pig:ctx-signal ctx))))))
              nil))
    (pig:on ext "message_update"
            (lambda (ctx data)
              (declare (ignore data))
              (when (and *fired* (not *again*))
                (setf *again* t)
                (log-line (concatenate 'string "second:" (signal-state (pig:ctx-signal ctx))))
                (pig:ctx-abort ctx)
                (log-line (concatenate 'string "second again:" (signal-state (pig:ctx-signal ctx)))))
              nil))
    (pig:on ext "agent_end"
            (lambda (ctx data)
              (declare (ignore ctx))
              (log-line (concatenate 'string "agent_end:" (last-stop-reason (pig:jget data "messages"))))))
    ext))
`

func writeAbortProbe(t *testing.T, layout string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(files map[string]string) {
		for name, content := range files {
			path := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	switch layout {
	case "node":
		write(map[string]string{"abort-probe.mjs": abortProbeNode})
		return filepath.Join(dir, "abort-probe.mjs")
	case "go isolated":
		return buildGoSDKFixture(t, "ctx-abort-go")
	case "go packed":
		write(map[string]string{"abortprobe/go.mod": "module example.test/abortprobe\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n", "abortprobe/extension.go": abortProbeGo})
		return filepath.Join(dir, "abortprobe")
	case "python packed":
		write(map[string]string{"abortprobe.py": abortProbePython})
		return filepath.Join(dir, "abortprobe.py")
	case "rust isolated":
		return buildRustSDKFixture(t, "ctx-abort-rs")
	case "lisp packed":
		if _, err := exec.LookPath(toolchain.SBCLExecutable(runtime.GOOS, exec.LookPath)); err != nil {
			t.Fatalf("sbcl is required for the Lisp SDK row: %v", err)
		}
		write(map[string]string{"abortprobe.lisp": abortProbeLisp})
		return filepath.Join(dir, "abortprobe.lisp")
	case "rust packed":
		write(map[string]string{"abortprobe/Cargo.toml": abortProbeRustManifest, "abortprobe/src/lib.rs": abortProbeRust})
		return filepath.Join(dir, "abortprobe")
	}
	t.Fatalf("unknown layout %q", layout)
	return ""
}

// buildRustSDKFixture builds testdata/<name>, a Rust SDK extension that is a program of its own, and returns the executable.
func buildRustSDKFixture(t *testing.T, name string) string {
	t.Helper()
	target := t.TempDir()
	cargo := exec.CommandContext(testbudget.Context(t), "cargo", "build", "--release", "--quiet")
	cargo.Dir = filepath.Join("testdata", name)
	cargo.Env = append(os.Environ(), "CARGO_TARGET_DIR="+target)
	if out, err := cargo.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", name, err, out)
	}
	return filepath.Join(target, "release", testExecutable(name))
}

// An extension's ctx.abort() from a handler the run awaits, in the TUI of the real binary, in every SDK: the call returns, the run's signal is aborted by the time
// it returns (for the signal the handler held before the call, for ctx.signal read again, and for a second handler of the same event queued behind it, whose own
// abort is a no-op), the turn ends aborted and the next prompt is answered. Pi aborts the run's AbortController before abort() returns and awaits nothing
// (interactive-mode.ts:4840-4857 `void this.session.abort()`, agent-session.ts:2519-2530, agent.ts:350-352); the interactive host action waited for the run that
// awaited the handler, so the call never returned and the session answered nothing more, and a version that only queued the abort replied before the run's
// signal was aborted. Every SDK reaches the one host action through subprocess.UIBridge.handleAbort.
func TestExtensionAbortFromAHandlerInInteractiveMode(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and extensions")
	}
	binary := buildPigBinaryForSignalTest(t)
	for _, layout := range []string{"node", "go isolated", "go packed", "python packed", "rust isolated", "rust packed", "lisp packed"} {
		t.Run(layout, func(t *testing.T) {
			extension := writeAbortProbe(t, layout)
			home, cwd, sessionDir := t.TempDir(), t.TempDir(), t.TempDir()
			agentDir := filepath.Join(home, "pig")
			if err := os.MkdirAll(agentDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"quietStartup":true}`), 0o600); err != nil {
				t.Fatal(err)
			}
			logPath := filepath.Join(t.TempDir(), "abort.log")
			env := []string{rustHome("CARGO_HOME", ".cargo"), rustHome("RUSTUP_HOME", ".rustup"), "HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + agentDir, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_OFFLINE=1", "ABORT_PROBE_LOG=" + logPath, "TERM=xterm-256color"}
			master, slave := openPTY(t, 40, 160)
			ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, "--no-extensions", "--model", "test-faux/faux-1", "--session-dir", sessionDir, "-e", extension, "--no-session", "--approve")
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
			awaitLog := func(want ...string) {
				t.Helper()
				deadline := time.Now().Add(testbudget.Wait(t))
				for {
					data, _ := os.ReadFile(logPath)
					if strings.TrimRight(string(data), "\n") == strings.Join(want, "\n") {
						return
					}
					if time.Now().After(deadline) {
						t.Fatalf("extension log:\n%s\nwant:\n%s\nscreen:\n%s", data, strings.Join(want, "\n"), output.since(0))
					}
					select {
					case <-done:
						t.Fatalf("pig exited early; log:\n%s\nscreen:\n%s", data, output.since(0))
					case <-time.After(50 * time.Millisecond):
					}
				}
			}
			// The faux provider streams this answer in 24 parts. Go's test-faux checks its context before each part and ends the answer aborted (ai/test_faux.go, TUI_LIVE_STREAM); Pi's fixture has no signal check in this script (test/parity/testdata/test-faux-provider.ts ends it `stop` with all 24 parts) and honors the signal only in its held compaction summaries, which are not turns. The rows rely on the Go twin's check: they hold that ctx.abort() returns to the handler, that the turn ends aborted and that the next prompt runs. The aborted message's own text is the twin's and is not asserted.
			if _, err := master.WriteString("TUI_LIVE_STREAM once\r"); err != nil {
				t.Fatal(err)
			}
			awaitLog("abort:live", "returned:aborted:aborted", "second:aborted", "second again:aborted", "agent_end:aborted")
			if _, err := master.WriteString("TUI_LIVE_STREAM again\r"); err != nil {
				t.Fatal(err)
			}
			awaitLog("abort:live", "returned:aborted:aborted", "second:aborted", "second again:aborted", "agent_end:aborted", "agent_end:stop")
		})
	}
}
