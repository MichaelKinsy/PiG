package cli

// Ports packages/coding-agent/test/auth-login.test.ts and, with auth_logout_test.go, the shared model-runtime setup. Expected texts name the command `pig`
// where Pi's name `pi` (D2).

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

const authLoginTestProviderID = "login-test"

// scriptedTerminal answers prompts in order; Secret and Question share one answer queue.
type scriptedTerminal struct {
	interactive bool
	answers     []string
	lines       []string
	errors      []string
	questions   []string
	opened      []string
	// question replaces the answer queue for Question when set.
	question func(ctx context.Context, message string) (string, error)
}

func newScriptedTerminal(answers ...string) *scriptedTerminal {
	return &scriptedTerminal{interactive: true, answers: answers}
}

func (t *scriptedTerminal) Interactive() bool { return t.interactive }
func (t *scriptedTerminal) Write(line string) { t.lines = append(t.lines, line) }
func (t *scriptedTerminal) Error(line string) { t.errors = append(t.errors, line) }
func (t *scriptedTerminal) OpenURL(url string) { t.opened = append(t.opened, url) }
func (t *scriptedTerminal) answer(ctx context.Context, message string) (string, error) {
	t.questions = append(t.questions, message)
	if ctx.Err() != nil {
		return "", errLoginCancelled
	}
	if len(t.answers) == 0 {
		return "", errors.New("Unexpected prompt: " + message)
	}
	next := t.answers[0]
	t.answers = t.answers[1:]
	return next, nil
}
func (t *scriptedTerminal) Question(ctx context.Context, message string) (string, error) {
	if t.question != nil {
		return t.question(ctx, message)
	}
	return t.answer(ctx, message)
}
func (t *scriptedTerminal) Secret(ctx context.Context, message string) (string, error) {
	return t.answer(ctx, message)
}

func (t *scriptedTerminal) contains(line string) bool { return slices.Contains(t.lines, line) }

func authTestProvider(auth ai.ProviderAuth, id string) *ai.ModelsProvider {
	return &ai.ModelsProvider{ID: id, Name: "Login Test", Auth: auth, GetModels: func() ([]*ai.Model, error) { return nil, nil }}
}

func authTestRuntime(t *testing.T, credentials map[string]ai.Credential, providers ...*ai.ModelsProvider) (*coding.ModelRuntime, *ai.InMemoryAuthStorage) {
	t.Helper()
	store := ai.NewInMemoryAuthStorage(credentials)
	var noModelsPath *string
	runtime, err := coding.CreateModelRuntime(t.Context(), coding.CreateModelRuntimeOptions{
		Credentials: store, ModelsPath: &noModelsPath, ModelsStore: ai.NewInMemoryModelsStore(), RefreshOnCreate: new(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	for _, provider := range providers {
		if err := runtime.RegisterNativeProvider(provider); err != nil {
			t.Fatal(err)
		}
	}
	return runtime, store
}

func oauthResult(access string) ai.Credential {
	return ai.Credential{Type: ai.CredentialOAuth, Access: access, Refresh: "refresh", Expires: time.Now().Add(time.Hour).UnixMilli()}
}

func bothMethods() ai.ProviderAuth {
	return ai.ProviderAuth{
		OAuth: &ai.OAuthAuth{
			Name: "Login Test account",
			Login: func(ctx context.Context, interaction ai.AuthInteraction, _ ai.LoginOptions) (ai.Credential, error) {
				method, err := interaction.Prompt(ctx, ai.AuthSelectPrompt{Message: "Sign in to Login Test:", Options: []ai.AuthSelectOption{
					{ID: "browser", Label: "Sign in with browser"}, {ID: "device", Label: "Sign in with device code"},
				}})
				if err != nil {
					return ai.Credential{}, err
				}
				if method == "device" {
					interaction.Notify(ai.AuthDeviceCodeEvent{UserCode: "ABCD-1234", VerificationURI: "https://example.test/device"})
					return oauthResult("access-device"), nil
				}
				interaction.Notify(ai.AuthURLEvent{URL: "https://example.test/authorize", Instructions: "Finish in the browser"})
				code, err := interaction.Prompt(ctx, ai.AuthManualCodePrompt{Message: "Paste the code"})
				if err != nil {
					return ai.Credential{}, err
				}
				return oauthResult("access-" + code), nil
			},
			Refresh: func(_ context.Context, credential ai.Credential) (ai.Credential, error) { return credential, nil },
			ToAuth:  func(credential ai.Credential) (ai.ModelAuth, error) { return ai.ModelAuth{APIKey: credential.Access}, nil },
		},
		APIKey: apiKeyOnly().APIKey,
	}
}

func apiKeyOnly() ai.ProviderAuth {
	return ai.ProviderAuth{APIKey: &ai.APIKeyAuth{
		Name: "Login Test API key",
		Login: func(ctx context.Context, interaction ai.AuthInteraction) (ai.Credential, error) {
			key, err := interaction.Prompt(ctx, ai.AuthSecretPrompt{Message: "Enter Login Test API key"})
			return ai.Credential{Type: ai.CredentialAPIKey, Key: key}, err
		},
		Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) { return nil, nil },
	}}
}

func oauthOnly(login func(context.Context, ai.AuthInteraction, ai.LoginOptions) (ai.Credential, error)) ai.ProviderAuth {
	auth := bothMethods().OAuth
	copied := *auth
	copied.Login = login
	return ai.ProviderAuth{OAuth: &copied}
}

func runAuthLoginForTest(ctx context.Context, args []string, runtime AuthLoginRuntime, terminal LoginTerminal, agentDir string) int {
	return RunAuthLogin(ctx, args, AuthLoginOptions{
		ModelRuntime: runtime, Terminal: terminal, AgentDir: agentDir, CWD: "/nonexistent-cwd", GetDeviceID: func() string { return "device" },
	})
}

func readStoredCredential(t *testing.T, store *ai.InMemoryAuthStorage, id string) *ai.Credential {
	t.Helper()
	credential, err := store.Read(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return credential
}

const nonexistentAgentDir = "/nonexistent-agent-dir"

func TestAuthLoginRequiresExactlyOneProviderAndKnownOptions(t *testing.T) {
	runtime, _ := authTestRuntime(t, nil, authTestProvider(bothMethods(), authLoginTestProviderID))
	usage := "Usage: pig auth login <provider> [--api-key] [--no-browser]"
	for _, args := range [][]string{{}, {"--api-key"}, {"--provider", authLoginTestProviderID}, {authLoginTestProviderID, "extra"}} {
		terminal := newScriptedTerminal()
		if code := runAuthLoginForTest(t.Context(), args, runtime, terminal, nonexistentAgentDir); code != 1 || !reflect.DeepEqual(terminal.errors, []string{usage}) {
			t.Fatalf("%v: code %d, errors %q", args, code, terminal.errors)
		}
	}
}

func TestAuthLoginRejectsNoBrowserWithAPIKey(t *testing.T) {
	runtime, _ := authTestRuntime(t, nil, authTestProvider(bothMethods(), authLoginTestProviderID))
	terminal := newScriptedTerminal()
	code := runAuthLoginForTest(t.Context(), []string{authLoginTestProviderID, "--api-key", "--no-browser"}, runtime, terminal, nonexistentAgentDir)
	if code != 1 || !reflect.DeepEqual(terminal.errors, []string{"--no-browser only applies to account sign-in; omit it with --api-key."}) {
		t.Fatalf("code %d, errors %q", code, terminal.errors)
	}
}

func TestAuthLoginRejectsAnUnknownProviderAndListsTheProvidersWithALoginFlow(t *testing.T) {
	runtime, _ := authTestRuntime(t, nil, authTestProvider(bothMethods(), authLoginTestProviderID))
	terminal := newScriptedTerminal()
	if code := runAuthLoginForTest(t.Context(), []string{"nope"}, runtime, terminal, nonexistentAgentDir); code != 1 {
		t.Fatalf("code %d", code)
	}
	if len(terminal.errors) != 2 || terminal.errors[0] != `Unknown provider "nope".` || !strings.Contains(terminal.errors[1], authLoginTestProviderID) {
		t.Fatalf("errors %q", terminal.errors)
	}
}

func TestAuthLoginRequiresAnInteractiveTerminalAfterCheckingTheArguments(t *testing.T) {
	runtime, _ := authTestRuntime(t, nil, authTestProvider(bothMethods(), authLoginTestProviderID))
	terminal := newScriptedTerminal()
	terminal.interactive = false
	code := runAuthLoginForTest(t.Context(), []string{authLoginTestProviderID}, runtime, terminal, nonexistentAgentDir)
	want := []string{`"pig auth login <provider> [--api-key] [--no-browser]" requires an interactive terminal`}
	if code != 1 || !reflect.DeepEqual(terminal.errors, want) {
		t.Fatalf("code %d, errors %q", code, terminal.errors)
	}
}

func TestAuthLoginSignsInWithAnAccountInTheBrowserWithoutAskingForAMethod(t *testing.T) {
	runtime, store := authTestRuntime(t, nil, authTestProvider(bothMethods(), authLoginTestProviderID))
	terminal := newScriptedTerminal("the-code")
	// Matches the provider name as well as the id.
	if code := runAuthLoginForTest(t.Context(), []string{"login test"}, runtime, terminal, nonexistentAgentDir); code != 0 {
		t.Fatalf("code %d, errors %q", code, terminal.errors)
	}
	if !reflect.DeepEqual(terminal.questions, []string{"Paste the code: "}) {
		t.Fatalf("questions %q", terminal.questions)
	}
	for _, line := range []string{
		"Open this URL to sign in:\n  https://example.test/authorize",
		"Finish in the browser",
		`To use "Sign in with device code" instead, run "pig auth login login-test --no-browser"`,
	} {
		if !terminal.contains(line) {
			t.Fatalf("lines %q lack %q", terminal.lines, line)
		}
	}
	if !reflect.DeepEqual(terminal.opened, []string{"https://example.test/authorize"}) {
		t.Fatalf("opened %q", terminal.opened)
	}
	if got := readStoredCredential(t, store, authLoginTestProviderID); got == nil || got.Type != ai.CredentialOAuth || got.Access != "access-the-code" {
		t.Fatalf("stored %+v", got)
	}
	if !slices.ContainsFunc(terminal.lines, func(line string) bool { return strings.HasPrefix(line, "Logged in to Login Test.") }) {
		t.Fatalf("lines %q", terminal.lines)
	}
}

func threeMethodSelect(ctx context.Context, interaction ai.AuthInteraction) (string, error) {
	return interaction.Prompt(ctx, ai.AuthSelectPrompt{Message: "Sign in to Login Test:", Options: []ai.AuthSelectOption{
		{ID: "browser", Label: "Browser"}, {ID: "device", Label: "Device code"}, {ID: "copy", Label: "Copy code"},
	}})
}

func TestAuthLoginListsSeveralOtherSignInMethods(t *testing.T) {
	auth := oauthOnly(func(ctx context.Context, interaction ai.AuthInteraction, _ ai.LoginOptions) (ai.Credential, error) {
		if _, err := threeMethodSelect(ctx, interaction); err != nil {
			return ai.Credential{}, err
		}
		interaction.Notify(ai.AuthURLEvent{URL: "https://example.test/authorize"})
		return oauthResult("access"), nil
	})
	runtime, _ := authTestRuntime(t, nil, authTestProvider(auth, authLoginTestProviderID))
	terminal := newScriptedTerminal()
	if code := runAuthLoginForTest(t.Context(), []string{authLoginTestProviderID}, runtime, terminal, nonexistentAgentDir); code != 0 {
		t.Fatalf("code %d, errors %q", code, terminal.errors)
	}
	if want := `Other sign-in methods: "Device code", "Copy code". Run "pig auth login login-test --no-browser" to choose one.`; !terminal.contains(want) {
		t.Fatalf("lines %q", terminal.lines)
	}
}

func TestAuthLoginOffersNoOtherSignInMethodsWhenTheProviderHasNoMethodChoice(t *testing.T) {
	auth := oauthOnly(func(ctx context.Context, interaction ai.AuthInteraction, _ ai.LoginOptions) (ai.Credential, error) {
		interaction.Notify(ai.AuthURLEvent{URL: "https://example.test/authorize"})
		code, err := interaction.Prompt(ctx, ai.AuthManualCodePrompt{Message: "Paste the code"})
		return oauthResult("access-" + code), err
	})
	runtime, store := authTestRuntime(t, nil, authTestProvider(auth, authLoginTestProviderID))
	terminal := newScriptedTerminal("the-code")
	if code := runAuthLoginForTest(t.Context(), []string{authLoginTestProviderID}, runtime, terminal, nonexistentAgentDir); code != 0 {
		t.Fatalf("code %d, errors %q", code, terminal.errors)
	}
	if !reflect.DeepEqual(terminal.opened, []string{"https://example.test/authorize"}) || slices.ContainsFunc(terminal.lines, func(l string) bool { return strings.Contains(l, "--no-browser") }) {
		t.Fatalf("opened %q, lines %q", terminal.opened, terminal.lines)
	}
	if got := readStoredCredential(t, store, authLoginTestProviderID); got == nil || got.Access != "access-the-code" {
		t.Fatalf("stored %+v", got)
	}
}

func TestAuthLoginNoBrowserTakesTheOnlyOtherSignInMethodAndOpensNothing(t *testing.T) {
	runtime, store := authTestRuntime(t, nil, authTestProvider(bothMethods(), authLoginTestProviderID))
	terminal := newScriptedTerminal()
	if code := runAuthLoginForTest(t.Context(), []string{authLoginTestProviderID, "--no-browser"}, runtime, terminal, nonexistentAgentDir); code != 0 {
		t.Fatalf("code %d, errors %q", code, terminal.errors)
	}
	if len(terminal.questions) != 0 || terminal.lines[0] != "Sign in to Login Test: Sign in with device code" || !terminal.contains("Open https://example.test/device and enter code: ABCD-1234") || len(terminal.opened) != 0 {
		t.Fatalf("questions %q, lines %q, opened %q", terminal.questions, terminal.lines, terminal.opened)
	}
	if slices.ContainsFunc(terminal.lines, func(l string) bool { return strings.Contains(l, "--no-browser") }) {
		t.Fatalf("lines %q", terminal.lines)
	}
	if got := readStoredCredential(t, store, authLoginTestProviderID); got == nil || got.Access != "access-device" {
		t.Fatalf("stored %+v", got)
	}
}

func TestAuthLoginNoBrowserAsksAmongSeveralOtherSignInMethodsAndReAsksOnAnInvalidSelection(t *testing.T) {
	auth := oauthOnly(func(ctx context.Context, interaction ai.AuthInteraction, _ ai.LoginOptions) (ai.Credential, error) {
		method, err := threeMethodSelect(ctx, interaction)
		return oauthResult("access-" + method), err
	})
	runtime, store := authTestRuntime(t, nil, authTestProvider(auth, authLoginTestProviderID))
	terminal := newScriptedTerminal("7", "2")
	if code := runAuthLoginForTest(t.Context(), []string{"--no-browser", authLoginTestProviderID}, runtime, terminal, nonexistentAgentDir); code != 0 {
		t.Fatalf("code %d, errors %q", code, terminal.errors)
	}
	if want := []string{"Sign in to Login Test:", "  1. Device code", "  2. Copy code"}; !reflect.DeepEqual(terminal.lines[:3], want) {
		t.Fatalf("lines %q", terminal.lines)
	}
	if !terminal.contains("Enter a number from 1 to 2.") {
		t.Fatalf("lines %q", terminal.lines)
	}
	if got := readStoredCredential(t, store, authLoginTestProviderID); got == nil || got.Access != "access-copy" {
		t.Fatalf("stored %+v", got)
	}
}

func TestAuthLoginNoBrowserKeepsAProvidersOnlySignInMethodWithoutOpeningTheBrowser(t *testing.T) {
	auth := oauthOnly(func(ctx context.Context, interaction ai.AuthInteraction, _ ai.LoginOptions) (ai.Credential, error) {
		method, err := interaction.Prompt(ctx, ai.AuthSelectPrompt{Message: "Sign in to Login Test:", Options: []ai.AuthSelectOption{{ID: "browser", Label: "Browser"}}})
		interaction.Notify(ai.AuthURLEvent{URL: "https://example.test/authorize"})
		return oauthResult("access-" + method), err
	})
	runtime, store := authTestRuntime(t, nil, authTestProvider(auth, authLoginTestProviderID))
	terminal := newScriptedTerminal()
	if code := runAuthLoginForTest(t.Context(), []string{authLoginTestProviderID, "--no-browser"}, runtime, terminal, nonexistentAgentDir); code != 0 {
		t.Fatalf("code %d, errors %q", code, terminal.errors)
	}
	if len(terminal.questions) != 0 || len(terminal.opened) != 0 {
		t.Fatalf("questions %q, opened %q", terminal.questions, terminal.opened)
	}
	if got := readStoredCredential(t, store, authLoginTestProviderID); got == nil || got.Access != "access-browser" {
		t.Fatalf("stored %+v", got)
	}
}

func TestAuthLoginAsksForSelectionsDuringAPIKeySetup(t *testing.T) {
	auth := apiKeyOnly()
	auth.APIKey.Login = func(ctx context.Context, interaction ai.AuthInteraction) (ai.Credential, error) {
		key, err := interaction.Prompt(ctx, ai.AuthSelectPrompt{Message: "Choose credentials:", Options: []ai.AuthSelectOption{
			{ID: "first", Label: "First"}, {ID: "second", Label: "Second", Description: "Other credentials"},
		}})
		return ai.Credential{Type: ai.CredentialAPIKey, Key: key}, err
	}
	runtime, store := authTestRuntime(t, nil, authTestProvider(auth, authLoginTestProviderID))
	terminal := newScriptedTerminal("2")
	if code := runAuthLoginForTest(t.Context(), []string{authLoginTestProviderID, "--api-key"}, runtime, terminal, nonexistentAgentDir); code != 0 {
		t.Fatalf("code %d, errors %q", code, terminal.errors)
	}
	if !reflect.DeepEqual(terminal.questions, []string{"Select 1-2: "}) || !terminal.contains("  2. Second - Other credentials") {
		t.Fatalf("questions %q, lines %q", terminal.questions, terminal.lines)
	}
	if got := readStoredCredential(t, store, authLoginTestProviderID); got == nil || got.Type != ai.CredentialAPIKey || got.Key != "second" {
		t.Fatalf("stored %+v", got)
	}
}

func TestAuthLoginCancelsAPendingPromptWithoutCancellingTheLoginWhenTheBrowserCallbackWins(t *testing.T) {
	auth := oauthOnly(func(ctx context.Context, interaction ai.AuthInteraction, _ ai.LoginOptions) (ai.Credential, error) {
		promptCtx, cancelPrompt := context.WithCancel(ctx)
		pending := make(chan error, 1)
		go func() {
			_, err := interaction.Prompt(promptCtx, ai.AuthManualCodePrompt{Message: "Paste the code", Placeholder: "redirect URL"})
			pending <- err
		}()
		cancelPrompt()
		if err := <-pending; !errors.Is(err, context.Canceled) {
			return ai.Credential{}, errors.New("pending prompt error: " + err.Error())
		}
		if ctx.Err() != nil {
			return ai.Credential{}, errors.New("login cancelled with the prompt")
		}
		return oauthResult("callback"), nil
	})
	runtime, store := authTestRuntime(t, nil, authTestProvider(auth, authLoginTestProviderID))
	terminal := newScriptedTerminal()
	var asked []string
	terminal.question = func(ctx context.Context, message string) (string, error) {
		asked = append(asked, message)
		<-ctx.Done()
		return "", context.Cause(ctx)
	}
	if code := runAuthLoginForTest(t.Context(), []string{authLoginTestProviderID}, runtime, terminal, nonexistentAgentDir); code != 0 {
		t.Fatalf("code %d, errors %q", code, terminal.errors)
	}
	if !reflect.DeepEqual(asked, []string{"Paste the code (redirect URL): "}) {
		t.Fatalf("asked %q", asked)
	}
	if got := readStoredCredential(t, store, authLoginTestProviderID); got == nil || got.Access != "callback" {
		t.Fatalf("stored %+v", got)
	}
}

// loginRuntimeOverride wraps a runtime to replace its Login or Refresh, as Pi's tests spy on modelRuntime.
type loginRuntimeOverride struct {
	AuthLoginRuntime
	login   func(context.Context, string, ai.AuthType, ai.AuthInteraction, ...ai.LoginOptions) (ai.Credential, error)
	refresh func(context.Context, ...ai.ModelsRefreshOptions) ai.ModelsRefreshResult
}

func (o loginRuntimeOverride) Login(ctx context.Context, id string, kind ai.AuthType, interaction ai.AuthInteraction, options ...ai.LoginOptions) (ai.Credential, error) {
	if o.login != nil {
		return o.login(ctx, id, kind, interaction, options...)
	}
	return o.AuthLoginRuntime.Login(ctx, id, kind, interaction, options...)
}

func (o loginRuntimeOverride) Refresh(ctx context.Context, options ...ai.ModelsRefreshOptions) ai.ModelsRefreshResult {
	if o.refresh != nil {
		return o.refresh(ctx, options...)
	}
	return o.AuthLoginRuntime.Refresh(ctx, options...)
}

func TestAuthLoginReportsACommittedLoginSeparatelyFromAFailedLogin(t *testing.T) {
	runtime, _ := authTestRuntime(t, nil, authTestProvider(bothMethods(), authLoginTestProviderID))
	override := loginRuntimeOverride{AuthLoginRuntime: runtime, login: func(context.Context, string, ai.AuthType, ai.AuthInteraction, ...ai.LoginOptions) (ai.Credential, error) {
		saved := ai.Credential{Type: ai.CredentialAPIKey, Key: "saved"}
		return ai.Credential{}, coding.NewCredentialSynchronizationError(authLoginTestProviderID, coding.CredentialSynchronizationLogin, &saved, errors.New("snapshot failed"))
	}}
	terminal := newScriptedTerminal()
	if code := runAuthLoginForTest(t.Context(), []string{authLoginTestProviderID}, override, terminal, nonexistentAgentDir); code != 1 {
		t.Fatalf("code %d", code)
	}
	if len(terminal.errors) == 0 || !strings.Contains(terminal.errors[0], "Logged in to Login Test, but local model state could not be synchronized:") {
		t.Fatalf("errors %q", terminal.errors)
	}
}

func TestAuthLoginAPIKeyFlagSavesAnAPIKey(t *testing.T) {
	runtime, store := authTestRuntime(t, nil, authTestProvider(bothMethods(), authLoginTestProviderID))
	terminal := newScriptedTerminal("sk-test")
	if code := runAuthLoginForTest(t.Context(), []string{authLoginTestProviderID, "--api-key"}, runtime, terminal, nonexistentAgentDir); code != 0 {
		t.Fatalf("code %d, errors %q", code, terminal.errors)
	}
	if !reflect.DeepEqual(terminal.questions, []string{"Enter Login Test API key: "}) {
		t.Fatalf("questions %q", terminal.questions)
	}
	if got := readStoredCredential(t, store, authLoginTestProviderID); got == nil || got.Type != ai.CredentialAPIKey || got.Key != "sk-test" {
		t.Fatalf("stored %+v", got)
	}
	if !slices.ContainsFunc(terminal.lines, func(line string) bool { return strings.HasPrefix(line, "Saved API key for Login Test.") }) {
		t.Fatalf("lines %q", terminal.lines)
	}
}

func TestAuthLoginUsesTheAPIKeyLoginOfProvidersWithoutAccountSignIn(t *testing.T) {
	runtime, _ := authTestRuntime(t, nil, authTestProvider(apiKeyOnly(), authLoginTestProviderID))
	terminal := newScriptedTerminal("sk-test")
	if code := runAuthLoginForTest(t.Context(), []string{authLoginTestProviderID}, runtime, terminal, nonexistentAgentDir); code != 0 {
		t.Fatalf("code %d, errors %q", code, terminal.errors)
	}
	if !reflect.DeepEqual(terminal.questions, []string{"Enter Login Test API key: "}) {
		t.Fatalf("questions %q", terminal.questions)
	}
}

func TestAuthLoginAPIKeyFlagFailsForProvidersWithoutAPIKeyLogin(t *testing.T) {
	runtime, _ := authTestRuntime(t, nil, authTestProvider(ai.ProviderAuth{OAuth: bothMethods().OAuth}, authLoginTestProviderID))
	terminal := newScriptedTerminal()
	code := runAuthLoginForTest(t.Context(), []string{authLoginTestProviderID, "--api-key"}, runtime, terminal, nonexistentAgentDir)
	if code != 1 || !reflect.DeepEqual(terminal.errors, []string{"Login Test has no API key login. Omit --api-key to sign in."}) {
		t.Fatalf("code %d, errors %q", code, terminal.errors)
	}
}

func TestAuthLoginReportsProvidersConfiguredOutsidePig(t *testing.T) {
	auth := ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "AWS profile", Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) { return nil, nil }}}
	runtime, _ := authTestRuntime(t, nil, authTestProvider(auth, authLoginTestProviderID))
	terminal := newScriptedTerminal()
	code := runAuthLoginForTest(t.Context(), []string{authLoginTestProviderID}, runtime, terminal, nonexistentAgentDir)
	if code != 1 || !reflect.DeepEqual(terminal.errors, []string{"Login Test is configured outside pig (AWS profile)."}) {
		t.Fatalf("code %d, errors %q", code, terminal.errors)
	}
}

func TestAuthLoginCtrlCDuringTheCatalogRefreshSkipsItAndKeepsTheLogin(t *testing.T) {
	runtime, store := authTestRuntime(t, nil, authTestProvider(apiKeyOnly(), authLoginTestProviderID))
	ctx, cancel := context.WithCancel(t.Context())
	// A refresh that only ends when aborted, interrupted as soon as it starts.
	override := loginRuntimeOverride{AuthLoginRuntime: runtime, refresh: func(refreshCtx context.Context, _ ...ai.ModelsRefreshOptions) ai.ModelsRefreshResult {
		cancel()
		<-refreshCtx.Done()
		return ai.ModelsRefreshResult{Aborted: true}
	}}
	terminal := newScriptedTerminal("sk-test")
	if code := runAuthLoginForTest(ctx, []string{authLoginTestProviderID}, override, terminal, nonexistentAgentDir); code != 0 {
		t.Fatalf("code %d, errors %q", code, terminal.errors)
	}
	if !terminal.contains("Skipped the Login Test model catalog refresh; pig refreshes it on next start.") {
		t.Fatalf("lines %q", terminal.lines)
	}
	if got := readStoredCredential(t, store, authLoginTestProviderID); got == nil || got.Key != "sk-test" {
		t.Fatalf("stored %+v", got)
	}
}

func TestAuthLoginReportsCancellation(t *testing.T) {
	runtime, store := authTestRuntime(t, nil, authTestProvider(apiKeyOnly(), authLoginTestProviderID))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	terminal := newScriptedTerminal("sk-test")
	code := runAuthLoginForTest(ctx, []string{authLoginTestProviderID}, runtime, terminal, nonexistentAgentDir)
	if code != 1 || !reflect.DeepEqual(terminal.errors, []string{"Login cancelled."}) {
		t.Fatalf("code %d, errors %q", code, terminal.errors)
	}
	if got := readStoredCredential(t, store, authLoginTestProviderID); got != nil {
		t.Fatalf("stored %+v", got)
	}
}

func TestAuthLoginKeepsUnrelatedMCPServersWhenTheFallbackNameIsOccupied(t *testing.T) {
	for _, fallback := range []string{"radius-mcp", "radius_mcp"} {
		t.Run(fallback, func(t *testing.T) {
			agentDir := t.TempDir()
			runtime, _ := authTestRuntime(t, nil, authTestProvider(apiKeyOnly(), ai.RadiusProviderID))
			mcpPath := filepath.Join(agentDir, "mcp.json")
			servers := map[string]any{
				"radius":       map[string]any{"command": "original-one"},
				fallback:       map[string]any{"command": "original-two"},
				"radius-mcp-2": map[string]any{"command": "original-three"},
			}
			data, _ := json.Marshal(map[string]any{"mcpServers": servers})
			if err := os.WriteFile(mcpPath, data, 0o600); err != nil {
				t.Fatal(err)
			}
			terminal := newScriptedTerminal("sk-test")
			if code := runAuthLoginForTest(t.Context(), []string{"radius"}, runtime, terminal, agentDir); code != 0 {
				t.Fatalf("code %d, errors %q", code, terminal.errors)
			}
			var written struct {
				McpServers map[string]any `json:"mcpServers"`
			}
			raw, err := os.ReadFile(mcpPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &written); err != nil {
				t.Fatal(err)
			}
			servers["radius-mcp-3"] = map[string]any{"url": "https://radius.pi.dev/mcp", "auth": map[string]any{"provider": "radius"}}
			if !reflect.DeepEqual(written.McpServers, servers) {
				t.Fatalf("servers %v, want %v", written.McpServers, servers)
			}
			if want := `Added MCP server "radius-mcp-3" in ` + mcpPath + " to use the Login Test login."; !terminal.contains(want) {
				t.Fatalf("lines %q", terminal.lines)
			}
		})
	}
}

func TestAuthLoginConfiguresTheRadiusMCPServerWithoutAskingOnce(t *testing.T) {
	agentDir := t.TempDir()
	runtime, _ := authTestRuntime(t, nil, authTestProvider(apiKeyOnly(), ai.RadiusProviderID))
	mcpPath := filepath.Join(agentDir, "mcp.json")
	first := newScriptedTerminal("sk-test")
	if code := runAuthLoginForTest(t.Context(), []string{"radius"}, runtime, first, agentDir); code != 0 {
		t.Fatalf("code %d, errors %q", code, first.errors)
	}
	if !reflect.DeepEqual(first.questions, []string{"Enter Login Test API key: "}) || !first.contains(`Added MCP server "radius" in `+mcpPath+" to use the Login Test login.") {
		t.Fatalf("questions %q, lines %q", first.questions, first.lines)
	}
	raw, err := os.ReadFile(mcpPath)
	if err != nil || !strings.Contains(string(raw), `"auth":{"provider":"radius"}`) && !strings.Contains(string(raw), `"provider": "radius"`) {
		t.Fatalf("mcp.json %q, %v", raw, err)
	}
	second := newScriptedTerminal("sk-test")
	if code := runAuthLoginForTest(t.Context(), []string{"radius"}, runtime, second, agentDir); code != 0 {
		t.Fatalf("code %d, errors %q", code, second.errors)
	}
	if slices.ContainsFunc(second.lines, func(line string) bool { return strings.Contains(line, "MCP server") }) {
		t.Fatalf("lines %q", second.lines)
	}
}
