// Ports packages/coding-agent/src/core/model-runtime.ts
package codingagent

import (
	"context"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
)

type backgroundRefreshKey struct{}

// backgroundRefresh reports whether ctx belongs to the deferred registration pass, which never notifies listeners on its own task and never yields to itself.
func backgroundRefresh(ctx context.Context) bool {
	background, _ := ctx.Value(backgroundRefreshKey{}).(bool)
	return background
}

// registrationRefresh is the queue of Pi's unawaited `void this.refresh({ allowNetwork: false })` (model-runtime.ts:750,788,796).
// In Pi that call is a continuation on the single JavaScript thread. It cannot pass its first await before the registering caller yields, and it never runs concurrently with provider callbacks or host code the caller runs before then. It runs on a yield of the caller, which is any awaited model-runtime call other than a provider request.
// Go has no implicit yield, so the queue is deferred work that starts only inside YieldToRegistrationRefresh or StartRegistrationRefresh, on the Services-owned task group. A yielding caller that covers every provider waits for it. Registrations that queue before a pass starts coalesce into one pass. Pi's earlier passes are superseded by the newest one through its availability generations and provider supersession, so they publish nothing, and the newest pass reaches the same state. A registration that queues while a pass runs starts its own concurrent pass at the next yield, and that pass supersedes the running one, as Pi's separate refreshes do.
// The queue keeps the providers whose registration or removal it holds, so a provider-scoped refresh can cover them (see coverRegistrationRefresh).
type registrationRefresh struct {
	mu        sync.Mutex
	providers map[string]struct{}
	holds     int
}

// deferRegistrationRefresh queues the local refresh of a registration or removal. It performs no work.
// pig divergence (D84): a direct Go registration starts no refresh; the caller's next awaited model-runtime call or an extension host callback starts it.
func (r *ModelRegistry) deferRegistrationRefresh(providerID string) {
	state := &r.registrationRefresh
	state.mu.Lock()
	if state.providers == nil {
		state.providers = map[string]struct{}{}
	}
	state.providers[providerID] = struct{}{}
	state.mu.Unlock()
}

// YieldToRegistrationRefresh runs the queued registration refresh before the caller's own awaited operation, as Pi's earlier-queued continuation runs before a later one, and waits for it. Only a caller whose operation covers every provider waits: Pi's refresh over every provider awaits each provider's callbacks, so its caller would wait for the same ones. The caller stops waiting when ctx ends, and a caller whose ctx has already ended only starts the pass; the pass then continues under the Services lifetime, and Services.Close cancels and drains it. A caller that finds nothing queued returns at once, even while a pass runs, so a provider callback that calls back into the runtime cannot deadlock the pass. The pass itself never yields.
// Ports packages/coding-agent/src/core/model-runtime.ts:registerNativeProvider,registerProvider,unregisterProvider.
func (r *ModelRegistry) YieldToRegistrationRefresh(ctx context.Context) {
	if ctx == nil {
		return
	}
	done := r.launchRegistrationRefresh(ctx, false)
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// StartRegistrationRefresh starts the queued registration refresh, unless a hold is active, and does not wait for it. A caller whose operation is scoped to some providers and does not itself refresh their catalogs uses it (availability of one provider, its auth check, its login): Pi launches the registration refresh unawaited (model-runtime.ts:750,788,796), and those calls select only their own providers, so they never wait for another provider's callbacks. Services.Close cancels and drains the pass.
// Ports packages/coding-agent/src/core/model-runtime.ts:registerNativeProvider,registerProvider,unregisterProvider.
func (r *ModelRegistry) StartRegistrationRefresh(ctx context.Context) {
	if ctx == nil {
		return
	}
	r.launchRegistrationRefresh(ctx, true)
}

// coverRegistrationRefresh removes the selected providers from the queue for a provider-scoped refresh that is about to refresh exactly those providers, and leaves the others queued. The scoped refresh reloads models.json, recomposes the selected providers and supersedes their in-flight refresh (model-runtime.ts:701-739), which is everything the queued pass would do for them, and Pi's refresh over some providers never awaits another provider's callbacks (models.ts:398-449). A concurrent pass would begin the same providers' refresh at an unrelated moment and could supersede the caller's own, where Pi's earlier-queued pass always begins first. The others stay queued for the next yield.
func (r *ModelRegistry) coverRegistrationRefresh(providers []string) (covered []string) {
	state := &r.registrationRefresh
	state.mu.Lock()
	for _, id := range providers {
		if _, queued := state.providers[id]; queued {
			delete(state.providers, id)
			covered = append(covered, id)
		}
	}
	state.mu.Unlock()
	return covered
}

// uncoverRegistrationRefresh returns covered providers to the queue and starts the queued pass. A provider-scoped refresh or credential operation calls it when it was aborted or never refreshed the providers: it then published no availability for them, while Pi's registration refresh takes no signal and still runs to completion (model-runtime.ts:750,788,796).
func (r *ModelRegistry) uncoverRegistrationRefresh(covered []string) {
	if len(covered) == 0 {
		return
	}
	for _, id := range covered {
		r.deferRegistrationRefresh(id)
	}
	r.StartRegistrationRefresh(context.Background())
}

// yieldForRefresh yields for a caller that refreshes every provider (nil selection) and covers the selected providers of a provider-scoped refresh. It returns the covered providers, which the caller passes to uncoverRegistrationRefresh when its refresh is aborted.
func (r *ModelRegistry) yieldForRefresh(ctx context.Context, providers []string) (covered []string) {
	if providers == nil {
		r.YieldToRegistrationRefresh(ctx)
		return nil
	}
	return r.coverRegistrationRefresh(providers)
}

// HoldRegistrationRefresh keeps StartRegistrationRefresh from starting the queued pass until the returned release function has run for every hold; the registrations only queue. A caller that loads several extension factories holds while it loads, as Pi queues their provider registrations and flushes them only after every factory has finished (agent-session-services.ts:158-182; the reload path's bindCore flush). An awaited yield ignores holds, because the awaiting caller has ended its turn. Releasing the last hold starts the queued pass. Release is idempotent.
func (r *ModelRegistry) HoldRegistrationRefresh() (release func()) {
	state := &r.registrationRefresh
	state.mu.Lock()
	state.holds++
	state.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			state.mu.Lock()
			state.holds--
			last := state.holds == 0
			state.mu.Unlock()
			if last {
				r.StartRegistrationRefresh(context.Background())
			}
		})
	}
}

// launchRegistrationRefresh starts the queued pass on the Services task owner and returns a channel closed when it ends. It returns nil when nothing is queued, the caller is the pass itself, a hold applies to a non-awaiting caller, or Services no longer admits work.
func (r *ModelRegistry) launchRegistrationRefresh(ctx context.Context, respectHold bool) <-chan struct{} {
	if backgroundRefresh(ctx) {
		return nil
	}
	state := &r.registrationRefresh
	state.mu.Lock()
	pending := len(state.providers) > 0 && (!respectHold || state.holds == 0)
	if pending {
		clear(state.providers)
	}
	state.mu.Unlock()
	if !pending {
		return nil
	}
	done := make(chan struct{})
	if !r.StartModelTask(context.Background(), func(task context.Context) {
		defer close(done)
		r.runRegistrationRefresh(task)
	}) {
		return nil
	}
	return done
}

// runRegistrationRefresh is the local refresh Pi starts after a registration: no network and no signal. Like every refresh it then notifies listeners, but on goroutines the listeners own, so Services.Close never waits for listener code.
func (r *ModelRegistry) runRegistrationRefresh(ctx context.Context) {
	r.refreshModelRuntime(context.WithValue(ctx, backgroundRefreshKey{}, true), ai.ModelsRefreshOptions{AllowNetwork: new(false)})
	r.mu.RLock()
	listener := r.onChange
	observers := slices.Clone(r.observers)
	r.mu.RUnlock()
	for _, observer := range observers {
		observer.publishAsync()
	}
	listener.publishAsync()
}
