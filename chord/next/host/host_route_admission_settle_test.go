package host

import (
	"context"
	"errors"
	"slices"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Go-only (no Pi case): what a route does with the verdict of a decision it settles on an owned task. Pi's settle of a RELEASED
// decision never settles, so nothing is ever told; the core's `settleWait` must end (a Go task cannot hang) and returns a
// `broken` verdict for a released decision and for a cancelled context. `broken` is also the REAL verdict of a decision that
// threw or read no allow nor denial, which holds are told. The route tells them apart by what they stand for (the decision
// was released, the route's context ended), never by the verdict's error: a genuinely broken source carries the same
// `released` code.

// principalHolder is a recording holder acting for a principal (a path's holder acts for the principal a route decides about).
type principalHolder struct {
	recordingHolder
	who data.Principal
}

func (h *principalHolder) principal() data.Principal { return h.who }

// settleRig is a host whose route asks admit, with one holder on the address S1 (made inside the section `join` runs).
type settleRig struct {
	t      *testing.T
	h      *chordHost
	route  *route
	rig    *pathRig
	holder *principalHolder
	cancel func(cause error)
}

func newSettleRig(t *testing.T, admit RouteAdmit) *settleRig {
	t.Helper()
	h := newHost(t, HostOptions{OnError: func(HostError) {}}).(*chordHost)
	routing, cancel := chordctx.WithCancel(background)
	_, err := h.Route(Session, RouteOptions{
		Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
		Resolve: func(contracts.Address, context.Context) (ResolvedLink, error) {
			t.Error("a route resolved for a hold its admission holds back")
			return ResolvedLink{}, errors.New("not expected")
		},
		Admit: admit,
	}, routing)
	must(t, err)
	mustLoad(t)(h.Load(nil, background))
	rig := newPathRig()
	return &settleRig{
		t: t, h: h, route: h.routes["session"], rig: rig, cancel: cancel,
		holder: &principalHolder{recordingHolder: *rig.holder("hold"), who: serverPrincipal()},
	}
}

// join attaches the holder to the path of S1 in one section, and runs inside, in that same section, what the settle task of
// its decision cannot have seen yet (its first section is queued behind this one).
func (r *settleRig) join(inside func()) {
	r.t.Helper()
	var err error
	r.h.enter(func() {
		var made *path
		made, err = r.route.path(jsonObject("id", "S1"), r.holder.who, background)
		if err != nil {
			return
		}
		made.attach(r.holder)
		if inside != nil {
			inside()
		}
	})
	must(r.t, err)
}

// settled waits for the route's settle tasks to return (the host's owned tasks: no timing).
func (r *settleRig) settled() { r.h.events.(*Loop).Wait() }

func (r *settleRig) told() []string { return slices.Clone(r.rig.log) }

func (r *settleRig) only() *asked {
	for _, each := range r.route.admission.asked {
		return each
	}
	r.t.Fatal("the route asked no decision")
	return nil
}

func constantAdmit(verdict delta.JsonValue) RouteAdmit {
	return func(_ data.Principal, _ contracts.Address, self AdmitSelf) (any, error) {
		return self.Derive(nil, func([]any) (delta.JsonValue, error) { return verdict, nil }), nil
	}
}

func TestRouteAdmissionTheVerdictOfAReleasedOrCancelledDecisionTellsNobody(t *testing.T) {
	t.Run("control: a settled denial is told to the hold", func(t *testing.T) {
		r := newSettleRig(t, constantAdmit(Deny("no access")))
		r.join(nil)
		r.settled()
		if got := r.told(); !slices.Equal(got, []string{"hold denied"}) {
			t.Fatalf("told = %v, want [hold denied]", got)
		}
	})

	t.Run("a decision released while its route still holds it is not told as broken", func(t *testing.T) {
		r := newSettleRig(t, constantAdmit(Deny("no access")))
		// Released by something other than the route's `left` (which marks its state first): the settle returns `broken`
		// (released) while the route still thinks the decision settling.
		r.join(func() { r.only().decision.release() })
		r.settled()
		if got := r.told(); len(got) != 0 {
			t.Fatalf("told = %v, want nobody told", got)
		}
		if verdict := r.only().verdict; verdict != nil {
			t.Fatalf("verdict = %+v, want none", verdict)
		}
	})

	t.Run("a decision settled after its route's context ended is told to nobody", func(t *testing.T) {
		r := newSettleRig(t, constantAdmit(Deny("no access")))
		r.join(func() { r.cancel(r.h.closedError()) })
		r.settled()
		if got := r.told(); len(got) != 0 {
			t.Fatalf("told = %v, want nobody told: neither the denial it read nor a broken one", got)
		}
	})
}

func TestRouteAdmissionAGenuinelyBrokenDecisionIsTold(t *testing.T) {
	t.Run("an admit that throws fails the hold broken", func(t *testing.T) {
		r := newSettleRig(t, func(data.Principal, contracts.Address, AdmitSelf) (any, error) {
			return nil, errors.New("the admit threw")
		})
		r.join(nil)
		r.settled()
		if got := r.told(); !slices.Equal(got, []string{"hold broken"}) {
			t.Fatalf("told = %v, want [hold broken]", got)
		}
	})

	t.Run("a decision that reads neither an allow nor a denial fails the hold broken, as it settles", func(t *testing.T) {
		r := newSettleRig(t, constantAdmit("nonsense"))
		r.join(nil)
		r.settled()
		if got := r.told(); !slices.Equal(got, []string{"hold broken"}) {
			t.Fatalf("told = %v, want [hold broken]", got)
		}
		shown := r.only().verdict
		if shown == nil || shown.kind != verdictBroken || shown.err == nil || shown.err.Message == "" {
			t.Fatalf("verdict = %+v, want broken with its error", shown)
		}
	})
}
