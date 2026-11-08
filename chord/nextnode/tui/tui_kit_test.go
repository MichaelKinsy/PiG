package tui

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/nextnode/app"
	chordhttp "github.com/MichaelKinsy/PiG/chord/nextnode/http"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Test support for the terminal surface (Pi's test/node/tui/support.ts): a kit app with the terminal surface and the fixture's
// entry, a Node new enough for the sandbox (26.5.1 or later: `CHORD_TUI_NODE`, else `node26`, else `node` on the PATH; the tests
// that need it are skipped without), and launchers run in this process with their terminal on pipes.
//
// Forced differences (D602). Pi runs the launcher as a child process (`drive.ts`) on a pipe or a pseudo-terminal; the Go launcher
// is a package, run here on a Terminal of pipes, its output a buffer a test waits on (no polling).

// clientRoot is the vendored client tree's Pi sources (surfaces/client/pi/chord).
var clientRoot = filepath.Join("..", "surfaces", "client", "pi", "chord")

// fixture is the vendored test fixture at path under test/node/fixtures.
func fixture(t testing.TB, path ...string) string {
	t.Helper()
	absolute, err := filepath.Abs(filepath.Join(append([]string{clientRoot, "test", "node", "fixtures"}, path...)...))
	if err != nil {
		t.Fatal(err)
	}
	return absolute
}

// linesPlugin is a terminal plugin contributing `Lines` the line text, how many of the sandbox's escapes it tried at its
// evaluation (`escapes.ts`, copied into it) and the ones that got through (`escaped=none` when none did).
func linesPlugin(t testing.TB, text string) map[string]string {
	t.Helper()
	escapes, err := os.ReadFile(fixture(t, "tui", "escapes.ts"))
	if err != nil {
		t.Fatal(err)
	}
	quoted, err := jsQuote(text)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"escapes.ts": string(escapes),
		"tui.ts": strings.Join([]string{
			`import { defineFacet } from "@test/app/v1";`,
			`import { Lines } from "@test/app/v1/tui";`,
			`import { escapes } from "./escapes.ts";`,
			`const found = Object.entries(escapes());`,
			`const escaped = found.filter(([, outcome]) => outcome === "ok").map(([name]) => name).join(", ") || "none";`,
			`export default defineFacet((_host, self) => self.contribute(Lines, ` + quoted + ` + " tried=" + found.length + " escaped=" + escaped));`,
		}, "\n"),
	}
}

// started is a started test app.
type started struct {
	app  *app.App
	port int
	root string
}

var background = context.Background()

// tuiApp is a kit app with the terminal surface and the lines plugin saying text, in a temporary data directory and plugins root,
// with the tests' API (the app package's testdata/api/v1.mjs).
func tuiApp(t testing.TB, text string) started {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "plugins")
	api, err := filepath.Abs(filepath.Join("..", "app", "testdata", "api", "v1.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	tuiAPI := fixture(t, "tui", "api.ts")
	writePlugin(t, root, "lines", "@test/lines", linesPlugin(t, text))
	options := app.AppOptions{
		Name: "chord-test", DataDir: filepath.Join(base, "data"), Root: root,
		Listen: &chordhttp.ListenAddress{Host: "127.0.0.1", Port: 0},
		API:    app.AppAPI{Name: "@test/app", Versions: []string{"v1"}, Modules: plugins.HostPackages{"@test/app/v1": api}},
		Kinds: []app.AppKind{
			{Name: "server", Platform: plugins.PlatformNode},
			{Name: "tui", Platform: plugins.PlatformNode, Packages: plugins.HostPackages{"@test/app/v1/tui": tuiAPI}},
		},
		Principal: app.AppPrincipals{Server: delta.JsonObjectOf("kind", "server"), Owner: delta.JsonObjectOf("kind", "owner")},
		Tui:       &app.AppTui{Entry: fixture(t, "tui", "main.ts"), Conditions: []string{"source"}},
		OnError:   func(err app.AppError) { t.Logf("app: %v", err) },
	}
	started, err := app.StartServer(options, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = started.Close(background) })
	var port int
	_, _ = fmt.Sscanf(started.OwnerLink(), "http://127.0.0.1:%d/", &port)
	return started2(started, port, root)
}

func started2(a *app.App, port int, root string) started {
	return started{app: a, port: port, root: root}
}

// writePlugin writes the plugin dir of root: its package.json and files.
func writePlugin(t testing.TB, root, dir, name string, files map[string]string) {
	t.Helper()
	all := map[string]string{"package.json": fmt.Sprintf(`{"name":%q,"private":true,"type":"module"}`, name)}
	for file, text := range files {
		all[file] = text
	}
	for file, text := range all {
		path := filepath.Join(root, dir, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// outputBuffer is what a launcher printed, a test waiting on it woken by each write.
type outputBuffer struct {
	mu   sync.Mutex
	text string
	wake chan struct{}
}

func newOutputBuffer() *outputBuffer { return &outputBuffer{wake: make(chan struct{})} }

func (o *outputBuffer) Write(data []byte) (int, error) {
	o.mu.Lock()
	o.text += string(data)
	wake := o.wake
	o.wake = make(chan struct{})
	o.mu.Unlock()
	close(wake)
	return len(data), nil
}

func (o *outputBuffer) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.text
}

// pipeTerminal is a terminal on pipes: 80x24, not a TTY.
type pipeTerminal struct {
	in     *io.PipeReader
	typed  *io.PipeWriter
	output *outputBuffer
}

func newPipeTerminal() *pipeTerminal {
	in, typed := io.Pipe()
	return &pipeTerminal{in: in, typed: typed, output: newOutputBuffer()}
}

func (p *pipeTerminal) Input() io.Reader              { return p.in }
func (p *pipeTerminal) Output() io.Writer             { return p.output }
func (p *pipeTerminal) Columns() int                  { return 80 }
func (p *pipeTerminal) Rows() int                     { return 24 }
func (p *pipeTerminal) IsTTY() bool                   { return false }
func (p *pipeTerminal) SetRaw(bool) error             { return nil }
func (p *pipeTerminal) OnResize(func()) (stop func()) { return func() {} }

// launched is a launcher running in this process on a terminal of pipes.
type launched struct {
	t        testing.TB
	terminal *pipeTerminal
	running  *RunningTerminal
	code     chan int
}

// launch starts a launcher against port with credentials.
func launch(t testing.TB, port int, credentials TerminalCredentials) *launched {
	t.Helper()
	terminal := newPipeTerminal()
	options := TerminalOptions{
		URL: "http://127.0.0.1:" + strconv.Itoa(port), Credentials: credentials, Terminal: terminal, Colors: "truecolor", Node: tuiNode(t),
		Opener: func(target string) error {
			_, _ = terminal.output.Write([]byte("(opener " + target + ")\r\n"))
			return nil
		},
	}
	running, err := StartTerminal(background, options)
	if err != nil {
		t.Fatal(err)
	}
	l := &launched{t: t, terminal: terminal, running: running, code: make(chan int, 1)}
	go func() { l.code <- <-running.Exited() }()
	t.Cleanup(func() {
		running.Stop()
		_ = terminal.typed.Close()
	})
	return l
}

// tokenFile launches with the app's owner token file.
func launchWithToken(t testing.TB, s started) *launched {
	t.Helper()
	return launch(t, s.port, TerminalCredentials{Kind: CredentialsFile, Path: s.app.OwnerTokenFile})
}

// typeKeys sends keys to the launcher's terminal.
func (l *launched) typeKeys(keys string) {
	l.t.Helper()
	if _, err := l.terminal.typed.Write([]byte(keys)); err != nil {
		l.t.Fatal(err)
	}
}

// answerDelay outlasts the launcher's prompt guard: keys typed in its first 300 ms after a prompt appears are ignored.
const answerDelay = 350 * time.Millisecond

// answer answers the prompt on screen with key once its keys count (the launcher ignores them for 300 ms).
func (l *launched) answer(key string) {
	l.t.Helper()
	time.Sleep(answerDelay)
	l.typeKeys(key)
}

// waitFor waits until the output from offset from on matches pattern (20 s at most): the match.
func (l *launched) waitFor(pattern string, from ...int) []string {
	l.t.Helper()
	re := regexp.MustCompile(pattern)
	offset := 0
	if len(from) > 0 {
		offset = from[0]
	}
	deadline := time.After(20 * time.Second)
	for {
		l.terminal.output.mu.Lock()
		text, wake := l.terminal.output.text, l.terminal.output.wake
		l.terminal.output.mu.Unlock()
		if offset <= len(text) {
			if found := re.FindStringSubmatch(text[offset:]); found != nil {
				return found
			}
		}
		select {
		case <-wake:
		case <-deadline:
			l.t.Fatalf("timed out waiting for %s in %q", pattern, text[min(offset, len(text)):])
		}
	}
}

// exited is the launcher's exit code.
func (l *launched) exited() int {
	l.t.Helper()
	select {
	case code := <-l.code:
		return code
	case <-time.After(30 * time.Second):
		l.t.Fatal("the launcher did not exit")
		return -1
	}
}

// output is what the launcher printed.
func (l *launched) output() string { return l.terminal.output.String() }

// httpDo is a request of method to path on port with headers: its status and body.
func httpDo(t testing.TB, port int, method, path string, headers map[string]string) (int, string) {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), method, "http://127.0.0.1:"+strconv.Itoa(port)+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(body)
}

// jsQuote is text as a JavaScript string literal (JSON.stringify).
func jsQuote(text string) (string, error) {
	quoted, err := jsstring.MarshalJSON(text)
	return string(quoted), err
}
