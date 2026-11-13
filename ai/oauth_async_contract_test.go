package ai

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// joinGate is a manual prompt that, once its context ends, stays inside the prompt until the test lets it return. A login that returns while the
// gate is closed has not joined its manual prompt.
type joinGate struct {
	onStart                         func(context.Context)
	started, ended, release         chan struct{}
	startOnce, endOnce, releaseOnce sync.Once
	returned                        atomic.Bool
}

func newJoinGate(t *testing.T, onStart func(context.Context)) *joinGate {
	gate := &joinGate{onStart: onStart, started: make(chan struct{}), ended: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(gate.open)
	return gate
}

func (gate *joinGate) open() { gate.releaseOnce.Do(func() { close(gate.release) }) }

func (gate *joinGate) prompt(ctx context.Context) (string, error) {
	gate.startOnce.Do(func() { close(gate.started) })
	if gate.onStart != nil {
		gate.onStart(ctx)
	}
	<-ctx.Done()
	gate.endOnce.Do(func() { close(gate.ended) })
	<-gate.release
	gate.returned.Store(true)
	return "", errors.New("manual prompt ended")
}

// joinProbeWaiter runs fn on a goroutine that blockedInManualPromptJoin can find by name.
func joinProbeWaiter(fn func(), done chan<- struct{}) {
	defer close(done)
	fn()
}

// blockedInManualPromptJoin reports whether the goroutine running joinProbeWaiter is parked on a channel receive inside WaitForCallbackOrManualInput.
func blockedInManualPromptJoin() bool {
	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	for goroutine := range strings.SplitSeq(string(buf), "\n\n") {
		header, _, _ := strings.Cut(goroutine, "\n")
		if strings.Contains(header, "[chan receive") && strings.Contains(goroutine, "joinProbeWaiter(") && strings.Contains(goroutine, "WaitForCallbackOrManualInput") {
			return true
		}
	}
	return false
}

// requireJoined waits until the prompt has seen its context end, then requires the wait under test to be parked joining it (not returned), and finally lets the prompt return.
func (gate *joinGate) requireJoined(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-gate.ended:
	case <-done:
		t.Fatal("the wait returned before the manual prompt's context ended")
	}
	for !blockedInManualPromptJoin() {
		select {
		case <-done:
			t.Fatal("the wait returned while the manual prompt had not returned")
		default:
			runtime.Gosched()
		}
	}
	gate.open()
	<-done
	if !gate.returned.Load() {
		t.Fatal("the wait returned before the manual prompt did")
	}
}

func getCallback(t *testing.T, ctx context.Context, target string) {
	// The page is awaited past the end of the login that its own callback ends, so the request outlives the prompt's context.
	request, err := http.NewRequestWithContext(context.WithoutCancel(ctx), http.MethodGet, target, nil)
	if err != nil {
		t.Error(err)
		return
	}
	response, err := oauthNativeClient.Do(request)
	if err != nil {
		t.Error(err)
		return
	}
	_ = response.Body.Close()
}

// callback-server.ts waitForCallbackOrManualInput aborts the manual prompt in finally. Go joins the prompt as well, so no prompt outlives its login: whatever
// ends the wait (the browser callback, the cancelled login), it returns only after the manual prompt has returned, and the prompt's own failure after that
// abort is not the wait's failure (manualError is read before the abort, and the abort only follows the return in upstream).
func TestWaitForCallbackOrManualInputReturnsOnlyAfterTheManualPromptReturned(t *testing.T) {
	type outcome struct {
		result OAuthCallbackOrManualInput[string]
		err    error
	}
	interaction := func(gate *joinGate) AuthInteraction {
		return AuthInteraction{Prompt: func(ctx context.Context, _ AuthPrompt) (string, error) { return gate.prompt(ctx) }}
	}
	t.Run("the browser callback wins", func(t *testing.T) {
		server := startTestCallbackServer(t, t.Context(), stringCallbackOptions(nil))
		gate := newJoinGate(t, func(ctx context.Context) {
			getCallback(t, ctx, oauthCallbackURL(t, server.RedirectURI, "code", "from-browser"))
		})
		var got outcome
		done := make(chan struct{})
		go joinProbeWaiter(func() {
			got.result, got.err = WaitForCallbackOrManualInput(t.Context(), interaction(gate), server, AuthManualCodePrompt{Message: "paste"})
		}, done)
		gate.requireJoined(t, done)
		if got.err != nil || got.result != (OAuthCallbackOrManualInput[string]{Callback: true, Value: "completed:from-browser"}) {
			t.Fatalf("result = %+v, err = %v", got.result, got.err)
		}
	})
	t.Run("the login is cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		server := startTestCallbackServer(t, ctx, stringCallbackOptions(nil))
		gate := newJoinGate(t, nil)
		var got outcome
		done := make(chan struct{})
		go joinProbeWaiter(func() {
			got.result, got.err = WaitForCallbackOrManualInput(ctx, interaction(gate), server, AuthManualCodePrompt{Message: "paste"})
		}, done)
		<-gate.started
		cancel()
		gate.requireJoined(t, done)
		if got.err == nil || got.err.Error() != "Login cancelled" {
			t.Fatalf("err = %v, want Login cancelled", got.err)
		}
	})
	// Without a callback server the manual prompt is the only thing awaited: the wait returns its answer, or its failure, and not before.
	for name, answer := range map[string]struct {
		input string
		err   error
	}{"an answer": {input: "pasted"}, "a failure": {err: errors.New("prompt failed")}} {
		t.Run("without a callback server "+name, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var got outcome
			done := make(chan struct{})
			go joinProbeWaiter(func() {
				got.result, got.err = WaitForCallbackOrManualInput[string](t.Context(), AuthInteraction{Prompt: func(context.Context, AuthPrompt) (string, error) {
					close(started)
					<-release
					return answer.input, answer.err
				}}, nil, AuthManualCodePrompt{Message: "paste"})
			}, done)
			<-started
			for !blockedInManualPromptJoin() {
				select {
				case <-done:
					t.Fatal("the wait returned before the manual prompt answered")
				default:
					runtime.Gosched()
				}
			}
			close(release)
			<-done
			if (answer.err == nil) != (got.err == nil) || (answer.err != nil && got.err.Error() != answer.err.Error()) || got.result.Input != answer.input || got.result.Callback {
				t.Fatalf("result = %+v, err = %v; want input %q, err %v", got.result, got.err, answer.input, answer.err)
			}
		})
	}
}

// The login flows return the callback winner only after their manual prompt has returned (anthropic.ts and openrouter.ts finish with the prompt's controller aborted
// and nothing left of the prompt running).
func TestAnthropicOAuthLoginReturnsOnlyAfterTheManualPromptReturned(t *testing.T) {
	mockAnthropicOAuthToken(t, `{"access_token":"access","refresh_token":"refresh","expires_in":3600}`, func(*http.Request, map[string]string) {})
	var callbackURL string
	gate := newJoinGate(t, func(ctx context.Context) { getCallback(t, ctx, callbackURL) })
	var credential OAuthCredentials
	var err error
	done := make(chan struct{})
	go joinProbeWaiter(func() {
		credential, err = LoginAnthropic(t.Context(), OAuthLoginCallbacks{OnAuth: func(info OAuthAuthInfo) {
			parsed, parseErr := url.Parse(info.URL)
			if parseErr != nil {
				t.Error(parseErr)
				return
			}
			redirect, parseErr := url.Parse(parsed.Query().Get("redirect_uri"))
			if parseErr != nil {
				t.Error(parseErr)
				return
			}
			redirect.Host = net.JoinHostPort(os.Getenv("PI_OAUTH_CALLBACK_HOST"), "53692")
			callbackURL = redirect.String() + "?code=callback-code&state=" + url.QueryEscape(parsed.Query().Get("state"))
		}, OnManualCodeInputContext: gate.prompt})
	}, done)
	gate.requireJoined(t, done)
	if err != nil || credential.Access != "access" {
		t.Fatalf("credential = %+v, err = %v", credential, err)
	}
}

func TestOpenRouterOAuthLoginReturnsOnlyAfterTheManualPromptReturned(t *testing.T) {
	withOpenRouterToken(t, func(*http.Request) (*http.Response, error) { return cannedResp(200, `{"key":"key-for-BROWSER"}`), nil })
	var callbackURL string
	gate := newJoinGate(t, func(ctx context.Context) { getCallback(t, ctx, callbackURL) })
	var credential OAuthCredentials
	var err error
	done := make(chan struct{})
	go joinProbeWaiter(func() {
		credential, err = LoginOpenRouter(t.Context(), OAuthLoginCallbacks{OnAuth: func(info OAuthAuthInfo) {
			parsed, parseErr := url.Parse(info.URL)
			if parseErr != nil {
				t.Error(parseErr)
				return
			}
			callbackURL = parsed.Query().Get("callback_url") + "?code=BROWSER"
		}, OnManualCodeInputContext: gate.prompt})
	}, done)
	gate.requireJoined(t, done)
	if err != nil || credential.Access != "key-for-BROWSER" {
		t.Fatalf("credential = %+v, err = %v", credential, err)
	}
}

// device-code.ts abortableSleep rejects with the cancel message when the signal aborts while the poll loop sleeps, and the loop never goes on to the deadline check:
// a login cancelled during the last sleep is "Login cancelled", not a timeout. The real sleep runs here; the clock is at the deadline and the signal aborts as it starts.
func TestPollDeviceCodeSleepCancelledByTheSignalIsLoginCancelledNotATimeout(t *testing.T) {
	for _, waitBeforeFirstPoll := range []bool{false, true} {
		name := "between polls"
		if waitBeforeFirstPoll {
			name = "before the first poll"
		}
		t.Run(name, func(t *testing.T) {
			now := 0.0
			previousNow, previousSleep := deviceCodeNow, deviceCodeSleep
			t.Cleanup(func() { deviceCodeNow, deviceCodeSleep = previousNow, previousSleep })
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			deviceCodeNow = func() float64 { return now }
			deviceCodeSleep = func(ctx context.Context, ms float64) error {
				now = 1e9
				cancel()
				return abortableDeviceSleepMs(ctx, ms)
			}
			polls := 0
			_, err := PollOAuthDeviceCodeFlow(ctx, DeviceCodePollOptions[string]{
				IntervalSeconds:     new(float64(5)),
				ExpiresInSeconds:    new(float64(30)),
				WaitBeforeFirstPoll: waitBeforeFirstPoll,
				Poll: func() (DeviceCodePollResult[string], error) {
					polls++
					return DeviceCodePollResult[string]{Status: DevicePollPending}, nil
				},
			})
			if err == nil || err.Error() != "Login cancelled" {
				t.Fatalf("err = %v, want Login cancelled", err)
			}
			if want := map[bool]int{false: 1, true: 0}[waitBeforeFirstPoll]; polls != want {
				t.Fatalf("polls = %d, want %d", polls, want)
			}
		})
	}
}

// abortableSleep resolves when the timer fires and rejects with the cancel message as soon as the signal aborts, however long the timer is.
func TestAbortableDeviceSleepSettlesWithTheTimerOrTheCancelMessage(t *testing.T) {
	if err := abortableDeviceSleepMs(t.Context(), 1); err != nil {
		t.Fatalf("a sleep that ran to its end = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	settled := make(chan error, 1)
	go func() { settled <- abortableDeviceSleepMs(ctx, 3.6e6) }()
	cancel()
	if err := <-settled; err == nil || err.Error() != "Login cancelled" {
		t.Fatalf("a sleep cancelled while it waited = %v, want Login cancelled", err)
	}
	if err := abortableDeviceSleepMs(ctx, 3.6e6); err == nil || err.Error() != "Login cancelled" {
		t.Fatalf("a sleep on an aborted signal = %v, want Login cancelled", err)
	}
}

// retry.ts sleep rejects before it arms a timer when the signal is already aborted, whatever the delay: a timer that is already due does not win the race.
func TestRetrySleepOnAnAbortedSignalIsAlwaysAborted(t *testing.T) {
	for _, delay := range []time.Duration{-time.Second, 0, time.Nanosecond} {
		for range 200 {
			if err := sleepContext(canceledContext(), delay); !errors.Is(err, errRetrySleepAborted) {
				t.Fatalf("sleepContext(aborted, %v) = %v, want the Aborted error", delay, err)
			}
		}
	}
}

// openrouter.ts exchangeAuthorizationCode throws "Login cancelled" before it makes the request when the signal is already aborted, and an abort wins over the
// timeout when the request fails after it.
func TestOpenRouterExchangeIsCancelledBeforeAndDuringTheRequest(t *testing.T) {
	t.Run("already aborted: no request", func(t *testing.T) {
		var requests atomic.Int32
		withOpenRouterToken(t, func(*http.Request) (*http.Response, error) {
			requests.Add(1)
			return cannedResp(200, `{"key":"k"}`), nil
		})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := exchangeOpenRouterCode(ctx, "code", "verifier"); err == nil || err.Error() != "Login cancelled" {
			t.Fatalf("err = %v, want Login cancelled", err)
		}
		if requests.Load() != 0 {
			t.Fatalf("%d requests after the login was cancelled", requests.Load())
		}
	})
	t.Run("aborted while the request is in flight", func(t *testing.T) {
		entered := make(chan struct{})
		withOpenRouterToken(t, func(r *http.Request) (*http.Response, error) {
			close(entered)
			<-r.Context().Done()
			return nil, r.Context().Err()
		})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		errs := make(chan error, 1)
		go func() { _, err := exchangeOpenRouterCode(ctx, "code", "verifier"); errs <- err }()
		<-entered
		cancel()
		if err := <-errs; err == nil || err.Error() != "Login cancelled" {
			t.Fatalf("err = %v, want Login cancelled", err)
		}
	})
}

// radius.ts requestOAuthToken and requestDeviceAuthorization turn a fetch that fails after the signal aborted into "Login cancelled" (not the fetch's abort error),
// whichever gateway request was in flight.
func TestRadiusOAuthLoginCancelledWhileTheGatewayHasNotAnswered(t *testing.T) {
	for _, stalled := range []string{"/v1/oauth/device", "/v1/oauth/token"} {
		t.Run(stalled, func(t *testing.T) {
			var enteredOnce sync.Once
			entered := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				// The body is read first: the server notices a dropped connection only once it has.
				_, _ = io.Copy(io.Discard, request.Body)
				if request.URL.Path == stalled {
					enteredOnce.Do(func() { close(entered) })
					<-request.Context().Done()
					return
				}
				writeRadiusJSON(writer, 200, map[string]any{"device_code": "d", "user_code": "u", "verification_uri": "https://v", "expires_in": 60})
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			errs := make(chan error, 1)
			go func() {
				_, err := newTestRadiusOAuth(server.URL, time.Now()).LoginContext(ctx, OAuthLoginCallbacks{OnSelect: selectRadiusMethod(RadiusLoginMethodDeviceCode)})
				errs <- err
			}()
			<-entered
			cancel()
			if err := <-errs; err == nil || err.Error() != "Login cancelled" {
				t.Fatalf("err = %v, want Login cancelled", err)
			}
		})
	}
}

// radius.ts loginWithBrowser closes the callback server in finally: when the login returns, the loopback port is free again, whether the callback completed the
// sign-in or the exchange failed.
func TestRadiusOAuthBrowserLoginReleasesTheLoopbackPortBeforeReturning(t *testing.T) {
	for _, tokenStatus := range []int{200, 400} {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			switch request.URL.Path {
			case "/v1/oauth":
				writeRadiusJSON(writer, 200, map[string]any{"authorizationEndpoint": "http://" + request.Host + "/authorize"})
			default:
				if tokenStatus == 200 {
					writeRadiusJSON(writer, 200, map[string]any{"access_token": "access", "refresh_token": "refresh", "expires_in": 3600})
				} else {
					writeRadiusJSON(writer, tokenStatus, map[string]any{"error": "invalid_grant", "error_description": "code expired"})
				}
			}
		}))
		oauth := newTestRadiusOAuth(server.URL, time.Now())
		oauth.callbackAddress = freeLoopbackAddress(t)
		pages := make(chan struct{})
		_, err := oauth.LoginContext(t.Context(), OAuthLoginCallbacks{
			OnSelect: selectRadiusMethod(RadiusLoginMethodBrowser),
			OnAuth: func(info OAuthAuthInfo) {
				authorize, parseErr := url.Parse(info.URL)
				if parseErr != nil {
					t.Error(parseErr)
					close(pages)
					return
				}
				callback := authorize.Query().Get("redirect_uri") + "?code=browser-code&state=" + url.QueryEscape(authorize.Query().Get("state"))
				go func() {
					defer close(pages)
					response, getErr := oauthNativeClient.Get(callback)
					if getErr != nil {
						t.Error(getErr)
						return
					}
					_, _ = io.Copy(io.Discard, response.Body)
					_ = response.Body.Close()
				}()
			},
		})
		<-pages
		server.Close()
		if (tokenStatus == 200) != (err == nil) {
			t.Fatalf("status %d: err = %v", tokenStatus, err)
		}
		listener, listenErr := net.Listen("tcp", oauth.callbackAddress)
		if listenErr != nil {
			t.Fatalf("status %d: the loopback port is still held after the login returned: %v", tokenStatus, listenErr)
		}
		_ = listener.Close()
	}
}
