package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// scopeProvider is an OpenAI-compatible endpoint that records the tool names of each request it receives.
type scopeProvider struct {
	mu      sync.Mutex
	offered [][]string
	// recorded receives after a request is recorded; its capacity of one coalesces wakeups for the one waiter.
	recorded chan struct{}
	server   *http.Server
	url      string
}

func newScopeProvider(t *testing.T) *scopeProvider {
	t.Helper()
	provider := &scopeProvider{recorded: make(chan struct{}, 1)}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	provider.url = "http://" + listener.Addr().String() + "/v1"
	provider.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		names := []string{}
		for _, tool := range body.Tools {
			names = append(names, tool.Function.Name)
		}
		provider.record(names)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, choice := range []string{`{"delta":{"role":"assistant","content":"ok"},"finish_reason":null}`, `{"delta":{},"finish_reason":"stop"}`} {
			_, _ = fmt.Fprintf(w, "data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"fake\",\"choices\":[{\"index\":0,%s]}\n\n", strings.TrimPrefix(choice, "{"))
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})}
	go func() { _ = provider.server.Serve(listener) }()
	t.Cleanup(func() { _ = provider.server.Close() })
	return provider
}

// record appends the tool names of one request and wakes the waiter.
func (p *scopeProvider) record(names []string) {
	p.mu.Lock()
	p.offered = append(p.offered, names)
	p.mu.Unlock()
	select {
	case p.recorded <- struct{}{}:
	default:
	}
}

func (p *scopeProvider) first() ([]string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.offered) == 0 {
		return nil, false
	}
	return p.offered[0], true
}

func (p *scopeProvider) requests() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.offered)
}

// terminalDriver runs pig on a terminal and owns the goroutine that reads the terminal. The reader never waits for the
// test: it appends what pig draws to the screen buffer, so pig never blocks on unread output, and close stops pig and
// joins the reader, so no reader outlives the test.
type terminalDriver struct {
	terminal io.Writer
	stop     func()
	mu       sync.Mutex
	screen   strings.Builder
	// drawn receives after the screen grows; its capacity of one coalesces wakeups for the one waiter.
	drawn chan struct{}
	// exited is closed when the reader returns, after the terminal reported an error or closed.
	exited chan struct{}
}

// startTerminalDriver starts cmd on a terminal (startPigOnTerminal) and starts reading it.
func startTerminalDriver(t *testing.T, cmd *exec.Cmd) *terminalDriver {
	t.Helper()
	terminal, stop := startPigOnTerminal(t, cmd)
	d := &terminalDriver{terminal: terminal, stop: stop, drawn: make(chan struct{}, 1), exited: make(chan struct{})}
	go d.read(terminal)
	return d
}

func (d *terminalDriver) read(terminal io.Reader) {
	defer close(d.exited)
	buf := make([]byte, 4096)
	for {
		n, err := terminal.Read(buf)
		if n > 0 {
			d.mu.Lock()
			d.screen.Write(buf[:n])
			d.mu.Unlock()
			select {
			case d.drawn <- struct{}{}:
			default:
			}
		}
		if err != nil {
			return
		}
	}
}

// close stops pig, releases the terminal, and returns once the reader has returned.
func (d *terminalDriver) close() {
	d.stop()
	<-d.exited
}

// send types text on the terminal.
func (d *terminalDriver) send(text string) { _, _ = io.WriteString(d.terminal, text) }

// shows reports whether the screen drawn since the last clear contains text.
func (d *terminalDriver) shows(text string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return strings.Contains(d.screen.String(), text)
}

// clear forgets the screen drawn so far.
func (d *terminalDriver) clear() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.screen.Reset()
}

// String returns the screen drawn since the last clear.
func (d *terminalDriver) String() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.screen.String()
}

// errTerminalClosed reports that pig's terminal closed, so pig exited, before the awaited event.
var errTerminalClosed = errors.New("pig exited")

// waitUntil returns nil once ready holds. It checks ready again whenever provider records a request or pig draws on
// terminal (nil when pig runs without one), and returns errTerminalClosed when the terminal closes first, or ctx's
// error when ctx ends first.
func waitUntil(ctx context.Context, provider *scopeProvider, terminal *terminalDriver, ready func() bool) error {
	var drawn, exited <-chan struct{}
	if terminal != nil {
		drawn, exited = terminal.drawn, terminal.exited
	}
	for !ready() {
		select {
		case <-provider.recorded:
		case <-drawn:
		case <-exited:
			if ready() {
				return nil
			}
			return errTerminalClosed
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// scopeFixture writes a Piglet whose `scoped` entry selects a Node extension that registers scoped_tool.
func scopeFixture(t *testing.T, provider *scopeProvider, tools string) (home, piglet string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home = filepath.Join(root, "home")
	writeStartupFixtureFile(t, filepath.Join(home, "agent", "models.json"), fmt.Sprintf(`{"providers":{"fake":{"baseUrl":%q,"api":"openai-completions","apiKey":"synthetic-test-key","models":[{"id":"fake","name":"Fake","reasoning":false,"input":["text"],"contextWindow":128000,"maxTokens":1024,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}]}}}`, provider.url))
	piglet = filepath.Join(root, "piglet")
	writeStartupFixtureFile(t, filepath.Join(piglet, "scoped.mjs"), `export default function (pi) {
  pi.registerTool({ name: "scoped_tool", label: "Scoped", description: "scoped", parameters: { type: "object", properties: {} }, execute: async () => ({ content: [{ type: "text", text: "ok" }] }) });
}
`)
	writeStartupFixtureFile(t, filepath.Join(piglet, "piglet.yaml"), "name: tool-scope\nrelease:\n  version: 0.1.0\nextensions:\n  - name: scoped\n    origins: [local:./scoped.mjs]\n"+tools+"skills: []\ndiscovery:\n  extensions: []\n  skills: []\n")
	return home, piglet
}

// killAndWait stops a started command.
func killAndWait(cmd *exec.Cmd) func() {
	return func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
}

// firstRequestTools starts pig in the given mode with the fixture Piglet, and returns the tool names of the first model
// request. The run ends once the provider has seen it.
func firstRequestTools(t *testing.T, provider *scopeProvider, home, piglet, mode string) []string {
	t.Helper()
	return firstRequestToolsIn(t, provider, home, filepath.Dir(piglet), mode, "--piglet", filepath.Join(piglet, "piglet.yaml"))
}

// firstRequestToolsIn starts pig in the given mode in dir with the launch arguments, and returns the tool names of the
// first model request. The launch loads scoped.mjs, whose name in the interactive startup listing marks readiness.
func firstRequestToolsIn(t *testing.T, provider *scopeProvider, home, dir, mode string, launch ...string) []string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required for the extension fixture: %v", err)
	}
	// A version-manager shim of node needs the real HOME; the run gets a private one.
	if resolved, err := exec.Command(node, "-p", "process.execPath").Output(); err == nil {
		node = strings.TrimSpace(string(resolved))
	}
	args := append(slices.Clone(launch), "--no-session", "--offline", "--no-context-files", "--model", "fake/fake")
	switch mode {
	case "print":
		args = append(args, "--print", "hi")
	case "json":
		args = append(args, "--mode", "json", "hi")
	case "rpc":
		args = append(args, "--mode", "rpc")
	case "interactive":
	default:
		t.Fatalf("unknown mode %q", mode)
	}
	// An existing settings.json skips the first-time setup dialog (D88) in interactive mode.
	if err := os.MkdirAll(filepath.Join(home, "agent"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "agent", "settings.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(buildPigBinaryForSignalTest(t), args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "PIG_HOME="+filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"),
		"PATH="+filepath.Dir(node)+string(os.PathListSeparator)+os.Getenv("PATH"), "PI_OFFLINE=1", "PI_TELEMETRY=0", "PI_SKIP_VERSION_CHECK=1", "TERM=xterm-256color")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	var stop func()
	// Every goroutine that reads pig's output is joined before the run returns, after stop ended pig and closed its output.
	var readers sync.WaitGroup
	defer readers.Wait()
	defer func() {
		if stop != nil {
			stop()
		}
	}()
	var terminal *terminalDriver
	switch mode {
	case "rpc":
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = stdin.Close() }()
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		stop = killAndWait(cmd)
		// The command loop answers get_state only after the extensions are loaded and the Piglet has scoped their tools; the prompt follows that response.
		_, _ = io.WriteString(stdin, `{"id":"ready","type":"get_state"}`+"\n")
		readers.Go(func() {
			reader := bufio.NewReader(stdout)
			prompted := false
			for {
				line, err := reader.ReadString('\n')
				if !prompted && strings.Contains(line, `"id":"ready"`) && strings.Contains(line, `"success":true`) {
					prompted = true
					_, _ = io.WriteString(stdin, `{"type":"prompt","message":"hi"}`+"\n")
				}
				if err != nil {
					return
				}
			}
		})
	case "interactive":
		terminal = startTerminalDriver(t, cmd)
		stop = terminal.close
	default:
		cmd.Stdin = nil
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		stop = killAndWait(cmd)
		readers.Go(func() { _, _ = io.Copy(io.Discard, bufio.NewReader(stdout)) })
	}
	ctx := testbudget.Context(t)
	if terminal != nil {
		// The startup listing names every loaded extension, which happens after the Piglet has scoped their tools; the prompt follows it.
		if err := waitUntil(ctx, provider, terminal, func() bool { return terminal.shows("scoped.mjs") }); err != nil {
			t.Fatalf("no model request in %s mode: %v before the startup listing\nstderr:\n%s", mode, err, stderr.String())
		}
		terminal.send("hi\r")
	}
	if err := waitUntil(ctx, provider, terminal, func() bool { _, ok := provider.first(); return ok }); err != nil {
		t.Fatalf("no model request in %s mode: %v\nstderr:\n%s", mode, err, stderr.String())
	}
	names, _ := provider.first()
	return names
}

// A Piglet extension entry's `tools` list scopes that extension's tools in every mode. Pi binds getAllTools,
// getActiveTools and setActiveTools to the session in every mode (agent-session.ts _bindExtensionCore), and the Piglet
// scopes through them. Print and JSON mode reported the tool's provenance object as a built-in and offered scoped_tool
// to the model although the entry declared `tools: []`; RPC mode resolved the owner itself, and interactive mode reads
// the runner's per-tool source.
func TestPigletExtensionToolsScopeInEveryMode(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and starts Node extensions")
	}
	t.Parallel()
	for _, mode := range []string{"print", "json", "rpc", "interactive"} {
		t.Run(mode, func(t *testing.T) {
			for _, test := range []struct {
				name, tools string
				offered     bool
			}{
				{"an empty list hides the extension's tool", "    tools: []\n", false},
				{"a list naming the tool keeps it", "    tools: [scoped_tool]\n", true},
				{"a list naming another tool hides it", "    tools: [other_tool]\n", false},
				{"no list keeps it", "", true},
			} {
				t.Run(test.name, func(t *testing.T) {
					provider := newScopeProvider(t)
					home, piglet := scopeFixture(t, provider, test.tools)
					names := firstRequestTools(t, provider, home, piglet, mode)
					if !slices.Contains(names, "read") {
						t.Errorf("built-in tools stay: offered %v", names)
					}
					if got := slices.Contains(names, "scoped_tool"); got != test.offered {
						t.Errorf("scoped_tool offered = %t, want %t: %v", got, test.offered, names)
					}
				})
			}
		})
	}
}
