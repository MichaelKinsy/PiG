package ai

// Pins the Anthropic SDK TokenCache that pi's federation client shares across requests:
// node_modules/@anthropic-ai/sdk@0.124.0 lib/credentials/token-cache.mjs:1-107 (two-tier proactive refresh,
// concurrent deduplication, invalidate after a 401).

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type tokenCacheClock struct{ now atomic.Int64 }

func (clock *tokenCacheClock) advance(seconds int64) { clock.now.Add(seconds) }

// newTokenCacheHarness installs a controllable clock (whole seconds) for anthropicFederationNow.
func newTokenCacheHarness(t *testing.T) *tokenCacheClock {
	t.Helper()
	clock := &tokenCacheClock{}
	clock.now.Store(1_800_000_000)
	previous := anthropicFederationNow
	anthropicFederationNow = func() time.Time { return time.Unix(clock.now.Load(), 0) }
	t.Cleanup(func() { anthropicFederationNow = previous })
	return clock
}

type tokenProviderCalls struct {
	mu     sync.Mutex
	calls  int
	forced []bool
	// expires is the lifetime in seconds of the nth issued token; nil means no expiry.
	expires func(n int) *int64
	// gate blocks the provider until it is closed or the context ends; nil does not block.
	gate chan struct{}
	// started receives the call number when a provider call begins.
	started chan int
	// fail makes the nth call fail.
	fail  func(n int) error
	clock *tokenCacheClock
}

func (p *tokenProviderCalls) provide(ctx context.Context, force bool) (anthropicAccessToken, error) {
	p.mu.Lock()
	n := p.calls
	p.calls++
	p.forced = append(p.forced, force)
	p.mu.Unlock()
	if p.started != nil {
		p.started <- n
	}
	if p.gate != nil {
		select {
		case <-p.gate:
		case <-ctx.Done():
			return anthropicAccessToken{}, context.Cause(ctx)
		}
	}
	if p.fail != nil {
		if err := p.fail(n); err != nil {
			return anthropicAccessToken{}, err
		}
	}
	token := anthropicAccessToken{Token: "token-" + string(rune('0'+n))}
	if p.expires != nil {
		if lifetime := p.expires(n); lifetime != nil {
			expiresAt := float64(p.clock.now.Load() + *lifetime)
			token.ExpiresAt = &expiresAt
		}
	}
	return token, nil
}

func (p *tokenProviderCalls) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func waitTokenCall(t *testing.T, started chan int, want int) {
	t.Helper()
	select {
	case got := <-started:
		if got != want {
			t.Fatalf("provider call %d started, want %d", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("provider call %d did not start", want)
	}
}

func TestAnthropicTokenCache(t *testing.T) {
	t.Run("the first token blocks and is then served from the cache", func(t *testing.T) {
		clock := newTokenCacheHarness(t)
		provider := &tokenProviderCalls{clock: clock, expires: func(int) *int64 { return new(int64(3600)) }}
		cache := newAnthropicTokenCache(provider.provide, nil)
		defer cache.Close()
		for range 3 {
			if token, err := cache.GetToken(t.Context()); err != nil || token != "token-0" {
				t.Fatalf("token = %q, err = %v", token, err)
			}
		}
		if provider.count() != 1 || provider.forced[0] {
			t.Fatalf("provider calls = %d, forced = %v", provider.count(), provider.forced)
		}
	})

	t.Run("a token without an expiry is cached forever", func(t *testing.T) {
		clock := newTokenCacheHarness(t)
		provider := &tokenProviderCalls{clock: clock}
		cache := newAnthropicTokenCache(provider.provide, nil)
		defer cache.Close()
		_, _ = cache.GetToken(t.Context())
		clock.advance(10 * 365 * 24 * 3600)
		if token, err := cache.GetToken(t.Context()); err != nil || token != "token-0" || provider.count() != 1 {
			t.Fatalf("token = %q, err = %v, calls = %d", token, err, provider.count())
		}
	})

	t.Run("more than 120 seconds left is served without a refresh", func(t *testing.T) {
		clock := newTokenCacheHarness(t)
		provider := &tokenProviderCalls{clock: clock, expires: func(int) *int64 { return new(int64(3600)) }}
		cache := newAnthropicTokenCache(provider.provide, nil)
		defer cache.Close()
		_, _ = cache.GetToken(t.Context())
		clock.advance(3600 - 121)
		if token, _ := cache.GetToken(t.Context()); token != "token-0" || provider.count() != 1 {
			t.Fatalf("token = %q, calls = %d", token, provider.count())
		}
	})

	t.Run("the advisory window serves the stale token and refreshes in the background", func(t *testing.T) {
		clock := newTokenCacheHarness(t)
		provider := &tokenProviderCalls{clock: clock, expires: func(int) *int64 { return new(int64(3600)) }, started: make(chan int, 4)}
		cache := newAnthropicTokenCache(provider.provide, nil)
		defer cache.Close()
		_, _ = cache.GetToken(t.Context())
		waitTokenCall(t, provider.started, 0)
		provider.gate = make(chan struct{})
		clock.advance(3600 - 60)
		if token, err := cache.GetToken(t.Context()); err != nil || token != "token-0" {
			t.Fatalf("stale token = %q, err = %v", token, err)
		}
		waitTokenCall(t, provider.started, 1)
		// A second call during the refresh neither blocks nor starts another refresh.
		if token, _ := cache.GetToken(t.Context()); token != "token-0" {
			t.Fatalf("token during refresh = %q", token)
		}
		close(provider.gate)
		deadline := time.Now().Add(5 * time.Second)
		for {
			token, err := cache.GetToken(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if token == "token-1" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("token = %q, want the background refresh", token)
			}
			time.Sleep(time.Millisecond)
		}
		if provider.count() != 2 {
			t.Fatalf("provider calls = %d, want 2", provider.count())
		}
	})

	t.Run("an advisory refresh failure keeps the stale token and backs off five seconds", func(t *testing.T) {
		clock := newTokenCacheHarness(t)
		failure := errors.New("exchange down")
		provider := &tokenProviderCalls{clock: clock, expires: func(n int) *int64 { return new(int64(3600)) }, started: make(chan int, 8),
			fail: func(n int) error {
				if n > 0 {
					return failure
				}
				return nil
			}}
		reported := make(chan error, 8)
		cache := newAnthropicTokenCache(provider.provide, func(err error) { reported <- err })
		defer cache.Close()
		_, _ = cache.GetToken(t.Context())
		waitTokenCall(t, provider.started, 0)
		clock.advance(3600 - 60)
		if token, err := cache.GetToken(t.Context()); err != nil || token != "token-0" {
			t.Fatalf("stale token = %q, err = %v", token, err)
		}
		waitTokenCall(t, provider.started, 1)
		select {
		case err := <-reported:
			if !errors.Is(err, failure) {
				t.Fatalf("reported %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("advisory failure was not reported")
		}
		// Within the five second backoff no refresh starts.
		clock.advance(4)
		if token, _ := cache.GetToken(t.Context()); token != "token-0" {
			t.Fatalf("token = %q", token)
		}
		if provider.count() != 2 {
			t.Fatalf("provider calls = %d within the backoff, want 2", provider.count())
		}
		clock.advance(1)
		if token, _ := cache.GetToken(t.Context()); token != "token-0" {
			t.Fatalf("token = %q", token)
		}
		waitTokenCall(t, provider.started, 2)
	})

	t.Run("a mandatory refresh blocks and surfaces a failure", func(t *testing.T) {
		clock := newTokenCacheHarness(t)
		failure := errors.New("exchange down")
		provider := &tokenProviderCalls{clock: clock, expires: func(int) *int64 { return new(int64(3600)) },
			fail: func(n int) error {
				if n == 1 {
					return failure
				}
				return nil
			}}
		cache := newAnthropicTokenCache(provider.provide, nil)
		defer cache.Close()
		_, _ = cache.GetToken(t.Context())
		clock.advance(3600 - 29)
		if token, err := cache.GetToken(t.Context()); !errors.Is(err, failure) || token != "" {
			t.Fatalf("token = %q, err = %v, want the exchange failure", token, err)
		}
		// The failed refresh is not cached: the next call refreshes again and succeeds.
		if token, err := cache.GetToken(t.Context()); err != nil || token != "token-2" {
			t.Fatalf("token = %q, err = %v", token, err)
		}
	})

	t.Run("concurrent mandatory callers share one exchange", func(t *testing.T) {
		clock := newTokenCacheHarness(t)
		provider := &tokenProviderCalls{clock: clock, expires: func(int) *int64 { return new(int64(3600)) }, gate: make(chan struct{}), started: make(chan int, 4)}
		cache := newAnthropicTokenCache(provider.provide, nil)
		defer cache.Close()
		var wg sync.WaitGroup
		tokens := make([]string, 8)
		for i := range tokens {
			wg.Go(func() { tokens[i], _ = cache.GetToken(t.Context()) })
		}
		waitTokenCall(t, provider.started, 0)
		time.Sleep(20 * time.Millisecond)
		close(provider.gate)
		wg.Wait()
		for _, token := range tokens {
			if token != "token-0" {
				t.Fatalf("tokens = %v", tokens)
			}
		}
		if provider.count() != 1 {
			t.Fatalf("provider calls = %d, want 1", provider.count())
		}
	})

	t.Run("invalidate forces the next refresh and the provider sees the flag", func(t *testing.T) {
		clock := newTokenCacheHarness(t)
		provider := &tokenProviderCalls{clock: clock, expires: func(int) *int64 { return new(int64(3600)) }}
		cache := newAnthropicTokenCache(provider.provide, nil)
		defer cache.Close()
		_, _ = cache.GetToken(t.Context())
		cache.Invalidate()
		if token, err := cache.GetToken(t.Context()); err != nil || token != "token-1" {
			t.Fatalf("token = %q, err = %v", token, err)
		}
		if token, _ := cache.GetToken(t.Context()); token != "token-1" {
			t.Fatalf("token = %q, want the forced refresh cached", token)
		}
		if provider.count() != 2 || provider.forced[0] || !provider.forced[1] {
			t.Fatalf("calls = %d, forced = %v, want [false true]", provider.count(), provider.forced)
		}
	})

	t.Run("a forced refresh does not join an in-flight one", func(t *testing.T) {
		clock := newTokenCacheHarness(t)
		provider := &tokenProviderCalls{clock: clock, expires: func(int) *int64 { return new(int64(3600)) }, gate: make(chan struct{}), started: make(chan int, 4)}
		cache := newAnthropicTokenCache(provider.provide, nil)
		defer cache.Close()
		var wg sync.WaitGroup
		wg.Go(func() { _, _ = cache.GetToken(t.Context()) })
		waitTokenCall(t, provider.started, 0)
		cache.Invalidate()
		wg.Go(func() { _, _ = cache.GetToken(t.Context()) })
		waitTokenCall(t, provider.started, 1)
		close(provider.gate)
		wg.Wait()
		if provider.forced[0] || !provider.forced[1] {
			t.Fatalf("forced = %v, want [false true]", provider.forced)
		}
	})

	t.Run("an aborted waiter leaves the exchange running for the next caller", func(t *testing.T) {
		clock := newTokenCacheHarness(t)
		provider := &tokenProviderCalls{clock: clock, expires: func(int) *int64 { return new(int64(3600)) }, gate: make(chan struct{}), started: make(chan int, 4)}
		cache := newAnthropicTokenCache(provider.provide, nil)
		defer cache.Close()
		ctx, cancel := context.WithCancel(t.Context())
		result := make(chan error, 1)
		go func() { _, err := cache.GetToken(ctx); result <- err }()
		waitTokenCall(t, provider.started, 0)
		cancel()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		close(provider.gate)
		if token, err := cache.GetToken(t.Context()); err != nil || token != "token-0" || provider.count() != 1 {
			t.Fatalf("token = %q, err = %v, calls = %d; want the original exchange", token, err, provider.count())
		}
	})

	t.Run("a context that ended before the call fails without an exchange", func(t *testing.T) {
		clock := newTokenCacheHarness(t)
		provider := &tokenProviderCalls{clock: clock}
		cache := newAnthropicTokenCache(provider.provide, nil)
		defer cache.Close()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := cache.GetToken(ctx); !errors.Is(err, context.Canceled) || provider.count() != 0 {
			t.Fatalf("err = %v, calls = %d", err, provider.count())
		}
	})

	t.Run("close cancels an in-flight exchange and waits for it", func(t *testing.T) {
		clock := newTokenCacheHarness(t)
		provider := &tokenProviderCalls{clock: clock, gate: make(chan struct{}), started: make(chan int, 4)}
		cache := newAnthropicTokenCache(provider.provide, nil)
		result := make(chan error, 1)
		go func() { _, err := cache.GetToken(t.Context()); result <- err }()
		waitTokenCall(t, provider.started, 0)
		closed := make(chan struct{})
		go func() { cache.Close(); close(closed) }()
		select {
		case <-closed:
		case <-time.After(5 * time.Second):
			t.Fatal("Close did not return")
		}
		if err := <-result; err == nil {
			t.Fatal("GetToken succeeded after Close cancelled its exchange")
		}
	})
}

// oidc-federation.mjs:59-66 keeps expires_in as a JavaScript number: expiresAt = nowAsSeconds() + expiresIn is
// fractional and never overflows, so token-cache.mjs:39-44 compares the exact remaining lifetime.
func TestAnthropicTokenCacheKeepsTheExactExpiresIn(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		calls      int
	}{
		// 120.5 s remain: more than the 120 s advisory threshold, so the token is served without a refresh.
		{name: "a fractional lifetime above the advisory threshold", body: `{"access_token":"a","expires_in":120.5}`, calls: 1},
		// A lifetime beyond int64 seconds is cached as a long-lived token.
		{name: "a lifetime beyond int64 seconds", body: `{"access_token":"a","expires_in":1e300}`, calls: 1},
		// 30.5 s remain: inside the advisory window, so one background refresh runs.
		{name: "a fractional lifetime inside the advisory window", body: `{"access_token":"a","expires_in":30.5}`, calls: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newTokenCacheHarness(t)
			var calls atomic.Int64
			cache := newAnthropicTokenCache(func(context.Context, bool) (anthropicAccessToken, error) {
				calls.Add(1)
				return parseAnthropicTokenResponse(http.StatusOK, tc.body)
			}, nil)
			for range 2 {
				if token, err := cache.GetToken(t.Context()); err != nil || token != "a" {
					t.Fatalf("token = %q, err = %v", token, err)
				}
			}
			cache.Close()
			if got := calls.Load(); got != int64(tc.calls) {
				t.Fatalf("provider calls = %d, want %d", got, tc.calls)
			}
		})
	}
}
