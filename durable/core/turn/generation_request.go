// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package turn

import (
	"errors"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// request is the request phase: it clears a stale partial and marks the attempt, runs the beforeRequest hooks, streams
// the response and classifies it.
func (r *run) request(cp *jv.Object) {
	h := r.g.Host
	attempt := num(cp, "attempt")
	compacted := idField(cp, "compacted")
	ref := modelRefOf(objField(cp, "model"))
	thinking := str(cp, "thinkingLevel")
	cutoff := idField(cp, "cutoff")
	r.commit(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
		live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
		if err != nil {
			return nil, err
		}
		if err := convertPartial(tx, live, r.conv); err != nil {
			return nil, err
		}
		return nil, live.Set("generation", jobj("attempt", attempt))
	}, func() {
		if _, ok := h.ContextWindow(ref); !ok {
			r.failNoModel(&ref)
			return
		}
		r.requestContext(cutoff, func(view RequestContext) {
			var replaced []*jv.Object
			// The hooks see every message; they are parsed only when a handler is registered, and each handler sees the messages as the one before replaced them.
			var messages []*jv.Object
			parsed := false
			r.eachHook("beforeRequest", func() *jv.Object {
				if !parsed {
					messages, parsed = view.Messages(), true
				}
				return jobj("messages", toAny(messages))
			}, func(result any) bool {
				if o, ok := result.(*jv.Object); ok {
					if list, ok := arrayField(o, "messages"); ok {
						replaced = objectsOf(list)
						messages = replaced
					}
				}
				return false
			}, func(err error) {
				if err != nil {
					r.fail(err)
					return
				}
				r.provider(func(sessionID string, err error) {
					if err != nil {
						r.fail(err)
						return
					}
					options := streamOrEmpty(objField(cp, "streamOptions"))
					options.Set("sessionId", sessionID)
					if thinking != "off" {
						options.Set("reasoning", thinking)
					}
					req := &StreamRequest{Conversation: r.conv, Model: ref, Context: view, Replaced: replaced, Options: options}
					r.streamResponse(req, attempt, func(message *jv.Object, err error) {
						if err != nil {
							r.fail(err)
							return
						}
						r.classify(requestInfo{attempt: attempt, compacted: compacted, model: ref, cutoff: cutoff, messages: view.SystemMessages()}, message)
					})
				})
			})
		})
	})
}

func toAny(list []*jv.Object) []any {
	out := make([]any, len(list))
	for i, o := range list {
		out[i] = o
	}
	return out
}

func objectsOf(list []any) []*jv.Object {
	out := make([]*jv.Object, 0, len(list))
	for _, v := range list {
		if o, ok := v.(*jv.Object); ok {
			out = append(out, o)
		}
	}
	return out
}

// eachHook is runtime.hooks.each (scheduler.ts): it calls every registered handler of the hook in order with a payload,
// passing each result to handle; handle returns true to stop early. A handler that throws is reported and the next one
// runs, unless the invocation was aborted, which ends the walk with that error. then runs after the last handler.
func (r *run) eachHook(name string, payload func() *jv.Object, handle func(result any) (stop bool), then func(error)) {
	r.eachHookRaw(name, payload, func(result any, err error) (bool, error) {
		if err != nil {
			if r.rt.Signal().Aborted() || errors.Is(err, session.ErrAborted) {
				return true, err
			}
			_ = r.rt.Report(err, session.ReportFrom{Extension: r.hookExtension(name)})
			return false, nil
		}
		return handle(result), nil
	}, then)
}

// hookExtension is the extension of the handler eachHookRaw is calling.
func (r *run) hookExtension(name string) string {
	if h := r.g.Host; h.HookExtension != nil {
		return h.HookExtension(name, r.hookIndex, r.conv)
	}
	return ""
}

// eachHookRaw walks the handlers like eachHook but hands a handler's error to handle, for hooks whose callback decides
// what a throw means (beforeTool blocks the call). handle returns whether to stop the walk and the error then ends
// with (nil: the walk ended normally).
func (r *run) eachHookRaw(name string, payload func() *jv.Object, handle func(result any, err error) (stop bool, fail error), then func(error)) {
	h := r.g.Host
	n := 0
	// hooks.each is an async function: its caller's `await` resumes one tick after it returns, or after it throws.
	end := func(err error) { r.hops(1, func() { then(err) }) }
	// The caller's invoke callback is an async arrow that awaits the handler (the host's answer) and settles a tick before
	// hooks.each's `await invoke(...)` resumes; a plain callback hands back the handler's promise itself.
	settles := asyncInvoke(name)
	var next func(i int)
	next = func(i int) {
		if i >= n {
			end(nil)
			return
		}
		r.hookIndex = i
		h.Hook(name, i, r.conv, r.rt.TaskID(), payload(), func(result any, err error) {
			stop, fail := handle(result, err)
			cont := func() {
				if stop {
					end(fail)
					return
				}
				next(i + 1)
			}
			if settles {
				r.hops(invokeSettleHops, cont)
				return
			}
			cont()
		})
	}
	if h.Hooks == nil {
		n = 0
		then(nil)
		return
	}
	if r.agentSeen && h.Hooks(name, r.conv) == 0 {
		// hooks.each walks the handlers of the phase's resolved agent (scheduler.ts :1973 agentHooks(await agent())), which does not
		// change once resolved: with none the walk is `await agent()` of a settled resolution (one tick) and the caller's await of
		// the async function (one tick), nothing between them to run, so one chain carries both.
		r.hops(eachHookNoHandlerHops, func() { then(nil) })
		return
	}
	r.withAgent(func() {
		n = h.Hooks(name, r.conv)
		next(0)
	})
}

// invokeSettleHops is the hop between an async invoke callback's settlement and hooks.each resuming from `await invoke(...)`.
const invokeSettleHops = 1

// asyncInvoke reports whether Pi's caller of hooks.each(name, invoke) passes an async arrow that awaits the handler
// (tool.ts beforeTool and afterTool, generation.ts beforeRequest and onYield, compaction.ts beforeCompact), rather than one
// that returns the handler's promise (afterResponse, afterTools).
func asyncInvoke(name string) bool {
	switch name {
	case "beforeTool", "afterTool", "beforeRequest", "onYield", "beforeCompact":
		return true
	}
	return false
}

// provider is ensureProviderSessionId: the persisted identity without writing in the normal path; a legacy conversation
// without pi.provider gets one migration commit before the provider request starts.
func (r *run) provider(then func(sessionID string, err error)) {
	// ensureProviderSessionId reads pi.provider through runtime.snapshot: a line job only on a cache miss.
	// The function is async and its caller awaits it: the caller resumes one tick after it returns or throws (provider.ts :20-35, awaited at generation.ts :204).
	done := func(id string, err error) { r.hops(1, func() { then(id, err) }) }
	if existing, err, hit := r.snapshotCached(ProviderDef); hit {
		if err != nil || existing != nil {
			// The cached answer reaches its caller after snapshotHitHops, and the function returns it after one tick more, with
			// nothing between them to run: one chain.
			r.hops(snapshotHitHops+1, func() {
				if err != nil {
					then("", err)
					return
				}
				then(str(existing, "sessionId"), nil)
			})
			return
		}
		r.hops(snapshotHitHops, func() { r.providerWith(nil, done) })
		return
	}
	r.snapshotResult(ProviderDef, func(existing *jv.Object, err error) {
		if err != nil {
			done("", err)
			return
		}
		r.providerWith(existing, done)
	})
}

func (r *run) providerWith(existing *jv.Object, then func(sessionID string, err error)) {
	h := r.g.Host
	if existing != nil {
		then(str(existing, "sessionId"), nil)
		return
	}
	var created string
	r.rt.Commit(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
		doc, err := tx.Doc(ProviderDef, session.DocArgs{Owner: r.conv, Seed: nil})
		if err != nil {
			return nil, err
		}
		if s, _ := doc.Get("sessionId").(string); s != "" {
			created = s
			return nil, nil
		}
		created = h.NewSessionID()
		return nil, doc.Set("sessionId", created)
	}, func(err error) {
		if err == nil && created == "" {
			err = errors.New("Conversation " + itoa(int64(r.conv)) + " has no provider session ID")
		}
		then(created, err)
	})
}

// requestInfo is what classification needs from the request that produced a message.
type requestInfo struct {
	attempt   float64
	compacted ID
	model     ModelRef
	cutoff    ID
	messages  []*jv.Object // committed model context through cutoff, when the phase already derived it
	pollAt    *float64     // set when the message came from polling, so a still deferred result polls strictly later
}

// streamResponse streams one request and returns the terminal message. Partials commit as trailing writes at most every
// progress.partialIntervalMs with one commit in flight; the cleanup stops the throttle and waits for that commit, so no
// stale partial lands after the outcome.
func (r *run) streamResponse(req *StreamRequest, attempt float64, then func(*jv.Object, error)) {
	h := r.g.Host
	interval := h.Settings().PartialIntervalMs
	var (
		pending  jv.Raw
		cancel   func()
		inFlight bool
		stopped  bool
		waiting  func()
	)
	var flush func()
	arm := func() { cancel = h.After(interval, flush) }
	flush = func() {
		cancel = nil
		partial := pending
		pending = nil
		if partial == nil || stopped {
			return
		}
		inFlight = true
		message := parseObjectText(partial)
		r.rt.Commit(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
			live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
			if err != nil {
				return nil, err
			}
			generation := live.Obj("generation")
			if generation == nil {
				if err := live.Set("generation", jobj("attempt", attempt)); err != nil {
					return nil, err
				}
				generation = live.Obj("generation")
			}
			return nil, assignObj(generation, "message", jv.Clone(message))
		}, func(err error) {
			// Rejections after an abort mark or close are expected; the committed state stays consistent.
			if err != nil && !r.rt.Signal().Aborted() {
				_ = r.rt.Report(err)
			}
			inFlight = false
			if pending != nil && !stopped {
				arm()
			}
			if waiting != nil {
				w := waiting
				waiting = nil
				w()
			}
		})
	}
	onPartial := func(partial jv.Raw) {
		// A partial without content, such as pi-ai's opening start event, shows nothing.
		if !hasContent(partial) {
			return
		}
		pending = partial
		if cancel == nil && !inFlight {
			arm()
		}
	}
	h.Stream(req, onPartial, r.rt.Signal(), func(message *jv.Object, err error) {
		// generation.ts :364-416. The terminal event reaches the loop; `for await` then ends (1), `await events.result()` resumes (1), and
		// the finally block awaits inFlight: undefined resumes one tick later, the in-flight partial commit's promise chain
		// (the IIFE's await, .catch, .finally with its thenable adoption) settles five ticks after the commit resolves and its await
		// resumes one more. The async function's return and its caller's await take one tick (:207).
		stopped = true
		if cancel != nil {
			cancel()
			cancel = nil
		}
		resulted, flushed := false, !inFlight
		if flushed {
			// No partial commit is in flight: the loop's two ticks and the return's two follow each other with nothing between.
			r.hops(2+2, func() { then(message, err) })
			return
		}
		join := func() {
			if resulted && flushed {
				r.hops(2, func() { then(message, err) })
			}
		}
		r.hops(2, func() { resulted = true; join() })
		if !flushed {
			waiting = func() { r.hops(inFlightSettleHops, func() { flushed = true; join() }) }
		}
	})
}

// hasContent reports whether the partial message, the text of a JSON object that the host's batch holds and that was validated
// with it, has content: its content member, the last of the name, is an array with an element.
func hasContent(partial jv.Raw) bool {
	return jv.IsNonEmptyArray(jv.MemberText(partial, "content"))
}

// parseObjectText parses the text of a partial message, a JSON object that was validated with its batch.
func parseObjectText(text jv.Raw) *jv.Object {
	v, _ := jv.Parse(text)
	o, _ := v.(*jv.Object)
	return o
}

// inFlightSettleHops is the ticks from the resolution of the partial commit's promise to the resolution of the `inFlight` chain
// `(async () => { await runtime.commit(...) })().catch(...).finally(...)` (generation.ts :382-399): the IIFE's await resumes and its
// promise resolves (1), `.catch` passes it on (1), `.finally`'s callback runs (1) and the promise it returns is adopted through a
// thenable job and a reaction (2).
const inFlightSettleHops = 5

// retry is the retry phase: sleep until the backoff ends, then prepare the next attempt.
func (r *run) retry(cp *jv.Object) {
	attempt := num(cp, "attempt")
	compacted := idField(cp, "compacted")
	until := num(cp, "until")
	// After the backoff's sleep; a run before until (an early wake, or a record of an earlier version) sleeps again.
	if until > r.now() {
		r.rt.SleepUntil(until, cp, func(err error) { r.done(err) })
		return
	}
	r.commitDone(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
		live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
		if err != nil {
			return nil, err
		}
		if err := live.Set("generation", jobj("attempt", attempt+1)); err != nil {
			return nil, err
		}
		next := jobj("phase", phasePrepare, "attempt", attempt+1)
		if compacted != 0 {
			next.Set("compacted", float64(compacted))
		}
		return jobj("status", "running", "checkpoint", next), nil
	})
}

// poll is the poll phase: wait for the deferred response's poll time, fetch it and classify it.
func (r *run) poll(cp *jv.Object) {
	h := r.g.Host
	ref := modelRefOf(objField(cp, "model"))
	pollAt := num(cp, "pollAt")
	// After the sleep until pollAt; a run before it (an early wake, or a record of an earlier version) sleeps again.
	if pollAt > r.now() {
		r.rt.SleepUntil(pollAt, cp, func(err error) { r.done(err) })
		return
	}
	if _, ok := h.ContextWindow(ref); !ok {
		r.failNoModel(&ref)
		return
	}
	h.FetchDeferred(ref, objField(cp, "handle"), r.rt.Signal(), func(message *jv.Object, err error) {
		if err != nil {
			r.fail(err)
			return
		}
		r.classify(requestInfo{attempt: num(cp, "attempt"), compacted: idField(cp, "compacted"), model: ref, cutoff: idField(cp, "cutoff"), pollAt: &pollAt}, message)
	})
}
