package pigletbuild

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// writeNodePiglet writes a Piglet whose only extension is a TypeScript
// directory extension: a tool the faux model calls (echo_bridge, through a
// relative import) and a command that writes a file in the working directory.
func writeNodePiglet(t *testing.T, root, extra string) string {
	t.Helper()
	writeFiles(t, map[string]string{
		filepath.Join(root, "greeter", "lib", "echo.ts"): "export function echo(text: string): string {\n  return `NODE-BINARY-ECHO ${text}`;\n}\n",
		filepath.Join(root, "greeter", "index.ts"): `import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
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
  pi.registerCommand("node-links", {
    description: "write node-links.txt with what each path named in args reads through the extension's links",
    handler: async (args: string) => {
      const here = dirname(fileURLToPath(import.meta.url));
      writeFileSync("node-links.txt", args.split(" ").map((path) => readFileSync(join(here, path), "utf8").trim()).join("|"));
    },
  });
}
`,
		filepath.Join(root, "nodebin.yaml"): "name: nodebin\nextensions:\n  - name: greeter\n    origins: [local:./greeter]\n" + extra,
	})
	return filepath.Join(root, "nodebin.yaml")
}

// nodeLinkFixtures gives the extension the link shapes package managers leave in node_modules, each reading a file that only its
// target holds: npm's .bin links (plain and scoped) and pnpm's store, whose links go down into .pnpm/<name>@<version> and whose
// sibling links climb out of one version's folder and down into another's. It returns the paths a command reads, in the order of
// the values it must report.
func nodeLinkFixtures(t *testing.T, root string) (paths []string, values []string) {
	t.Helper()
	member := filepath.Join(root, "greeter")
	files := map[string]string{
		filepath.Join(member, "node_modules", "dep", "bin", "cli.js"):                                                     "NPM-BIN",
		filepath.Join(member, "node_modules", "@scope", "pkg", "bin.js"):                                                  "NPM-SCOPED-BIN",
		filepath.Join(member, "node_modules", ".pnpm", "store@1.0.0", "node_modules", "store", "index.js"):                "PNPM-STORE",
		filepath.Join(member, "node_modules", ".pnpm", "sibling@2.0.0", "node_modules", "sibling", "index.js"):            "PNPM-SIBLING",
		filepath.Join(member, "node_modules", ".pnpm", "@scope+deep@3.0.0", "node_modules", "@scope", "deep", "index.js"): "PNPM-SCOPED",
	}
	writeFiles(t, files)
	nm := filepath.Join(member, "node_modules")
	for link, target := range map[string]string{
		filepath.Join(nm, ".bin", "dep"):                                            "../dep/bin/cli.js",
		filepath.Join(nm, ".bin", "scoped"):                                         "../@scope/pkg/bin.js",
		filepath.Join(nm, "store"):                                                  ".pnpm/store@1.0.0/node_modules/store",
		filepath.Join(nm, ".pnpm", "store@1.0.0", "node_modules", "sibling"):        "../../sibling@2.0.0/node_modules/sibling",
		filepath.Join(nm, ".pnpm", "store@1.0.0", "node_modules", "@scope", "deep"): "../../../@scope+deep@3.0.0/node_modules/@scope/deep",
		filepath.Join(nm, ".pnpm", "node_modules", "hoisted"):                       "../store@1.0.0/node_modules/store",
		filepath.Join(nm, ".pnpm", "store@1.0.0", "node_modules", "self"):           "..",
	} {
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		testenv.Symlink(t, filepath.FromSlash(target), link)
	}
	return []string{
			"node_modules/.bin/dep",
			"node_modules/.bin/scoped",
			"node_modules/store/index.js",
			"node_modules/.pnpm/store@1.0.0/node_modules/sibling/index.js",
			"node_modules/.pnpm/store@1.0.0/node_modules/@scope/deep/index.js",
			"node_modules/.pnpm/node_modules/hoisted/index.js",
			"node_modules/.pnpm/store@1.0.0/node_modules/self/node_modules/store/index.js",
		},
		[]string{"NPM-BIN", "NPM-SCOPED-BIN", "PNPM-STORE", "PNPM-SIBLING", "PNPM-SCOPED", "PNPM-STORE", "PNPM-STORE"}
}

// isolateBuildHome points the build's PiG and user homes at root.
func isolateBuildHome(t *testing.T, root string) {
	t.Helper()
	t.Setenv("PIG_HOME", filepath.Join(root, "home"))
	testenv.SetHome(t, filepath.Join(root, "home"))
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
	code := runBuild([]string{path, "--format", "binary", "--builder", "native", "--out", filepath.Join(root, nativeBinaryName("pig-nodebin"))}, &stdout, &stderr)
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
	return runNodeBinaryAt(t, binary, t.TempDir(), "", pathEnv, args...)
}

// runNodeBinaryAt is runNodeBinary with the home it runs in given, so a second start sees what the first left in the
// configuration root, and the text it reads on standard input.
func runNodeBinaryAt(t *testing.T, binary, home, stdin, pathEnv string, args ...string) (int, string, string, string) {
	t.Helper()
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
		// SystemRoot: Windows programs locate the system through it. LOCALAPPDATA:
		// with no TEMP the Binary's temp directory is USERPROFILE, too deep for a
		// Unix socket path (107 bytes), so the extension Host places its sockets in
		// %LOCALAPPDATA%\pig\s as it does for any deep temp directory.
		cmd.Env = append(cmd.Env, "SystemRoot="+os.Getenv("SystemRoot"), "LOCALAPPDATA="+os.Getenv("LOCALAPPDATA"))
	}
	var stdout, stderr strings.Builder
	cmd.Stdin = strings.NewReader(stdin)
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
	binary := filepath.Join(root, nativeBinaryName("pig-nodebin"))
	piglet := writeNodePiglet(t, root, "")
	var linkPaths, linkValues []string
	if runtime.GOOS != "windows" {
		linkPaths, linkValues = nodeLinkFixtures(t, root)
	}
	var stdout, stderr strings.Builder
	if code := runBuild([]string{piglet, "--format", "binary", "--builder", "native", "--out", binary}, &stdout, &stderr); code != 0 {
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
	t.Run("package manager links", func(t *testing.T) {
		if len(linkPaths) == 0 {
			t.Skip("the link fixtures are not built on Windows")
		}
		code, out, errOut, work := runNodeBinary(t, binary, withNode, "-p", "/node-links "+strings.Join(linkPaths, " "))
		got, readErr := os.ReadFile(filepath.Join(work, "node-links.txt"))
		if want := strings.Join(linkValues, "|"); code != 0 || readErr != nil || string(got) != want {
			t.Fatalf("exit %d, node-links.txt %q (%v), want %q: the extracted tree's links do not resolve like the source's\nstdout:\n%s\nstderr:\n%s", code, got, readErr, want, out, errOut)
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

// A Piglet Binary starts again with a Node source file of its extracted copy edited, a file added that the extension
// would import, and a node_modules beside the sources: the Node runtime still runs the code the Binary was built with,
// because every start compares the extracted copy with the archive in the Binary, as it compares an embedded Go, Rust
// or Python cell binary with its bytes. The repair is announced once, on stderr, never on stdout.
func TestPigletBinaryRunsTheOriginalNodeSourcesAfterTheExtractedCopyIsEdited(t *testing.T) {
	execPath, err := exec.CommandContext(t.Context(), "node", "-p", "process.execPath").Output()
	if err != nil {
		t.Fatalf("node is required to run the Binary's TypeScript extension: %v", err)
	}
	withNode := filepath.Dir(strings.TrimSpace(string(execPath)))
	root := t.TempDir()
	isolateBuildHome(t, root)
	binary := filepath.Join(root, nativeBinaryName("pig-nodebin"))
	piglet := writeNodePiglet(t, root, "")
	var stdout, stderr strings.Builder
	if code := runBuild([]string{piglet, "--format", "binary", "--builder", "native", "--out", binary}, &stdout, &stderr); code != 0 {
		t.Fatalf("pig piglet build exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	home := t.TempDir()
	const ask = "Run: extension echo hello"
	run := func() (string, string) {
		t.Helper()
		code, out, errOut, _ := runNodeBinaryAt(t, binary, home, "", withNode, "--mode", "json", "-p", ask)
		if code != 0 {
			t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
		}
		return out, errOut
	}
	if out, errOut := run(); !strings.Contains(out, "NODE-BINARY-ECHO hello") || strings.Contains(errOut, "extracted them again") {
		t.Fatalf("first start\nstdout:\n%s\nstderr:\n%s", out, errOut)
	}
	entries, err := filepath.Glob(filepath.Join(home, ".pig", "piglet-binary-cells", "node", "*", "sources", "greeter"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("extracted member folders = %v, %v; want one", entries, err)
	}
	member := entries[0]
	for path, body := range map[string]string{
		filepath.Join(member, "lib", "echo.ts"):                           "export function echo(text: string): string { return `EDITED ${text}`; }\n",
		filepath.Join(member, "lib", "extra.ts"):                          "export const extra = 1;\n",
		filepath.Join(member, "node_modules", "shadow", "package.json"):   "{\"name\":\"shadow\"}\n",
		filepath.Join(filepath.Dir(filepath.Dir(member)), "package.json"): "{\"type\":\"commonjs\"}\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, errOut := run()
	if !strings.Contains(out, "NODE-BINARY-ECHO hello") || strings.Contains(out, "EDITED") {
		t.Fatalf("the edited extracted copy ran\nstdout:\n%s\nstderr:\n%s", out, errOut)
	}
	if strings.Count(errOut, "extracted them again") != 1 || !strings.Contains(errOut, "pig: warning: the extracted Node sources in ") || strings.Contains(out, "extracted them again") {
		t.Fatalf("the repair is not announced once on stderr\nstdout:\n%s\nstderr:\n%s", out, errOut)
	}
	for _, gone := range []string{
		filepath.Join(member, "lib", "extra.ts"),
		filepath.Join(member, "node_modules"),
		filepath.Join(filepath.Dir(filepath.Dir(member)), "package.json"),
	} {
		if _, err := os.Lstat(gone); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s survived the repair: %v", gone, err)
		}
	}
	if body, err := os.ReadFile(filepath.Join(member, "lib", "echo.ts")); err != nil || !strings.Contains(string(body), "NODE-BINARY-ECHO") {
		t.Errorf("echo.ts after the repair = %q, %v", body, err)
	}
	if out, errOut := run(); !strings.Contains(out, "NODE-BINARY-ECHO hello") || strings.Contains(errOut, "extracted them again") {
		t.Fatalf("the start after the repair\nstdout:\n%s\nstderr:\n%s", out, errOut)
	}
}

// The repair's warning is a line on stderr and never anything a mode prints: standard output is byte-equal, in print, JSON
// and RPC mode, between a start that repaired the extracted copy and a start that found it clean. The two runs share one home.
func TestPigletBinaryRepairWarnsOnStderrAndLeavesStdoutAlone(t *testing.T) {
	execPath, err := exec.CommandContext(t.Context(), "node", "-p", "process.execPath").Output()
	if err != nil {
		t.Fatalf("node is required to run the Binary's TypeScript extension: %v", err)
	}
	withNode := filepath.Dir(strings.TrimSpace(string(execPath)))
	root := t.TempDir()
	isolateBuildHome(t, root)
	binary := filepath.Join(root, nativeBinaryName("pig-nodebin"))
	piglet := writeNodePiglet(t, root, "")
	var stdout, stderr strings.Builder
	if code := runBuild([]string{piglet, "--format", "binary", "--builder", "native", "--out", binary}, &stdout, &stderr); code != 0 {
		t.Fatalf("pig piglet build exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	for name, mode := range map[string]struct {
		stdin string
		args  []string
	}{
		"print": {"", []string{"-p", "What is 20+22?"}},
		"json":  {"", []string{"--mode", "json", "-p", "Run: extension echo hello"}},
		"rpc":   {`{"id":"1","type":"get_commands"}` + "\n", []string{"--mode", "rpc"}},
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			run := func() (string, string) {
				t.Helper()
				code, out, errOut, work := runNodeBinaryAt(t, binary, home, mode.stdin, withNode, mode.args...)
				if code != 0 {
					t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
				}
				return replaceWorkPath(out, work), errOut
			}
			run() // extracts
			clean, cleanErr := run()
			if strings.Contains(cleanErr, "extracted them again") {
				t.Fatalf("a clean start warned:\n%s", cleanErr)
			}
			entries, err := filepath.Glob(filepath.Join(home, ".pig", "piglet-binary-cells", "node", "*", "sources", "greeter", "lib", "echo.ts"))
			if err != nil || len(entries) != 1 {
				t.Fatalf("extracted echo.ts = %v, %v; want one", entries, err)
			}
			if err := os.WriteFile(entries[0], []byte("export const echo = () => 'EDITED';\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			repaired, repairedErr := run()
			if strings.Count(repairedErr, "extracted them again") != 1 {
				t.Fatalf("the repair is not announced once on stderr:\n%s", repairedErr)
			}
			if strings.Contains(repaired, "extracted them again") || strings.Contains(repaired, "pig: warning") {
				t.Fatalf("the warning reached standard output:\n%s", repaired)
			}
			if normalizeRunOutput(repaired) != normalizeRunOutput(clean) {
				t.Fatalf("standard output differs between a clean and a repaired start\nclean:\n%s\nrepaired:\n%s", clean, repaired)
			}
		})
	}
}

// replaceWorkPath replaces every encoding of the run's working directory in stdout: raw, JSON-escaped (Windows
// backslashes doubled inside JSON strings) and forward-slash normalized (Windows paths inside the system prompt's
// <cwd> block).
func replaceWorkPath(out, work string) string {
	if encoded, err := json.Marshal(work); err == nil && len(encoded) >= 2 {
		out = strings.ReplaceAll(out, string(encoded[1:len(encoded)-1]), "<work>")
	}
	if slash := strings.ReplaceAll(work, "\\", "/"); slash != work {
		out = strings.ReplaceAll(out, slash, "<work>")
	}
	return strings.ReplaceAll(out, work, "<work>")
}

// TestReplaceWorkPathNormalizesWindowsEncodings proves replaceWorkPath matches the three forms JSON mode emits for a
// Windows working directory (JSON-escaped, slash-normalized, and raw) while leaving any unrelated output intact.
func TestReplaceWorkPathNormalizesWindowsEncodings(t *testing.T) {
	cleanWork := `C:\Users\RUNNER~1\AppData\Local\Temp\pig-plb\003`
	repairedWork := `C:\Users\RUNNER~1\AppData\Local\Temp\pig-plb\004`
	clean := `{"type":"session","id":"a","timestamp":"2026-11-21T06:11:13.022Z","cwd":"C:\\Users\\RUNNER~1\\AppData\\Local\\Temp\\pig-plb\\003"}` + "\n" +
		`{"type":"message_start","message":{"sections":{"cwd":"<cwd>\nC:/Users/RUNNER~1/AppData/Local/Temp/pig-plb/003\n</cwd>"},"timestamp":1795241473088}}`
	repaired := `{"type":"session","id":"b","timestamp":"2026-11-21T06:11:13.556Z","cwd":"C:\\Users\\RUNNER~1\\AppData\\Local\\Temp\\pig-plb\\004"}` + "\n" +
		`{"type":"message_start","message":{"sections":{"cwd":"<cwd>\nC:/Users/RUNNER~1/AppData/Local/Temp/pig-plb/004\n</cwd>"},"timestamp":1795241473613}}`
	if gotC, gotR := normalizeRunOutput(replaceWorkPath(clean, cleanWork)), normalizeRunOutput(replaceWorkPath(repaired, repairedWork)); gotC != gotR {
		t.Fatalf("normalized Windows JSON outputs still differ:\nclean:\n%s\nrepaired:\n%s", gotC, gotR)
	}
	// A warning leaked to stdout must still fail the equality check.
	leaked := "pig: warning: extracted them again\n" + repaired
	if normalizeRunOutput(replaceWorkPath(leaked, repairedWork)) == normalizeRunOutput(replaceWorkPath(clean, cleanWork)) {
		t.Fatal("leaked warning on stdout was masked")
	}
}

// normalizeRunOutput blanks what differs between two runs of the same input: timestamps, durations and generated ids.
func normalizeRunOutput(out string) string {
	return runVaryingValues.ReplaceAllString(out, "$1<varies>")
}

var runVaryingValues = regexp.MustCompile(`("(?:timestamp|id|toolCallId|sessionId|duration|durationMs|startedAt|createdAt|responseId)":)(?:"[^"]*"|\d+(?:\.\d+)?)`)

// nativeBinaryName is the --out name of a native Piglet Binary: Windows rejects an output without an executable extension
// (nativeArtifactPath), so a build test that names its output adds .exe there.
func nativeBinaryName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
