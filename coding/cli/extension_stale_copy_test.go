package cli

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func writeCopy(t *testing.T, dir string, files map[string]string, modified time.Time) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, name)
		writeStartupFixtureFile(t, path, content)
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatal(err)
		}
	}
}

func conflictBetween(owner, path string) codingagent.ToolConflict {
	return codingagent.ToolConflict{Path: path, Owner: owner, Tool: "ask"}
}

// Two copies of one install that register the same tool: the diagnostic names
// both directories and the one that is older.
func TestStaleCopyDiagnosticNamesBothPathsAndTheOlderCopy(t *testing.T) {
	root := t.TempDir()
	current, stale := filepath.Join(root, "kinsy-utils", "ask"), filepath.Join(root, "kinsy-utils-spec-pig-revision", "ask")
	files := map[string]string{"extension.go": "package ask\n", "go.mod": "module example.com/ask\n", "README.md": "ask\n"}
	writeCopy(t, current, files, time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC))
	writeCopy(t, stale, files, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	configs := []subprocess.ExtConfig{{Name: "ask", Source: current}, {Name: "ask", Source: stale}}

	for name, conflict := range map[string]codingagent.ToolConflict{
		"the stale copy loads second": conflictBetween(current, stale),
		"the stale copy loads first":  conflictBetween(stale, current),
	} {
		t.Run(name, func(t *testing.T) {
			diagnostics := staleCopyDiagnostics([]codingagent.ToolConflict{conflict}, configs)
			if len(diagnostics) != 1 || diagnostics[0].Type != "warning" {
				t.Fatalf("diagnostics = %+v", diagnostics)
			}
			message := diagnostics[0].Message
			for _, want := range []string{current, stale, `register the tool "ask"`, "Remove the stale copy, " + stale + ", and keep " + current + "."} {
				if !strings.Contains(message, want) {
					t.Errorf("message lacks %q:\n%s", want, message)
				}
			}
		})
	}
}

// Different extensions that happen to register one tool name are Pi's plain conflict.
func TestStaleCopyDiagnosticIgnoresDifferentExtensions(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "a"), filepath.Join(root, "b")
	now := time.Now()
	writeCopy(t, first, map[string]string{"extension.go": "package a\n// one\n", "go.mod": "module example.com/a\n"}, now)
	writeCopy(t, second, map[string]string{"main.go": "package b\n// two\n", "go.mod": "module example.com/b\n"}, now)
	configs := []subprocess.ExtConfig{{Name: "a", Source: first}, {Name: "b", Source: second}}
	if got := staleCopyDiagnostics([]codingagent.ToolConflict{conflictBetween(first, second)}, configs); len(got) != 0 {
		t.Fatalf("diagnostics = %+v", got)
	}
	// A conflict inside one directory is not a pair of copies.
	if got := staleCopyDiagnostics([]codingagent.ToolConflict{conflictBetween(first, first)}, configs); len(got) != 0 {
		t.Fatalf("diagnostics = %+v", got)
	}
}

// Startup prints the stale-copy warning next to Pi's conflict error, and the
// exit status and the error line stay as Pi's.
func TestStartupNamesTheStaleCopyOfADuplicatedExtension(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the extension fixture: %v", err)
	}
	binary := buildPigBinaryForSignalTest(t)
	home := t.TempDir()
	agentDir, cwd := filepath.Join(home, "agent"), filepath.Join(home, "cwd")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	const body = `pi.registerTool({ name: "ask_user", label: "Ask", description: "ask", parameters: { type: "object", properties: {} }, execute: async () => ({ content: [{ type: "text", text: "ok" }] }) });`
	var roots []string
	for name, modified := range map[string]time.Time{"ask-new": time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), "ask-old": time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)} {
		pkg := filepath.Join(home, name)
		writeCopy(t, pkg, map[string]string{
			"package.json":       `{"name":"ask","pi":{"extensions":["extensions/ask.mjs"]}}`,
			"extensions/ask.mjs": "export default function (pi) { " + body + " }\n",
		}, modified)
		roots = append(roots, pkg)
	}
	// json.Marshal escapes the backslashes of a Windows path; interpolating the path would write invalid JSON there.
	settings, err := json.Marshal(map[string]any{"packages": roots})
	if err != nil {
		t.Fatal(err)
	}
	writeStartupFixtureFile(t, filepath.Join(agentDir, "settings.json"), string(settings))
	run := runRPCStartup(t, binary, home, agentDir, cwd)
	if exitErr, ok := errors.AsType[*exec.ExitError](run.err); !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("startup error = %v\n%s", run.err, run.stderr)
	}
	var newer, older string
	for _, root := range roots {
		if strings.HasSuffix(root, "ask-new") {
			newer = root
		} else {
			older = root
		}
	}
	for _, want := range []string{"are copies of one extension", "Remove the stale copy, " + older + ", and keep " + newer + ".", `Tool "ask_user" conflicts with`, `Hint: Start without extensions using "pig -ne".`} {
		if !strings.Contains(run.stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, run.stderr)
		}
	}
}
