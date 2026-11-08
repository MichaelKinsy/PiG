package boltruntime

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// locatingExtension is an extension that finds its data beside its own modules every way Pi-Bolt's rewrite covers: import.meta.url, import.meta.dirname, createRequire
// from import.meta.url, __dirname and __filename in a CommonJS module, and a computed require() there. It reads at the time its factory runs, so the registration carries
// what the files hold.
func writeLocatingExtension(t *testing.T, dir string) (entry string) {
	t.Helper()
	files := map[string]string{
		"package.json":    `{"name":"locating","type":"module"}`,
		"data/note.txt":   "note-one\n",
		"data/other.txt":  "other\n",
		"data/obj.json":   `{"v": "json-value"}`,
		"data/legacy.txt": "legacy-text\n",
		"lib/legacy.cjs": `const fs = require("fs");
const path = require("path");
const name = ["obj", "json"].join(".");
module.exports = {
  text: fs.readFileSync(path.join(__dirname, "..", "data", "legacy.txt"), "utf8").trim(),
  computed: require(path.join(__dirname, "..", "data", name)).v,
  file: path.basename(__filename),
};
`,
		"lib/skills.ts": `import { readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
export const skills = readdirSync(join(dirname(fileURLToPath(import.meta.url)), "..", "data")).sort().join(",");
`,
		"index.ts": `import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import legacy from "./lib/legacy.cjs";
import { skills } from "./lib/skills.ts";

const here = dirname(fileURLToPath(import.meta.url));
const viaPath = readFileSync(join(here, "data", "note.txt"), "utf8").trim();
const viaUrl = readFileSync(new URL("./data/note.txt", import.meta.url), "utf8").trim();
const viaDir = readFileSync(join(import.meta.dirname, "data", "note.txt"), "utf8").trim();
const viaRequire = createRequire(import.meta.url)("./data/obj.json").v;
const resolved = import.meta.resolve("./data/note.txt").startsWith("file://");

export default function (pi: { registerCommand(name: string, command: unknown): void }) {
  pi.registerCommand("locating", {
    description: ["PROBE", viaPath, viaUrl, viaDir, viaRequire, legacy.text, legacy.computed, legacy.file, skills, String(resolved), here].join("|"),
    handler: async () => {},
  });
}
`,
	}
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "index.ts")
}

// registrationOf starts the compiled host on entry as PiG does and returns the first frame it sends on its socket.
func registrationOf(t *testing.T, executable, entry, work, tree string) string {
	t.Helper()
	// A Unix socket address holds about 104 bytes of path (sun_path: 108 on Linux, 104 on macOS) and a test's temporary directory under a long TMPDIR is longer, so the
	// socket lives in a short directory of its own, as trainRun's does.
	socketDir, err := os.MkdirTemp("", "pbt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	sock := filepath.Join(socketDir, "s")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	cmd := exec.Command(executable, "cli", entry)
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "PIG_EXT_SOCKET="+sock, "PIG_EXT_NAME=locating", "PIG_EXT_CWD="+work, "PIG_TS_RUNTIME_TREE="+tree)
	stdin, _ := cmd.StdinPipe()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	_ = listener.(*net.UnixListener).SetDeadline(time.Now().Add(30 * time.Second))
	conn, err := listener.Accept()
	if err != nil {
		t.Fatalf("the compiled host never connected: %v\n%s", err, stderr.String())
	}
	defer func() { _ = conn.Close() }()
	buf := make([]byte, 256<<10)
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("no registration: %v\n%s", err, stderr.String())
	}
	_ = stdin.Close()
	return string(buf[:n])
}

// A module that finds data beside itself is compiled in, and finds it at the place it is installed: the files are read as the program runs, not when it is built.
func TestBuildCompilesAnExtensionThatLocatesItselfAndItFindsItsFiles(t *testing.T) {
	compiler, tree := realBuildInputs(t)
	extDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entry := writeLocatingExtension(t, extDir)
	work := t.TempDir()
	var log strings.Builder
	result, err := build(context.Background(), buildOptions{
		Compiler: compiler, Platform: platformName(), CPU: "native", Version: "test",
		Request: Request{Key: strings.Repeat("b", 64), Members: []Member{{Name: "locating", Entry: entry}}, RuntimeTree: tree},
		WorkDir: work, Log: &log,
	})
	if err != nil {
		t.Fatalf("build: %v\n%s", err, log.String())
	}
	t.Logf("mode %s, %d bytes, phases %v\n%s", result.Mode, result.Bytes, result.Phases, log.String())
	if !strings.Contains(log.String(), "build from copies") && !strings.Contains(log.String(), "building from copies") {
		t.Errorf("the build did not compile from copies:\n%s", log.String())
	}

	// The staleness inputs are the original files, never the copies in the stage.
	listed := map[string]bool{}
	for _, input := range result.Inputs {
		listed[input.Path] = true
		if strings.HasPrefix(input.Path, work) {
			t.Errorf("input %s is a copy in the build's work directory", input.Path)
		}
	}
	for _, want := range []string{entry, filepath.Join(extDir, "lib", "skills.ts"), filepath.Join(extDir, "lib", "legacy.cjs"), filepath.Join(extDir, "package.json")} {
		if !listed[want] {
			t.Errorf("inputs %v do not list %s", result.Inputs, want)
		}
	}

	frame := registrationOf(t, result.Executable, entry, work, tree)
	want := "PROBE|note-one|note-one|note-one|json-value|legacy-text|json-value|legacy.cjs|legacy.txt,note.txt,obj.json,other.txt|true|" + extDir
	if !strings.Contains(frame, want) {
		t.Fatalf("the compiled extension did not find its files.\nregistration: %s\nwant it to contain: %s", frame, want)
	}

	// The files are read as the program runs: an edit of a data file shows without a build.
	if err := os.WriteFile(filepath.Join(extDir, "data", "note.txt"), []byte("note-two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if frame := registrationOf(t, result.Executable, entry, work, tree); !strings.Contains(frame, "PROBE|note-two|note-two|note-two|") {
		t.Errorf("the compiled extension did not read its data file as it ran: %s", frame)
	}
}

// A file that names its location in a way the rewrite cannot pin and reads files is refused, and the refusal reaches the worker as errPathSensitive.
func TestBuildRefusesWhatTheRewriteCannotPin(t *testing.T) {
	compiler, tree := realBuildInputs(t)
	extDir := t.TempDir()
	entry := writeExtension(t, extDir, `import { readFileSync } from "node:fs";
const meta = import.meta;
const text = readFileSync(new URL("./note.txt", meta.url), "utf8");
export default function (pi: { registerCommand(name: string, command: unknown): void }) { pi.registerCommand("x", { description: text, handler: async () => {} }); }
`)
	_, err := build(context.Background(), buildOptions{
		Compiler: compiler, Platform: platformName(), CPU: "native", Version: "test",
		Request: Request{Key: strings.Repeat("c", 64), Members: []Member{{Name: "x", Entry: entry}}, RuntimeTree: tree},
		WorkDir: t.TempDir(),
	})
	var sensitive errPathSensitive
	if !errors.As(err, &sensitive) || sensitive.finding.File != entry || sensitive.finding.Marker != "import.meta" {
		t.Fatalf("build error = %v, want errPathSensitive for %s", err, entry)
	}
}
