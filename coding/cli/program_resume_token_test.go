// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"encoding/json"
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/mcp"
	"github.com/MichaelKinsy/PiG/tui"
)

const (
	osc7577ChildModeEnv = "PIG_TEST_OSC7577_CHILD_MODE"
	osc7577ChildOutEnv  = "PIG_TEST_OSC7577_CHILD_OUT"
)

// resolveTestFauxModelSpec resolves the harness test-faux provider/model spec from the shared
// coding.AgentSessionServices model runtime instead of hard-coding "test-faux/faux-1".
func resolveTestFauxModelSpec(t *testing.T) string {
	t.Helper()
	t.Setenv("PIG_TEST_FAUX", "1")
	coding.EnableTestFauxProvider()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{
		CWD:      t.TempDir(),
		AgentDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("create test services: %v", err)
	}
	defer services.Close()
	models := services.ModelRuntime().GetModels("test-faux")
	if len(models) == 0 {
		t.Fatal("test-faux provider registered no models in shared test configuration")
	}
	return models[0].ProviderMeta.ProviderID + "/" + models[0].ID
}

// TestMainTakesProgramResumeTokenBeforeAnyCommand proves at the AST level that Main calls
// tui.TakeProgramResumeToken() immediately after exportTelemetryOverride(), before any subcommand,
// extension, MCP server, editor, or tool child can be spawned.
func TestMainTakesProgramResumeTokenBeforeAnyCommand(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Main" {
			continue
		}
		if len(fn.Body.List) < 3 {
			t.Fatalf("Main has %d statements, want at least 3", len(fn.Body.List))
		}
		// Pi v1.7.0 (main.ts:610-619): ApplyAgentDirArg (+ its error exit) runs first, then TakeProgramResumeToken() before any subcommand or child process.
		assign, ok := fn.Body.List[2].(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 {
			t.Fatalf("Main's third statement is %T, want programResumeToken := tui.TakeProgramResumeToken()", fn.Body.List[2])
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok {
			t.Fatalf("Main's second statement RHS is %T, want CallExpr", assign.Rhs[0])
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "TakeProgramResumeToken" {
			t.Fatalf("Main's second statement does not call TakeProgramResumeToken: %#v", call.Fun)
		}
		return
	}
	t.Fatal("Main not found")
}

// TestMainStripsOSC7577TokenFromChildren runs real children from inside a Main() process started with
// OSC7577_TOKEN set and verifies that:
//   - an inline/loaded extension sees no OSC7577_TOKEN;
//   - a real bash tool child (CreateLocalBashOperations) sees no OSC7577_TOKEN;
//   - a real MCP stdio server child (mcp.NewStdioTransport with default InheritEnv=true) sees no OSC7577_TOKEN.
func TestMainStripsOSC7577TokenFromChildren(t *testing.T) {
	mode := os.Getenv(osc7577ChildModeEnv)
	if mode == "mcp-stdio-server" {
		v, ok := os.LookupEnv(tui.ProgramResumeTokenEnv)
		b, _ := json.Marshal(map[string]any{"present": ok, "value": v})
		_, _ = os.Stderr.Write(b)
		os.Exit(0)
	}
	if mode == "main-host" {
		outFile := os.Getenv(osc7577ChildOutEnv)
		exe, _ := os.Executable()
		Main(flag.Args(), &MainOptions{ExtensionFactories: []extension.InlineExtension{extension.ExtensionFactory(func(extension.API) error {
			// 1. Process/extension env
			extVal, extPresent := os.LookupEnv(tui.ProgramResumeTokenEnv)

			// 2. Real bash tool child
			var bashOut strings.Builder
			_, bashErr := tools.CreateLocalBashOperations(nil).Exec(
				context.Background(),
				`if [ -z "${OSC7577_TOKEN+x}" ]; then printf "UNSET"; else printf "SET:%s" "$OSC7577_TOKEN"; fi`,
				os.TempDir(),
				tools.BashOperationsExecOptions{OnData: func(d []byte) { bashOut.Write(d) }},
			)

			// 3. Real MCP stdio server child (default InheritEnv = true)
			transport := mcp.NewStdioTransport(mcp.StdioTransportOptions{
				Command: exe,
				Args:    []string{"-test.run=^TestMainStripsOSC7577TokenFromChildren$"},
				Env:     map[string]string{osc7577ChildModeEnv: "mcp-stdio-server"},
			})
			closed := make(chan struct{})
			transport.OnClose(func() { close(closed) })
			mcpErr := transport.Start()
			if mcpErr == nil {
				select {
				case <-closed:
				case <-time.After(testbudget.Wait(t)):
				}
			}

			report, _ := json.Marshal(map[string]any{
				"extPresent": extPresent,
				"extValue":   extVal,
				"bashOut":    strings.TrimSpace(bashOut.String()),
				"bashErr":    errString(bashErr),
				"mcpStderr":  strings.TrimSpace(transport.Stderr()),
				"mcpErr":     errString(mcpErr),
			})
			return os.WriteFile(outFile, report, 0o600)
		})}})
		return
	}
	if testing.Short() {
		t.Skip("runs Main in a child process")
	}

	const secretToken = "cli-secret-osc7577-token-98765"
	modelSpec := resolveTestFauxModelSpec(t)
	root := t.TempDir()
	outFile := filepath.Join(root, "report.json")
	cmd := exec.CommandContext(t.Context(), os.Args[0],
		"-test.run=^TestMainStripsOSC7577TokenFromChildren$", "--",
		"--no-session", "--model", modelSpec, "-p", "reply with exactly: ok")
	cmd.Env = append(noHomeDirectoryEnvironment(t),
		"HOME="+root,
		tui.ProgramResumeTokenEnv+"="+secretToken,
		osc7577ChildModeEnv+"=main-host",
		osc7577ChildOutEnv+"="+outFile,
	)
	cmd.Dir = root
	cmd.Stdin = strings.NewReader("")
	output, _ := cmd.CombinedOutput()

	raw, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("report never written: %v\n%s", err, output)
	}
	if strings.Contains(string(raw), secretToken) {
		t.Fatalf("secret token leaked into child report: %s", raw)
	}
	var rep struct {
		ExtPresent bool   `json:"extPresent"`
		ExtValue   string `json:"extValue"`
		BashOut    string `json:"bashOut"`
		BashErr    string `json:"bashErr"`
		MCPSterr   string `json:"mcpStderr"`
		MCPErr     string `json:"mcpErr"`
	}
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatalf("decode report %q: %v", raw, err)
	}
	if rep.ExtPresent || rep.ExtValue != "" {
		t.Fatalf("extension saw OSC7577_TOKEN: present=%v value=%q", rep.ExtPresent, rep.ExtValue)
	}
	if rep.BashErr != "" || rep.BashOut != "UNSET" {
		t.Fatalf("bash tool child: out=%q err=%q, want UNSET and no error", rep.BashOut, rep.BashErr)
	}
	if rep.MCPErr != "" || !strings.Contains(rep.MCPSterr, `"present":false`) {
		t.Fatalf("MCP stdio server child: stderr=%q err=%q, want present:false", rep.MCPSterr, rep.MCPErr)
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
