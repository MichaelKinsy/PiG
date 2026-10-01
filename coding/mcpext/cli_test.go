package mcpext_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/mcpext"
)

// `pig mcp` cases that mcp-command.test.ts does not run: the help text, argument errors, and the OAuth commands. Every expected text below was also produced by upstream 0.99.1's `pi mcp` for the same input (compared with PiG's identity substituted), except where a case says otherwise.

func TestMcpCommandHelpIsPisHelpWithPiGsIdentity(t *testing.T) {
	// cli.ts:31-70, HELP; testdata/mcp_help.txt is `pi mcp --help` of 0.99.1 with `pi` → `pig` and `.pi` → `.pig`.
	golden, err := os.ReadFile(filepath.Join("testdata", "mcp_help.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSuffix(string(golden), "\n")
	for _, args := range [][]string{nil, {"help"}, {"--help"}, {"-h"}, {"list", "--help"}, {"add", "x", "-h"}, {"frobnicate", "--help"}} {
		if result := runMcp(t, args, nil, ""); result.exitCode != 0 || result.output != want {
			t.Errorf("mcp %v = %d, want the help text:\n%s", args, result.exitCode, result.output)
		}
	}
}

func TestMcpCommandStylesHelpAndHintsLikeChalk(t *testing.T) {
	// cli.ts:31 chalk.bold("Usage:"), :72 chalk.dim(HELP_HINT).
	var output []string
	add := func(line string) { output = append(output, line) }
	options := mcpext.McpCommandOptions{Cwd: t.TempDir(), AgentDir: t.TempDir(), Color: true, Log: add, Error: add}
	if code := mcpext.RunMcpCommand(context.Background(), []string{"--help"}, options); code != 0 || !strings.HasPrefix(output[0], "\x1b[1mUsage:\x1b[22m\n  pig mcp add") {
		t.Fatalf("help = %d %q", code, output)
	}
	output = nil
	if code := mcpext.RunMcpCommand(context.Background(), []string{"frobnicate"}, options); code != 1 || output[0] != "Unknown mcp command \"frobnicate\".\n\x1b[2mUse \"pig mcp --help\" for usage.\x1b[22m" {
		t.Fatalf("unknown command = %d %q", code, output)
	}
}

func TestMcpCommandReportsArgumentErrors(t *testing.T) {
	const hint = `Use "pig mcp --help" for usage.`
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"frobnicate"}, "Unknown mcp command \"frobnicate\".\n" + hint},
		{[]string{"list", "extra"}, "Usage: pig mcp list [--json]\n" + hint},
		{[]string{"list", "--bogus"}, "Unknown option --bogus.\n" + hint},
		{[]string{"list", "-l"}, "Unknown option --local.\n" + hint},
		{[]string{"login"}, "Usage: pig mcp login <server>\n" + hint},
		{[]string{"logout", "a", "b"}, "Usage: pig mcp logout <server>\n" + hint},
		{[]string{"login", "x", "--timeout"}, "--timeout needs a value."},
		{[]string{"logout", "x", "--timeout", "5"}, "Unknown option --timeout.\n" + hint},
		{[]string{"remove"}, "Usage: pig mcp remove <server> [-l]\n" + hint},
		{[]string{"remove", "x", "y"}, "Usage: pig mcp remove <server> [-l]\n" + hint},
		{[]string{"add"}, "Usage: pig mcp add <server> [options] (--url <url> | -- <command> [args...])\n" + hint},
		{[]string{"add", "x", "--url"}, "--url needs a value."},
		{[]string{"add", "x", "--bogus", "--", "c"}, "Unknown option --bogus.\n" + hint},
		{[]string{"add", "x", "--env", "NOVALUE", "--", "c"}, `--env expects KEY=VALUE, got "NOVALUE".`},
		{[]string{"add", "x", "--header", "=v", "--url", "https://example.com"}, `--header expects KEY=VALUE, got "=v".`},
		{[]string{"add", "x", "--header", "A=1", "--", "c"}, "--header only applies to HTTP servers (--url)."},
		{[]string{"add", "x", "--env", "A=1", "--url", "https://example.com"}, "--env only applies to stdio servers."},
		{[]string{"add", "x", "--url", "ftp://example.com"}, `server "x": url must be an http or https URL`},
		{[]string{"add", "x", "--exposure", "loud", "--", "c"}, `server "x": exposure must be one of "codemode", "deferred", "direct", "hidden"`},
		{[]string{"add", "x", "--url", "https://example.com", "--oauth-callback-port", "abc"}, `server "x": oauth.callbackPort must be a port number`},
		{[]string{"add", "x", "--url", "https://example.com", "--oauth-callback-port", "1.5"}, `server "x": oauth.callbackPort must be a port number`},
	}
	for _, c := range cases {
		if result := runMcp(t, c.args, nil, ""); result.exitCode != 1 || result.output != c.want {
			t.Errorf("mcp %v = %d %q, want 1 %q", c.args, result.exitCode, result.output, c.want)
		}
	}
	for _, timeout := range []string{"abc", "0", "-1", "Infinity"} {
		args := []string{"login", "fixture", "--timeout", timeout}
		if result := runMcp(t, args, commandServers()[:1], ""); result.exitCode != 1 || result.output != "--timeout must be a positive number of seconds." {
			// The server is looked up first: a stdio server has no OAuth, so its own error comes before the timeout check.
			if !strings.Contains(result.output, "does not use OAuth") {
				t.Errorf("mcp %v = %d %q", args, result.exitCode, result.output)
			}
		}
	}
}

func TestMcpCommandListsAnEmptyConfigurationAndKeepsTheOptionOrder(t *testing.T) {
	empty := runMcp(t, []string{"list"}, nil, "")
	if empty.exitCode != 0 || empty.output != "No MCP servers configured. Add them to "+filepath.Join(empty.agentDir, "mcp.json")+" or .pig/mcp.json." {
		t.Fatalf("empty list = %d %q", empty.exitCode, empty.output)
	}
	emptyJSON := runMcp(t, []string{"list", "--json"}, nil, "")
	if emptyJSON.exitCode != 0 || emptyJSON.output != "{\n  \"servers\": [],\n  \"errors\": []\n}" {
		t.Fatalf("empty list --json = %d %q", emptyJSON.exitCode, emptyJSON.output)
	}
	// The report's members, in upstream's order, with the overrides of a server whose exposure differs from its tools'.
	server := strings.TrimSuffix(fixtureServer(), "}") + `,"exposure":"direct","toolExposure":{"echo":"deferred"}}`
	result := runMcp(t, []string{"list", "--json"}, []serverEntry{{"ov", server}}, "")
	var document struct {
		Servers []json.RawMessage `json:"servers"`
	}
	if err := json.Unmarshal([]byte(result.output), &document); err != nil || len(document.Servers) != 1 {
		t.Fatalf("%v\n%s", err, result.output)
	}
	var members []string
	decoder := json.NewDecoder(strings.NewReader(string(document.Servers[0])))
	if _, err := decoder.Token(); err != nil {
		t.Fatal(err)
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			t.Fatal(err)
		}
		members = append(members, key.(string))
		var skip json.RawMessage
		if err := decoder.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := strings.Join(members, " "), "name scope source enabled exposure transport state tools toolExposure"; got != want {
		t.Fatalf("members = %q, want %q", got, want)
	}
	text := runMcp(t, []string{"list"}, []serverEntry{{"ov", server}}, "")
	mustContain(t, text.output, "ov: connected, 1 tool (direct, global)\n")
	mustContain(t, text.output, "  tools: echo [deferred]")
}

// An untrusted project's `mcp.json` is ignored with a note (cli.ts:196-200), and a trusted one is read.
func TestMcpCommandReadsProjectServersOnlyWhenTheProjectIsTrusted(t *testing.T) {
	agentDir := t.TempDir()
	projectConfig := filepath.Join(agentDir, ".pig", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(projectConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectConfig, []byte(`{"mcpServers":{"local":{"command":"x","enabled":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(trusted func(string) (bool, error), args ...string) (int, string) {
		var output []string
		add := func(line string) { output = append(output, line) }
		code := mcpext.RunMcpCommand(context.Background(), args, mcpext.McpCommandOptions{
			Cwd: agentDir, AgentDir: agentDir, IsProjectTrusted: trusted, Log: add, Error: add,
		})
		return code, strings.Join(output, "\n")
	}
	code, output := run(nil, "list")
	if want := projectConfig + " is ignored because the project is not trusted. Start pig in the project to trust it."; code != 0 || output != "No MCP servers configured. Add them to "+filepath.Join(agentDir, "mcp.json")+" or .pig/mcp.json.\n"+want {
		t.Fatalf("untrusted list = %d %q", code, output)
	}
	code, output = run(func(string) (bool, error) { return true, nil }, "list")
	if code != 0 || !strings.HasPrefix(output, "local: disabled (codemode, project)\n  x") {
		t.Fatalf("trusted list = %d %q", code, output)
	}
	code, output = run(func(string) (bool, error) { return false, os.ErrPermission }, "list")
	if code != 1 || output != os.ErrPermission.Error() {
		t.Fatalf("trust store failure = %d %q", code, output)
	}
}

type oauthRun struct {
	agentDir string
	server   *oauthMcpServer
	entry    []serverEntry
	// oauth is the `oauth` member of the server entry, as JSON.
	oauth string
}

func newOAuthRun(t *testing.T) oauthRun {
	t.Helper()
	server := startOAuthMcpServer(t)
	return oauthRun{agentDir: t.TempDir(), server: server, entry: []serverEntry{{"oauth", `{"url":"` + server.URL + `"}`}}}
}

func (o oauthRun) run(t *testing.T, options mcpext.McpCommandOptions, args ...string) (int, string) {
	t.Helper()
	var mu sync.Mutex
	var output []string
	add := func(line string) {
		mu.Lock()
		output = append(output, line)
		mu.Unlock()
	}
	oauth := ""
	if o.oauth != "" {
		oauth = `,"oauth":` + o.oauth
	}
	content := `{"mcpServers":{"oauth":{"url":"` + o.server.URL + `"` + oauth + `}}}`
	if err := os.WriteFile(filepath.Join(o.agentDir, "mcp.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	options.Cwd, options.AgentDir, options.Log, options.Error = o.agentDir, o.agentDir, add, add
	code := mcpext.RunMcpCommand(context.Background(), args, options)
	mu.Lock()
	defer mu.Unlock()
	return code, strings.Join(output, "\n")
}

// follow opens an authorization URL like a browser: it follows the redirect to the loopback callback.
func follow(t *testing.T, wg *sync.WaitGroup) func(string) {
	return func(target string) {
		wg.Go(func() {
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
			if err != nil {
				t.Error(err)
				return
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Error(err)
				return
			}
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		})
	}
}

func TestMcpCommandSignsInAndOutOfAnOAuthServer(t *testing.T) {
	o := newOAuthRun(t)
	if code, output := o.run(t, mcpext.McpCommandOptions{}, "list"); code != 1 ||
		!strings.Contains(output, "oauth: needs sign-in (codemode, global)\n  "+o.server.URL+"\n  sign in with: pig mcp login oauth") {
		t.Fatalf("list before sign-in = %d %q", code, output)
	}

	var browsers sync.WaitGroup
	code, output := o.run(t, mcpext.McpCommandOptions{OpenURL: follow(t, &browsers)}, "login", "oauth")
	browsers.Wait()
	if code != 0 || !strings.HasPrefix(output, `Sign in to MCP server "oauth" in your browser:`+"\n"+o.server.origin+"/authorize?") || !strings.HasSuffix(output, "\n"+`Signed in to MCP server "oauth" (1 tools).`) {
		t.Fatalf("login = %d %q", code, output)
	}
	if code, output := o.run(t, mcpext.McpCommandOptions{}, "login", "oauth"); code != 0 || output != `Already signed in to MCP server "oauth" (1 tools).` {
		t.Fatalf("second login = %d %q", code, output)
	}
	if code, output := o.run(t, mcpext.McpCommandOptions{}, "list"); code != 0 || !strings.HasPrefix(output, "oauth: connected, 1 tool (codemode, global)\n") {
		t.Fatalf("list after sign-in = %d %q", code, output)
	}
	if code, output := o.run(t, mcpext.McpCommandOptions{}, "logout", "oauth"); code != 0 || output != `Signed out of MCP server "oauth".` {
		t.Fatalf("logout = %d %q", code, output)
	}
	if code, output := o.run(t, mcpext.McpCommandOptions{}, "logout", "oauth"); code != 0 || output != `No stored credentials for MCP server "oauth".` {
		t.Fatalf("second logout = %d %q", code, output)
	}
	if code, output := o.run(t, mcpext.McpCommandOptions{}, "list"); code != 1 || !strings.Contains(output, "oauth: needs sign-in") {
		t.Fatalf("list after logout = %d %q", code, output)
	}
}

// agent-session-mcp-oauth.test.ts "registers with the configured client name" (#10226): the client name of the dynamic
// client registration is oauth.clientName, else the app name.
func TestMcpLoginRegistersWithTheConfiguredClientName(t *testing.T) {
	configured := newOAuthRun(t)
	configured.oauth = `{"clientName":"Claude Code"}`
	var browsers sync.WaitGroup
	if code, output := configured.run(t, mcpext.McpCommandOptions{OpenURL: follow(t, &browsers)}, "login", "oauth"); code != 0 {
		t.Fatalf("login = %d %q", code, output)
	}
	browsers.Wait()
	if got := configured.server.registrationClientNames(); !reflect.DeepEqual(got, []any{"Claude Code"}) {
		t.Fatalf("client names = %v, want [Claude Code]", got)
	}

	fallback := newOAuthRun(t)
	if code, output := fallback.run(t, mcpext.McpCommandOptions{OpenURL: follow(t, &browsers)}, "login", "oauth"); code != 0 {
		t.Fatalf("login = %d %q", code, output)
	}
	browsers.Wait()
	if got := fallback.server.registrationClientNames(); !reflect.DeepEqual(got, []any{"pig"}) {
		t.Fatalf("client names = %v, want [pig]", got)
	}
}

// A terminal takes the pasted redirect URL instead of the browser callback (cli.ts:548-575).
func TestMcpCommandAcceptsAPastedRedirectURL(t *testing.T) {
	o := newOAuthRun(t)
	reader, writer := io.Pipe()
	defer func() { _ = writer.Close() }()
	var prompt strings.Builder
	var mu sync.Mutex
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	code, output := o.run(t, mcpext.McpCommandOptions{
		Interactive: true, Stdin: reader, PromptOutput: writerFunc(func(p []byte) (int, error) {
			mu.Lock()
			defer mu.Unlock()
			return prompt.Write(p)
		}),
		OpenURL: func(target string) {
			go func() {
				request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
				if err != nil {
					t.Error(err)
					return
				}
				response, err := client.Do(request)
				if err != nil {
					t.Error(err)
					return
				}
				_ = response.Body.Close()
				redirect, err := url.Parse(response.Header.Get("Location"))
				if err != nil {
					t.Error(err)
					return
				}
				// A browser that cannot reach this machine: only the address bar's URL arrives.
				_, _ = io.WriteString(writer, redirect.String()+"\n")
			}()
		},
	}, "login", "oauth")
	if code != 0 || !strings.HasSuffix(output, `Signed in to MCP server "oauth" (1 tools).`) {
		t.Fatalf("login = %d %q", code, output)
	}
	mu.Lock()
	defer mu.Unlock()
	if prompt.String() != "If the browser cannot reach this machine, paste the URL it was redirected to: " {
		t.Fatalf("prompt = %q", prompt.String())
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// Without a browser callback or a terminal the sign-in ends at --timeout (cli.ts:520-528, 538-541).
func TestMcpCommandEndsASignInThatIsNotCompletedInTime(t *testing.T) {
	o := newOAuthRun(t)
	started := time.Now()
	code, output := o.run(t, mcpext.McpCommandOptions{OpenURL: func(string) {}}, "login", "oauth", "--timeout", "0.3")
	if code != 1 || !strings.HasSuffix(output, `Sign-in to MCP server "oauth" was cancelled or not completed within 0 seconds.`) {
		t.Fatalf("login = %d %q", code, output)
	}
	if time.Since(started) > 30*time.Second {
		t.Fatalf("the sign-in took %v", time.Since(started))
	}
	// Math.round(1600 / 1000) is 2: the printed count is rounded, not truncated.
	code, output = o.run(t, mcpext.McpCommandOptions{OpenURL: func(string) {}}, "login", "oauth", "--timeout", "1.6")
	if code != 1 || !strings.HasSuffix(output, "within 2 seconds.") {
		t.Fatalf("login = %d %q", code, output)
	}
}

func TestMcpCommandRejectsATimeoutThatIsNotPositive(t *testing.T) {
	o := newOAuthRun(t)
	for _, timeout := range []string{"abc", "0", "-1", "Infinity", ""} {
		code, output := o.run(t, mcpext.McpCommandOptions{}, "login", "oauth", "--timeout", timeout)
		if code != 1 || output != "--timeout must be a positive number of seconds." {
			t.Errorf("--timeout %q = %d %q", timeout, code, output)
		}
	}
}

// `list` connects to every enabled server at once (cli.ts:404-437, Promise.all): each fixture server answers `initialize` only after all of them started.
func TestMcpCommandListConnectsToTheServersConcurrently(t *testing.T) {
	rendezvous := t.TempDir()
	entry := func(name string) serverEntry {
		return serverEntry{name, fmt.Sprintf(`{"command":%q,"args":["-test.run=^$"],"timeout":30,"env":{%q:"stdio-server","MCP_TEST_RENDEZVOUS":%q,"GORACE":"atexit_sleep_ms=0"}}`,
			os.Args[0], fixtureEnv, rendezvous+":3")}
	}
	result := runMcp(t, []string{"list"}, []serverEntry{entry("a"), entry("b"), entry("c")}, "")
	if result.exitCode != 0 {
		t.Fatalf("exit code = %d\n%s", result.exitCode, result.output)
	}
	for _, name := range []string{"a", "b", "c"} {
		mustContain(t, result.output, name+": connected, 1 tool (codemode, global)")
	}
	if strings.Index(result.output, "a: connected") > strings.Index(result.output, "b: connected") || strings.Index(result.output, "b: connected") > strings.Index(result.output, "c: connected") {
		t.Fatalf("the servers are not reported in configuration order:\n%s", result.output)
	}
}
