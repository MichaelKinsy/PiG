package main

// Ports upstream packages/coding-agent/test/credential-print.test.ts and adds
// command-level coverage for output form, exit codes, and secret exposure.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func resolvePrint(t *testing.T, runtime *codingagent.RequestAuthRuntime, kind AuthCommandKind, args ...string) (string, error) {
	t.Helper()
	return ResolveCredentialForPrint(context.Background(), parseFlags(args), args, runtime, kind, nil)
}

func TestCredentialPrintPrintsResolvedAPIKey(t *testing.T) {
	isolateAuthEnv(t)
	runtime := createAuthTestRuntime(t, ai.NewInMemoryAuthStorage(map[string]ai.Credential{"openai": {Type: ai.CredentialAPIKey, Key: "test-api-key"}}), true)
	value, err := resolvePrint(t, runtime, AuthCommandAPIKey, "--provider", "openai")
	if err != nil || value != "test-api-key" {
		t.Fatalf("got %q, %v", value, err)
	}
}

func TestCredentialPrintPrintsBearerTokenFromAuthorizationHeader(t *testing.T) {
	isolateAuthEnv(t)
	runtime := createAuthTestRuntime(t, ai.NewInMemoryAuthStorage(map[string]ai.Credential{"kimi-coding": {
		Type: ai.CredentialOAuth, Access: "header-test-token", Refresh: "test-refresh-token", Expires: time.Now().Add(time.Hour).UnixMilli(),
	}}), true)
	value, err := resolvePrint(t, runtime, AuthCommandBearerToken, "--provider", "kimi-coding")
	if err != nil || value != "header-test-token" {
		t.Fatalf("got %q, %v", value, err)
	}
}

func TestCredentialPrintRefreshesExpiredOAuthTokenBeforePrinting(t *testing.T) {
	isolateAuthEnv(t)
	storage := ai.NewInMemoryAuthStorage(map[string]ai.Credential{"openai-codex": {Type: ai.CredentialOAuth, Access: "old-test-token", Refresh: "test-refresh-token", Expires: 0}})
	runtime := createAuthTestRuntime(t, storage, true)
	var refreshes atomic.Int32
	replaceOAuthRefresh(t, runtime, "openai-codex", func(context.Context, ai.Credential) (ai.Credential, error) {
		refreshes.Add(1)
		return ai.Credential{Type: ai.CredentialOAuth, Access: "fresh-test-token", Refresh: "test-refresh-token", Expires: time.Now().Add(time.Hour).UnixMilli()}, nil
	})
	value, err := resolvePrint(t, runtime, AuthCommandBearerToken, "--provider", "openai-codex")
	if err != nil || value != "fresh-test-token" {
		t.Fatalf("got %q, %v", value, err)
	}
	if refreshes.Load() != 1 {
		t.Fatalf("refresh called %d times, want once", refreshes.Load())
	}
	stored, err := storage.Read(context.Background(), "openai-codex")
	if err != nil || stored.Access != "fresh-test-token" {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
}

// misspelledCredentialsFlag is the upstream test's deliberately misspelled option.
const misspelledCredentialsFlag = "credentails" //nolint:misspell // the typo is the input under test

func TestCredentialPrintReportsUnknownAuthOptionsLikePackageCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runAuthCommandWith([]string{"auth", "check", "--provider", "openai-codex", "--" + misspelledCredentialsFlag}, authRunEnv{stdout: &stdout, stderr: &stderr, agentDir: t.TempDir()})
	if !strings.Contains(stderr.String(), `Unknown option --`+misspelledCredentialsFlag+` for "auth check".`) {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), `Use "pig --help" or "pig auth check --provider <provider> [--json] [--credentials] [--no-refresh]".`) {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if code != 1 || stdout.Len() != 0 {
		t.Fatalf("code = %d, stdout = %q", code, stdout.String())
	}
}

func TestCredentialPrintParsesCommandsAndRejectsInvalidArgumentsOrCredentialTypes(t *testing.T) {
	isolateAuthEnv(t)
	runtime := createAuthTestRuntime(t, ai.NewInMemoryAuthStorage(map[string]ai.Credential{"openai-codex": {
		Type: ai.CredentialOAuth, Access: "test-token-not-to-be-printed", Refresh: "test-refresh-token", Expires: time.Now().Add(time.Hour).UnixMilli(),
	}}), true)

	got, err := ParseAuthCommand([]string{"auth", "print-api-key", "--provider", "openai"})
	if err != nil || !reflect.DeepEqual(got, &AuthCommand{Kind: AuthCommandAPIKey, Args: []string{"--provider", "openai"}}) {
		t.Fatalf("print-api-key = %+v, %v", got, err)
	}
	if got, err := ParseAuthCommand([]string{"auth", "print-bearer-token"}); err != nil || got.Kind != AuthCommandBearerToken {
		t.Fatalf("print-bearer-token = %+v, %v", got, err)
	}
	got, err = ParseAuthCommand([]string{"auth", "print-bearer-token", "--min-expiry", "30m"})
	thirtyMinutes := float64(30 * 60_000)
	if err != nil || !reflect.DeepEqual(got, &AuthCommand{Kind: AuthCommandBearerToken, Args: []string{}, MinExpiryMs: &thirtyMinutes}) {
		t.Fatalf("--min-expiry = %+v, %v", got, err)
	}
	if _, err := ParseAuthCommand([]string{"auth", "print-api-key", "--min-expiry", "30m"}); err == nil || !strings.Contains(err.Error(), "only supported by print-bearer-token") {
		t.Fatalf("--min-expiry on print-api-key = %v", err)
	}
	for _, args := range [][]string{{"auth", "--help"}, {"auth", "print-api-key", "--help"}, {"auth", "print-bearer-token", "-h"}, {"auth", "check", "--help"}} {
		if !IsAuthCommandHelp(args) {
			t.Fatalf("IsAuthCommandHelp(%v) = false", args)
		}
	}
	if _, err := ParseAuthCommand([]string{"auth", "unknown"}); !isAuthCommandError(err) {
		t.Fatalf("unknown subcommand error = %v", err)
	}
	if _, err := resolvePrint(t, runtime, AuthCommandAPIKey); err == nil || !strings.Contains(err.Error(), "requires --provider <provider> or --model <model>") {
		t.Fatalf("missing target error = %v", err)
	}
	if _, err := resolvePrint(t, runtime, AuthCommandAPIKey, "--provider", "openai-codex"); err == nil || !strings.Contains(err.Error(), "configured with OAuth") {
		t.Fatalf("oauth api-key error = %v", err)
	}
}

func isAuthCommandError(err error) bool {
	_, ok := errors.AsType[*AuthCommandError](err)
	return ok
}

func TestParseAuthCommandMinExpiryUnits(t *testing.T) {
	cases := map[string]float64{"250ms": 250, "45s": 45_000, "30m": 1_800_000, "1h": 3_600_000, "2H": 7_200_000, "5M": 18_000_000}
	for value, want := range cases {
		command, err := ParseAuthCommand([]string{"auth", "print-bearer-token", "--min-expiry", value})
		if err != nil || command.MinExpiryMs == nil || *command.MinExpiryMs != want {
			t.Fatalf("--min-expiry %s = %+v, %v; want %g", value, command, err, want)
		}
	}
	for _, value := range []string{"", "30", "1d", "-5m", "1.5h"} {
		args := []string{"auth", "print-bearer-token", "--min-expiry"}
		if value != "" {
			args = append(args, value)
		}
		if _, err := ParseAuthCommand(args); err == nil || err.Error() != "--min-expiry must use a duration such as 30m or 1h" {
			t.Fatalf("--min-expiry %q error = %v", value, err)
		}
	}
	if _, err := ParseAuthCommand([]string{"auth", "print-api-key", "--json"}); err == nil || err.Error() != "--json is only supported by auth check" {
		t.Fatalf("--json on print-api-key = %v", err)
	}
	if _, err := ParseAuthCommand([]string{"auth"}); err == nil ||
		err.Error() != `Unknown auth command "". Use "pig auth print-api-key", "pig auth print-bearer-token", or "pig auth check".` {
		t.Fatalf("bare auth parse = %v", err)
	}
}

func TestGetAuthCredentialExtractsOnlyBearerTokens(t *testing.T) {
	cases := []struct {
		name string
		auth *ai.AuthResult
		want string
	}{
		{"nil", nil, ""},
		{"api key wins", &ai.AuthResult{Auth: ai.ModelAuth{APIKey: "key", Headers: ai.ProviderHeaders{"Authorization": new("Bearer tok")}}}, "key"},
		{"bearer header", &ai.AuthResult{Auth: ai.ModelAuth{Headers: ai.ProviderHeaders{"authorization": new("bearer  tok")}}}, "tok"},
		{"basic header", &ai.AuthResult{Auth: ai.ModelAuth{Headers: ai.ProviderHeaders{"Authorization": new("Basic abc")}}}, ""},
		{"deleted header", &ai.AuthResult{Auth: ai.ModelAuth{Headers: ai.ProviderHeaders{"Authorization": nil}}}, ""},
		{"multi-line value", &ai.AuthResult{Auth: ai.ModelAuth{Headers: ai.ProviderHeaders{"Authorization": new("Bearer a\nb")}}}, ""},
		{"other header", &ai.AuthResult{Auth: ai.ModelAuth{Headers: ai.ProviderHeaders{"cf-aig-authorization": new("Bearer cf")}}}, ""},
	}
	for _, tc := range cases {
		if got := GetAuthCredential(tc.auth); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func writeAuthJSON(t *testing.T, agentDir string, content map[string]ai.Credential) {
	t.Helper()
	data, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "auth.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func runAuthForTest(t *testing.T, agentDir string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runAuthCommandWith(append([]string{"auth"}, args...), authRunEnv{stdout: &stdout, stderr: &stderr, agentDir: agentDir})
	return code, stdout.String(), stderr.String()
}

func TestAuthCommandPrintsSecretsOnlyInTheRequestedForm(t *testing.T) {
	isolateAuthEnv(t)
	agentDir := t.TempDir()
	const apiKey = "sk-test-openai-secret"
	const token = "codex-access-secret"
	writeAuthJSON(t, agentDir, map[string]ai.Credential{
		"openai":       {Type: ai.CredentialAPIKey, Key: apiKey},
		"openai-codex": {Type: ai.CredentialOAuth, Access: token, Refresh: "refresh-secret", Expires: time.Now().Add(2 * time.Hour).UnixMilli()},
	})
	noSecret := func(name string, outputs ...string) {
		t.Helper()
		for _, output := range outputs {
			for _, secret := range []string{apiKey, token, "refresh-secret"} {
				if strings.Contains(output, secret) {
					t.Fatalf("%s leaked %q in %q", name, secret, output)
				}
			}
		}
	}

	code, stdout, stderr := runAuthForTest(t, agentDir, "check", "--provider", "openai")
	if code != 0 || stdout != "ready\n" {
		t.Fatalf("check = %d %q %q", code, stdout, stderr)
	}
	noSecret("check", stdout, stderr)

	code, stdout, stderr = runAuthForTest(t, agentDir, "check", "--provider", "openai", "--json")
	if code != 0 || stdout != `{"status":"ready","provider":"openai","authType":"api_key"}`+"\n" {
		t.Fatalf("check --json = %d %q %q", code, stdout, stderr)
	}
	noSecret("check --json", stdout, stderr)

	code, stdout, _ = runAuthForTest(t, agentDir, "check", "--provider", "openai", "--credentials")
	if code != 0 || stdout != apiKey+"\n" {
		t.Fatalf("check --credentials = %d %q", code, stdout)
	}
	code, stdout, _ = runAuthForTest(t, agentDir, "check", "--provider", "openai-codex", "--json", "--credentials")
	if code != 0 || stdout != `{"status":"ready","provider":"openai-codex","authType":"oauth","credentials":"`+token+`"}`+"\n" {
		t.Fatalf("check --json --credentials = %d %q", code, stdout)
	}

	code, stdout, stderr = runAuthForTest(t, agentDir, "print-api-key", "--provider", "openai-codex")
	if code != 1 || stdout != "" || stderr != `Error: Provider "openai-codex" is configured with OAuth, not an API key`+"\n" {
		t.Fatalf("print-api-key oauth = %d %q %q", code, stdout, stderr)
	}
	noSecret("print-api-key oauth", stdout, stderr)

	code, stdout, stderr = runAuthForTest(t, agentDir, "print-bearer-token", "--provider", "openai")
	if code != 1 || stdout != "" || stderr != `Error: Provider "openai" is not configured with an OAuth bearer token`+"\n" {
		t.Fatalf("print-bearer-token api key = %d %q %q", code, stdout, stderr)
	}
	noSecret("print-bearer-token api key", stdout, stderr)

	// Both stored providers carry gpt-5.5; each print command selects only
	// its own credential type.
	code, stdout, stderr = runAuthForTest(t, agentDir, "print-api-key", "--model", "gpt-5.5")
	if code != 0 || stdout != apiKey+"\n" {
		t.Fatalf("print-api-key --model = %d %q %q", code, stdout, stderr)
	}
	code, stdout, stderr = runAuthForTest(t, agentDir, "print-bearer-token", "--model", "gpt-5.5")
	if code != 0 || stdout != token+"\n" {
		t.Fatalf("print-bearer-token --model = %d %q %q", code, stdout, stderr)
	}
}

func TestAuthCommandExitCodesAndErrors(t *testing.T) {
	isolateAuthEnv(t)
	agentDir := t.TempDir()
	cases := []struct {
		name           string
		args           []string
		code           int
		stdout, stderr string
	}{
		{"help", []string{}, 0, authCommandHelp + "\n", ""},
		{"unknown subcommand", []string{"nope"}, 1, "", `Error: Unknown auth command "nope". Use "pig auth print-api-key", "pig auth print-bearer-token", or "pig auth check".` + "\n"},
		{"check without target", []string{"check"}, 2, "", "Error: Auth checks require --provider <provider> or --model <model>\n"},
		{"print without target", []string{"print-api-key"}, 1, "", "Error: Credential printing requires --provider <provider> or --model <model>\n"},
		{"check with message", []string{"check", "--provider", "openai", "hello"}, 2, "", "Error: Auth commands only accept --provider and --model\n"},
		{"print with api key", []string{"print-api-key", "--provider", "openai", "--api-key", "x"}, 1, "", "Error: Auth commands only accept --provider and --model\n"},
		{"check diagnostics", []string{"check", "--provider", "openai", "-x"}, 2, "", "Error: Unknown option: -x\n"},
		{"not ready", []string{"check", "--provider", "openai"}, 1, "not_ready\n", ""},
		{"not ready json", []string{"check", "--provider", "openai", "--json"}, 1, `{"status":"not_ready","provider":"openai","reason":"credentials_not_configured"}` + "\n", ""},
		{"unknown provider", []string{"check", "--provider", "nope", "--json"}, 1, `{"status":"not_ready","provider":"nope","reason":"provider_not_found"}` + "\n", ""},
		{"unknown model", []string{"check", "--model", "no-such-model-xyz", "--json"}, 2, `{"status":"invalid","provider":"no-such-model-xyz","reason":"invalid_state"}` + "\n", ""},
		{"print unknown provider", []string{"print-api-key", "--provider", "nope"}, 1, "", `Error: Unknown provider "nope". Use --list-models to see available providers.` + "\n"},
		{"print unknown model", []string{"print-api-key", "--model", "gpt-5.5"}, 1, "", `Error: Model "gpt-5.5" not found. Use --list-models to see available models.` + "\n"},
		{"print unconfigured", []string{"print-api-key", "--provider", "openai"}, 1, "", "Error: No usable API key is configured\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runAuthForTest(t, agentDir, tc.args...)
			if code != tc.code || stdout != tc.stdout || stderr != tc.stderr {
				t.Fatalf("got %d %q %q; want %d %q %q", code, stdout, stderr, tc.code, tc.stdout, tc.stderr)
			}
		})
	}
	if code := runAuthCommandWith([]string{"login"}, authRunEnv{agentDir: agentDir}); code != -1 {
		t.Fatalf("non-auth command = %d, want -1", code)
	}
}

func TestAuthCheckNoRefreshCreatesNothingAndDoesNotRefresh(t *testing.T) {
	isolateAuthEnv(t)
	agentDir := filepath.Join(t.TempDir(), "agent")
	code, stdout, _ := runAuthForTest(t, agentDir, "check", "--provider", "openai", "--no-refresh")
	if code != 1 || stdout != "not_ready\n" {
		t.Fatalf("check --no-refresh = %d %q", code, stdout)
	}
	if _, err := os.Stat(agentDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("--no-refresh created the agent dir: %v", err)
	}

	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeAuthJSON(t, agentDir, map[string]ai.Credential{"openai-codex": {Type: ai.CredentialOAuth, Access: "stale-token", Refresh: "r", Expires: 1}})
	code, stdout, _ = runAuthForTest(t, agentDir, "check", "--provider", "openai-codex", "--no-refresh", "--credentials")
	if code != 0 || stdout != "stale-token\n" {
		t.Fatalf("check --no-refresh --credentials = %d %q", code, stdout)
	}
}

func TestAuthCheckWithModelsJSONProvider(t *testing.T) {
	isolateAuthEnv(t)
	t.Setenv("PIG_TEST_CUSTOM_KEY", "custom-secret")
	agentDir := t.TempDir()
	models := `{"providers":{"custom":{"baseUrl":"https://example.test/v1","apiKey":"$PIG_TEST_CUSTOM_KEY","api":"openai-completions","models":[{"id":"custom-model"}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runAuthForTest(t, agentDir, "check", "--model", "custom/custom-model", "--json")
	if code != 0 || stdout != `{"status":"ready","provider":"custom","authType":"api_key"}`+"\n" {
		t.Fatalf("check custom = %d %q %q", code, stdout, stderr)
	}
	code, stdout, stderr = runAuthForTest(t, agentDir, "print-api-key", "--provider", "custom")
	if code != 0 || stdout != "custom-secret\n" {
		t.Fatalf("print custom = %d %q %q", code, stdout, stderr)
	}

	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(`{"providers":{`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ = runAuthForTest(t, agentDir, "check", "--provider", "openai")
	if code != 2 || stdout != "invalid\n" {
		t.Fatalf("check with broken models.json = %d %q", code, stdout)
	}
}

func TestParseAuthCommandLargeMinimumExpiry(t *testing.T) {
	for _, value := range []string{"9223372036854775807h", "9223372036854775808ms", strings.Repeat("9", 400) + "h"} {
		command, err := ParseAuthCommand([]string{"auth", "print-bearer-token", "--min-expiry", value})
		if err != nil || command.MinExpiryMs == nil || float64(*command.MinExpiryMs) < 9e18 {
			t.Fatalf("large upstream duration %s overflowed or was rejected: %+v, %v", value, command, err)
		}
	}
}
