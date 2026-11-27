package coding

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/profiling"
)

// authPassStore is the credential store of an availability pass under test. Reads run one at a time, so "the first read of the pass" is well
// defined although a pass reads from many goroutines: afterFirst runs right after it, which places another writer's change inside the pass.
// A hold makes one provider's reads wait until an extension provider's check has run, so they are reads of the pass that follow the check.
type authPassStore struct {
	ai.CredentialStore
	mu         sync.Mutex
	reads      int
	afterFirst func()
	calls      atomic.Int64
	hold       atomic.Pointer[authPassHold]
}

type authPassHold struct {
	provider string
	release  chan struct{}
}

func (s *authPassStore) step(read func()) {
	s.calls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	read()
	if s.reads == 1 && s.afterFirst != nil {
		s.afterFirst()
	}
}

func (s *authPassStore) Read(ctx context.Context, id string) (credential *ai.Credential, err error) {
	if hold := s.hold.Load(); hold != nil && hold.provider == id {
		select {
		case <-hold.release:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
	s.step(func() { credential, err = s.CredentialStore.Read(ctx, id) })
	return credential, err
}

func (s *authPassStore) List(ctx context.Context) (infos []ai.CredentialInfo, err error) {
	s.step(func() { infos, err = s.CredentialStore.List(ctx) })
	return infos, err
}

func (s *authPassStore) arm(afterFirst func()) {
	s.mu.Lock()
	s.reads, s.afterFirst = 0, afterFirst
	s.mu.Unlock()
}

// authPassRig is a model runtime over one auth.json whose built-in providers all send their requests to one local server.
type authPassRig struct {
	t        *testing.T
	authPath string
	auth     *ai.AuthStorage
	store    *authPassStore
	runtime  *ModelRuntime
	seen     chan string
	// inCheck runs inside the extension provider's auth check, once per arming.
	inCheck atomic.Pointer[func()]
}

// authPassRequest is what one request did: the Authorization header the provider received, or the error of a request that never reached it.
type authPassRequest struct {
	Reached       bool
	Authorization string
	Error         string
}

const authPassExtensionID = "pass-extension"

// newAuthPassServer answers as an OpenAI-compatible provider and reports each request's Authorization header.
func newAuthPassServer(t *testing.T) (*httptest.Server, chan string) {
	t.Helper()
	seen := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case seen <- r.Header.Get("Authorization"):
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	return server, seen
}

// newAuthPassRig creates the runtime over auth.json holding content. direct reads the file through the runtime's own AuthStorage; otherwise the
// reads go through an authPassStore. extension registers an extension provider whose check runs the rig's inCheck.
func newAuthPassRig(t *testing.T, server *httptest.Server, seen chan string, content string, direct, extension bool) *authPassRig {
	t.Helper()
	agent := t.TempDir()
	rig := &authPassRig{t: t, authPath: filepath.Join(agent, "auth.json"), seen: seen}
	rig.rewrite(content)
	modelsPath := filepath.Join(agent, "models.json")
	config := `{"providers":{"openai":{"baseUrl":"` + server.URL + `"},"openai-codex":{"baseUrl":"` + server.URL + `"},"groq":{"baseUrl":"` + server.URL + `"}}}`
	if err := os.WriteFile(modelsPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	options := CreateModelRuntimeOptions{ModelsPath: new(&modelsPath), ModelsStorePath: filepath.Join(agent, "models-store.json"), RefreshOnCreate: new(false)}
	if direct {
		options.AuthPath = rig.authPath
	} else {
		auth, err := ai.NewAuthStorage(rig.authPath)
		if err != nil {
			t.Fatal(err)
		}
		rig.auth = auth
		rig.store = &authPassStore{CredentialStore: auth}
		options.Credentials = rig.store
	}
	runtime, err := CreateModelRuntime(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	rig.runtime = runtime
	if extension {
		provider := &ai.ModelsProvider{ID: authPassExtensionID, Name: authPassExtensionID, GetModels: func() ([]*ai.Model, error) {
			return []*ai.Model{{ID: "dynamic", DisplayName: "Dynamic", Input: []string{"text"}, ProviderMeta: ai.ProviderMetadata{ProviderID: authPassExtensionID, API: ai.APIOpenAICompletions, BaseURL: "https://extension.invalid/v1"}}}, nil
		}, Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "API key", Check: func(_ context.Context, input ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
			if run := rig.inCheck.Swap(nil); run != nil {
				(*run)()
			}
			if input.Credential == nil {
				return nil, nil
			}
			return &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "stored"}, nil
		}}}}
		if err := runtime.RegisterNativeProvider(provider); err != nil {
			t.Fatal(err)
		}
		rig.registry().YieldToRegistrationRefresh(t.Context())
	}
	return rig
}

func (rig *authPassRig) registry() *icodingagent.ModelRegistry {
	return rig.runtime.services.Registry().ModelRegistry
}

func (rig *authPassRig) rewrite(content string) {
	rig.t.Helper()
	if err := os.WriteFile(rig.authPath, []byte(content), 0o600); err != nil {
		rig.t.Error(err)
	}
}

// request sends one request to provider's model through the runtime's request path.
func (rig *authPassRig) request(provider, model string) authPassRequest {
	rig.t.Helper()
	select {
	case <-rig.seen:
	default:
	}
	entry := rig.runtime.GetModel(provider, model)
	if entry == nil {
		rig.t.Fatalf("no model %s/%s", provider, model)
	}
	stream := rig.runtime.Stream(context.Background(), entry, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hello")}}}, ai.StreamOptions{})
	_ = collectRuntimeEvents(context.Background(), stream)
	select {
	case authorization := <-rig.seen:
		return authPassRequest{Reached: true, Authorization: authorization}
	default:
	}
	result := stream.Result()
	if result == nil {
		return authPassRequest{Error: "no result"}
	}
	return authPassRequest{Error: result.ErrorMessage}
}

// authPass is one kind of availability pass: it makes the pass and reports what the pass published about provider.
type authPass struct {
	name      string
	extension bool
	run       func(rig *authPassRig, provider string) string
}

func authPassRefresh(rig *authPassRig, provider string) string {
	if err := rig.runtime.queueAvailabilityRefresh(rig.t.Context()); err != nil {
		return "error: " + err.Error() + "; GetError: " + rig.runtime.GetError()
	}
	listed := slices.ContainsFunc(rig.runtime.GetAvailableSnapshot(), func(model *ai.Model) bool { return model.ProviderMeta.ProviderID == provider })
	return fmt.Sprintf("configured=%v listed=%v", rig.runtime.HasConfiguredAuth(provider), listed)
}

var authPasses = []authPass{
	{name: "GetAvailableModelData", run: func(rig *authPassRig, provider string) string {
		listed := slices.ContainsFunc(rig.registry().GetAvailableModelData(), func(model *ai.Model) bool { return model.ProviderMeta.ProviderID == provider })
		return fmt.Sprintf("listed=%v", listed)
	}},
	{name: "AvailableProviderCount", run: func(rig *authPassRig, _ string) string {
		return fmt.Sprintf("providers=%d", rig.registry().AvailableProviderCount())
	}},
	{name: "availability refresh", run: authPassRefresh},
	{name: "availability refresh with an extension provider", extension: true, run: authPassRefresh},
}

// authPassChange is a change another process makes to auth.json: before and after differ in size, so the change is a new file revision
// whatever the clock's resolution. want is the Authorization header of the next request; empty when that request must not reach the provider.
type authPassChange struct {
	name, provider, model string
	before, after         string
	want                  string
}

func authPassChanges() []authPassChange {
	refreshed := strings.TrimSuffix(codexTestAccessToken, "sig") + "refreshed-elsewhere"
	oauth := func(access, refresh string, expires time.Time) string {
		return fmt.Sprintf(`{"openai-codex":{"type":"oauth","refresh":%q,"access":%q,"expires":%d}}`, refresh, access, expires.UnixMilli())
	}
	return []authPassChange{
		{name: "API key replaced", provider: "openai", model: "gpt-4o-mini",
			before: `{"openai":{"type":"api_key","key":"old-key"}}`, after: `{"openai":{"type":"api_key","key":"rotated-key"}}`, want: "Bearer rotated-key"},
		{name: "OAuth token refreshed by another process", provider: "openai-codex", model: "gpt-5.6-sol",
			before: oauth(codexTestAccessToken, "refresh", time.Now().Add(time.Hour)), after: oauth(refreshed, "refresh-rotated", time.Now().Add(2*time.Hour)), want: "Bearer " + refreshed},
		{name: "OpenAI-compatible provider with a custom base URL", provider: "groq", model: "openai/gpt-oss-120b",
			before: `{"groq":{"type":"api_key","key":"old-groq"}}`, after: `{"groq":{"type":"api_key","key":"rotated-groq-key"}}`, want: "Bearer rotated-groq-key"},
		{name: "credential removed by a logout elsewhere", provider: "openai", model: "gpt-4o-mini",
			before: `{"openai":{"type":"api_key","key":"old-key"}}`, after: `{}`},
		{name: "credential added by a login elsewhere", provider: "openai", model: "gpt-4o-mini",
			before: `{}`, after: `{"openai":{"type":"api_key","key":"added-key"}}`, want: "Bearer added-key"},
	}
}

func authPassEnvironment(t *testing.T) {
	t.Helper()
	clearProviderEnvKeys(t)
	t.Setenv("HOME", t.TempDir())
}

// The owner's rule for one auth.json check per pass (2026-11-12): a write to auth.json in the middle of an availability pass is used by the next
// request. The pass itself publishes from the view it took at its start, the next request succeeds or fails exactly as a process started over
// the new file does, and the next pass publishes the new file. Another process writes right after the pass's first credential read.
func TestAuthWriteDuringAvailabilityPassIsUsedByNextRequest(t *testing.T) {
	authPassEnvironment(t)
	server, seen := newAuthPassServer(t)
	for _, pass := range authPasses {
		for _, change := range authPassChanges() {
			t.Run(pass.name+"/"+change.name, func(t *testing.T) {
				fresh := newAuthPassRig(t, server, seen, change.after, false, pass.extension)
				freshPass, freshRequest := pass.run(fresh, change.provider), fresh.request(change.provider, change.model)

				rig := newAuthPassRig(t, server, seen, change.before, false, pass.extension)
				earlier := pass.run(rig, change.provider)
				rig.store.arm(func() { rig.rewrite(change.after) })
				during := pass.run(rig, change.provider)
				rig.store.arm(nil)
				if during != earlier {
					t.Errorf("the pass during which auth.json was written published %q, want the view of its start %q", during, earlier)
				}
				if next := rig.request(change.provider, change.model); next != freshRequest {
					t.Errorf("the next request: %+v, want %+v as over the new file", next, freshRequest)
				} else if next.Reached != (change.want != "") || next.Authorization != change.want {
					t.Errorf("the next request: %+v, want Authorization %q", next, change.want)
				}
				if next := pass.run(rig, change.provider); next != freshPass {
					t.Errorf("the next pass published %q, want %q as over the new file", next, freshPass)
				}
			})
		}
	}
}

// D628: an extension provider's own check rewrites auth.json on disk in the middle of the pass. Pi's later reads of the same pass
// stat again and use the rewrite; here the pass publishes from the view of its start, and the next request and the next pass use the rewrite.
func TestExtensionCheckRewritingAuthFileIsUsedByNextRequest(t *testing.T) {
	authPassEnvironment(t)
	server, seen := newAuthPassServer(t)
	pass := authPasses[3]
	for _, change := range authPassChanges() {
		t.Run(change.name, func(t *testing.T) {
			fresh := newAuthPassRig(t, server, seen, change.after, false, true)
			freshPass, freshRequest := pass.run(fresh, change.provider), fresh.request(change.provider, change.model)

			rig := newAuthPassRig(t, server, seen, change.before, false, true)
			earlier := pass.run(rig, change.provider)
			// The provider's reads wait for the check, so they follow the rewrite.
			hold := &authPassHold{provider: change.provider, release: make(chan struct{})}
			rig.store.hold.Store(hold)
			inCheck := func() {
				rig.rewrite(change.after)
				close(hold.release)
			}
			rig.inCheck.Store(&inCheck)
			during := pass.run(rig, change.provider)
			rig.store.hold.Store(nil)
			if during != earlier {
				t.Errorf("the pass whose extension check rewrote auth.json published %q, want the view of its start %q", during, earlier)
			}
			if next := rig.request(change.provider, change.model); next != freshRequest {
				t.Errorf("the next request: %+v, want %+v as over the new file", next, freshRequest)
			} else if next.Reached != (change.want != "") || next.Authorization != change.want {
				t.Errorf("the next request: %+v, want Authorization %q", next, change.want)
			}
			if next := pass.run(rig, change.provider); next != freshPass {
				t.Errorf("the next pass published %q, want %q as over the new file", next, freshPass)
			}
		})
	}
}

// A write through the credential API in this process ends the pass's view at once: the reads of the same pass that follow it use the write.
func TestCredentialAPIWriteDuringAvailabilityPassIsUsedByTheSamePass(t *testing.T) {
	authPassEnvironment(t)
	server, seen := newAuthPassServer(t)
	rig := newAuthPassRig(t, server, seen, `{}`, false, true)
	if earlier := authPassRefresh(rig, "openai"); earlier != "configured=false listed=false" {
		t.Fatalf("before the login: %s", earlier)
	}
	hold := &authPassHold{provider: "openai", release: make(chan struct{})}
	rig.store.hold.Store(hold)
	inCheck := func() {
		if _, err := rig.auth.Modify(context.Background(), "openai", func(*ai.Credential) (*ai.Credential, error) {
			return &ai.Credential{Type: ai.CredentialAPIKey, Key: "logged-in-key"}, nil
		}); err != nil {
			t.Error(err)
		}
		close(hold.release)
	}
	rig.inCheck.Store(&inCheck)
	during := authPassRefresh(rig, "openai")
	rig.store.hold.Store(nil)
	if during != "configured=true listed=true" {
		t.Errorf("the pass during which the credential API wrote published %q, want the write", during)
	}
	if next := rig.request("openai", "gpt-4o-mini"); next != (authPassRequest{Reached: true, Authorization: "Bearer logged-in-key"}) {
		t.Errorf("the next request: %+v", next)
	}
}

// authFileStats is the number of auth.json revision stats the process has made.
func authFileStats() uint64 {
	return profiling.FileRevisionStats.Load() - profiling.ModelsFileStats.Load()
}

// leastAuthFileStats runs pass three times and returns the least number of auth.json stats one run made: the counters are the process's, so a
// run can include a stat of another test's background work, and the least run is the pass's own.
func leastAuthFileStats(pass func()) uint64 {
	least := ^uint64(0)
	for range 3 {
		before := authFileStats()
		pass()
		least = min(least, authFileStats()-before)
	}
	return least
}

// An availability pass checks the revision of each auth file once: the bound is the number of auth files a pass reads (auth.json is the only
// one), not the number of providers or of reads.
func TestAvailabilityPassStatsEachAuthFileOnce(t *testing.T) {
	authPassEnvironment(t)
	server, seen := newAuthPassServer(t)
	content := `{"openai":{"type":"api_key","key":"k"},"groq":{"type":"api_key","key":"g"},"openai-codex":{"type":"oauth","refresh":"r","access":"` + codexTestAccessToken + `","expires":` + fmt.Sprint(time.Now().Add(time.Hour).UnixMilli()) + `}}`
	for _, pass := range authPasses {
		t.Run(pass.name, func(t *testing.T) {
			rig := newAuthPassRig(t, server, seen, content, true, pass.extension)
			authFiles := []string{rig.authPath}
			if got := leastAuthFileStats(func() { pass.run(rig, "openai") }); got > uint64(len(authFiles)) {
				t.Errorf("%d auth.json stats in one pass, want at most %d (one per auth file)", got, len(authFiles))
			}
		})
	}
}

// A "!command" key runs its command on read, and the command can rewrite auth.json, so a pass over such a store keeps a stat per read, with
// or without an extension provider.
func TestAvailabilityPassStatsEveryReadAfterACommandKey(t *testing.T) {
	authPassEnvironment(t)
	server, seen := newAuthPassServer(t)
	for _, pass := range authPasses[2:] {
		t.Run(pass.name, func(t *testing.T) {
			rig := newAuthPassRig(t, server, seen, `{"anthropic":{"type":"api_key","key":"!echo k"},"openai":{"type":"api_key","key":"o"}}`, false, pass.extension)
			pass.run(rig, "openai")
			var reads int64
			stats := leastAuthFileStats(func() {
				before := rig.store.calls.Load()
				pass.run(rig, "openai")
				reads = rig.store.calls.Load() - before
			})
			if stats != uint64(reads) {
				t.Errorf("%d auth.json stats for the %d credential reads of a pass over a command key, want one per read", stats, reads)
			}
		})
	}
}

// auth.json becomes malformed or unreadable in the middle of a pass: the pass publishes from the view of its start, and the next request and
// the next pass report exactly what they report when the file breaks between two passes.
func TestBrokenAuthFileDuringAvailabilityPass(t *testing.T) {
	authPassEnvironment(t)
	server, seen := newAuthPassServer(t)
	const content = `{"openai":{"type":"api_key","key":"old-key"}}`
	for _, broken := range []struct {
		name    string
		breakIt func(t *testing.T, path string)
	}{
		{name: "malformed", breakIt: func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte(`{"openai":`), 0o600); err != nil {
				t.Error(err)
			}
		}},
		{name: "unreadable", breakIt: func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Error(err)
			}
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Error(err)
			}
		}},
	} {
		for _, pass := range authPasses {
			t.Run(broken.name+"/"+pass.name, func(t *testing.T) {
				between := newAuthPassRig(t, server, seen, content, false, pass.extension)
				pass.run(between, "openai")
				broken.breakIt(t, between.authPath)
				wantRequest, wantPass := between.request("openai", "gpt-4o-mini"), pass.run(between, "openai")

				rig := newAuthPassRig(t, server, seen, content, false, pass.extension)
				earlier := pass.run(rig, "openai")
				rig.store.arm(func() { broken.breakIt(t, rig.authPath) })
				during := pass.run(rig, "openai")
				rig.store.arm(nil)
				next, nextPass := rig.request("openai", "gpt-4o-mini"), pass.run(rig, "openai")
				t.Logf("this pass %q; next request %+v; next pass %q", during, next, nextPass)
				if during != earlier {
					t.Errorf("the pass during which auth.json broke published %q, want the view of its start %q", during, earlier)
				}
				if next != wantRequest {
					t.Errorf("the next request: %+v, want %+v as after a break between passes", next, wantRequest)
				}
				if nextPass != wantPass {
					t.Errorf("the next pass published %q, want %q as after a break between passes", nextPass, wantPass)
				}
			})
		}
	}
}
