package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Upstream 0.99.1 changes only how its harness starts the CLI: `--import` receives the source resolver as a file URL (pathToFileURL) instead of a path
// (.upstream/v0.99.1/packages/coding-agent/test/stdout-cleanliness.test.ts:11-12). The Go test starts the compiled binary and has no Node loader flag, so that substitution has no Go
// counterpart: the inputs and expectations of every case are unchanged.
func TestStdoutCleanlinessUpstream(t *testing.T) {
	bin := buildPigBinaryForSignalTest(t)
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		args       []string
		structured bool
	}{
		// .upstream/v0.99.1/packages/coding-agent/test/stdout-cleanliness.test.ts:90
		{"prints plain --help to stdout when stdout is redirected", []string{"--help"}, false},
		// .upstream/v0.99.1/packages/coding-agent/test/stdout-cleanliness.test.ts:100
		{"keeps stdout empty for --mode json --help while routing trusted startup chatter to stderr", []string{"--mode", "json", "--help", "--approve"}, true},
		// main.ts:129,638-642 also distinguishes explicit text/print/RPC metadata from plain help.
		{"explicit text metadata reserves stdout", []string{"--mode", "text", "--help", "--approve"}, true},
		{"explicit print metadata reserves stdout", []string{"--print", "--help", "--approve"}, true},
		{"explicit RPC metadata reserves stdout", []string{"--mode", "rpc", "--help", "--approve"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			agentDir, project := filepath.Join(root, "agent"), filepath.Join(root, "project")
			if err := os.MkdirAll(agentDir, 0o755); err != nil {
				t.Fatal(err)
			}
			configDir := filepath.Join(project, codingagent.CONFIG_DIR_NAME)
			if err := os.MkdirAll(configDir, 0o755); err != nil {
				t.Fatal(err)
			}
			npm := filepath.Join(root, "fake-npm.mjs")
			if err := os.WriteFile(npm, []byte("console.log('changed 1 package in 471ms');\nconsole.log('found 0 vulnerabilities');\nprocess.exit(0);\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			settings, err := json.Marshal(map[string]any{"packages": []string{"npm:fake-package"}, "npmCommand": []string{node, npm}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(configDir, "settings.json"), settings, 0o644); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
			defer cancel()
			cmd := exec.CommandContext(ctx, bin, tc.args...)
			cmd.Dir = project
			cmd.Env = append(os.Environ(), "PIG_HOME="+filepath.Join(root, "home"), codingagent.ENV_AGENT_DIR+"="+agentDir, "PIG_OFFLINE=", "PI_OFFLINE=", "PI_SKIP_VERSION_CHECK=1", "PIG_SKIP_VERSION_CHECK=1")
			var stdout, stderr strings.Builder
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("help failed: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
			}
			if tc.structured {
				if stdout.Len() != 0 {
					t.Fatalf("JSON help wrote stdout: %q", stdout.String())
				}
				for _, want := range []string{"changed 1 package in 471ms", "found 0 vulnerabilities", "Usage:"} {
					if !strings.Contains(stderr.String(), want) {
						t.Errorf("stderr missing %q: %s", want, stderr.String())
					}
				}
			} else {
				if !strings.Contains(stdout.String(), "Usage:") {
					t.Fatalf("help missing from stdout: %s", stdout.String())
				}
				for _, unwanted := range []string{"Usage:", "changed 1 package in 471ms", "found 0 vulnerabilities"} {
					if strings.Contains(stderr.String(), unwanted) {
						t.Errorf("unexpected stderr %q: %s", unwanted, stderr.String())
					}
				}
			}
			trace, err := json.Marshal([]any{tc.args, stdout.Len() == 0, strings.Contains(stdout.String(), "Usage:"), strings.Contains(stderr.String(), "Usage:"), strings.Contains(stderr.String(), "changed 1 package in 471ms"), strings.Contains(stderr.String(), "found 0 vulnerabilities")})
			if err != nil {
				t.Fatal(err)
			}
			fmt.Printf("STDOUT_CONTRACT %s\n", trace)
		})
	}
}
