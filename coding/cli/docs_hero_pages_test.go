package cli

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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

// stalledStderrPipe is the stderr of a child process that a test can stall: stall fills the pipe, so the child's next write to its stderr blocks until the reader runs again; release starts it after a delay. finish waits for the reader to reach the end of the pipe. What the child wrote reaches out. The write end is the child's stderr, so exec has put it in blocking mode and a deadline cannot bound a write: the filler is a goroutine of one-byte writes, and the pipe is full when it makes no more progress.
type stalledStderrPipe struct {
	t        *testing.T
	r, w     *os.File
	out      *lockedBuffer
	resume   chan struct{}
	done     chan struct{}
	resumed  sync.Once
	fillStop atomic.Bool
	fillDone sync.WaitGroup
	filled   atomic.Int64
}

func newStalledStderrPipe(t *testing.T, out *lockedBuffer) *stalledStderrPipe {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	p := &stalledStderrPipe{t: t, r: r, w: w, out: out, resume: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(p.done)
		<-p.resume
		_, _ = io.Copy(out, r)
	}()
	t.Cleanup(func() {
		p.release(0)
		p.stopFilling()
		_ = w.Close()
		<-p.done
		_ = r.Close()
	})
	return p
}

// stall fills the pipe to its capacity while the reader is parked on p.resume.
func (p *stalledStderrPipe) stall() {
	p.t.Helper()
	p.fillDone.Go(func() {
		chunk := make([]byte, 4096)
		for i := range chunk {
			chunk[i] = '.'
		}
		for !p.fillStop.Load() {
			if _, err := p.w.Write(chunk); err != nil {
				return
			}
			p.filled.Add(int64(len(chunk)))
		}
	})
	// Full: three checks in a row see no progress.
	for last, still := int64(-1), 0; still < 3; {
		time.Sleep(100 * time.Millisecond)
		if now := p.filled.Load(); now == last {
			still++
		} else {
			last, still = now, 0
		}
	}
}

// release lets the reader run after delay.
func (p *stalledStderrPipe) release(delay time.Duration) {
	time.AfterFunc(delay, func() { p.resumed.Do(func() { close(p.resume) }) })
}

// stopFilling ends the filler, which needs the reader to run for the write it is blocked in.
func (p *stalledStderrPipe) stopFilling() {
	p.fillStop.Store(true)
	p.fillDone.Wait()
}

func (p *stalledStderrPipe) finish() {
	p.t.Helper()
	p.stopFilling()
	_ = p.w.Close()
	<-p.done
}

// TestDocsEventBusPage runs the page's Go extension with each of its two answering extensions, and alone.
func TestDocsEventBusPage(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and starts Go, Node and Python extensions")
	}
	page, examples := docsHeroExamples(t, docsEventBusPagePath, "asker/extension.go", "geo.mjs", "geo.py", "waiter/extension.go", "slow-geo.mjs", "slow-geo.py")
	binary := buildPigBinaryForSignalTest(t)
	dir := t.TempDir()
	env := append(listenEnv(t, dir), "PIG_OFFLINE=1")
	asker := docsHeroGoExtension(t, binary, env, dir, "asker", examples["asker/extension.go"])
	// The table's Go calls of an answering extension: a listener that the factory subscribes with ext.Events(), and an
	// emit from that listener with ctx.Events().
	geoGo := docsHeroGoExtension(t, binary, env, dir, "geogo", "package geogo\n\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\n\nfunc Extension() *sdk.Extension {\n\text := sdk.New(\"geogo\")\n\tif _, err := ext.Events().On(\"geo:locate\", func(ctx sdk.Context, data any) error {\n\t\trequest, _ := data.(map[string]any)\n\t\treturn ctx.Events().Emit(\"geo:found\", map[string]any{\"id\": request[\"id\"], \"name\": request[\"name\"], \"lat\": 48.86, \"lon\": 2.35})\n\t}); err != nil {\n\t\tpanic(err)\n\t}\n\treturn ext\n}\n")
	waiter := docsHeroGoExtension(t, binary, env, dir, "waiter", examples["waiter/extension.go"])
	paths := map[string]string{"asker": asker, "geogo": geoGo, "waiter": waiter}
	for name, source := range map[string]string{
		"geo.mjs": examples["geo.mjs"],
		"geo.py":  examples["geo.py"],
		// The page's second example: answers that come after emit returned, to an asker that waits.
		"slow-geo.mjs": examples["slow-geo.mjs"],
		"slow-geo.py":  examples["slow-geo.py"],
		// The page's rule for a listener that fails: its error is reported as an extension error, and the emitter and the listeners after it do not see it.
		"failing.mjs": "export default function (pi) {\n  pi.events.on(\"geo:locate\", () => {\n    throw new Error(\"failing listener\");\n  });\n}\n",
		// The page's rule for an async listener that fails: it is reported when it fails, after emit returned.
		"asyncfailing.mjs": "export default function (pi) {\n  pi.events.on(\"geo:locate\", async () => {\n    await new Promise((resolve) => setTimeout(resolve, 1500));\n    throw new Error(\"async failing listener\");\n  });\n}\n",
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
		stalled    bool
	}{
		{"a Node extension answers", []string{"asker", "geo.mjs"}, found, false},
		{"a Python extension answers", []string{"asker", "geo.py"}, found, false},
		{"a Go extension answers", []string{"asker", "geogo"}, found, false},
		{"the answering extension is loaded first", []string{"geo.mjs", "asker"}, found, false},
		{"no extension listens", []string{"asker"}, "warning: no extension answers geo:locate", false},
		{"a listener before the answering one fails", []string{"asker", "failing.mjs", "geo.mjs"}, found, false},
		{"a listener's report is delivered while stderr stalls as pig ends", []string{"asker", "failing.mjs", "geo.mjs"}, found, true},
		{"an async listener that fails is reported after the command answered", []string{"asker", "asyncfailing.mjs", "geo.mjs"}, found, false},
		{"an async listener replies after emit returned", []string{"asker", "late.mjs"}, "warning: no extension answers geo:locate", false},
		{"a waiting asker hears a Node listener that answers after an await", []string{"waiter", "slow-geo.mjs"}, found, false},
		{"a waiting asker hears a Python extension that answers from a thread", []string{"waiter", "slow-geo.py"}, found, false},
		{"a waiting asker gives up when no extension answers", []string{"waiter"}, "warning: no extension answered geo:locate in 2 s", false},
		{"a reply to another request is not taken", []string{"asker", "stranger.mjs"}, "warning: no extension answers geo:locate", false},
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
			var pipe *stalledStderrPipe
			if row.stalled {
				pipe = newStalledStderrPipe(t, stderr)
				cmd.Stderr = pipe.w
			} else {
				cmd.Stderr = stderr
			}
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
			if pipe != nil {
				// Past the start the pig process writes nothing to stderr: its next write would block.
				session.send(`{"id":"state","type":"get_state"}`)
				session.until("the response to get_state", func(record map[string]any) bool { return record["type"] == "response" && record["id"] == "state" })
				pipe.stall()
			}
			notes := docsHeroCommand(t, session, "locate", "/locate Paris")
			t.Logf("/locate Paris answered after %s: %q", time.Since(started).Round(time.Millisecond), notes)
			if slices.Contains(row.extensions, "asyncfailing.mjs") {
				// The rejection comes 1.5 s after the emit, long after the response: the session must still be open when it does.
				session.until("the async listener's extension_error", func(record map[string]any) bool { return record["type"] == "extension_error" })
			}
			if pipe != nil {
				// A stalled stderr must neither hold up the report, which RPC mode writes to stdout as an event, nor keep pig from ending: the pipe has no reader until after pig was told to end.
				pipe.release(150 * time.Millisecond)
			}
			lines := session.end()
			if pipe != nil {
				pipe.finish()
			}
			if !slices.Equal(notes, []string{row.want}) {
				t.Fatalf("/locate Paris notified %q, want %q", notes, row.want)
			}
			if _, message, _ := strings.Cut(row.want, ": "); !strings.Contains(page, message) {
				t.Fatalf("%s does not show %q, which /locate Paris shows", docsEventBusPagePath, message)
			}
			// Pi 18e99051be24 (loader.ts `on`, runner.ts constructor, rpc-mode.ts:355-356 `onError: (err) => output({ type: "extension_error", extensionPath, event, error })`):
			// a failing listener is an extension error, which RPC mode writes as an `extension_error` event carrying the extension's path, the event `events:<channel>` and
			// the message, and no stack. A listener that throws is reported inside the emit, so its event comes before the reply of the listener after it and before the
			// command's response; an async listener that rejects after its first await is reported when it rejects, after the response (probed on Pi with the commit).
			// The bus's own `Event handler error (...)` line is no longer written for a pi.events handler.
			if strings.Contains(stderr.String(), "Event handler error") {
				t.Fatalf("stderr holds the bus's report of a failing listener, which the extension-error route replaced:\n%s", stderr.String())
			}
			failing, message := "", ""
			for name, text := range map[string]string{"failing.mjs": "failing listener", "asyncfailing.mjs": "async failing listener"} {
				if slices.Contains(row.extensions, name) {
					failing, message = name, text
				}
			}
			var reports []map[string]any
			reportAt, answerAt, responseAt := -1, -1, -1
			for i, line := range lines {
				var record map[string]any
				if json.Unmarshal([]byte(line), &record) != nil {
					continue
				}
				if record["type"] == "extension_error" {
					reports = append(reports, record)
					reportAt = i
				}
				if text, _ := record["message"].(string); record["type"] == "extension_ui_request" && record["method"] == "notify" && answerAt < 0 && strings.Contains(text, "Paris is at") {
					answerAt = i
				}
				if record["type"] == "response" && record["id"] == "locate" {
					responseAt = i
				}
			}
			if failing == "" {
				if len(reports) != 0 {
					t.Fatalf("an extension_error event for a session without a failing listener: %v", reports)
				}
				return
			}
			if len(reports) != 1 {
				t.Fatalf("got %d extension_error events, want one for the failing listener:\n%s", len(reports), strings.Join(lines, "\n"))
			}
			report := reports[0]
			path, _ := report["extensionPath"].(string)
			if filepath.Base(path) != failing || report["event"] != "events:geo:locate" || report["error"] != message {
				t.Fatalf("extension_error = %v, want the path of %s, event events:geo:locate and the message %q", report, failing, message)
			}
			if _, hasStack := report["stack"]; hasStack {
				t.Fatalf("extension_error carries a stack, which Pi's RPC event does not: %v", report)
			}
			if answerAt < 0 || responseAt < 0 {
				t.Fatalf("no reply notification or no response among the lines:\n%s", strings.Join(lines, "\n"))
			}
			if failing == "failing.mjs" && (reportAt > answerAt || reportAt > responseAt) {
				t.Fatalf("the throwing listener's extension_error (line %d) came after the answering listener's reply (line %d) or the response (line %d)", reportAt, answerAt, responseAt)
			}
			if failing == "asyncfailing.mjs" && reportAt < responseAt {
				t.Fatalf("the async listener's extension_error (line %d) came before the response (line %d), though it fails after emit returned", reportAt, responseAt)
			}
		})
	}
}
