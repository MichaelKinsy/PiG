package tui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Tests of the launcher's credentials (slice 11 §4; fix round 1, Fable M2): a pairing answer the server sends is shown on the
// real terminal only when it is a plain code; anything else (an escape sequence) fails the pairing. Ports
// packages/chord/test/node/tui/credentials.test.ts.

// pairingServer answers every pairing request with answer.
func pairingServer(t *testing.T, answer any) *url.URL {
	t.Helper()
	text, err := jsstring.MarshalJSON(answer)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write(text)
	}))
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return base
}

func TestPairingRefusesACodeThatIsNoPlainCodeShowingNothing(t *testing.T) {
	for _, code := range []any{"\x1b]52;c;Y3VybA==\x07", "AB\r\nCD", strings.Repeat("x", 40), float64(7)} {
		base := pairingServer(t, map[string]any{"code": code, "poll": "p"})
		var said []string
		_, err := TokenOf(context.Background(), TerminalCredentials{Kind: CredentialsPairing, Label: "t"}, base, func(line string) { said = append(said, line) }, nil)
		if err == nil || !strings.Contains(err.Error(), "pairing answer is malformed") {
			t.Fatalf("code %v: error = %v, want a malformed pairing answer", code, err)
		}
		if len(said) != 0 {
			t.Fatalf("code %v: said %v, want nothing", code, said)
		}
	}
}

// pollingServer is a server pairing a device with the code ABCD whose poll answers `pending` after delay, noting when each poll
// request arrived; a poll arriving with its request's context ended is noted by the server as dropped.
type pollingServer struct {
	base    *url.URL
	mu      sync.Mutex
	arrived []time.Time
}

func newPollingServer(t *testing.T, delay time.Duration, release <-chan struct{}) *pollingServer {
	t.Helper()
	p := &pollingServer{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		if r.URL.Path == "/_chord/pair" {
			_, _ = w.Write([]byte(`{"code":"ABCD","poll":"p"}`))
			return
		}
		p.mu.Lock()
		p.arrived = append(p.arrived, time.Now())
		p.mu.Unlock()
		select {
		case <-time.After(delay):
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = w.Write([]byte(`{"status":"pending"}`))
	}))
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	p.base = base
	return p
}

func (p *pollingServer) polls() []time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]time.Time(nil), p.arrived...)
}

func waitUntil(t *testing.T, check func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !check(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("not within 10 s")
		}
	}
}

func setPollInterval(t *testing.T, interval time.Duration) {
	t.Helper()
	was := pollInterval
	pollInterval = interval
	t.Cleanup(func() { pollInterval = was })
}

// Pi's `pair` is `for (;;) { await sleep(POLL_MS); throwIfAborted(); await post(poll); ... }`: the wait is the whole interval after
// the poll before it was answered, so two polls start `POLL_MS` plus the first poll's own time apart (the interval is not counted from
// the poll's start).
func TestPairingWaitsTheWholeIntervalAfterEachPollWasAnswered(t *testing.T) {
	setPollInterval(t, 200*time.Millisecond)
	server := newPollingServer(t, 300*time.Millisecond, nil)
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	done := make(chan error, 1)
	go func() {
		_, err := TokenOf(ctx, TerminalCredentials{Kind: CredentialsPairing, Label: "t"}, server.base, func(string) {}, nil)
		done <- err
	}()
	waitUntil(t, func() bool { return len(server.polls()) >= 3 })
	stop := errors.New("test over")
	cancel(stop)
	if err := <-done; !errors.Is(err, stop) {
		t.Fatalf("TokenOf = %v, want the cancellation's cause", err)
	}
	polls := server.polls()
	for i := 1; i < 3; i++ {
		if gap := polls[i].Sub(polls[i-1]); gap < 490*time.Millisecond {
			t.Fatalf("poll %d started %v after the one before it was started, want at least 200ms (the interval) + 300ms (the poll's answer)", i+1, gap)
		}
	}
}

// Pi's wait is a bare setTimeout the signal does not cut short, then `signal.throwIfAborted()` before the next poll: an abort during
// the wait sends no poll, and rejects with the signal's reason (when the interval ends: TestAnAbortDuringThePairingWaitSettlesWhenTheIntervalEnds).
// An abort during a poll's request ends the request.
func TestAnAbortedPairingSendsNoMorePollsAndEndsWithTheCause(t *testing.T) {
	setPollInterval(t, 300*time.Millisecond)
	hold := make(chan struct{})
	defer close(hold)
	server := newPollingServer(t, time.Hour, hold)
	stop := errors.New("the caller left")
	ctx, cancel := context.WithCancelCause(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel(stop)
	}()
	if _, err := TokenOf(ctx, TerminalCredentials{Kind: CredentialsPairing, Label: "t"}, server.base, func(string) {}, nil); !errors.Is(err, stop) {
		t.Fatalf("an abort during the wait: %v, want its cause", err)
	}
	time.Sleep(450 * time.Millisecond)
	if n := len(server.polls()); n != 0 {
		t.Fatalf("%d polls after the abort, want none (Pi's throwIfAborted precedes the post)", n)
	}
	// An abort while a poll is in flight ends it.
	setPollInterval(t, 10*time.Millisecond)
	ctx, cancel = context.WithCancelCause(context.Background())
	go func() {
		for len(server.polls()) == 0 {
			time.Sleep(5 * time.Millisecond)
		}
		cancel(stop)
	}()
	if _, err := TokenOf(ctx, TerminalCredentials{Kind: CredentialsPairing, Label: "t"}, server.base, func(string) {}, nil); err == nil || !errors.Is(err, context.Canceled) && !errors.Is(err, stop) {
		t.Fatalf("an abort during a poll: %v, want the abort", err)
	}
}

// Pi's wait is a bare setTimeout the signal does not cut short (`credentials.ts` 52), then `signal.throwIfAborted()`: an abort during
// the wait settles `tokenOf` when the interval ends, not at the abort, with the signal's reason and no poll. (An abort during a poll's
// request ends the request at once: fetch takes the signal; TestAnAbortedPairingSendsNoMorePollsAndEndsWithTheCause.)
func TestAnAbortDuringThePairingWaitSettlesWhenTheIntervalEnds(t *testing.T) {
	setPollInterval(t, 400*time.Millisecond)
	server := newPollingServer(t, time.Hour, nil)
	stop := errors.New("the caller left")
	ctx, cancel := context.WithCancelCause(context.Background())
	var shown atomic.Int64
	go func() {
		for shown.Load() == 0 {
			time.Sleep(time.Millisecond)
		}
		time.Sleep(50 * time.Millisecond)
		cancel(stop)
	}()
	_, err := TokenOf(ctx, TerminalCredentials{Kind: CredentialsPairing, Label: "t"}, server.base, func(string) { shown.Store(time.Now().UnixNano()) }, nil)
	settled := time.Now()
	if !errors.Is(err, stop) {
		t.Fatalf("TokenOf = %v, want the cancellation's cause", err)
	}
	if wait := settled.Sub(time.Unix(0, shown.Load())); wait < 390*time.Millisecond {
		t.Fatalf("settled %v after the code was shown, want the whole 400ms interval (an abort at 50ms does not cut Pi's wait short)", wait)
	}
	if n := len(server.polls()); n != 0 {
		t.Fatalf("%d polls, want none", n)
	}
}
