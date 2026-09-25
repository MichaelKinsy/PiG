package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// `pig --diagnose` produces a human-readable
// introspection dump. Tests pin section headers + the helper
// shapes (mask, credential label) so that:
//   - bug-report consumers can rely on stable section boundaries
//   - secret-shaped env vars never leak verbatim

func TestDiagnoseSectionHeaders(t *testing.T) {
	var buf bytes.Buffer
	runDiagnose(&buf, "/test/binary")
	out := buf.String()

	wantSections := []string{
		"pig diagnostics",
		"\nbuild:",
		"\npaths\n",
		"config root:",
		"\nauth (logged-in providers)\n",
		"\ntools (built-in coding set)\n",
		"\nmodels (registered providers)\n",
		"\ndefault model\n",
		"\nenvironment\n",
	}
	for _, want := range wantSections {
		if !strings.Contains(out, want) {
			t.Errorf("missing section %q in:\n%s", want, out)
		}
	}
	// The pin names the Pi release PiG targets, not a parity result.
	if want := "\nupstream-pin:      " + UpstreamVersion + "\n"; !strings.Contains(out, want) || strings.Contains(out, "upstream-parity") {
		t.Errorf("want %q and no upstream-parity label in:\n%s", want, out)
	}
	// Built-in tool names should appear (sorted).
	for _, tool := range []string{"bash", "edit", "find", "grep", "ls", "read", "write"} {
		if !strings.Contains(out, "  "+tool+"\n") {
			t.Errorf("missing tool %q in tools section", tool)
		}
	}
}

func TestDiagnoseIncludesBinaryPath(t *testing.T) {
	var buf bytes.Buffer
	runDiagnose(&buf, "/Users/me/.local/bin/pig")
	if !strings.Contains(buf.String(), "binary:            /Users/me/.local/bin/pig") {
		t.Errorf("binary path not echoed verbatim:\n%s", buf.String())
	}
}

func TestMaskEnvSecretShaped(t *testing.T) {
	cases := []struct {
		key, value, want string
	}{
		{"OPENAI_API_KEY", "sk-1234567890abcdef", "sk-123…(masked)"}, // gitleaks:allow -- synthetic masking fixture
		{"ANTHROPIC_API_KEY", "ak-abcdefgh", "ak-abc…(masked)"},
		{"GITHUB_TOKEN", "ghp_xxxxxxxxxxxx", "ghp_xx…(masked)"},
		{"TOGETHER_API_KEY", "tgt_1234567890abcdef", "tgt_12…(masked)"}, // gitleaks:allow -- synthetic masking fixture
		{"SOME_SECRET_FOO", "lol", "(set)"},                             // ≤6 chars
		{"PATH", "/usr/local/bin:/usr/bin", "/usr/local/bin:/usr/bin"},
		{"TERM", "xterm-256color", "xterm-256color"},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			got := maskEnv(c.key, c.value)
			if got != c.want {
				t.Errorf("mask(%q, %q) = %q want %q", c.key, c.value, got, c.want)
			}
		})
	}
}

func TestCredentialKindLabel(t *testing.T) {
	cases := []struct {
		c    ai.Credential
		want string
	}{
		{ai.Credential{Type: ai.CredentialAPIKey}, "api_key"},
		{ai.Credential{Type: ai.CredentialOAuth}, "oauth"},
	}
	for _, c := range cases {
		got := credentialKindLabel(c.c)
		if got != c.want {
			t.Errorf("got %q want %q", got, c.want)
		}
	}
}

// Startup warns that extensions may build against a stale SDK and points the
// user at this report, so the report has to carry that detail. It pointed at
// "pig doctor", which does not exist, and the section it promised was missing.
func TestDiagnoseReportsExtensionSDKStaging(t *testing.T) {
	var buf bytes.Buffer
	runDiagnose(&buf, "/usr/local/bin/pig")
	out := buf.String()

	if !strings.Contains(out, "extension SDKs") {
		t.Fatal("diagnose has no extension SDK section, so the startup warning points at nothing")
	}
	for _, lang := range []string{"go:", "python:", "rust:"} {
		if !strings.Contains(out, lang) {
			t.Errorf("diagnose omits the %s SDK", strings.TrimSuffix(lang, ":"))
		}
	}
}

// The warning must name a command that exists. pig has diagnose, not doctor.
func TestStaleSDKWarningNamesARealCommand(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(source, []byte("pig doctor")) {
		t.Error("main.go tells the user to run 'pig doctor', which is not a pig command")
	}
	if !bytes.Contains(source, []byte("pig diagnose")) {
		t.Error("the stale-SDK warning should point at 'pig diagnose'")
	}
}
