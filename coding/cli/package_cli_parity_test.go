//go:build parity

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
)

// The same hermetic npm child runs under both complete CLIs. It exercises inherited stdout/stderr, failures, update progress, alias routing, and persisted package arrays without network access.
func TestPackageCLIComparedWithPi(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	pi := filepath.Join(repo, "extensions", "sdk-ts", "node_modules", ".bin", "pi")
	version := exec.CommandContext(t.Context(), pi, "--version")
	version.Env = append(os.Environ(), "HOME="+t.TempDir(), "PI_CODING_AGENT_DIR="+t.TempDir(), "PIG_CODING_AGENT_DIR="+t.TempDir())
	out, err := version.Output()
	if err != nil || strings.TrimSpace(string(out)) != coding.UpstreamVersion {
		t.Fatalf("Pi pin: %q, %v", out, err)
	}
	pig := buildPigBinaryForSignalTest(t)
	root := t.TempDir()
	shim := filepath.Join(root, "npm.cjs")
	if err := os.WriteFile(shim, []byte(`const fs = require('node:fs');
const path = require('node:path');
const args = process.argv.slice(2);
if (args[0] === 'view') { console.log('"2.0.0"'); process.exit(0); }
console.log('child stdout '+args.join(' ')); console.error('child stderr');
if (args[1] === 'broken') process.exit(17);
if (args[0] === 'install' && args.includes('--prefix')) {
 const root = args[args.indexOf('--prefix')+1];
 for (const spec of args.slice(1, args.indexOf('--prefix'))) {
  const name = spec.replace(/@[^@/]+$/, '');
  const dir = path.join(root, 'node_modules', name);
  fs.mkdirSync(dir, {recursive:true}); fs.writeFileSync(path.join(dir,'package.json'), JSON.stringify({name,version:'1.0.0'}));
 }
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, local := range []bool{false, true} {
		t.Run(fmt.Sprint(local), func(t *testing.T) {
			type result struct {
				stdout, stderr, settings string
				code                     int
			}
			run := func(binary, name string) []result {
				t.Helper()
				home := t.TempDir()
				cwd := filepath.Join(home, "work")
				agentDir := filepath.Join(home, "agent")
				for _, dir := range []string{cwd, agentDir} {
					if err := os.MkdirAll(dir, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				settings, err := json.Marshal(map[string]any{"npmCommand": []string{"node", shim}})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), settings, 0o600); err != nil {
					t.Fatal(err)
				}
				commands := [][]string{{"uninstall", "--help"}, {"install", "npm:fixture"}, {"install", "npm:second"}, {"update", "npm:fixture"}, {"update", "--extensions"}, {"uninstall", "npm:second"}, {"uninstall", "npm:fixture"}, {"uninstall", "npm:fixture"}, {"install", "npm:broken"}, {"update", "npm:missing"}, {"--model", "nope/nope", "-p", "hi"}}
				var results []result
				for _, args := range commands {
					if local && (args[0] == "install" || args[0] == "uninstall") && args[1] != "--help" {
						args = append(args, "-l")
					}
					args = append(args, "--approve")
					cmd := exec.CommandContext(context.Background(), binary, args...)
					cmd.Dir = cwd
					cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR="+agentDir, "PI_CODING_AGENT_DIR="+agentDir, "PI_OFFLINE=", "PIG_OFFLINE=", "NO_COLOR=1", "FORCE_COLOR=0")
					var stdout, stderr bytes.Buffer
					cmd.Stdout = &stdout
					cmd.Stderr = &stderr
					err := cmd.Run()
					code := 0
					if err != nil {
						if e, ok := errors.AsType[*exec.ExitError](err); ok {
							code = e.ExitCode()
						} else {
							t.Fatal(err)
						}
					}
					normalize := func(s string) string {
						return strings.NewReplacer(home, "<HOME>", ".pig/", ".pi/", "pig ", "pi ").Replace(s)
					}
					settingsPath := filepath.Join(agentDir, "settings.json")
					if local {
						settingsPath = filepath.Join(cwd, "."+name, "settings.json")
					}
					data, readErr := os.ReadFile(settingsPath)
					if readErr != nil && !os.IsNotExist(readErr) {
						t.Fatal(readErr)
					}
					// Settings object key order is outside these findings; compare the exact packages JSON value, including omission and [] versus null.
					var values map[string]json.RawMessage
					if len(data) > 0 {
						if err := json.Unmarshal(data, &values); err != nil {
							t.Fatal(err)
						}
					}
					results = append(results, result{normalize(stdout.String()), normalize(stderr.String()), string(values["packages"]), code})
				}
				return results
			}
			want := run(pi, "pi")
			got := run(pig, "pig")
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("step %d:\npig=%+v\n pi=%+v", i, got[i], want[i])
				}
			}
		})
	}
}
