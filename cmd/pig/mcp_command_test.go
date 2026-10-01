//go:build !pig_strip_mcp

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
)

// Pi's main.ts routes `mcp` after the package and config commands and before argument parsing, and hands the rest of the arguments and the process exit code to runMcpCommand (.upstream/v0.99.1/packages/coding-agent/src/main.ts:608-612).

type mcpCLIRun struct {
	stdout, stderr string
	exitCode       int
}

func runPigMCP(t *testing.T, agentDir, cwd string, args ...string) mcpCLIRun {
	t.Helper()
	home := t.TempDir()
	cmd := exec.Command(buildPigBinaryForSignalTest(t), args...)
	cmd.Dir = cwd
	// The proxy variables are removed, not emptied: an explicitly empty value stays authoritative over the httpProxy setting.
	cmd.Env = slices.DeleteFunc(os.Environ(), func(entry string) bool {
		name, _, _ := strings.Cut(entry, "=")
		return slices.Contains([]string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "NO_PROXY", "no_proxy", "ALL_PROXY", "all_proxy"}, name)
	})
	cmd.Env = append(cmd.Env, "HOME="+home, "PIG_HOME="+filepath.Join(home, "pig"), "PIG_CODING_AGENT_DIR="+agentDir, "PIG_OFFLINE=1", "NO_COLOR=", "FORCE_COLOR=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return mcpCLIRun{stdout.String(), stderr.String(), code}
}

func TestPigMcpRunsBeforeSessionStartup(t *testing.T) {
	agentDir, cwd := filepath.Join(t.TempDir(), "agent"), t.TempDir()
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	list := runPigMCP(t, agentDir, cwd, "mcp", "list", "--json")
	if list.exitCode != 0 || list.stderr != "" {
		t.Fatalf("mcp list --json = %+v", list)
	}
	var empty map[string]any
	if err := json.Unmarshal([]byte(list.stdout), &empty); err != nil || !reflect.DeepEqual(empty, map[string]any{"servers": []any{}, "errors": []any{}}) {
		t.Fatalf("mcp list --json printed %q (%v)", list.stdout, err)
	}

	added := runPigMCP(t, agentDir, cwd, "mcp", "add", "files", "--", "node", "server.js")
	if added.exitCode != 0 || !strings.Contains(added.stdout, `Added global MCP server "files" in `+filepath.Join(agentDir, "mcp.json")) {
		t.Fatalf("mcp add = %+v", added)
	}
	if !strings.Contains(added.stdout, "Check it with: pig mcp list") {
		t.Fatalf("mcp add does not name its own command: %q", added.stdout)
	}
	unknown := runPigMCP(t, agentDir, cwd, "mcp", "frobnicate")
	if unknown.exitCode != 1 || !strings.Contains(unknown.stderr, `Unknown mcp command "frobnicate".`) || !strings.Contains(unknown.stderr, `Use "pig mcp --help" for usage.`) {
		t.Fatalf("mcp frobnicate = %+v", unknown)
	}
}

func TestPigMcpHelpIsPisHelpWithPiGsIdentity(t *testing.T) {
	agentDir := filepath.Join(t.TempDir(), "agent")
	for _, args := range [][]string{{"mcp", "--help"}, {"mcp", "-h"}, {"mcp", "help"}, {"mcp"}, {"mcp", "list", "--help"}} {
		run := runPigMCP(t, agentDir, t.TempDir(), args...)
		if run.exitCode != 0 || run.stderr != "" {
			t.Fatalf("pig %v = %+v", args, run)
		}
		for _, want := range []string{
			"Usage:\n  pig mcp add <server> [options] -- <command> [args...]\n",
			"Reads ~/.pig/agent/mcp.json and, in trusted projects, .pig/mcp.json.\n",
			"  -l, --local             Use .pig/mcp.json in the current project instead of the global file\n",
			"  --timeout <seconds>     How long login waits for the browser (default: 300)",
		} {
			if !strings.Contains(run.stdout, want) {
				t.Fatalf("pig %v help lacks %q:\n%s", args, want, run.stdout)
			}
		}
	}
}

// Project `.pig/mcp.json` is read only for trusted projects (config.ts:141-145 via cli.ts:196-197 `ProjectTrustStore(agentDir).get(cwd) === true`).
func TestPigMcpReadsProjectConfigOnlyForTrustedProjects(t *testing.T) {
	agentDir, cwd := filepath.Join(t.TempDir(), "agent"), t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".pig"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	projectConfig := filepath.Join(cwd, ".pig", "mcp.json")
	if err := os.WriteFile(projectConfig, []byte(`{"mcpServers":{"local":{"command":"pi-test-missing-mcp-server","enabled":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	untrusted := runPigMCP(t, agentDir, cwd, "mcp", "list")
	if untrusted.exitCode != 0 || strings.Contains(untrusted.stdout, "local:") ||
		!strings.Contains(untrusted.stdout, projectConfig+" is ignored because the project is not trusted. Start pig in the project to trust it.") {
		t.Fatalf("untrusted list = %+v", untrusted)
	}
	resolved, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := json.Marshal(map[string]bool{resolved: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "trust.json"), trust, 0o600); err != nil {
		t.Fatal(err)
	}
	trusted := runPigMCP(t, agentDir, cwd, "mcp", "list")
	if trusted.exitCode != 0 || !strings.Contains(trusted.stdout, "local: disabled (codemode, project)") || strings.Contains(trusted.stdout, "is ignored") {
		t.Fatalf("trusted list = %+v", trusted)
	}
}

// Pi 0.99.1 prints these lines in --help (cli/args.ts:285-286,313-314). PiG renders them with its identity (automation/gen/gen-help.sh).
func TestHelpMentionsMcpCommandAndBuiltinExtensions(t *testing.T) {
	var out bytes.Buffer
	printHelp(&out, false)
	for _, want := range []string{
		"  pig mcp <command>             Check MCP servers, sign in to or out of OAuth servers\n",
		"  pig <command> --help          Show help for install/remove/uninstall/update/list/config/auth/mcp\n",
		"  --extension, -e <path>         Load an extension file or builtin:<name> (can be used multiple times)\n",
		"  --no-extensions, -ne           Disable extension discovery and built-in extensions (explicit -e paths still work)\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("help lacks %q", want)
		}
	}
}

// main.ts:591-593 applies the global httpProxy setting before it routes `mcp`, so the command's HTTP connections go through the configured proxy.
func TestPigMcpAppliesTheHTTPProxySetting(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var hosts []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hosts = append(hosts, r.Host)
		mu.Unlock()
		http.Error(w, "proxy refused", http.StatusBadGateway)
	}))
	t.Cleanup(proxy.Close)
	agentDir := filepath.Join(t.TempDir(), "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"settings.json": `{"httpProxy":"` + proxy.URL + `"}`,
		"mcp.json":      `{"mcpServers":{"remote":{"url":"http://mcp.example.invalid/mcp"}}}`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(agentDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	list := runPigMCP(t, agentDir, t.TempDir(), "mcp", "list")
	if list.exitCode != 1 || !strings.Contains(list.stdout, "remote: failed (codemode, global)") {
		t.Fatalf("mcp list = %+v", list)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Contains(hosts, "mcp.example.invalid") {
		t.Fatalf("the proxy saw hosts %v, want a request for mcp.example.invalid", hosts)
	}
}
