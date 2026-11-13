package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// callbackLoginProvider is an OAuth provider whose Login is the test's function, so a test decides which callbacks the flow calls and what it does with
// their answers.
type callbackLoginProvider struct {
	id    string
	login func(ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error)
}

func (p callbackLoginProvider) ID() string               { return p.id }
func (p callbackLoginProvider) Name() string             { return p.id }
func (p callbackLoginProvider) UsesCallbackServer() bool { return false }
func (p callbackLoginProvider) Login(callbacks ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	return p.login(callbacks)
}
func (p callbackLoginProvider) RefreshToken(ai.OAuthCredentials) (ai.OAuthCredentials, error) {
	return ai.OAuthCredentials{}, errors.New("unused")
}
func (p callbackLoginProvider) GetAPIKey(creds ai.OAuthCredentials) string { return creds.Access }

// failingStoreLoginProvider keeps its credential in a store of its own that refuses the write.
type failingStoreLoginProvider struct{ callbackLoginProvider }

func (failingStoreLoginProvider) OAuthCredentialStatus() (ai.OAuthCredentialStatus, bool) {
	return ai.OAuthCredentialStatus{}, false
}
func (failingStoreLoginProvider) StoreOAuthCredentials(ai.OAuthCredentials) (string, error) {
	return "", errors.New("store refused the credential")
}
func (failingStoreLoginProvider) DeleteOAuthCredentials() (bool, error) { return false, nil }

func registerLoginProvider(t *testing.T, provider ai.OAuthProviderInterface) {
	t.Helper()
	ai.RegisterOAuthProvider(provider.ID(), provider)
	t.Cleanup(func() { ai.UnregisterOAuthProvider(provider.ID()) })
}

func loggedIn(ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	return ai.OAuthCredentials{Access: "token", Refresh: "refresh", Expires: 4102444800000}, nil
}

// login (cli.ts:78-81) saves after the awaited provider login and prints `Credentials saved to auth.json` only after saveAuth (:28-33) returned: a
// writeFileSync that throws rejects login, and main().catch (:124-127) prints `Error: <message>` and exits 1. So a failed save reports the failure
// and never says the credential was saved.
func TestLoginCommandFailedSaveIsTheErrorAndNothingIsReportedSaved(t *testing.T) {
	t.Run("auth.json cannot be written", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("PIG_CODING_AGENT_DIR", dir)
		// As in Pi, auth.json exists (existsSync, :31), its mode can be changed, and writing it fails: it is a directory.
		if err := os.Mkdir(filepath.Join(dir, "auth.json"), 0o700); err != nil {
			t.Fatal(err)
		}
		registerLoginProvider(t, callbackLoginProvider{id: "save-failure-probe", login: loggedIn})
		stdout, stderr, code := captureWithStdin(t, "", func() int { return runLoginCommand([]string{"login", "save-failure-probe"}) })
		if code != 1 || !strings.HasPrefix(stderr, "Error: ") || strings.Contains(stdout, "Credentials saved") {
			t.Fatalf("code %d, want 1 with an `Error: ` line and no saved message\nstdout: %q\nstderr: %q", code, stdout, stderr)
		}
	})
	t.Run("the provider's own store refuses", func(t *testing.T) {
		t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
		registerLoginProvider(t, failingStoreLoginProvider{callbackLoginProvider{id: "store-failure-probe", login: loggedIn}})
		stdout, stderr, code := captureWithStdin(t, "", func() int { return runLoginCommand([]string{"login", "store-failure-probe"}) })
		if code != 1 || stderr != "Error: store refused the credential\n" || strings.Contains(stdout, "Credentials saved") {
			t.Fatalf("code %d, want 1 with the store's error and no saved message\nstdout: %q\nstderr: %q", code, stdout, stderr)
		}
	})
}

// The provider awaits each prompt (cli.ts:57 `prompt: (authPrompt) => answerPrompt(rl, authPrompt)`), and a prompt that rejects rejects the login,
// which main().catch (:124-127) prints. `--no-input` is PiG's own flag: with it a manual code prompt and a selection reject at once, before anything
// is printed or read, so the login ends with that error although an answer is waiting on stdin.
func TestLoginCommandNoInputRejectsTheManualCodeAndTheSelectionWithoutAsking(t *testing.T) {
	for _, c := range []struct {
		name, id, want string
		ask            func(ai.OAuthLoginCallbacks) (string, error)
	}{
		{"manual code", "no-input-manual-probe", "Error: provider no-input-manual-probe requires a callback code; rerun without --no-input\n",
			func(callbacks ai.OAuthLoginCallbacks) (string, error) { return callbacks.OnManualCodeInput() }},
		{"selection", "no-input-select-probe", "Error: provider no-input-select-probe requires a selection; rerun without --no-input\n",
			func(callbacks ai.OAuthLoginCallbacks) (string, error) {
				return callbacks.OnSelect(ai.OAuthSelectPrompt{Message: "Pick a method", Options: []ai.OAuthSelectOption{{ID: "first", Label: "First"}}})
			}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("PIG_CODING_AGENT_DIR", dir)
			registerLoginProvider(t, callbackLoginProvider{id: c.id, login: func(callbacks ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
				if _, err := c.ask(callbacks); err != nil {
					return ai.OAuthCredentials{}, err
				}
				return loggedIn(callbacks)
			}})
			stdout, stderr, code := captureWithStdin(t, "1\n", func() int { return runLoginCommand([]string{"login", c.id, "--no-input"}) })
			if code != 1 || stderr != c.want || stdout != "" {
				t.Fatalf("code %d, stderr %q, stdout %q; want 1, %q and nothing printed", code, stderr, stdout, c.want)
			}
			if _, err := os.Stat(filepath.Join(dir, "auth.json")); !os.IsNotExist(err) {
				t.Fatalf("credentials were written: %v", err)
			}
		})
	}
}

// answerPrompt (cli.ts:41-43) and the provider menu (:110-111) await the answer and only then parse it; `Invalid selection` (:43) is the verdict on an
// answer that was read. rl.question has no rejection, so a failed read has no Pi counterpart: PiG reports the read error and does not parse the
// answer it never got.
func TestLoginCommandFailedReadOfASelectionIsTheReadErrorNotAnInvalidSelection(t *testing.T) {
	closedStdin := func(t *testing.T) *os.File {
		t.Helper()
		file, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		return file
	}
	check := func(t *testing.T, stdout, stderr string, code int) {
		t.Helper()
		if code != 1 || !strings.HasPrefix(stderr, "Error: ") || !strings.Contains(stderr, os.ErrClosed.Error()) || strings.Contains(stderr, "Invalid selection") {
			t.Fatalf("code %d, stderr %q; want 1 and the read error %q\nstdout: %q", code, stderr, os.ErrClosed, stdout)
		}
	}
	t.Run("provider selection prompt", func(t *testing.T) {
		t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
		registerLoginProvider(t, callbackLoginProvider{id: "read-failure-probe", login: func(callbacks ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
			if _, err := callbacks.OnSelect(ai.OAuthSelectPrompt{Message: "Pick a method", Options: []ai.OAuthSelectOption{{ID: "first", Label: "First"}}}); err != nil {
				return ai.OAuthCredentials{}, err
			}
			return loggedIn(callbacks)
		}})
		stdin := closedStdin(t)
		stdout, stderr, code := captureWithStdin(t, "", func() int {
			os.Stdin = stdin
			return runLoginCommand([]string{"login", "read-failure-probe"})
		})
		check(t, stdout, stderr, code)
	})
	t.Run("provider menu", func(t *testing.T) {
		t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
		stdin := closedStdin(t)
		stdout, stderr, code := captureWithStdin(t, "", func() int {
			os.Stdin = stdin
			return runLoginCommand([]string{"login"})
		})
		check(t, stdout, stderr, code)
	})
}
