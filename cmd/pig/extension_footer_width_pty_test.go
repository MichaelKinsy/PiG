//go:build linux

package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Public issue #104: PiG 0.3.0 exited with "Rendered line N exceeds terminal
// width (128 > 114)" when an extension footer laid out for one width was painted
// after the terminal narrowed. Pi 0.87.1 renders a footer component at the
// current width every frame (interactive-mode.ts:2418-2445) and lays a string[]
// widget out with Text(line, 1, 0) (interactive-mode.ts:2321-2336), so neither
// can reach the renderer wider than the pane. These tests run the real binary in
// a pseudo-terminal, build a real Go, Python and Rust extension, narrow the pane
// from 128 to 114 columns, and stream a reply so the differential renderer
// repaints the footer rows.
//
// Pi crashes the same way for an in-process component that ignores its width
// (probed on Pi 0.87.1: "Rendered line 14 exceeds terminal width (128 > 114)"),
// so the extensions here always lay their rows out for the width they are given.

const goFooterProbeSource = `package goprobe

import (
	"fmt"
	"os"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func fit(width int) []string {
	if width <= 0 {
		width = 128
	}
	first := "PROBE-A "
	return []string{first + strings.Repeat("a", width-len(first)-6) + fmt.Sprintf(" w=%3d", width), "PROBE-B ok"}
}

func Extension() *sdk.Extension {
	ext := sdk.New("goprobe")
	ext.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if os.Getenv("F104_WIDGET") == "1" {
			_ = ctx.SetWidget("probe-wide", []string{"WIDGET-" + strings.Repeat("W", 200)})
		}
		switch os.Getenv("F104_MODE") {
		case "none":
			return nil, nil
		case "renderer":
			return nil, ctx.SetFooterRenderer(fit)
		case "contextinfo":
			// The context-info shape: fit once, then re-push after a delay. The
			// delay is long enough for a repaint to land before the re-push.
			_, err := ctx.OnWidthChange(func(c sdk.Context, width int) {
				go func() {
					time.Sleep(1500 * time.Millisecond)
					_ = c.SetFooter(fit(width))
				}()
			})
			if err != nil {
				return nil, err
			}
		}
		return nil, ctx.SetFooter(fit(ctx.Width()))
	})
	return ext
}
`

const pyFooterProbeSource = `import os
import sys

sys.path.insert(0, os.environ["PIG_SDK_PY_ROOT"])
import pig_sdk


def fit(width):
    width = width or 128
    first = "PROBE-A "
    return [first + "a" * (width - len(first) - 6) + " w=%3d" % width, "PROBE-B ok"]


def new_extension() -> pig_sdk.Extension:
    ext = pig_sdk.Extension("pyprobe")

    def start(ctx, _event):
        if os.environ.get("F104_WIDGET") == "1":
            ctx.set_widget("probe-wide", ["WIDGET-" + "W" * 200])
        if os.environ.get("F104_MODE") == "none":
            return
        if os.environ.get("F104_MODE") == "renderer":
            ctx.set_footer_renderer(fit)
        else:
            ctx.set_footer(fit(ctx.width))

    ext.on_event("session_start", start)
    return ext
`

const rsFooterProbeSource = `use pig_sdk::Extension;

fn fit(width: u32) -> Vec<String> {
    let width = if width == 0 { 128 } else { width as usize };
    let first = "PROBE-A ";
    vec![
        format!("{first}{} w={width:3}", "a".repeat(width - first.len() - 6)),
        "PROBE-B ok".to_string(),
    ]
}

pub fn new_extension() -> Extension {
    let mut ext = Extension::new("rsprobe");
    ext.on_event("session_start", false, |ctx, _data| {
        if std::env::var("F104_WIDGET").as_deref() == Ok("1") {
            let _ = ctx.set_widget("probe-wide", vec![format!("WIDGET-{}", "W".repeat(200))]);
        }
        if std::env::var("F104_MODE").as_deref() == Ok("none") {
            return None;
        }
        if std::env::var("F104_MODE").as_deref() == Ok("renderer") {
            let _ = ctx.set_footer_renderer(Some(fit));
        } else {
            let _ = ctx.set_footer(fit(ctx.width()));
        }
        None
    });
    ext
}
`

func writeFooterProbe(t *testing.T, language string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), language+"probe")
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	switch language {
	case "go":
		write("go.mod", fmt.Sprintf("module example.com/goprobe\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.3.0\n\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n", filepath.Join(fixtureSourceRoot, "extensions", "sdk")))
		write("extension.go", goFooterProbeSource)
	case "py":
		write("pyprobe.py", pyFooterProbeSource)
	case "rs":
		write("Cargo.toml", fmt.Sprintf("[package]\nname = \"rsprobe\"\nversion = \"0.1.0\"\nedition = \"2024\"\npublish = false\n\n[dependencies]\npig-sdk = { path = %q }\n", filepath.Join(fixtureSourceRoot, "extensions", "sdk-rs")))
		write("src/lib.rs", rsFooterProbeSource)
	}
	return root
}

type footerProbeCase struct {
	language, mode string
	widget         bool
	// staleFooterUnpainted is true when the extension never re-pushes after
	// the resize: the host must then paint no footer rather than the
	// 128-column rows.
	staleFooterUnpainted bool
	// probe is the language's shared probe: its extension and PiG home, so the
	// cases of one language build the extension once.
	probe footerProbe
}

// footerProbe is one language's probe extension and the PiG home whose build
// cache its cases share.
type footerProbe struct{ extension, pigHome string }

// footerProbes writes each language's probe once per test.
func footerProbes(t *testing.T) map[string]footerProbe {
	t.Helper()
	probes := map[string]footerProbe{}
	for _, language := range []string{"go", "py", "rs"} {
		probes[language] = footerProbe{extension: writeFooterProbe(t, language), pigHome: t.TempDir()}
	}
	return probes
}

// TestExtensionFooterSurvivesNarrowingResize proves every SDK's footer reaches
// the differential renderer no wider than the pane after the pane narrows.
func TestExtensionFooterSurvivesNarrowingResize(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	probes := footerProbes(t)
	for _, tc := range []footerProbeCase{
		{language: "go", mode: "static", staleFooterUnpainted: true},
		{language: "go", mode: "contextinfo"},
		{language: "go", mode: "renderer"},
		{language: "py", mode: "static", staleFooterUnpainted: true},
		{language: "py", mode: "renderer"},
		{language: "rs", mode: "static", staleFooterUnpainted: true},
		{language: "rs", mode: "renderer"},
	} {
		tc.probe = probes[tc.language]
		t.Run(tc.language+"/"+tc.mode, func(t *testing.T) { runFooterProbe(t, binary, tc) })
	}
}

// TestExtensionStringWidgetIsWrappedByTheHost proves a string list widget wider
// than the pane is laid out by the host as Pi's Text does, for every SDK, at the
// starting width and after the pane narrows.
func TestExtensionStringWidgetIsWrappedByTheHost(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	probes := footerProbes(t)
	for _, language := range []string{"go", "py", "rs"} {
		t.Run(language, func(t *testing.T) {
			runFooterProbe(t, binary, footerProbeCase{language: language, mode: "none", widget: true, probe: probes[language]})
		})
	}
}

func runFooterProbe(t *testing.T, binary string, tc footerProbeCase) {
	t.Helper()
	extension, pigHome := tc.probe.extension, tc.probe.pigHome
	master, slave := openPTY(t, 30, 128)
	defer func() { _ = master.Close() }()
	widget := "0"
	if tc.widget {
		widget = "1"
	}
	cmd := exec.Command(binary, "--no-extensions", "-e", extension, "--model", "test-faux/faux-1")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "PIG_HOME="+pigHome, "PIG_CODING_AGENT_DIR="+t.TempDir(), "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "F104_MODE="+tc.mode, "F104_WIDGET="+widget, "TERM=xterm-256color")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pig: %v", err)
	}
	_ = slave.Close()
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})
	output := &ptyOutput{}
	go func() { _, _ = io.Copy(output, master) }()
	budget := testbudget.Wait(t)
	tail := func() string {
		all := output.since(0)
		return string(all[max(0, len(all)-3000):])
	}
	// await returns once output after mark holds marker and has stayed idle for
	// a moment. A crash or exit fails at once, with the terminal tail.
	await := func(stage string, mark int, marker string) {
		t.Helper()
		deadline := time.Now().Add(budget)
		for time.Now().Before(deadline) {
			select {
			case <-exited:
				t.Fatalf("pig exited %s; last output: %q", stage, tail())
			default:
			}
			if bytes.Contains(output.since(0), []byte("exceeds terminal width")) {
				t.Fatalf("renderer overflow %s; last output: %q", stage, tail())
			}
			// Bracketed paste is disabled only when the TUI stops, so its
			// appearance means the session ended (a crash may still be unwinding
			// and not have exited yet).
			if bytes.Contains(output.since(0), []byte("\x1b[?2004l")) {
				t.Fatalf("the TUI stopped %s; last output: %q", stage, tail())
			}
			if bytes.Contains(output.since(mark), []byte(marker)) {
				output.waitQuiet(mark, []byte(marker), 300*time.Millisecond, 10*time.Second)
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("%s: %q never appeared; last output: %q", stage, marker, tail())
	}

	// The extension's first surface is painted at the starting width.
	startMarker := "w=128"
	if tc.widget {
		startMarker = "WIDGET-"
	}
	await("at 128 columns", 0, startMarker)

	mark := output.mark()
	setPTYSize(t, master, 30, 114)
	await("narrowing to 114 columns", mark, "\x1b[3J")

	// Stream a reply: the differential renderer repaints the rows around the
	// footer and widget many times.
	if _, err := master.WriteString("TUI_LIVE_STREAM\r"); err != nil {
		t.Fatal(err)
	}
	await("while repainting after the resize", mark, "LIVE-STREAM-24")
	if tc.mode == "contextinfo" || tc.mode == "renderer" {
		await("laying the footer out again for 114 columns", mark, "w=114")
	}
	after := output.since(mark)
	if tc.staleFooterUnpainted && bytes.Contains(after, []byte(" w=128")) {
		t.Fatalf("a footer laid out for 128 columns was painted at 114 columns")
	}
	if tc.widget && !bytes.Contains(after, []byte("WIDGET-")) {
		t.Fatalf("the widget was not repainted after the resize")
	}
}
