package pigletbuild

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeNodePiglet writes a Piglet whose only extension is a TypeScript
// directory extension: a tool the faux model calls (echo_bridge, through a
// relative import) and a command that writes a file in the working directory.
func writeNodePiglet(t *testing.T, root, extra string) string {
	t.Helper()
	writeFiles(t, map[string]string{
		filepath.Join(root, "greeter", "lib", "echo.ts"): "export function echo(text: string): string {\n  return `NODE-BINARY-ECHO ${text}`;\n}\n",
		filepath.Join(root, "greeter", "index.ts"): `import { writeFileSync } from "node:fs";
import { echo } from "./lib/echo.ts";

export default function (pi: any) {
  pi.registerTool({
    name: "echo_bridge",
    label: "Echo",
    description: "Echo text back",
    parameters: { type: "object", properties: { text: { type: "string" } }, required: ["text"] },
    execute: async (_id: string, params: { text: string }) => ({ content: [{ type: "text", text: echo(params.text) }], details: {} }),
  });
  pi.registerCommand("node-mark", {
    description: "write node-mark.txt in the working directory",
    handler: async (args: string) => { writeFileSync("node-mark.txt", ` + "`NODE-MARK ${args}`" + `); },
  });
}
`,
		filepath.Join(root, "nodebin.yaml"): "name: nodebin\nextensions:\n  - name: greeter\n    origins: [local:./greeter]\n" + extra,
	})
	return filepath.Join(root, "nodebin.yaml")
}

// isolateBuildHome points the build's PiG and user homes at root.
func isolateBuildHome(t *testing.T, root string) {
	t.Helper()
	t.Setenv("PIG_HOME", filepath.Join(root, "home"))
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("GOFLAGS", strings.TrimSpace(os.Getenv("GOFLAGS")+" -modcacherw"))
}

// strip.features: [node-extensions] with a Node extension reaches its
// documented conflict. Before the component plan recorded the Node runtime,
// the build failed earlier with "node subprocess requires a runtime".
func TestNodeExtensionStripConflictNamesTheStripEntry(t *testing.T) {
	root := t.TempDir()
	isolateBuildHome(t, root)
	path := writeNodePiglet(t, root, "strip:\n  features: [node-extensions]\n")
	var stdout, stderr strings.Builder
	code := runBuild([]string{path, "--format", "binary", "--builder", "native", "--out", filepath.Join(root, "pig-nodebin")}, &stdout, &stderr)
	want := "strip.features names node-extensions, but the Piglet Binary runs extension cell"
	if code == 0 || !strings.Contains(stderr.String(), want) {
		t.Fatalf("build exit %d, want the strip conflict %q\nstderr:\n%s", code, want, stderr.String())
	}
}

// runNodeBinary runs a built Binary with the faux provider in an isolated
// home. path is the PATH it sees. It returns the exit code, stdout, stderr,
// and the working directory.
func runNodeBinary(t *testing.T, binary, pathEnv string, args ...string) (int, string, string, string) {
	t.Helper()
	home := t.TempDir()
	work := t.TempDir()
	cmd := exec.CommandContext(t.Context(), binary, append(args, "--model", "test-faux/faux-1", "--no-session", "--offline")...)
	cmd.Dir = work
	cmd.Env = []string{
		"PATH=" + pathEnv,
		"HOME=" + home,
		"USERPROFILE=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"PIG_HOME=" + filepath.Join(home, ".pig"),
		"PIG_CODING_AGENT_DIR=" + filepath.Join(home, ".pig", "agent"),
		"PI_CODING_AGENT_DIR=" + filepath.Join(home, ".pi", "agent"),
		"PIG_TEST_FAUX=1",
		"TMPDIR=" + os.TempDir(),
	}
	if runtime.GOOS == "windows" {
		cmd.Env = append(cmd.Env, "SystemRoot="+os.Getenv("SystemRoot"))
	}
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, stdout.String(), stderr.String(), work
	case errors.As(err, &exit):
		return exit.ExitCode(), stdout.String(), stderr.String(), work
	default:
		t.Fatalf("run %s: %v", binary, err)
		return 0, "", "", ""
	}
}

// A Piglet Binary carries a TypeScript extension: the build embeds its
// sources, and the Binary runs it with the installed Node exactly as Stock
// PiG runs it. The extension's tool runs for the model and its command runs.
// Without Node on PATH the Binary stops at startup with Stock PiG's error for
// a missing Node, naming the extension and the Node version it needs.
func TestPigletBinaryRunsItsNodeExtension(t *testing.T) {
	// The Binary runs with PATH naming only Node's directory, so take the
	// directory of the Node executable itself: a version-manager shim on PATH
	// (mise, asdf, a wrapper script) needs a shell and other tools that PATH
	// would not hold.
	execPath, err := exec.CommandContext(t.Context(), "node", "-p", "process.execPath").Output()
	if err != nil {
		t.Fatalf("node is required to run the Binary's TypeScript extension: %v", err)
	}
	nodePath := strings.TrimSpace(string(execPath))
	root := t.TempDir()
	isolateBuildHome(t, root)
	name := "pig-nodebin"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(root, name)
	var stdout, stderr strings.Builder
	if code := runBuild([]string{writeNodePiglet(t, root, ""), "--format", "binary", "--builder", "native", "--out", binary}, &stdout, &stderr); code != 0 {
		t.Fatalf("pig piglet build exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "Checksumming embedded cells") || strings.Contains(stderr.String(), "not-embedded") {
		t.Fatalf("the build did not embed the Node cell:\n%s", stderr.String())
	}
	withNode := filepath.Dir(nodePath)

	t.Run("tool", func(t *testing.T) {
		code, out, errOut, _ := runNodeBinary(t, binary, withNode, "--mode", "json", "-p", "Run: extension echo hello")
		if code != 0 || !strings.Contains(out, "NODE-BINARY-ECHO hello") {
			t.Fatalf("exit %d; the extension tool did not run\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
		}
	})
	t.Run("command", func(t *testing.T) {
		code, out, errOut, work := runNodeBinary(t, binary, withNode, "-p", "/node-mark from-binary")
		mark, readErr := os.ReadFile(filepath.Join(work, "node-mark.txt"))
		if code != 0 || readErr != nil || string(mark) != "NODE-MARK from-binary" {
			t.Fatalf("exit %d, mark %q (%v); the extension command did not run\nstdout:\n%s\nstderr:\n%s", code, mark, readErr, out, errOut)
		}
	})
	t.Run("node missing", func(t *testing.T) {
		code, out, errOut, _ := runNodeBinary(t, binary, t.TempDir(), "-p", "Run: extension echo hello")
		for _, want := range []string{
			`Error: Failed to load extension "`,
			filepath.Join("sources", "greeter") + `": Failed to load extension: `,
			"TypeScript extensions need Node.js 22.13 or newer; node was not found on PATH",
			`Hint: Start without extensions using "pig -ne".`,
		} {
			if !strings.Contains(errOut, want) {
				t.Errorf("stderr lacks %q", want)
			}
		}
		if code != 1 || strings.Contains(out, "NODE-BINARY-ECHO") {
			t.Fatalf("exit %d, want Stock PiG's startup exit 1\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
		}
	})
}
