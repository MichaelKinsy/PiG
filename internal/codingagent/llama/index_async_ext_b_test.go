package llama

// pi: packages/coding-agent/src/extensions/llama/index.ts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// asyncRouter is a llama.cpp router whose load of one model stays pending, ends in an exit failure, or is refused.
type asyncRouter struct {
	mu       sync.Mutex
	status   map[string]string
	failed   map[string]bool
	order    []string
	posts    []string
	hold     string
	exitLoad string
	refuse   string
	loadSeen chan struct{}
	seenOnce sync.Once
}

func newAsyncRouter(t *testing.T, models ...[2]string) (*asyncRouter, string) {
	router := &asyncRouter{status: map[string]string{}, failed: map[string]bool{}, loadSeen: make(chan struct{})}
	for _, entry := range models {
		router.status[entry[0]] = entry[1]
		router.order = append(router.order, entry[0])
	}
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return router, server.URL
}

func (r *asyncRouter) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var body struct{ Model string }
	_ = json.NewDecoder(request.Body).Decode(&body)
	switch {
	case request.URL.Path == "/models/sse":
		w.WriteHeader(http.StatusNotFound)
	case request.Method == http.MethodPost:
		r.posts = append(r.posts, request.URL.Path+" "+body.Model)
		switch request.URL.Path {
		case "/models/load":
			switch body.Model {
			case r.refuse:
				w.WriteHeader(http.StatusInternalServerError)
				writeJSON(w, map[string]any{"error": "restore exploded"})
				return
			case r.hold:
				r.status[body.Model] = "loading"
				r.seenOnce.Do(func() { close(r.loadSeen) })
			case r.exitLoad:
				r.status[body.Model], r.failed[body.Model] = "unloaded", true
			default:
				r.status[body.Model] = "loaded"
			}
		case "/models/unload":
			r.status[body.Model], r.failed[body.Model] = "unloaded", false
		}
		writeJSON(w, map[string]any{"success": true})
	case request.URL.Path == "/models":
		data := []any{}
		for _, id := range r.order {
			status := map[string]any{"value": r.status[id]}
			if r.failed[id] {
				status["failed"], status["exit_code"] = true, 1
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

func (r *asyncRouter) requests() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.posts)
}

// stopLoadUI confirms a stop of the load as soon as the router has seen it, like a user pressing Esc and confirming.
type stopLoadUI struct {
	*fakeUI
	loadSeen <-chan struct{}
}

func (u *stopLoadUI) Progress(ProgressState) <-chan struct{} {
	<-u.loadSeen
	stop := make(chan struct{})
	close(stop)
	return stop
}

// index.ts:93-118 loadModel: a load the user stops after "Unload all and load" is cancelled on the server, aborted, awaited, and
// the previously loaded models are loaded again before the command goes on (`if (result.cancelled) { if (replace) await
// restoreLoaded(); return; }`), without an error notice.
func TestLlamaCommandRestoresReplacedModelsWhenTheUserStopsTheLoad(t *testing.T) {
	router, url := newAsyncRouter(t, [2]string{"alpha", "loaded"}, [2]string{"beta", "unloaded"})
	router.hold = "beta"
	session, notices := commandFixture(t, url)
	ui := &stopLoadUI{
		fakeUI:   &fakeUI{t: t, confirm: true, actions: []LlamaManagerAction{{Type: LlamaManagerActionModel, Model: model("beta", LlamaModelStatusUnloaded)}}, selects: map[string]string{"1 model is loaded": "Unload all and load"}},
		loadSeen: router.loadSeen,
	}
	if err := session.manage(ui); err != nil {
		t.Fatal(err)
	}
	want := []string{"/models/unload alpha", "/models/load beta", "/models/unload beta", "/models/load alpha"}
	if got := router.requests(); !reflect.DeepEqual(got, want) {
		t.Fatalf("posts = %v, want %v", got, want)
	}
	if wantNotices := []notification{{"Restoring previously loaded models", "info"}}; !reflect.DeepEqual(*notices, wantNotices) {
		t.Fatalf("notices = %+v, want %+v", *notices, wantNotices)
	}
}

// index.ts:105-117 loadModel: a load that fails after "Unload all and load" restores the previous models, and when that restore
// fails too (`catch { // Preserve the original load error. }`) the notice is still the load's own error.
func TestLlamaCommandReportsTheLoadErrorWhenTheRestoreAlsoFails(t *testing.T) {
	router, url := newAsyncRouter(t, [2]string{"alpha", "loaded"}, [2]string{"beta", "unloaded"})
	router.exitLoad, router.refuse = "beta", "alpha"
	session, notices := commandFixture(t, url)
	ui := &fakeUI{t: t, actions: []LlamaManagerAction{{Type: LlamaManagerActionModel, Model: model("beta", LlamaModelStatusUnloaded)}}, selects: map[string]string{"1 model is loaded": "Unload all and load"}}
	if err := session.manage(ui); err != nil {
		t.Fatal(err)
	}
	want := []notification{{"Restoring previously loaded models", "info"}, {"Model exited with code 1", "error"}}
	if !reflect.DeepEqual(*notices, want) {
		t.Fatalf("notices = %+v, want %+v", *notices, want)
	}
	if got := router.requests(); !reflect.DeepEqual(got, []string{"/models/unload alpha", "/models/load beta", "/models/load alpha"}) {
		t.Fatalf("posts = %v", got)
	}
}

// index.ts:46-61 syncCatalog shares one AbortSignal.timeout(15_000) between the catalog list and the registry refresh: a list that
// never answers rejects at 15 s with the timeout error, and a refresh that never finishes by then rejects with "Model catalog
// refresh timed out.". The fake transport answers the catalog and holds every other request until the request's context ends.
func TestLlamaCommandSyncCatalogEndsAtTheSharedFifteenSecondTimeout(t *testing.T) {
	for _, tc := range []struct {
		name        string
		holdCatalog bool
		wantMessage string
	}{
		{"the catalog list hangs", true, "The operation was aborted due to timeout"},
		{"the registry refresh hangs", false, "Model catalog refresh timed out."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, _ := commandFixture(t, "http://llama.invalid")
			settleCommandSession(session)
			transport := http.DefaultClient.Transport
			http.DefaultClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/models" && !tc.holdCatalog {
					body := `{"data":[{"id":"qwen","status":{"value":"loaded"},"meta":{"n_ctx":32768}}]}`
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
				}
				<-r.Context().Done()
				return nil, r.Context().Err()
			})
			t.Cleanup(func() { http.DefaultClient.Transport = transport })
			synctest.Test(t, func(t *testing.T) {
				defer settleCommandSession(session)
				result := make(chan error, 1)
				go func() {
					_, err := session.syncCatalog(nil, false)
					result <- err
				}()
				time.Sleep(15*time.Second - time.Millisecond)
				synctest.Wait()
				select {
				case err := <-result:
					t.Fatalf("sync ended before 15 s: %v", err)
				default:
				}
				time.Sleep(time.Millisecond)
				synctest.Wait()
				select {
				case err := <-result:
					if err == nil || err.Error() != tc.wantMessage {
						t.Fatalf("sync error = %v, want %q", err, tc.wantMessage)
					}
				default:
					t.Fatal("sync did not end at 15 s")
				}
			})
		})
	}
}

// pendingDeadline is a caller's signal whose timeout is due at a known instant and has not fired: it reports the deadline, and only the test aborts it. That is the state
// in which two timers are due together, held still, so the test sees which of the two signals ends a request.
type pendingDeadline struct {
	context.Context
	due time.Time
}

func (c pendingDeadline) Deadline() (time.Time, bool) { return c.due, true }

// client.ts:193-194 and huggingface.ts:75-76 join the caller's signal with a 15 s timeout of the request's own. syncCatalog's 15 s timeout (index.ts:51) is created before
// every request it bounds, and Node fires the timers of one delay in creation order, so in Pi the caller's signal is aborted before such a request's own timer can fire: a
// refresh that hangs is an aborted refresh ("Model catalog refresh timed out.", index.ts:60), never a refresh that failed with a request's timeout (models.ts:575, :639 and
// model-runtime.ts:925 read only the caller's signal). Both callers of fetchJSON, LlamaClient.request and HuggingFaceClient.request, therefore start a timer of their own
// only without a caller's deadline or under one that is due later, and the request then ends at 15 s with the timeout error. Under a caller's deadline due at the same
// instant or earlier the request is still pending at 15 s and ends with the caller's reason when the caller's signal fires.
func TestLlamaRequestLeavesTheTimeoutToACallerWhoseDeadlineIsDueNoLater(t *testing.T) {
	callers := []struct {
		name string
		call func(ctx context.Context) error
	}{
		{"LlamaClient.request", func(ctx context.Context) error {
			client, err := NewLlamaClient("http://llama.invalid", "")
			if err != nil {
				return err
			}
			_, err = client.List(ctx, false)
			return err
		}},
		{"HuggingFaceClient.request", func(ctx context.Context) error {
			_, err := NewHuggingFaceClient("", "http://hf.invalid").Search(ctx, "qwen")
			return err
		}},
	}
	deadlines := []struct {
		name        string
		hasDeadline bool
		later       time.Duration
		endedBy     string
	}{
		{"no caller deadline", false, 0, "the request's own timeout"},
		{"a caller deadline due a nanosecond later", true, time.Nanosecond, "the request's own timeout"},
		{"a caller deadline due at the same instant", true, 0, "the caller's signal"},
		{"a caller deadline due a nanosecond earlier", true, -time.Nanosecond, "the caller's signal"},
	}
	transport := http.DefaultClient.Transport
	http.DefaultClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	t.Cleanup(func() { http.DefaultClient.Transport = transport })
	for _, caller := range callers {
		for _, deadline := range deadlines {
			t.Run(caller.name+"/"+deadline.name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					signal, abort := context.WithCancelCause(context.Background())
					defer abort(nil)
					ctx := context.Context(signal)
					if deadline.hasDeadline {
						ctx = pendingDeadline{Context: signal, due: time.Now().Add(requestTimeout + deadline.later)}
					}
					result := make(chan error, 1)
					go func() { result <- caller.call(ctx) }()
					time.Sleep(requestTimeout - time.Nanosecond)
					synctest.Wait()
					select {
					case err := <-result:
						t.Fatalf("the request ended before 15 s: %v", err)
					default:
					}
					time.Sleep(time.Nanosecond)
					synctest.Wait()
					endedBy, text := "the caller's signal", "the caller's signal fired"
					select {
					case err := <-result:
						endedBy = "the request's own timeout"
						if !errors.Is(err, timeoutError{}) || err.Error() != "The operation was aborted due to timeout" {
							t.Fatalf("the request ended at 15 s with %v, want the timeout error", err)
						}
					default:
						abort(errors.New(text))
						synctest.Wait()
						select {
						case err := <-result:
							if err == nil || err.Error() != text {
								t.Fatalf("request error = %v, want the caller's reason %q", err, text)
							}
						default:
							t.Fatal("the request did not end when the caller's signal fired")
						}
					}
					if endedBy != deadline.endedBy {
						t.Fatalf("the request was ended by %s, want %s", endedBy, deadline.endedBy)
					}
				})
			})
		}
	}
}
