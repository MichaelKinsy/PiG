package host

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports packages/chord/test/next/jobs/job.test.ts (keep-alive over a link, the object's end, GC); the other cases are
// in jobs_job_test.go and jobs_job_runs_test.go. The Go forms are described in jobs_job_test.go.

// jobClosingLink is `failing` of the keep-alive test: the server's end of the link closes while it sends.
type jobClosingLink struct {
	link.Link
	closeOnSend atomic.Bool
}

// Send closes the link instead of sending when closeOnSend is set (once).
func (l *jobClosingLink) Send(message []byte) {
	if l.closeOnSend.CompareAndSwap(true, false) {
		l.Close()
		return
	}
	l.Link.Send(message)
}

var jobViewType = mustContract(contracts.Remote("app.view", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{},
	HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
	Variants: []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{
		State: []contracts.Property{contracts.Prop("running", contracts.Arr(contracts.Unknown()))},
	})},
}))

var jobsType = mustContract(contracts.Remote("app.jobs", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{},
	HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
	Variants:    []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{})},
}))

func guestRevokedBy(principal data.Principal) ([]delta.JsonValue, error) {
	if principalKind(principal) == "guest" {
		return []delta.JsonValue{jsonObject("session", principal.Value("session"))}, nil
	}
	return nil, nil
}

// jobServer is a server host (principal `server`, session revocations) loaded with one facet serving typ with factory.
func jobServer(t *testing.T, serve func(host FacetHost) error) Host {
	t.Helper()
	server := newJobHost(t, HostOptions{
		Address: new(Server.Address(nil)), Principal: serverPrincipal(), RevokedBy: guestRevokedBy, OnError: func(err HostError) { t.Logf("server error: %+v", err) },
	})
	must(t, server.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return serve(h) })}}, background))
	return server
}

// jobClient is a client host loaded with nothing.
func jobClient(t *testing.T) Host {
	t.Helper()
	client := newJobHost(t, HostOptions{OnError: func(err HostError) { t.Logf("client error: %+v", err) }})
	must(t, client.Load(nil, background))
	return client
}

// job.test.ts › keep-alive › keeps an object whose last reference goes while its start's commit publishes (a link closing as it sends)
func TestKeepAliveKeepsAnObjectWhoseLastReferenceGoesWhileItsStartsCommitPublishes(t *testing.T) {
	var job JobHandle
	var ran atomic.Bool
	server := jobServer(t, func(h FacetHost) error {
		return h.Serve(jobViewType, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			job = self.Job(jobEcho, JobImplementation{Run: func(args any, _ JobCtx) (any, error) {
				ran.Store(true)
				return textOf(args), nil
			}})
			// The running list as a state member, so the start's commit sends a state message.
			return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"running": Expose(job.Running())},
			}}, nil
		}, ServeOptions{})
	})
	client := jobClient(t)
	a, b := link.MemoryLinkPair()
	failing := &jobClosingLink{Link: a}
	must(t, server.Accept(failing, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
	must(t, client.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
	reference, err := client.Get(jobViewType, jsonObject(), background, GetOptions{})
	must(t, err)
	transported(a, b)
	failing.closeOnSend.Store(true)
	_ = reference
	run := jobStart(t, job, textArgs("a"), background)
	if outcome := runOutcome(t, run); outcome.Kind != OutcomeDone || outcome.Result != "a" {
		t.Fatalf("outcome = %+v", outcome)
	}
	if !ran.Load() {
		t.Fatal("run did not run")
	}
	must(t, client.Close(background))
	must(t, server.Close(background))
}

// job.test.ts › object end › revocation (R11): runs' signals fire with the end's error, the drain waits for them, no finish
func TestObjectEndRevocationR11RunsSignalsFireWithTheEndsErrorTheDrainWaitsForThemNoFinish(t *testing.T) {
	var job JobHandle
	var mu sync.Mutex
	var finished []string
	var reason error
	started := newGate()
	var server Host
	server = jobServer(t, func(h FacetHost) error {
		return h.Serve(jobsType, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			job = self.Job(jobEcho, JobImplementation{
				Run: func(args any, ctx JobCtx) (any, error) {
					started.Open()
					untilDone(ctx)
					mu.Lock()
					reason = context.Cause(ctx)
					mu.Unlock()
					timerWait(server.(*chordHost).loop(), 10*time.Millisecond)
					mu.Lock()
					finished = append(finished, "run stopped")
					mu.Unlock()
					return textOf(args), nil
				},
				Finish: func(Outcome, documents.Tx) error {
					mu.Lock()
					finished = append(finished, "finish")
					mu.Unlock()
					return nil
				},
			})
			return contracts.Implementation{Variant: "v"}, nil
		}, ServeOptions{})
	})
	client := jobClient(t)
	a, b := link.MemoryLinkPair()
	must(t, server.Accept(a, LinkOptions{Principal: guestPrincipal("S1"), Codec: testCodec()}, background))
	must(t, client.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
	_, err := client.Get(jobsType, jsonObject(), background, GetOptions{})
	must(t, err)
	run := jobStart(t, job, textArgs("a"), background)
	started.Wait()
	must(t, server.Revoke(jsonObject("session", "S1"), background))
	expectChordError(t, runOutcomeErr(t, run, background), data.CodeForbidden, data.ReasonRevoked)
	mu.Lock()
	expectChordError(t, reason, data.CodeForbidden, data.ReasonRevoked)
	mu.Unlock()
	eventually(t, "run to stop", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(finished) == 1
	})
	transported(a, b)
	mu.Lock()
	if strings.Join(finished, ",") != "run stopped" {
		t.Fatalf("finished = %v", finished)
	}
	mu.Unlock()
	eventually(t, "the activity to drain", func() bool { return activityCount(t, server, "jobs") == 0 })
	must(t, client.Close(background))
	must(t, server.Close(background))
}

// job.test.ts › object end › drains a run that ends its own object before its first await: its natives stay open until it stops
func TestObjectEndDrainsARunThatEndsItsOwnObjectBeforeItsFirstAwaitItsNativesStayOpenUntilItStops(t *testing.T) {
	var job JobHandle
	g := newGate()
	var mu sync.Mutex
	var events []string
	note := func(event string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	}
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(events)
	}
	var server Host
	server = jobServer(t, func(h FacetHost) error {
		return h.Serve(jobsType, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			self.Open(
				func(context.Context) (any, error) { return nil, nil },
				func(any, NativeCloseReason, context.Context) error { note("native closed"); return nil },
			)
			job = self.Job(jobEcho, JobImplementation{Run: func(args any, _ JobCtx) (any, error) {
				must(t, server.Revoke(jsonObject("session", "S1"), background))
				g.Wait()
				note("run stopped")
				return textOf(args), nil
			}})
			return contracts.Implementation{Variant: "v"}, nil
		}, ServeOptions{})
	})
	client := jobClient(t)
	a, b := link.MemoryLinkPair()
	must(t, server.Accept(a, LinkOptions{Principal: guestPrincipal("S1"), Codec: testCodec()}, background))
	must(t, client.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
	_, err := client.Get(jobsType, jsonObject(), background, GetOptions{})
	must(t, err)
	run := jobStart(t, job, textArgs("a"), background)
	rejected := make(chan error, 1)
	go func() { rejected <- runOutcomeErr(t, run, background) }()
	hostDelivered(server)
	nexttest.Flushed()
	if n := count(); n != 0 {
		t.Fatalf("events before the run stopped = %d", n)
	}
	g.Open()
	eventually(t, "the run and the native", func() bool { return count() == 2 })
	mu.Lock()
	if strings.Join(events, ",") != "run stopped,native closed" {
		t.Fatalf("events = %v", events)
	}
	mu.Unlock()
	expectChordError(t, <-rejected, data.CodeForbidden, data.ReasonRevoked)
	must(t, client.Close(background))
	must(t, server.Close(background))
}

// job.test.ts › object end › host.close: signals fire, close waits for the run (drain), no finish, waiters rejected released
func TestObjectEndHostCloseSignalsFireCloseWaitsForTheRunDrainNoFinishWaitersRejectedReleased(t *testing.T) {
	var mu sync.Mutex
	var finished []string
	started := newGate()
	var fixture *jobFixture
	fixture = echoJob(t, JobImplementation{
		Run: func(args any, ctx JobCtx) (any, error) {
			started.Open()
			untilDone(ctx)
			timerWait(fixture.Host.(*chordHost).loop(), 10*time.Millisecond)
			mu.Lock()
			finished = append(finished, "run stopped")
			mu.Unlock()
			return textOf(args), nil
		},
		Finish: func(Outcome, documents.Tx) error {
			mu.Lock()
			finished = append(finished, "finish")
			mu.Unlock()
			return nil
		},
	}, jobHostOptions{})
	run := jobStart(t, fixture.Job, textArgs("a"), background)
	started.Wait()
	waiting := make(chan error, 1)
	go func() { waiting <- runOutcomeErr(t, run, background) }()
	must(t, fixture.Host.Close(background))
	mu.Lock()
	if strings.Join(finished, ",") != "run stopped" {
		t.Fatalf("finished = %v", finished)
	}
	mu.Unlock()
	expectChordError(t, <-waiting, data.CodeReleased, "")
}

// job.test.ts › GC › finished and dropped runs leave nothing on their object: finished runs
func TestGCFinishedAndDroppedRunsLeaveNothingOnTheirObject(t *testing.T) {
	var mu sync.Mutex
	var args []weak.Pointer[delta.JsonObject]
	var ctxs []weak.Pointer[jobContext]
	fixture := echoJob(t, JobImplementation{Run: func(a any, ctx JobCtx) (any, error) {
		mu.Lock()
		args = append(args, weak.Make(a.(*delta.JsonObject)))
		ctxs = append(ctxs, weak.Make(ctx.(*jobContext)))
		mu.Unlock()
		return textOf(a), nil
	}}, jobHostOptions{})
	runOutcome(t, jobStart(t, fixture.Job, textArgs("a"), background))
	eventually(t, "the run to be gone", func() bool { return len(jobRunning(t, fixture.Job)) == 0 })
	nexttest.Flushed()
	mu.Lock()
	defer mu.Unlock()
	collectedWithin(t, args...)
	collectedWithin(t, ctxs...)
}

// job.test.ts › GC › of dropped runs › a dropped blocked run leaves nothing on its object
func TestGCOfDroppedRunsADroppedBlockedRunLeavesNothingOnItsObject(t *testing.T) {
	loop := newVirtualLoop()
	var mu sync.Mutex
	var args []weak.Pointer[delta.JsonObject]
	var ctxs []weak.Pointer[jobContext]
	fixture := echoJob(t, JobImplementation{
		Run: func(a any, ctx JobCtx) (any, error) {
			mu.Lock()
			args = append(args, weak.Make(a.(*delta.JsonObject)))
			ctxs = append(ctxs, weak.Make(ctx.(*jobContext)))
			mu.Unlock()
			return textOf(a), nil
		},
		Finish: func(Outcome, documents.Tx) error { return errors.New("stuck") },
	}, jobHostOptions{Loop: loop})
	job := fixture.Job
	run := jobStart(t, job, textArgs("a"), background)
	for _, delay := range []int64{1_000, 2_000, 4_000, 8_000} {
		advanceBackoff(t, loop, delay)
	}
	eventually(t, "the run to be blocked", func() bool {
		entries := jobRunning(t, job)
		return len(entries) == 1 && phases(entries)[0] == "blocked"
	})
	dropped := make(chan error, 1)
	go func() { dropped <- runOutcomeErr(t, run, background) }()
	go func() { _, _ = job.Abort(run.ID(), background) }()
	expectChordError(t, <-dropped, data.CodeAborted, "")
	eventually(t, "the entry to be removed", func() bool { return len(jobRunning(t, job)) == 0 })
	mu.Lock()
	defer mu.Unlock()
	collectedWithin(t, args...)
	collectedWithin(t, ctxs...)
}

// job.test.ts › GC › keeps nothing of a cancelled wait while its run still runs
//
// Pi checks the aborted signals of the cancelled waits are collected; the registration of a wait is what would keep one
// in Go, so the test checks the run's waiters hold none once the waits were cancelled.
func TestGCKeepsNothingOfACancelledWaitWhileItsRunStillRuns(t *testing.T) {
	g := newGate()
	fixture := echoJob(t, JobImplementation{Run: gated(g)}, jobHostOptions{})
	job := fixture.Job
	run := jobStart(t, job, textArgs("a"), background)
	records := recordsOf(job)
	for range 5 {
		waitCtx, cancel := chordctx.WithCancel(background)
		waiting := make(chan error, 1)
		go func() { waiting <- runOutcomeErr(t, run, waitCtx) }()
		eventually(t, "the wait to register", func() bool {
			var registered int
			records.loop.Run(func() { registered = len(records.runs[run.ID()].waiters.waiting) })
			return registered == 1
		})
		cancel(errors.New("gave up"))
		if err := <-waiting; err == nil || err.Error() != "gave up" {
			t.Fatalf("a cancelled wait = %v", err)
		}
	}
	var left int
	records.loop.Run(func() { left = len(records.runs[run.ID()].waiters.waiting) })
	if left != 0 {
		t.Fatalf("%d cancelled waits left registered", left)
	}
	g.Open()
	if kind := runOutcome(t, run).Kind; kind != OutcomeDone {
		t.Fatalf("kind = %s", kind)
	}
}

// job.test.ts › GC › keeps no ended run's args or state in a job handle kept after its object ended
func TestGCKeepsNoEndedRunsArgsOrStateInAJobHandleKeptAfterItsObjectEnded(t *testing.T) {
	var mu sync.Mutex
	var args, states []weak.Pointer[delta.JsonObject]
	started := newGate()
	fixture := declaredJob(t, jobShell, func(Self, *singletonDoc) JobImplementation {
		return JobImplementation{Run: func(a any, ctx JobCtx) (any, error) {
			state, err := ctx.State().Value()
			if err != nil {
				return nil, err
			}
			mu.Lock()
			args = append(args, weak.Make(a.(*delta.JsonObject)))
			states = append(states, weak.Make(state))
			mu.Unlock()
			started.Open()
			untilDone(ctx)
			return exitResult(1), nil
		}}
	}, jobHostOptions{})
	job := fixture.Job
	jobStart(t, job, commandArgs("a"), background)
	started.Wait()
	must(t, fixture.Host.Close(background))
	mu.Lock()
	collectedWithin(t, args...)
	collectedWithin(t, states...)
	mu.Unlock()
	var err error
	recordsOf(job).loop.Run(func() { _, err = job.Running().Value() })
	if _, ok := data.IsChordError(err); !ok {
		t.Fatalf("running after the end = %v, want a ChordError", err)
	}
}

// job.test.ts › GC › an ended object releases its runs, their documents and waiters
func TestGCAnEndedObjectReleasesItsRunsTheirDocumentsAndWaiters(t *testing.T) {
	var mu sync.Mutex
	var args, states []weak.Pointer[delta.JsonObject]
	var ctxs []weak.Pointer[jobContext]
	var handles []weak.Pointer[job]
	started := newGate()
	var held JobHandle
	host, _ := jobsLoaded(t, func(h FacetHost) error {
		return h.Serve(jobItemType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			log := stateOf(self, jobLogState)
			held = self.Job(jobShell, JobImplementation{Run: func(a any, ctx JobCtx) (any, error) {
				state, err := ctx.State().Value()
				if err != nil {
					return nil, err
				}
				mu.Lock()
				args = append(args, weak.Make(a.(*delta.JsonObject)))
				ctxs = append(ctxs, weak.Make(ctx.(*jobContext)))
				states = append(states, weak.Make(state))
				mu.Unlock()
				started.Open()
				untilDone(ctx)
				return exitResult(1), nil
			}})
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"log": log.Member()},
			}}, nil
		}, ServeOptions{})
	}, jobHostOptions{})
	item, err := host.Get(jobItemType, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	jobStart(t, held, commandArgs("a"), background)
	started.Wait()
	mu.Lock()
	handles = append(handles, weak.Make(held.(*job)))
	mu.Unlock()
	held = nil
	must(t, host.Close(background))
	_ = item
	mu.Lock()
	defer mu.Unlock()
	collectedWithin(t, args...)
	collectedWithin(t, ctxs...)
	collectedWithin(t, states...)
	collectedWithin(t, handles...)
}
