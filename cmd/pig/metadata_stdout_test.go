package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Pi main.ts:129-130,638-642 reserves protocol stdout before all non-interactive startup, except plain help/list-models without explicit mode or print.
func TestListModelsStdoutRouting(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	models := `{"providers":{"routing-test":{"baseUrl":"http://127.0.0.1:1/v1","api":"openai-completions","apiKey":"fixture","models":[{"id":"routing-model","name":"Routing model","contextWindow":1024,"maxTokens":64,"reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	var listing string
	for _, tc := range []struct {
		name       string
		flags      []string
		redirected bool
	}{
		{"plain", nil, false},
		{"json", []string{"--mode", "json"}, true},
		{"rpc", []string{"--mode", "rpc"}, true},
		{"explicit text", []string{"--mode", "text"}, true},
		{"print", []string{"--print"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--offline", "--no-extensions", "--list-models", "routing-model"}, tc.flags...)
			cmd := exec.CommandContext(t.Context(), binary, args...)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "HOME="+root, "PIG_HOME="+filepath.Join(root, "home"), "PIG_CODING_AGENT_DIR="+agentDir, "FORCE_COLOR=0")
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("list models: %v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
			}
			if !tc.redirected {
				listing = stdout.String()
				if !strings.Contains(listing, "routing-model") || stderr.Len() != 0 {
					t.Fatalf("plain stdout=%q stderr=%q", listing, stderr.String())
				}
			} else if stdout.Len() != 0 || stderr.String() != listing {
				t.Fatalf("metadata stdout=%q stderr=%q, want empty stdout and listing %q", stdout.String(), stderr.String(), listing)
			}
		})
	}
}

// Pi main.ts:857-871 answers --help and --list-models from the extension-populated runtime before main.ts:895-906 reports runtime diagnostics. Every mode lists -e providers, and a failed extension neither reports its error nor changes exit status 0.
func TestRuntimeMetadataUsesLoadedExtensionsInEveryMode(t *testing.T) {
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	provider, err := filepath.Abs(filepath.Join("testdata", "startup-provider.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	broken := filepath.Join(root, "broken.mjs")
	if err := os.WriteFile(broken, []byte("export default function () { throw new Error(\"broken startup fixture\"); }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), binary, append([]string{"--offline", "--no-extensions", "-e", provider, "-e", broken}, args...)...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "HOME="+root, "PIG_HOME="+filepath.Join(root, "home"), "PIG_CODING_AGENT_DIR="+filepath.Join(root, "agent"), "FORCE_COLOR=0")
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Errorf("%q: %v stdout=%q stderr=%q", args, err, stdout.String(), stderr.String())
		}
		return stdout.String(), stderr.String()
	}
	listing, stderr := run("--list-models", "startup")
	if !strings.Contains(listing, "startup-prov  startup-model") || stderr != "" {
		t.Errorf("plain list stdout=%q stderr=%q", listing, stderr)
	}
	help, stderr := run("--help")
	if !strings.HasPrefix(help, "pig - ") || stderr != "" {
		t.Errorf("plain help stdout=%q stderr=%q", help, stderr)
	}
	for _, mode := range []string{"json", "rpc"} {
		if stdout, stderr := run("--mode", mode, "--list-models", "startup"); stdout != "" || stderr != listing {
			t.Errorf("%s list stdout=%q stderr=%q, want empty stdout and listing %q", mode, stdout, stderr, listing)
		}
	}
	if stdout, stderr := run("--mode", "rpc", "--help"); stdout != "" || stderr != help {
		t.Errorf("rpc help stdout=%q stderr=%q, want empty stdout and plain help", stdout, stderr)
	}
}

func TestPlainRuntimeMetadataPredicate(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		plain bool
	}{
		{[]string{"--help"}, true},
		{[]string{"--list-models"}, true},
		{[]string{"--list-models", ""}, true},
		{[]string{"--list-models", "routing-model"}, true},
		{[]string{"--mode", "json", "--help"}, false},
		{[]string{"--mode", "rpc", "--list-models"}, false},
		{[]string{"--mode", "text", "--list-models"}, false},
		{[]string{"--print", "--list-models"}, false},
		{nil, false},
	} {
		flags := parseFlags(tc.args)
		if got := isPlainRuntimeMetadataCommand(flags); got != tc.plain {
			t.Errorf("%q: plain metadata=%v, want %v", tc.args, got, tc.plain)
		}
	}
}

// An empty search is still a supplied list-models request, and repeated flags use the last value.
func TestListModelsFlagPresence(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		search string
		all    bool
	}{
		{[]string{"--list-models", ""}, "", true},
		{[]string{"--list-models", "old", "--list-models"}, "", true},
		{[]string{"--list-models", "--list-models", "new"}, "new", false},
	} {
		got := parseFlags(tc.args)
		if got.ListModels != tc.search || got.ListModelsAll != tc.all {
			t.Errorf("%q: search=%q all=%v, want %q/%v", tc.args, got.ListModels, got.ListModelsAll, tc.search, tc.all)
		}
	}
}
