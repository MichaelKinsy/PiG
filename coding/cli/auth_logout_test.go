package cli

// pi: packages/coding-agent/src/cli/auth-logout.ts

// Ports packages/coding-agent/test/auth-logout.test.ts

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

// outputTerminal records the lines of an AuthOutput.
type outputTerminal struct{ lines, errors []string }

func (t *outputTerminal) Write(line string) { t.lines = append(t.lines, line) }
func (t *outputTerminal) Error(line string) { t.errors = append(t.errors, line) }

type logoutRuntimeOverride struct {
	AuthLogoutRuntime
	logout func(context.Context, string) error
}

func (o logoutRuntimeOverride) Logout(ctx context.Context, id string) error {
	if o.logout != nil {
		return o.logout(ctx, id)
	}
	return o.AuthLogoutRuntime.Logout(ctx, id)
}

func logoutOAuthCredential() ai.Credential {
	return ai.Credential{Type: ai.CredentialOAuth, Access: "access", Refresh: "refresh", Expires: time.Now().Add(time.Hour).UnixMilli()}
}

func runAuthLogoutForTest(t *testing.T, runtime AuthLogoutRuntime, args ...string) (*outputTerminal, int) {
	t.Helper()
	terminal := &outputTerminal{}
	return terminal, RunAuthLogout(t.Context(), args, AuthLogoutOptions{ModelRuntime: runtime, Terminal: terminal})
}

func TestAuthLogoutRequiresExactlyOneProvider(t *testing.T) {
	runtime, _ := authTestRuntime(t, nil)
	for _, args := range [][]string{{}, {"--api-key"}, {"radius", "extra"}} {
		terminal, code := runAuthLogoutForTest(t, runtime, args...)
		if code != 1 || !reflect.DeepEqual(terminal.errors, []string{"Usage: pig auth logout <provider>"}) {
			t.Fatalf("%v: code %d, errors %q", args, code, terminal.errors)
		}
	}
}

func TestAuthLogoutRemovesAStoredAccountLogin(t *testing.T) {
	runtime, store := authTestRuntime(t, map[string]ai.Credential{"radius": logoutOAuthCredential()})
	terminal, code := runAuthLogoutForTest(t, runtime, "radius")
	if code != 0 || !reflect.DeepEqual(terminal.lines, []string{"Logged out of Radius."}) {
		t.Fatalf("code %d, lines %q, errors %q", code, terminal.lines, terminal.errors)
	}
	if got := readStoredCredential(t, store, "radius"); got != nil {
		t.Fatalf("stored %+v", got)
	}
}

func TestAuthLogoutRemovesAStoredAPIKeyByProviderNameAndKeepsOtherCredentials(t *testing.T) {
	runtime, store := authTestRuntime(t, map[string]ai.Credential{
		"openai": {Type: ai.CredentialAPIKey, Key: "sk-test"},
		"radius": logoutOAuthCredential(),
	})
	terminal, code := runAuthLogoutForTest(t, runtime, "OpenAI")
	want := []string{"Removed stored API key for OpenAI. Environment variables and models.json config are unchanged."}
	if code != 0 || !reflect.DeepEqual(terminal.lines, want) {
		t.Fatalf("code %d, lines %q, errors %q", code, terminal.lines, terminal.errors)
	}
	if got := readStoredCredential(t, store, "openai"); got != nil {
		t.Fatalf("openai stored %+v", got)
	}
	if got := readStoredCredential(t, store, "radius"); got == nil || got.Type != ai.CredentialOAuth {
		t.Fatalf("radius stored %+v", got)
	}
}

func TestAuthLogoutSucceedsWhenNothingIsStoredSoItCanBeRepeated(t *testing.T) {
	runtime, _ := authTestRuntime(t, nil)
	terminal, code := runAuthLogoutForTest(t, runtime, "radius")
	want := []string{"No stored credentials for Radius. Environment variables and models.json config are unchanged."}
	if code != 0 || !reflect.DeepEqual(terminal.lines, want) {
		t.Fatalf("code %d, lines %q", code, terminal.lines)
	}
}

func TestAuthLogoutRemovesCredentialsOfProvidersThatAreNotLoadedMatchedByID(t *testing.T) {
	runtime, store := authTestRuntime(t, map[string]ai.Credential{"extension-provider": logoutOAuthCredential()})
	terminal, code := runAuthLogoutForTest(t, runtime, "extension-provider")
	if code != 0 || !reflect.DeepEqual(terminal.lines, []string{"Logged out of extension-provider."}) {
		t.Fatalf("code %d, lines %q", code, terminal.lines)
	}
	if got := readStoredCredential(t, store, "extension-provider"); got != nil {
		t.Fatalf("stored %+v", got)
	}
}

func TestAuthLogoutReportsCredentialRemovalFailures(t *testing.T) {
	runtime, _ := authTestRuntime(t, map[string]ai.Credential{"radius": logoutOAuthCredential()})
	override := logoutRuntimeOverride{AuthLogoutRuntime: runtime, logout: func(context.Context, string) error { return errors.New("storage failed") }}
	terminal, code := runAuthLogoutForTest(t, override, "radius")
	if code != 1 || !reflect.DeepEqual(terminal.errors, []string{"Logout failed: storage failed"}) {
		t.Fatalf("code %d, errors %q", code, terminal.errors)
	}
}

func TestAuthLogoutReportsCommittedCredentialRemovalSeparatelyFromAFailedLogout(t *testing.T) {
	runtime, _ := authTestRuntime(t, map[string]ai.Credential{"radius": logoutOAuthCredential()})
	override := logoutRuntimeOverride{AuthLogoutRuntime: runtime, logout: func(context.Context, string) error {
		return coding.NewCredentialSynchronizationError("radius", coding.CredentialSynchronizationLogout, nil, errors.New("snapshot failed"))
	}}
	terminal, code := runAuthLogoutForTest(t, override, "radius")
	if code != 1 || len(terminal.errors) == 0 || terminal.errors[0][:len("Credentials removed for Radius, but local model state could not be synchronized:")] != "Credentials removed for Radius, but local model state could not be synchronized:" {
		t.Fatalf("code %d, errors %q", code, terminal.errors)
	}
}

func TestAuthLogoutRejectsAnUnknownProviderAndListsTheStoredCredentials(t *testing.T) {
	runtime, _ := authTestRuntime(t, map[string]ai.Credential{"radius": logoutOAuthCredential()})
	terminal, code := runAuthLogoutForTest(t, runtime, "nope")
	if code != 1 || !reflect.DeepEqual(terminal.errors, []string{`Unknown provider "nope".`, "Stored credentials: radius"}) {
		t.Fatalf("code %d, errors %q", code, terminal.errors)
	}
}
