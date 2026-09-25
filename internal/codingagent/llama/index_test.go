package llama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// fakeUI scripts LlamaUi answers and records what the flow showed.
type fakeUI struct {
	t        *testing.T
	actions  []LlamaManagerAction
	selects  map[string]string
	confirm  bool
	search   string
	selected []string
	statuses []string
	retries  []string
}

func (u *fakeUI) ShowModels(string, []LlamaModelInfo) LlamaManagerAction {
	if len(u.actions) == 0 {
		return LlamaManagerAction{Type: LlamaManagerActionClose}
	}
	action := u.actions[0]
	u.actions = u.actions[1:]
	return action
}

func (u *fakeUI) Select(title string, options []string) (string, bool) {
	u.selected = append(u.selected, title+" => "+strings.Join(options, " | "))
	for prefix, answer := range u.selects {
		if strings.HasPrefix(title, prefix) {
			return answer, true
		}
	}
	u.t.Errorf("unexpected select %q", title)
	return "", false
}

func (u *fakeUI) Confirm(title, message string) bool {
	u.selected = append(u.selected, title+" "+message)
	return u.confirm
}

func (u *fakeUI) ConnectionError(_, message string) string {
	u.retries = append(u.retries, message)
	return "close"
}

func (u *fakeUI) SearchModels(HuggingFaceSearchFunc) (string, bool) {
	return u.search, u.search != ""
}

func (u *fakeUI) ShowStatus(title, message string) {
	u.statuses = append(u.statuses, title+": "+message)
}

func (u *fakeUI) Progress(ProgressState) <-chan struct{} { return make(chan struct{}) }
func (u *fakeUI) UpdateProgress(ProgressState)           {}

// routerServer is a llama.cpp router whose loads and downloads complete
// immediately.
type routerServer struct {
	mu       sync.Mutex
	models   map[string]string
	order    []string
	requests []string
	failLoad bool
}

func newRouterServer(t *testing.T, models ...[2]string) (*routerServer, string) {
	router := &routerServer{models: map[string]string{}}
	for _, model := range models {
		router.models[model[0]] = model[1]
		router.order = append(router.order, model[0])
	}
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return router, server.URL
}

func (r *routerServer) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var body struct{ Model string }
	_ = json.NewDecoder(request.Body).Decode(&body)
	switch {
	case request.URL.Path == "/models/sse":
		w.WriteHeader(http.StatusNotFound)
		return
	case request.Method == http.MethodPost:
		r.requests = append(r.requests, request.URL.Path+" "+body.Model)
		switch request.URL.Path {
		case "/models/load":
			r.models[body.Model] = "loaded"
		case "/models/unload":
			r.models[body.Model] = "unloaded"
		case "/models":
			r.models[body.Model] = "unloaded"
			r.order = append(r.order, body.Model)
		}
		writeJSON(w, map[string]any{"success": true})
	case request.URL.Path == "/models":
		data := []any{}
		for _, id := range r.order {
			status := map[string]any{"value": r.models[id]}
			if r.failLoad && r.models[id] == "loaded" && id == "beta" {
				status = map[string]any{"value": "unloaded", "failed": true, "exit_code": 1}
			}
			data = append(data, map[string]any{"id": id, "status": status})
		}
		writeJSON(w, map[string]any{"data": data})
	case request.URL.Path == "/props":
		writeJSON(w, map[string]any{})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (r *routerServer) posts() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.requests)
}

type notification struct{ message, kind string }

func commandFixture(t *testing.T, url string) (*commandSession, *[]notification) {
	t.Helper()
	clearLlamaEnv(t)
	t.Setenv("HF_TOKEN", "hf-test")
	dir := t.TempDir()
	host, _, credentials := newTestHost(t, dir)
	if err := credentials.Set(LlamaProviderID, ai.Credential{Type: ai.CredentialAPIKey, Env: map[string]string{"LLAMA_BASE_URL": url}}); err != nil {
		t.Fatal(err)
	}
	var notices []notification
	ctx := CommandContext{Ctx: context.Background(), Mode: "tui", Notify: func(message, kind string) {
		notices = append(notices, notification{message, kind})
	}}
	client, err := host.configuredClient(ctx)
	if err != nil || client == nil {
		t.Fatalf("configuredClient = %v, %v", client, err)
	}
	return &commandSession{host: host, ctx: ctx, client: client}, &notices
}

func model(id string, status LlamaModelStatus) LlamaModelInfo {
	return LlamaModelInfo{ID: id, Status: LlamaModelInfoStatus{Value: status}}
}

func TestLlamaCommandRequiresInteractiveModeAndConfiguration(t *testing.T) {
	clearLlamaEnv(t)
	host, _, _ := newTestHost(t, t.TempDir())
	var notices []notification
	customCalled := false
	ctx := CommandContext{
		Ctx:    context.Background(),
		Mode:   "rpc",
		Notify: func(message, kind string) { notices = append(notices, notification{message, kind}) },
		Custom: func(func(requestRender, done func()) CustomComponent) { customCalled = true },
	}
	if err := host.HandleCommand(ctx); err != nil {
		t.Fatal(err)
	}
	ctx.Mode = "tui"
	if err := host.HandleCommand(ctx); err != nil {
		t.Fatal(err)
	}
	want := []notification{
		{"/llama is available in interactive mode", "warning"},
		{"Configure llama.cpp with /login llama.cpp", "warning"},
	}
	if !reflect.DeepEqual(notices, want) || customCalled {
		t.Fatalf("notices = %+v, custom shown %v", notices, customCalled)
	}
}

func TestLlamaCommandLoadsKeepingOrReplacingLoadedModels(t *testing.T) {
	for _, test := range []struct {
		choice string
		posts  []string
	}{
		{"Keep loaded and load", []string{"/models/load beta"}},
		{"Unload all and load", []string{"/models/unload alpha", "/models/load beta"}},
	} {
		t.Run(test.choice, func(t *testing.T) {
			router, url := newRouterServer(t, [2]string{"alpha", "loaded"}, [2]string{"beta", "unloaded"})
			session, notices := commandFixture(t, url)
			ui := &fakeUI{t: t, actions: []LlamaManagerAction{{Type: LlamaManagerActionModel, Model: model("beta", LlamaModelStatusUnloaded)}}, selects: map[string]string{"1 model is loaded": test.choice}}
			if err := session.manage(ui); err != nil {
				t.Fatal(err)
			}
			if got := router.posts(); !reflect.DeepEqual(got, test.posts) {
				t.Fatalf("posts = %v, want %v", got, test.posts)
			}
			if want := []notification{{"Loaded beta", "info"}}; !reflect.DeepEqual(*notices, want) {
				t.Fatalf("notices = %+v", *notices)
			}
			if got := modelIDs(session.host.Provider().GetModels()); !slices.Contains(got, "beta") {
				t.Fatalf("provider models after load = %v", got)
			}
		})
	}
}

func TestLlamaCommandUnloadsAndWarnsAboutBusyModels(t *testing.T) {
	router, url := newRouterServer(t, [2]string{"alpha", "loaded"})
	session, notices := commandFixture(t, url)
	ui := &fakeUI{t: t, confirm: true, actions: []LlamaManagerAction{
		{Type: LlamaManagerActionModel, Model: model("alpha", LlamaModelStatusLoaded)},
		{Type: LlamaManagerActionModel, Model: model("gamma", LlamaModelStatusDownloading)},
	}}
	if err := session.manage(ui); err != nil {
		t.Fatal(err)
	}
	if got := router.posts(); !reflect.DeepEqual(got, []string{"/models/unload alpha"}) {
		t.Fatalf("posts = %v", got)
	}
	want := []notification{{"Unloaded alpha", "info"}, {"gamma is downloading", "warning"}}
	if !reflect.DeepEqual(*notices, want) {
		t.Fatalf("notices = %+v", *notices)
	}
	if !slices.Contains(ui.selected, "Unload model? alpha") {
		t.Fatalf("confirmations = %v", ui.selected)
	}
}

func TestLlamaCommandReportsLoadFailuresAfterRestoringModels(t *testing.T) {
	router, url := newRouterServer(t, [2]string{"alpha", "loaded"}, [2]string{"beta", "unloaded"})
	router.failLoad = true
	session, notices := commandFixture(t, url)
	ui := &fakeUI{t: t, actions: []LlamaManagerAction{{Type: LlamaManagerActionModel, Model: model("beta", LlamaModelStatusUnloaded)}}, selects: map[string]string{"1 model is loaded": "Unload all and load"}}
	if err := session.manage(ui); err != nil {
		t.Fatal(err)
	}
	wantPosts := []string{"/models/unload alpha", "/models/load beta", "/models/load alpha"}
	if got := router.posts(); !reflect.DeepEqual(got, wantPosts) {
		t.Fatalf("posts = %v, want %v", got, wantPosts)
	}
	want := []notification{{"Restoring previously loaded models", "info"}, {"Model exited with code 1", "error"}}
	if !reflect.DeepEqual(*notices, want) {
		t.Fatalf("notices = %+v", *notices)
	}
}

func TestLlamaCommandDownloadsAfterAccessAndQuantizationChoices(t *testing.T) {
	router, url := newRouterServer(t)
	huggingFace := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer hf-test" || r.URL.Path != "/api/models/owner/repo" {
			t.Errorf("hugging face request %s with %q", r.URL, r.Header.Get("Authorization"))
		}
		writeJSON(w, map[string]any{"id": "owner/repo", "gated": "auto", "siblings": []any{
			map[string]any{"rfilename": "repo-Q8_0.gguf", "size": 8192},
			map[string]any{"rfilename": "repo-Q4_K_M.gguf", "size": 4096},
		}})
	}))
	defer huggingFace.Close()
	session, notices := commandFixture(t, url)
	session.host.huggingFaceURL = huggingFace.URL
	ui := &fakeUI{t: t, search: "owner/repo", actions: []LlamaManagerAction{{Type: LlamaManagerActionDownload}}, selects: map[string]string{
		"Hugging Face access required": "Continue",
		"Select quantization":          "Q4_K_M · 4.00 KiB · recommended",
	}}
	if err := session.manage(ui); err != nil {
		t.Fatal(err)
	}
	if got := router.posts(); !reflect.DeepEqual(got, []string{"/models owner/repo:Q4_K_M"}) {
		t.Fatalf("posts = %v", got)
	}
	if want := []notification{{"Downloaded owner/repo:Q4_K_M", "info"}}; !reflect.DeepEqual(*notices, want) {
		t.Fatalf("notices = %+v", *notices)
	}
	wantSelects := []string{
		"Hugging Face access required\nowner/repo\n\nAccept the access terms at:\nhttps://huggingface.co/owner/repo\n\nThe llama.cpp server needs HF_TOKEN with access. => Continue | Back",
		"Select quantization\nowner/repo => Q4_K_M · 4.00 KiB · recommended | Q8_0 · 8.00 KiB",
	}
	if !reflect.DeepEqual(ui.selected, wantSelects) || !reflect.DeepEqual(ui.statuses, []string{"Loading model details: owner/repo"}) {
		t.Fatalf("selects = %q, statuses = %q", ui.selected, ui.statuses)
	}
}

func TestLlamaCommandOffersRetryWhenTheServerIsUnreachable(t *testing.T) {
	_, url := newRouterServer(t)
	session, notices := commandFixture(t, url)
	unreachable := httptest.NewServer(http.NotFoundHandler())
	unreachableURL := unreachable.URL
	unreachable.Close()
	session.client, _ = NewLlamaClient(unreachableURL, "")
	ui := &fakeUI{t: t}
	if err := session.manage(ui); err != nil {
		t.Fatal(err)
	}
	if want := []string{"Could not connect to the server."}; !reflect.DeepEqual(ui.retries, want) || len(*notices) != 0 {
		t.Fatalf("retries = %v, notices = %+v", ui.retries, *notices)
	}
}

func TestParseHuggingFaceModel(t *testing.T) {
	for input, want := range map[string][2]string{
		"owner/repo":        {"owner/repo", ""},
		"owner/repo:Q4_K_M": {"owner/repo", "Q4_K_M"},
		"repo:Q8":           {"repo", "Q8"},
		"owner/repo:":       {"owner/repo", ""},
		"a:b/c:d":           {"a:b/c", "d"},
	} {
		repository, quantization := parseHuggingFaceModel(input)
		if repository != want[0] || quantization != want[1] {
			t.Errorf("parseHuggingFaceModel(%q) = %q, %q; want %q", input, repository, quantization, want)
		}
	}
}
