//go:build linux

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Pi 0.87.1 setExtensionWidget (interactive-mode.ts:2321-2336) wraps a string[]
// widget for the TUI only. RPC mode forwards the list unchanged as a setWidget
// request with widgetLines (rpc-mode.ts), and print and JSON modes have no UI, so
// the widget is ignored. The Go, Python and Rust SDKs send the list as a
// width-less widget_push, which the host now lays out as a string list instead
// of painting it as a pre-rendered frame, so each mode must keep its Pi behavior
// (public issue #104).
func TestSDKStringWidgetKeepsPiBehaviorInEveryMode(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	wantLines := []any{"WIDGET-" + strings.Repeat("W", 200)}
	for _, language := range []string{"go", "py", "rs"} {
		extension := writeFooterProbe(t, language)
		env := func() []string {
			home := t.TempDir()
			// A fresh HOME hides rustup's toolchain selection; keep the real Cargo and rustup homes as extension_fresh_home_test.go does.
			return []string{rustHome("CARGO_HOME", ".cargo"), rustHome("RUSTUP_HOME", ".rustup"), "HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "pig"), "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "F104_MODE=none", "F104_WIDGET=1"}
		}
		args := []string{"--no-extensions", "--no-skills", "--no-prompt-templates", "--model", "test-faux/faux-1", "-e", extension}
		t.Run(language+"/rpc", func(t *testing.T) {
			process := startRPCProcessAt(t, t.TempDir(), env(), args...)
			var request rpcRecord
			process.await("the setWidget request", func(r rpcRecord) bool {
				if r["type"] == "extension_ui_request" && r["method"] == "setWidget" && r["widgetKey"] == "probe-wide" {
					request = r
					return true
				}
				return false
			})
			if lines, _ := request["widgetLines"].([]any); !slices.Equal(lines, wantLines) {
				t.Fatalf("widgetLines = %q, want the list unchanged (%q)", request["widgetLines"], wantLines)
			}
			if _, present := request["widgetPlacement"]; present {
				t.Fatalf("widgetPlacement = %v, want it absent as Pi omits an undefined placement", request["widgetPlacement"])
			}
			process.closeAndWait("string widget")
			if text := process.stderr.String(); text != "" {
				t.Fatalf("RPC stderr: %s", text)
			}
		})
		for _, mode := range []string{"print", "json"} {
			t.Run(language+"/"+mode, func(t *testing.T) {
				modeArgs := append([]string{}, args...)
				if mode == "print" {
					modeArgs = append(modeArgs, "--print")
				} else {
					modeArgs = append(modeArgs, "--mode", "json")
				}
				cmd := exec.CommandContext(t.Context(), binary, append(modeArgs, "What is 20+22?")...)
				cmd.Dir, cmd.Env = t.TempDir(), append(os.Environ(), env()...)
				out, err := cmd.CombinedOutput()
				if err != nil || strings.Contains(string(out), "Extension error") || strings.Contains(string(out), "WIDGET-") {
					t.Fatalf("%s: %v\n%s", mode, err, out)
				}
			})
		}
	}
}

// rustHome keeps the real Cargo or rustup home for a Rust extension build under a fresh HOME.
func rustHome(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return key + "=" + value
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return key + "="
	}
	return key + "=" + filepath.Join(home, fallback)
}
