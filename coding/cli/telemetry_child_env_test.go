package cli

import (
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

const telemetryChildEnv, telemetryChildFileEnv = "PIG_TEST_TELEMETRY_CHILD", "PIG_TEST_TELEMETRY_CHILD_FILE"

// A real command line in print and RPC mode, as a child of this test binary, with PIG_TELEMETRY=0 beside a PI_TELEMETRY=1 in its
// environment: the environment that a program started by the run (the Node extension runtime, the bash tool, a package command) inherits
// holds PI_TELEMETRY=0 by the time the run's extensions load, the PIG_TELEMETRY it was given, and the opposite order resolves to 1.
// Owner decision 2026-11-17 (divergence record D2). The interactive mode needs a terminal: TestMainExportsTheResolvedTelemetryBeforeAnyCommand
// holds the order for every mode and command, which all start after the top of Main.
func TestMainChildSeesThePigTelemetryChoice(t *testing.T) {
	if os.Getenv(telemetryChildEnv) == "1" {
		Main(flag.Args(), &MainOptions{ExtensionFactories: []extension.InlineExtension{extension.ExtensionFactory(func(extension.API) error {
			line := "PI_TELEMETRY=" + os.Getenv("PI_TELEMETRY") + " PIG_TELEMETRY=" + os.Getenv("PIG_TELEMETRY") + "\n"
			return os.WriteFile(os.Getenv(telemetryChildFileEnv), []byte(line), 0o644)
		})}})
		return
	}
	if testing.Short() {
		t.Skip("runs the command line in a child process")
	}
	for _, tc := range []struct{ pig, pi, want string }{{"0", "1", "0"}, {"1", "0", "1"}} {
		for name, args := range map[string][]string{
			"print": {"--model", "test-faux/faux-1", "-p", "reply with exactly: ok"},
			"rpc":   {"--mode", "rpc", "--model", "test-faux/faux-1"},
		} {
			t.Run(name+" PIG="+tc.pig+" PI="+tc.pi, func(t *testing.T) {
				root := t.TempDir()
				seen := filepath.Join(root, "seen")
				command := exec.CommandContext(t.Context(), os.Args[0], append([]string{"-test.run=^TestMainChildSeesThePigTelemetryChoice$", "--", "--no-session"}, args...)...)
				command.Env = append(noHomeDirectoryEnvironment(t), "HOME="+root, "PIG_TELEMETRY="+tc.pig, "PI_TELEMETRY="+tc.pi, telemetryChildEnv+"=1", telemetryChildFileEnv+"="+seen)
				command.Dir = root
				command.Stdin = strings.NewReader("")
				output, _ := command.CombinedOutput()
				data, err := os.ReadFile(seen)
				if err != nil {
					t.Fatalf("the extension never ran: %v\n%s", err, output)
				}
				if want := "PI_TELEMETRY=" + tc.want + " PIG_TELEMETRY=" + tc.pig + "\n"; string(data) != want {
					t.Fatalf("the extension saw %q, want %q", data, want)
				}
			})
		}
	}
}

// Every command of pig, the ones that run before a session (package, extension, verify, mcp, login) included, starts after the top of Main,
// so one call there reaches them all: exportTelemetryOverride is the first call of Main and runs before the first command is dispatched.
func TestMainExportsTheResolvedTelemetryBeforeAnyCommand(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Main" {
			continue
		}
		for _, stmt := range fn.Body.List {
			call, ok := stmt.(*ast.ExprStmt)
			if !ok {
				t.Fatalf("Main's first statement is %T, want the call of exportTelemetryOverride", stmt)
			}
			if ident, ok := call.X.(*ast.CallExpr).Fun.(*ast.Ident); !ok || ident.Name != "exportTelemetryOverride" {
				t.Fatalf("Main's first statement is not the call of exportTelemetryOverride")
			}
			return
		}
	}
	t.Fatal("Main not found")
}
