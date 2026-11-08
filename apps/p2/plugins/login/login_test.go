package login_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/login/contract"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/models"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// A hermetic identity provider: a real HTTP server the fixture provider's flows talk to (a device-code OAuth flow and an
// API-key check), so the flows are the providers' own and nothing is stubbed in the login plugin.
type identity struct {
	*httptest.Server
	mu     sync.Mutex
	issued []string
}

func newIdentity(t *testing.T) *identity {
	t.Helper()
	id := &identity{}
	mux := http.NewServeMux()
	mux.HandleFunc("/device", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"user_code": "CODE-1", "verification_uri": id.URL + "/verify", "device_code": "dev-1"})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("device_code") != "dev-1" || r.Form.Get("code") != "private-entered-code" {
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "private-access-token", "refresh_token": "private-refresh", "expires_in": 600})
	})
	mux.HandleFunc("/check", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-api-key" {
			http.Error(w, "bad key", http.StatusUnauthorized)
		}
	})
	id.Server = httptest.NewServer(mux)
	t.Cleanup(id.Close)
	return id
}

func (id *identity) get(ctx context.Context, path string, form map[string]string) (map[string]any, error) {
	url := id.URL + path
	var request *http.Request
	var err error
	if form == nil {
		request, err = http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	} else {
		values := make([]string, 0, len(form))
		for key, value := range form {
			values = append(values, key+"="+value)
		}
		request, err = http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(strings.Join(values, "&")))
		if request != nil {
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", path, response.Status)
	}
	out := map[string]any{}
	_ = json.NewDecoder(response.Body).Decode(&out)
	return out, nil
}

// fixtureModels is a faux provider with an API-key login (secret prompt, checked against the identity server) and an OAuth
// login (a device code from the identity server, a select, a pasted code exchanged for tokens).
func fixtureModels(id *identity, deviceIDs *[]string, mu *sync.Mutex) func(ai.CredentialStore, ai.ModelsStore) (*ai.MutableModels, error) {
	return func(credentials ai.CredentialStore, store ai.ModelsStore) (*ai.MutableModels, error) {
		models := ai.CreateModels(ai.CreateModelsOptions{Credentials: credentials, ModelsStore: store})
		faux := ai.NewFauxProvider(ai.FauxConfig{API: "test-login"})
		provider := *faux.Provider()
		provider.Auth = ai.ProviderAuth{
			APIKey: &ai.APIKeyAuth{
				Name: "Fixture key",
				Login: func(ctx context.Context, interaction ai.AuthInteraction) (ai.Credential, error) {
					key, err := interaction.Prompt(ctx, ai.AuthSecretPrompt{Message: "Enter fixture key"})
					if err != nil {
						return ai.Credential{}, err
					}
					request, _ := http.NewRequestWithContext(ctx, http.MethodGet, id.URL+"/check", nil)
					request.Header.Set("Authorization", "Bearer "+key)
					response, err := http.DefaultClient.Do(request)
					if err != nil {
						return ai.Credential{}, err
					}
					response.Body.Close()
					if response.StatusCode != http.StatusOK {
						return ai.Credential{}, fmt.Errorf("the provider rejected the key: %s", response.Status)
					}
					return ai.Credential{Type: ai.CredentialAPIKey, Key: key}, nil
				},
				Resolve: func(_ context.Context, input ai.APIKeyAuthInput) (*ai.AuthResult, error) {
					if input.Credential == nil {
						return nil, nil
					}
					return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: input.Credential.Key}, Source: "stored"}, nil
				},
			},
			OAuth: &ai.OAuthAuth{
				Name: "Fixture OAuth",
				Login: func(ctx context.Context, interaction ai.AuthInteraction, options ai.LoginOptions) (ai.Credential, error) {
					if options.GetDeviceID != nil {
						mu.Lock()
						*deviceIDs = append(*deviceIDs, options.GetDeviceID())
						mu.Unlock()
					}
					device, err := id.get(ctx, "/device", nil)
					if err != nil {
						return ai.Credential{}, err
					}
					interaction.Notify(ai.AuthURLEvent{URL: "https://example.test/login", Instructions: "Fixture"})
					if _, err := interaction.Prompt(ctx, ai.AuthSelectPrompt{Message: "Select account", Options: []ai.AuthSelectOption{{ID: "account", Label: "Fixture account"}}}); err != nil {
						return ai.Credential{}, err
					}
					interaction.Notify(ai.AuthDeviceCodeEvent{UserCode: device["user_code"].(string), VerificationURI: device["verification_uri"].(string)})
					code, err := interaction.Prompt(ctx, ai.AuthManualCodePrompt{Message: "Paste authorization code"})
					if err != nil {
						return ai.Credential{}, err
					}
					token, err := id.get(ctx, "/token", map[string]string{"device_code": device["device_code"].(string), "code": code})
					if err != nil {
						return ai.Credential{}, err
					}
					return ai.Credential{Type: ai.CredentialOAuth, Access: token["access_token"].(string), Refresh: token["refresh_token"].(string), Expires: time.Now().Add(10 * time.Minute).UnixMilli()}, nil
				},
				Refresh: func(_ context.Context, credential ai.Credential) (ai.Credential, error) { return credential, nil },
				ToAuth: func(credential ai.Credential) (ai.ModelAuth, error) {
					return ai.ModelAuth{APIKey: credential.Access}, nil
				},
			},
		}
		models.SetProvider(&provider)
		return models, nil
	}
}

func start(t *testing.T) (*p2test.Fixture, *identity, *[]string) {
	t.Helper()
	id := newIdentity(t)
	var mu sync.Mutex
	deviceIDs := &[]string{}
	fixture := p2test.StartFixture(t, hosts.ServerOptions{
		Plugins:   []string{"login", "models"},
		Resources: p2runtime.ServerResourceOptions{CreateModels: fixtureModels(id, deviceIDs, &mu)},
	})
	return fixture, id, deviceIDs
}

func object(value delta.JsonValue) *delta.JsonObject { o, _ := value.(*delta.JsonObject); return o }

func field(value delta.JsonValue, key string) any {
	if o := object(value); o != nil {
		return o.Value(key)
	}
	return nil
}

func hold(t *testing.T, h host.Host) *host.Reference {
	t.Helper()
	ref, err := p2test.HoldIn(h, contract.ProviderLogin, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func call(ref *host.Reference, method string, args *delta.JsonObject) (delta.JsonValue, error) {
	return ref.Call(method, args, p2test.Background)
}

func until(t *testing.T, ref *host.Reference, member, what string, predicate func(delta.JsonValue) bool) delta.JsonValue {
	t.Helper()
	value, err := p2test.Until(ref.Member(member), predicate, 15*time.Second)
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	return value
}

func loginIs(status string) func(delta.JsonValue) bool {
	return func(v delta.JsonValue) bool { return field(v, "status") == status }
}

func promptIs(kind string) func(delta.JsonValue) bool {
	return func(v delta.JsonValue) bool { return field(field(v, "prompt"), "type") == kind }
}

func answer(t *testing.T, ref *host.Reference, kind, value string) {
	t.Helper()
	state := until(t, ref, "login", "the "+kind+" prompt", promptIs(kind))
	prompt := object(field(state, "prompt"))
	if _, err := call(ref, "answer", delta.JsonObjectOf("attemptId", field(state, "attemptId"), "promptId", prompt.Value("id"), "value", value)); err != nil {
		t.Fatalf("answer %s: %v", kind, err)
	}
	if now, _ := ref.Member("login").Value(); field(field(now, "prompt"), "id") == prompt.Value("id") {
		t.Fatalf("the answered %s prompt is still shown when the call resolves", kind)
	}
}

func providerOf(t *testing.T, ref *host.Reference, id string) *delta.JsonObject {
	t.Helper()
	list := until(t, ref, "providers", "the providers", func(v delta.JsonValue) bool { l, _ := v.([]any); return len(l) > 0 })
	for _, each := range list.([]any) {
		if object(each).Value("id") == id {
			return object(each)
		}
	}
	t.Fatalf("no provider %s in %v", id, list)
	return nil
}

func methodTypes(provider *delta.JsonObject) []string {
	var out []string
	for _, each := range provider.Value("loginMethods").([]any) {
		out = append(out, object(each).Value("type").(string))
	}
	return out
}

// The providers list is what the web's sheet shows, over the web's own transport.
func TestProvidersListWithTheirLoginMethods(t *testing.T) {
	fixture, _, _ := start(t)
	ref := hold(t, fixture.Browser())
	defer ref.Release()
	faux := providerOf(t, ref, "faux")
	got := fmt.Sprint(methodTypes(faux))
	if got != "[api_key oauth]" && got != "[oauth api_key]" {
		t.Fatalf("login methods = %s", got)
	}
	if faux.Value("signedIn") != nil {
		t.Fatalf("signedIn = %v", faux.Value("signedIn"))
	}
}

func TestOAuthFlowThroughPromptsAndNoticesAgainstTheIdentityServer(t *testing.T) {
	fixture, _, deviceIDs := start(t)
	ref := hold(t, fixture.Browser())
	defer ref.Release()
	var mu sync.Mutex
	var seen []string
	watch, err := ref.Member("login").Watch(p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = watch.Stop() }()
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if text, err := json.Marshal(call.Value); err == nil {
			mu.Lock()
			seen = append(seen, string(text))
			mu.Unlock()
		}
		return nil
	})
	started, err := call(ref, "start", delta.JsonObjectOf("providerId", "faux", "authType", "oauth"))
	if err != nil {
		t.Fatal(err)
	}
	attempt := field(started, "attemptId").(string)
	if now, _ := ref.Member("login").Value(); field(now, "attemptId") != attempt {
		t.Fatalf("the started login is not shown when the call resolves: %v", now)
	}
	if _, err := call(ref, "start", delta.JsonObjectOf("providerId", "faux", "authType", "api_key")); err == nil || !regexp.MustCompile(`already in progress`).MatchString(err.Error()) {
		t.Fatalf("second start = %v", err)
	}
	state := until(t, ref, "login", "the select prompt", promptIs("select"))
	if _, err := call(ref, "answer", delta.JsonObjectOf("attemptId", attempt, "promptId", object(field(state, "prompt")).Value("id"), "value", "unlisted")); err == nil || !strings.Contains(err.Error(), "listed") {
		t.Fatalf("unlisted answer = %v", err)
	}
	answer(t, ref, "select", "account")
	answer(t, ref, "manual_code", "private-entered-code")
	done := until(t, ref, "login", "success", loginIs("success"))
	var kinds []string
	for _, notice := range field(done, "notices").([]any) {
		kinds = append(kinds, object(object(notice).Value("event")).Value("type").(string))
	}
	if fmt.Sprint(kinds) != "[auth_url device_code]" {
		t.Fatalf("notices = %v", kinds)
	}
	if signed := providerOf(t, ref, "faux").Value("signedIn"); signed != "oauth" {
		until(t, ref, "providers", "signed in", func(v delta.JsonValue) bool {
			l, _ := v.([]any)
			return len(l) > 0 && object(l[0]).Value("signedIn") == "oauth"
		})
	}
	stored, err := os.ReadFile(filepath.Join(fixture.DataDir, "auth.json"))
	if err != nil || !strings.Contains(string(stored), "private-access-token") {
		t.Fatalf("auth.json = %q, %v", stored, err)
	}
	mu.Lock()
	published := strings.Join(seen, "\n")
	mu.Unlock()
	for _, secret := range []string{"private-access-token", "private-entered-code", "private-refresh"} {
		if strings.Contains(published, secret) {
			t.Fatalf("%s reached the client", secret)
		}
	}
	if len(*deviceIDs) != 1 || (*deviceIDs)[0] == "" {
		t.Fatalf("device ids = %v", *deviceIDs)
	}
	if _, err := os.Stat(filepath.Join(fixture.DataDir, "device.json")); err != nil {
		t.Fatalf("device.json: %v", err)
	}
	if _, err := call(ref, "dismiss", delta.JsonObjectOf("attemptId", attempt)); err != nil {
		t.Fatal(err)
	}
	until(t, ref, "login", "dismissed", func(v delta.JsonValue) bool { return v == nil })
}

func TestAPIKeyFlowChecksTheKeyAndSignsOut(t *testing.T) {
	fixture, _, _ := start(t)
	ref := hold(t, fixture.Browser())
	defer ref.Release()
	// A wrong key is the provider's refusal: the login ends in error without echoing the key.
	if _, err := call(ref, "start", delta.JsonObjectOf("providerId", "faux", "authType", "api_key")); err != nil {
		t.Fatal(err)
	}
	answer(t, ref, "secret", "wrong-key")
	failed := until(t, ref, "login", "error", loginIs("error"))
	if strings.Contains(fmt.Sprint(failed), "wrong-key") {
		t.Fatalf("the key is echoed: %v", failed)
	}
	if _, err := call(ref, "dismiss", delta.JsonObjectOf("attemptId", field(failed, "attemptId"))); err != nil {
		t.Fatal(err)
	}
	until(t, ref, "login", "dismissed", func(v delta.JsonValue) bool { return v == nil })

	if _, err := call(ref, "start", delta.JsonObjectOf("providerId", "faux", "authType", "api_key")); err != nil {
		t.Fatal(err)
	}
	answer(t, ref, "secret", "private-api-key")
	until(t, ref, "login", "success", loginIs("success"))
	until(t, ref, "providers", "signed in", func(v delta.JsonValue) bool {
		l, _ := v.([]any)
		return len(l) > 0 && object(l[0]).Value("signedIn") == "api_key"
	})
	counted, err := call(ref, "refresh", delta.JsonObjectOf("providerId", "faux"))
	if err != nil || field(counted, "models") == nil {
		t.Fatalf("refresh = %v, %v", counted, err)
	}
	// A login in progress blocks removing credentials.
	if _, err := call(ref, "logout", delta.JsonObjectOf("providerId", "faux")); err != nil {
		t.Fatal(err)
	}
	until(t, ref, "providers", "signed out", func(v delta.JsonValue) bool {
		l, _ := v.([]any)
		return len(l) > 0 && object(l[0]).Value("signedIn") == nil
	})
}

func TestCancelAndLeavingEndsAnUnfinishedLogin(t *testing.T) {
	fixture, _, _ := start(t)
	ref := hold(t, fixture.Browser())
	started, err := call(ref, "start", delta.JsonObjectOf("providerId", "faux", "authType", "oauth"))
	if err != nil {
		t.Fatal(err)
	}
	until(t, ref, "login", "the prompt", promptIs("select"))
	if _, err := call(ref, "cancel", delta.JsonObjectOf("attemptId", field(started, "attemptId"))); err != nil {
		t.Fatal(err)
	}
	if now, _ := ref.Member("login").Value(); field(now, "status") != "cancelled" {
		t.Fatalf("cancel resolves once the login finished: %v", now)
	}
	if _, err := call(ref, "dismiss", delta.JsonObjectOf("attemptId", field(started, "attemptId"))); err != nil {
		t.Fatal(err)
	}
	// The owner leaving (its last hold released) ends its unfinished login.
	other := fixture.Browser()
	second := hold(t, other)
	restarted, err := call(second, "start", delta.JsonObjectOf("providerId", "faux", "authType", "oauth"))
	if err != nil {
		t.Fatal(err)
	}
	until(t, second, "login", "the prompt", promptIs("select"))
	second.Release()
	ref.Release()
	// A new hold of the sheet makes the owner present again; the login ended while nobody held it.
	var ended delta.JsonValue
	if err := p2test.Poll(func() (bool, error) {
		again, err := p2test.HoldIn(fixture.Browser(), contract.ProviderLogin, delta.NewJsonObject(0), "owner")
		if err != nil {
			return false, err
		}
		defer again.Release()
		ended, _ = again.Member("login").Value()
		return field(ended, "attemptId") == field(restarted, "attemptId") && field(ended, "status") == "cancelled", nil
	}, 10*time.Second, "the login to end with its owner"); err != nil {
		t.Fatalf("%v: %v", err, ended)
	}
}

// A login belongs to the owner that started it (a device, the local owner): only it sees its prompts and notices, answers,
// cancels or dismisses it, and another owner's sheet coming and going does not end it.
func TestALoginBelongsToTheOwnerThatStartedIt(t *testing.T) {
	fixture, _, _ := start(t)
	device := hold(t, fixture.As(p2runtime.OwnerPrincipal{Access: p2runtime.AccessLocal}))
	defer device.Release()
	laptopHost := fixture.As(p2runtime.OwnerPrincipal{Access: p2runtime.AccessDevice, Device: "laptop"})
	laptop := hold(t, laptopHost)

	started, err := call(device, "start", delta.JsonObjectOf("providerId", "faux", "authType", "oauth"))
	if err != nil {
		t.Fatal(err)
	}
	attempt := field(started, "attemptId")
	prompted := until(t, device, "login", "the prompt", promptIs("select"))
	if n := field(prompted, "notices").([]any); len(n) == 0 || object(object(n[0]).Value("event")).Value("type") != "auth_url" {
		t.Fatalf("notices = %v", n)
	}
	time.Sleep(300 * time.Millisecond)
	if seen, _ := laptop.Member("login").Value(); seen != nil {
		t.Fatalf("another owner sees the login: %v", seen)
	}
	if _, err := call(laptop, "start", delta.JsonObjectOf("providerId", "faux", "authType", "oauth")); err == nil || !strings.Contains(err.Error(), "Another owner device is signing in") {
		t.Fatalf("laptop start = %v", err)
	}
	promptID := object(field(prompted, "prompt")).Value("id")
	if _, err := call(laptop, "answer", delta.JsonObjectOf("attemptId", attempt, "promptId", promptID, "value", "account")); err == nil || !strings.Contains(err.Error(), "no longer active for this connection") {
		t.Fatalf("laptop answer = %v", err)
	}
	if _, err := call(laptop, "cancel", delta.JsonObjectOf("attemptId", attempt)); err == nil || !strings.Contains(err.Error(), "no longer active for this connection") {
		t.Fatalf("laptop cancel = %v", err)
	}
	// The other owner closing its sheet does not abandon the login.
	laptop.Release()
	time.Sleep(150 * time.Millisecond)
	if now, _ := device.Member("login").Value(); field(now, "attemptId") != attempt || field(now, "status") != "working" {
		t.Fatalf("the login ended with another owner's sheet: %v", now)
	}
	answer(t, device, "select", "account")
	answer(t, device, "manual_code", "private-entered-code")
	until(t, device, "login", "success", loginIs("success"))
	if _, err := call(device, "dismiss", delta.JsonObjectOf("attemptId", attempt)); err != nil {
		t.Fatal(err)
	}
	until(t, device, "login", "dismissed", func(v delta.JsonValue) bool { return v == nil })

	// The laptop's own login ends when its connection goes; the device may sign in again at once.
	laptop = hold(t, laptopHost)
	second, err := call(laptop, "start", delta.JsonObjectOf("providerId", "faux", "authType", "oauth"))
	if err != nil {
		t.Fatal(err)
	}
	until(t, laptop, "login", "the laptop's prompt", promptIs("select"))
	if now, _ := device.Member("login").Value(); now != nil {
		t.Fatalf("the device sees the laptop's login: %v", now)
	}
	laptop.Release()
	var again delta.JsonValue
	if err := p2test.Poll(func() (bool, error) {
		again, err = call(device, "start", delta.JsonObjectOf("providerId", "faux", "authType", "oauth"))
		return err == nil, nil
	}, 10*time.Second, "the laptop's login to end with its sheet"); err != nil {
		t.Fatal(err)
	}
	if field(again, "attemptId") == field(second, "attemptId") {
		t.Fatal("the same attempt")
	}
	if _, err := call(device, "cancel", delta.JsonObjectOf("attemptId", field(again, "attemptId"))); err != nil {
		t.Fatal(err)
	}
	until(t, device, "login", "cancelled", loginIs("cancelled"))
}

func TestOnlyOwnersHoldTheProviderLogin(t *testing.T) {
	fixture, _, _ := start(t)
	guest := fixture.As(p2runtime.GuestPrincipal{Name: "g", Sessions: map[string]p2runtime.GuestMode{}})
	if _, err := p2test.HoldIn(guest, contract.ProviderLogin, delta.NewJsonObject(0), "owner"); err == nil {
		t.Fatal("a guest holds the provider login")
	}
}
