package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// cli/auth-command.ts, cli/credential-print.ts and cli/auth-check.ts end to end: the pinned Pi CLI (`pi auth ...`) and Pig's auth command
// run against the same agent directory (auth.json, models.json) and environment, and must agree on exit code, stdout and stderr.
func TestAuthCommandsMatchPiEndToEnd(t *testing.T) {
	cli := filepath.Join("..", "..", "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent", "dist", "cli.js")
	if _, err := os.Stat(cli); err != nil {
		t.Skipf("pinned Pi is not installed: %v", err)
	}
	pkg, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(cli)), "package.json"))
	if err != nil || !strings.Contains(string(pkg), `"version": "`+pigversion.UpstreamVersion+`"`) {
		t.Fatalf("pinned Pi is not %s: %v", pigversion.UpstreamVersion, err)
	}
	const far = "99999999999999"
	setups := []struct {
		name   string
		auth   string
		models string
		env    map[string]string
	}{
		{name: "empty"},
		{name: "stored", auth: `{"openai":{"type":"api_key","key":"sk-stored"},"openai-codex":{"type":"oauth","access":"codex-token","refresh":"r","expires":` + far + `},"anthropic":{"type":"api_key","key":"sk-ant"}}`},
		{name: "custom provider", auth: `{"openai":{"type":"api_key","key":"sk-stored"}}`, models: `{"providers":{"local":{"baseUrl":"http://localhost:1/v1","api":"openai-completions","apiKey":"sk-local","models":[{"id":"local-model"},{"id":"gpt-5.5"}]}}}`},
		{name: "environment", env: map[string]string{"OPENAI_API_KEY": "sk-env", "ANTHROPIC_API_KEY": "sk-env-ant"}},
		{name: "env reference", models: `{"providers":{"local":{"baseUrl":"http://localhost:1/v1","api":"openai-completions","apiKey":"MY_LOCAL_KEY","models":[{"id":"local-model"}]}}}`, env: map[string]string{"MY_LOCAL_KEY": "sk-from-env"}},
		{name: "oauth only", auth: `{"openai-codex":{"type":"oauth","access":"codex-token","refresh":"r","expires":` + far + `}}`},
	}
	targets := [][]string{{"--provider", "openai"}, {"--provider", "openai-codex"}, {"--provider", "anthropic"}, {"--provider", "local"}, {"--provider", "nope"},
		{"--model", "gpt-5.5"}, {"--model", "openai/gpt-5.5"}, {"--model", "openai-codex/gpt-5.5"}, {"--model", "local/local-model"}, {"--model", "local-model"}, {"--model", "nope"},
		{"--model", "openai/nope"}, {"--model", "gpt-5.5:high"}, {"--provider", "openai", "--model", "gpt-5.5"}, {"--provider", "local", "--model", "gpt-5.5"}, {},
	}
	flags := [][]string{{}, {"--json", "--credentials"}, {"--no-refresh"}}
	type probe struct {
		setup int
		args  []string
	}
	var probes []probe
	for si := range setups {
		for _, sub := range []string{"check", "print-api-key", "print-bearer-token"} {
			for _, target := range targets {
				for _, flag := range flags {
					if sub != "check" && (len(flag) > 0 && flag[0] != "--min-expiry" && flag[0] != "--no-refresh") {
						continue
					}
					args := append(append([]string{sub}, target...), flag...)
					probes = append(probes, probe{si, args})
				}
			}
		}
	}
	dirs := make([]string, len(setups))
	for i, setup := range setups {
		dirs[i] = t.TempDir()
		if setup.auth != "" {
			if err := os.WriteFile(filepath.Join(dirs[i], "auth.json"), []byte(setup.auth), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if setup.models != "" {
			if err := os.WriteFile(filepath.Join(dirs[i], "models.json"), []byte(setup.models), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	type outcome struct {
		code           int
		stdout, stderr string
	}
	piResults := make([]outcome, len(probes))
	var wg sync.WaitGroup
	slots := make(chan struct{}, 8)
	for i, p := range probes {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer func() { <-slots; wg.Done() }()
			cmd := exec.CommandContext(t.Context(), "node", append([]string{cli, "auth"}, p.args...)...)
			env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dirs[p.setup], "PI_CODING_AGENT_DIR=" + dirs[p.setup], "PI_OFFLINE=1", "NO_COLOR=1"}
			for name, value := range setups[p.setup].env {
				env = append(env, name+"="+value)
			}
			cmd.Env = env
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			code := 0
			if err := cmd.Run(); err != nil {
				if exit, ok := errors.AsType[*exec.ExitError](err); ok {
					code = exit.ExitCode()
				} else {
					code = -2
					stderr.WriteString(err.Error())
				}
			}
			piResults[i] = outcome{code, stdout.String(), stderr.String()}
		}()
	}
	wg.Wait()
	// The probes must reach every kind of outcome, or agreement would prove nothing.
	seen := map[string]bool{}
	for _, result := range piResults {
		switch {
		case result.code == 0 && result.stdout == "ready\n":
			seen["ready"] = true
		case result.code == 0 && strings.Contains(result.stdout, `"credentials"`):
			seen["json credentials"] = true
		case result.code == 0 && strings.HasPrefix(result.stdout, "sk-"):
			seen["printed key"] = true
		case result.code == 0 && strings.HasPrefix(result.stdout, "codex-token"):
			seen["printed token"] = true
		case result.code == 1 && result.stdout == "not_ready\n":
			seen["not ready"] = true
		case result.code == 2:
			seen["invalid"] = true
		case strings.Contains(result.stderr, "configured with OAuth, not an API key"):
			seen["oauth not api key"] = true
		case strings.Contains(result.stderr, "Unknown provider"):
			seen["unknown provider"] = true
		}
	}
	for _, kind := range []string{"ready", "json credentials", "printed key", "printed token", "not ready", "invalid", "oauth not api key", "unknown provider"} {
		if !seen[kind] {
			t.Errorf("no probe produced the outcome %q", kind)
		}
	}

	for _, name := range os.Environ() {
		key, _, _ := strings.Cut(name, "=")
		if strings.HasSuffix(key, "_API_KEY") || strings.HasSuffix(key, "_TOKEN") || strings.HasSuffix(key, "_AUTH_TOKEN") || strings.HasPrefix(key, "PIG_") || strings.HasPrefix(key, "PI_") {
			t.Setenv(key, "")
		}
	}
	isolateAuthEnv(t)
	t.Setenv("PI_OFFLINE", "1")
	current := -1
	failures := 0
	for i, p := range probes {
		if p.setup != current {
			current = p.setup
			for _, name := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "MY_LOCAL_KEY"} {
				t.Setenv(name, setups[current].env[name])
			}
		}
		code, stdout, stderr := runAuthForTest(t, dirs[p.setup], p.args...)
		want := piResults[i]
		// Pi's messages name the program `pi` (APP_NAME); this program is `pig`.
		want.stdout, want.stderr = strings.ReplaceAll(want.stdout, `"pi auth`, `"pig auth`), strings.ReplaceAll(want.stderr, `"pi auth`, `"pig auth`)
		if code != want.code || stdout != want.stdout || stderr != want.stderr {
			if failures++; failures <= 12 {
				t.Errorf("%s auth %v:\n  Pig %d %q %q\n  Pi  %d %q %q", setups[p.setup].name, p.args, code, stdout, stderr, want.code, want.stdout, want.stderr)
			}
		}
	}
	if failures > 12 {
		t.Errorf("%d of %d probes differ", failures, len(probes))
	}
}
