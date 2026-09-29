package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2653
func TestPackageCapturedCommandWaitsForStreamClosureUpstream(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	// IPC disconnect occurs after the parent exits, so stdout must remain open until the child emits its data. No scheduling delay orders these events. On Windows, libuv puts every child that is not detached in a job object that kills it when the parent exits, so the child is detached to outlive the parent on every platform.
	script := `const {spawn}=require('node:child_process'); spawn(process.execPath,['-e','process.on("disconnect",()=>process.stdout.write("abc123\\n"))'],{stdio:['ignore',process.stdout,'ignore','ipc'],detached:true}); process.exit(0);`
	got, err := packagemanager.RunCmdInDir(t.TempDir(), node, "-e", script)
	if err != nil || got != "abc123" {
		t.Fatalf("capture=%q error=%v", got, err)
	}
}

func TestNpmUpdateSkipsExactVersionsAndIncludesBothScopes(t *testing.T) {
	for _, target := range []string{"", "npm:shared"} {
		t.Run("target="+target, func(t *testing.T) {
			cwd, agentDir := t.TempDir(), t.TempDir()
			t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
			t.Setenv("PI_OFFLINE", "")
			t.Setenv("PIG_OFFLINE", "")
			node, err := exec.LookPath("node")
			if err != nil {
				t.Fatal(err)
			}
			log, script := filepath.Join(t.TempDir(), "calls.jsonl"), filepath.Join(t.TempDir(), "npm.mjs")
			t.Setenv("PIG_TEST_NPM_LOG", log)
			if err := os.WriteFile(script, []byte(`import fs from 'node:fs'; fs.appendFileSync(process.env.PIG_TEST_NPM_LOG,JSON.stringify(process.argv.slice(2))+'\n');`), 0o600); err != nil {
				t.Fatal(err)
			}
			sm := codingagent.NewSettingsManager(cwd, agentDir)
			if err := sm.SetNpmCommand([]string{node, script}); err != nil {
				t.Fatal(err)
			}
			packages := []codingagent.PackageSource{{Source: "npm:shared"}, {Source: "npm:fixed@1.0.0"}}
			if err := sm.SetPackages(packages); err != nil {
				t.Fatal(err)
			}
			if err := sm.SetProjectPackages(packages); err != nil {
				t.Fatal(err)
			}
			if err := updatePackages(cwd, sm, target, nil); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			calls := map[string][]string{}
			for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
				var args []string
				if err := json.Unmarshal([]byte(line), &args); err != nil {
					t.Fatal(err)
				}
				if len(args) != 5 {
					t.Fatalf("unexpected command: %q", args)
				}
				if _, duplicate := calls[args[3]]; duplicate {
					t.Fatalf("duplicate scope command: %q", args)
				}
				calls[args[3]] = args
			}
			for _, root := range []string{filepath.Join(agentDir, "npm"), filepath.Join(codingagent.ProjectConfigDir(cwd), "npm")} {
				want := []string{"install", "shared@latest", "--prefix", root, "--legacy-peer-deps"}
				if !reflect.DeepEqual(calls[root], want) {
					t.Fatalf("scope %s calls=%q, want %q", root, calls[root], want)
				}
			}
			if len(calls) != 2 {
				t.Fatalf("scope commands=%v", calls)
			}
			userArgs := append([]string(nil), calls[filepath.Join(agentDir, "npm")]...)
			projectArgs := append([]string(nil), calls[filepath.Join(codingagent.ProjectConfigDir(cwd), "npm")]...)
			userArgs[3], projectArgs[3] = "USER_NPM", "PROJECT_NPM"
			row, err := json.Marshal([]any{target, userArgs, projectArgs})
			if err != nil {
				t.Fatal(err)
			}
			fmt.Printf("NPM_SCOPES %s\n", row)
		})
	}
}

func TestNpmUpdateVersionReconciliationUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, source, installed, response string
		install                           bool
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2263
		{"should update npm range packages using the configured spec", "npm:example@^1.0.0", "1.0.0", `["1.0.0","1.2.0"]`, true},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2288
		{"should skip project npm update when installed version matches latest", "npm:example@^1.0.0", "1.3.1", `["1.0.0","1.3.1","1.0.2"]`, false},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2309
		{"should skip npm updates when the installed version is newer than the registry version", "npm:example", "2.0.0", `"1.9.0"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd, agentDir := t.TempDir(), t.TempDir()
			t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
			t.Setenv("PI_OFFLINE", "")
			t.Setenv("PIG_OFFLINE", "")
			node, err := exec.LookPath("node")
			if err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(t.TempDir(), "commands.jsonl")
			script := filepath.Join(t.TempDir(), "npm.mjs")
			t.Setenv("PIG_TEST_NPM_LOG", log)
			t.Setenv("PIG_TEST_NPM_VIEW", tc.response)
			body := `import fs from 'node:fs'; const args=process.argv.slice(2); fs.appendFileSync(process.env.PIG_TEST_NPM_LOG,JSON.stringify(args)+'\n'); if(args[0]==='view')console.log(process.env.PIG_TEST_NPM_VIEW); else if(args[0]!=='install')throw new Error('unexpected command');`
			if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			sm := codingagent.NewSettingsManager(cwd, agentDir)
			if err := sm.SetNpmCommand([]string{node, script}); err != nil {
				t.Fatal(err)
			}
			if err := sm.SetProjectPackages([]codingagent.PackageSource{{Source: tc.source}}); err != nil {
				t.Fatal(err)
			}
			installed := filepath.Join(codingagent.ProjectConfigDir(cwd), "npm", "node_modules", "example")
			if err := os.MkdirAll(installed, 0o755); err != nil {
				t.Fatal(err)
			}
			content, _ := json.Marshal(map[string]string{"name": "example", "version": tc.installed})
			if err := os.WriteFile(filepath.Join(installed, "package.json"), content, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := updatePackages(cwd, sm, "npm:example", nil); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			var calls [][]string
			for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
				var args []string
				if err := json.Unmarshal([]byte(line), &args); err != nil {
					t.Fatal(err)
				}
				calls = append(calls, args)
			}
			spec := strings.TrimPrefix(tc.source, "npm:")
			want := [][]string{{"view", spec, "version", "--json"}}
			if tc.install {
				want = append(want, []string{"install", spec, "--prefix", filepath.Join(codingagent.ProjectConfigDir(cwd), "npm"), "--legacy-peer-deps"})
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("commands=%q, want %q", calls, want)
			}
			for _, args := range calls {
				for i, value := range args {
					if value == filepath.Join(codingagent.ProjectConfigDir(cwd), "npm") {
						args[i] = "PROJECT_NPM"
					}
				}
			}
			row, err := json.Marshal([]any{tc.source, tc.installed, calls})
			if err != nil {
				t.Fatal(err)
			}
			fmt.Printf("NPM_RECONCILE %s\n", row)
		})
	}
}
