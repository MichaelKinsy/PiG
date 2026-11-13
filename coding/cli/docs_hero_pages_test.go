package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// The tests in this file and in docs_go_hot_reload_test.go run what two documentation pages teach: docs/site/docs/event-bus.md
// (extensions in different languages that call each other through pi.events) and docs/site/docs/go-hot-reload.md (an edited Go
// extension and /reload). They take each page's complete extensions out of its code fences and load them in the real binary.
// An example that a page breaks fails here.

const (
	docsEventBusPagePath    = "../../docs/site/docs/event-bus.md"
	docsGoHotReloadPagePath = "../../docs/site/docs/go-hot-reload.md"
)

var (
	docsHeroFence       = regexp.MustCompile("(?ms)^```(?:go|js|python)\n(.*?)\n```$")
	docsHeroExampleName = regexp.MustCompile(`^(?://|#) ([a-z./-]+): `)
)

// docsHeroExamples returns the page at pagePath and its complete extensions by the file name their first line gives.
func docsHeroExamples(t *testing.T, pagePath string, names ...string) (page string, examples map[string]string) {
	t.Helper()
	raw, err := os.ReadFile(pagePath)
	if err != nil {
		t.Fatal(err)
	}
	page, examples = string(raw), map[string]string{}
	for _, m := range docsHeroFence.FindAllStringSubmatch(page, -1) {
		if name := docsHeroExampleName.FindStringSubmatch(m[1]); name != nil {
			examples[name[1]] = m[1] + "\n"
		}
	}
	for _, name := range names {
		if examples[name] == "" {
			t.Fatalf("%s has no code fence whose first line names %s", pagePath, name)
		}
	}
	return page, examples
}

// docsHeroGoExtension creates a Go extension as the pages tell the reader to: `pig extension init <name> --lang go` in dir, then
// extension.go replaced with source. It returns the extension's directory.
func docsHeroGoExtension(t *testing.T, binary string, env []string, dir, name, source string) string {
	t.Helper()
	cmd := exec.Command(binary, "extension", "init", name, "--lang", "go")
	cmd.Dir, cmd.Env = dir, append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pig extension init %s --lang go: %v\n%s", name, err, out)
	}
	extension := filepath.Join(dir, name)
	if err := os.WriteFile(filepath.Join(extension, "extension.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return extension
}

// docsHeroCommand runs a slash command in an RPC session and returns the notifications the session wrote before the command's
// response, each as "<level>: <message>".
func docsHeroCommand(t *testing.T, session *rpcLines, id, command string) []string {
	t.Helper()
	line, err := json.Marshal(map[string]string{"id": id, "type": "prompt", "message": command})
	if err != nil {
		t.Fatal(err)
	}
	session.send(string(line))
	notes := []string{}
	_, response := session.until("the response to "+command, func(record map[string]any) bool {
		if record["type"] == "extension_ui_request" && record["method"] == "notify" {
			level, _ := record["notifyType"].(string)
			message, _ := record["message"].(string)
			notes = append(notes, level+": "+message)
		}
		return record["type"] == "response" && record["id"] == id
	})
	if response["success"] != true {
		t.Fatalf("%s was refused: %v", command, response)
	}
	return notes
}

// TestDocsEventBusPage runs the page's Go extension with each of its two answering extensions, and alone.
func TestDocsEventBusPage(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and starts Go, Node and Python extensions")
	}
	page, examples := docsHeroExamples(t, docsEventBusPagePath, "asker/extension.go", "geo.mjs", "geo.py")
	binary := buildPigBinaryForSignalTest(t)
	dir := t.TempDir()
	env := append(listenEnv(t, dir), "PIG_OFFLINE=1")
	asker := docsHeroGoExtension(t, binary, env, dir, "asker", examples["asker/extension.go"])
	// The table's Go calls of an answering extension: a listener that the factory subscribes with ext.Events(), and an
	// emit from that listener with ctx.Events().
	geoGo := docsHeroGoExtension(t, binary, env, dir, "geogo", "package geogo\n\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\n\nfunc Extension() *sdk.Extension {\n\text := sdk.New(\"geogo\")\n\tif _, err := ext.Events().On(\"geo:locate\", func(ctx sdk.Context, data any) error {\n\t\trequest, _ := data.(map[string]any)\n\t\treturn ctx.Events().Emit(\"geo:found\", map[string]any{\"id\": request[\"id\"], \"name\": request[\"name\"], \"lat\": 48.86, \"lon\": 2.35})\n\t}); err != nil {\n\t\tpanic(err)\n\t}\n\treturn ext\n}\n")
	paths := map[string]string{"asker": asker, "geogo": geoGo}
	for name, source := range map[string]string{
		"geo.mjs": examples["geo.mjs"],
		"geo.py":  examples["geo.py"],
		// The page's rule for a listener that fails: its error is reported, and the emitter and the listeners after it do not see it.
		"failing.mjs": "export default function (pi) {\n  pi.events.on(\"geo:locate\", () => {\n    throw new Error(\"failing listener\");\n  });\n}\n",
		// The page's rule for an async listener: emit returns at the listener's first await, so a reply after it comes too late for an
		// asker that does not wait.
		"late.mjs": "export default function (pi) {\n  pi.events.on(\"geo:locate\", async (request) => {\n    await new Promise((resolve) => setTimeout(resolve, 300));\n    pi.events.emit(\"geo:found\", { id: request.id, name: request.name, lat: 48.86, lon: 2.35 });\n  });\n}\n",
		// The page's reason for the id: a reply to another request reaches every listener of the channel.
		"stranger.mjs": "export default function (pi) {\n  pi.events.on(\"geo:locate\", (request) => {\n    pi.events.emit(\"geo:found\", { id: \"another request\", name: request.name, lat: 1, lon: 1 });\n  });\n}\n",
	} {
		paths[name] = filepath.Join(dir, name)
		if err := os.WriteFile(paths[name], []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const found = "info: Paris is at 48.86, 2.35"
	for _, row := range []struct {
		name       string
		extensions []string
		want       string
	}{
		{"a Node extension answers", []string{"asker", "geo.mjs"}, found},
		{"a Python extension answers", []string{"asker", "geo.py"}, found},
		{"a Go extension answers", []string{"asker", "geogo"}, found},
		{"the answering extension is loaded first", []string{"geo.mjs", "asker"}, found},
		{"no extension listens", []string{"asker"}, "warning: no extension answers geo:locate"},
		{"a listener before the answering one fails", []string{"asker", "failing.mjs", "geo.mjs"}, found},
		{"an async listener replies after emit returned", []string{"asker", "late.mjs"}, "warning: no extension answers geo:locate"},
		{"a reply to another request is not taken", []string{"asker", "stranger.mjs"}, "warning: no extension answers geo:locate"},
	} {
		t.Run(row.name, func(t *testing.T) {
			args := []string{"--mode", "rpc", "--model", "test-faux/echo", "--no-session", "--no-extensions"}
			for _, name := range row.extensions {
				args = append(args, "-e", paths[name])
			}
			cmd := exec.Command(binary, args...)
			cmd.Dir, cmd.Env = dir, append(os.Environ(), env...)
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			stderr := &lockedBuffer{}
			cmd.Stderr = stderr
			cmd.WaitDelay = 10 * time.Second
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			})
			session := newRPCLines(t, stdin, stdout, stderr)
			// The first build of a Go extension takes longer than an RPC answer.
			session.budget = probeWait(t, 5*time.Minute)
			started := time.Now()
			notes := docsHeroCommand(t, session, "locate", "/locate Paris")
			t.Logf("/locate Paris answered after %s: %q", time.Since(started).Round(time.Millisecond), notes)
			session.end()
			if !slices.Equal(notes, []string{row.want}) {
				t.Fatalf("/locate Paris notified %q, want %q", notes, row.want)
			}
			if _, message, _ := strings.Cut(row.want, ": "); !strings.Contains(page, message) {
				t.Fatalf("%s does not show %q, which /locate Paris shows", docsEventBusPagePath, message)
			}
			if report := "Event handler error (geo:locate)"; slices.Contains(row.extensions, "failing.mjs") != strings.Contains(stderr.String(), report) {
				t.Fatalf("stderr holds %q: %v, want it for the failing listener only:\n%s", report, strings.Contains(stderr.String(), report), stderr.String())
			}
		})
	}
}
