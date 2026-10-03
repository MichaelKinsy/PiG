//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Equivalence port of packages/coding-agent/test/suite/regressions/8237-node-sea-extension-loading.test.ts:51 and
// 9540-extension-loader-lazy.test.ts:38 (Pi 1.0.0). Pi asserts that its startup does not load jiti, the Node SEA
// loader or the bundled virtual modules until an extension is imported, because loading them costs every start.
// PiG's equivalent cost is its extension runtime: the Node runtime cell, the Node process and the extension
// toolchains (go, npm, bun, cargo, python). The invariant is the same: a start with no configured extension runs
// none of them, and a start with one TypeScript extension starts Node only for it. TestUpstreamExtensionLoaderDefers
// RuntimeUntilSelection keeps the host-level half (no module or factory effect before selection).

// runtimeTools are the programs PiG's extension runtime may start; each is shimmed to log its invocations.
var runtimeTools = []string{"node", "npm", "npx", "bun", "deno", "tsc", "esbuild", "go", "cargo", "rustc", "python3", "python", "uv"}

// writeRuntimeShims puts a logging shim for each runtime tool in a directory, forwarding to the real tool when
// PATH has one.
func writeRuntimeShims(t *testing.T, logPath string) string {
	t.Helper()
	dir := t.TempDir()
	for _, tool := range runtimeTools {
		forward := "exit 127"
		if real, err := exec.LookPath(tool); err == nil {
			forward = `exec "` + real + `" "$@"`
		}
		script := "#!/bin/sh\nprintf '%s\\n' \"" + tool + " $*\" >> \"" + logPath + "\"\n" + forward + "\n"
		if err := os.WriteFile(filepath.Join(dir, tool), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// runShimmedPig runs one print-mode turn with the default extension discovery and returns the runtime tools it ran
// and whether a runtime cell was built under PIG_HOME/cache/cells.
func runShimmedPig(t *testing.T, bin string, extra ...string) ([]string, bool) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "runtime-tools.log")
	shims := writeRuntimeShims(t, logPath)
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	workDir := filepath.Join(home, "work")
	for _, dir := range []string{agentDir, workDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	args := append([]string{"--model", "test-faux/faux-1", "--no-session"}, extra...)
	cmd := exec.Command(bin, append(args, "--print", "TUI_LIVE_STREAM")...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"PATH="+shims+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+home,
		"XDG_CACHE_HOME="+filepath.Join(home, "cache"),
		"PIG_HOME="+filepath.Join(home, "pig"),
		"PIG_CODING_AGENT_DIR="+agentDir,
		"PIG_TEST_FAUX=1",
		"PIG_TEST_FAUX_SCENARIO=parity-basic",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pig %v: %v\n%s", extra, err, output)
	}
	_, cellErr := os.Stat(filepath.Join(home, "pig", "cache", "cells"))
	cells := cellErr == nil
	data, err := os.ReadFile(logPath)
	if os.IsNotExist(err) {
		return nil, cells
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n"), cells
}

func TestUpstreamStartupDefersExtensionRuntimeUntilAnExtensionIsConfigured(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("the TypeScript extension half needs node on PATH: %v", err)
	}
	bin := buildPigBinaryForSignalTest(t)
	// .upstream/current/packages/coding-agent/test/suite/regressions/8237-node-sea-extension-loading.test.ts:51
	// .upstream/current/packages/coding-agent/test/suite/regressions/9540-extension-loader-lazy.test.ts:38
	t.Run("no configured extension starts no extension runtime", func(t *testing.T) {
		ran, cells := runShimmedPig(t, bin)
		if len(ran) != 0 {
			t.Fatalf("startup without extensions ran extension runtime tools:\n%s", strings.Join(ran, "\n"))
		}
		if cells {
			t.Fatal("startup without extensions built a runtime cell")
		}
	})
	t.Run("a TypeScript extension starts Node only for itself", func(t *testing.T) {
		dir := t.TempDir()
		extension := filepath.Join(dir, "extension.ts")
		// The factory appends one line per evaluation: upstream requires one loaded extension, no errors and one
		// createJiti call (8237:56-60, 9540:43-48).
		marker := filepath.Join(dir, "factory-evaluations")
		quoted, err := json.Marshal(marker)
		if err != nil {
			t.Fatal(err)
		}
		source := "import { appendFileSync } from \"node:fs\";\n" +
			"export default function (pi: any) {\n" +
			"  appendFileSync(" + string(quoted) + ", \"factory\\n\");\n" +
			"  pi.registerCommand(\"lazy\", { handler: async () => {} });\n}\n"
		if err := os.WriteFile(extension, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		ran, cells := runShimmedPig(t, bin, "-e", extension)
		if !cells {
			t.Fatal("the TypeScript extension ran without a Node runtime cell")
		}
		hosts := 0
		for _, line := range ran {
			tool, args, _ := strings.Cut(line, " ")
			if tool != "node" {
				t.Errorf("one TypeScript extension ran %q; only node may start (upstream: jiti/static loads 0)", line)
				continue
			}
			if args != "--version" {
				hosts++
			}
		}
		if hosts != 1 {
			t.Fatalf("the TypeScript extension started %d node hosts, want exactly 1; ran:\n%s", hosts, strings.Join(ran, "\n"))
		}
		evaluations, err := os.ReadFile(marker)
		if err != nil || string(evaluations) != "factory\n" {
			t.Fatalf("extension factory evaluations = %q (%v), want exactly one", evaluations, err)
		}
		t.Logf("runtime tools for one TypeScript extension:\n%s", strings.Join(ran, "\n"))
	})
}
