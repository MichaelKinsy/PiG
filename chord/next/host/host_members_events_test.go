package host

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports the `event members` describe of packages/chord/test/host/members.test.ts (Pi 8d30e6306).
//
// Pi's `as never` casts put a plain object and a state member where an event member is declared. Go's
// VariantImplementation.Events map holds contracts.EventMember values, so each of the two is a type with an `On`
// method: a plain one, and one that is a state member (the constant watchable) as well.

var membersTicks = mustContract(contracts.Local("app.ticks", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{contracts.Prop("n", contracts.Int())},
	Members: []contracts.LocalEntry{contracts.Mem("ticks", contracts.Event(Sample))},
}))

// plainEvents is a plain object with `on` (and `emit`): neither an own source nor a reference's event member.
type plainEvents struct{}

type plainSubscription struct{}

func (plainSubscription) Stop() {}

func (plainEvents) On(func(contracts.EventDelivery[delta.JsonValue]), context.Context) (contracts.EventSubscription, error) {
	return plainSubscription{}, nil
}

func (plainEvents) Emit(delta.JsonValue) error { return nil }

// stateAsEvents is a state member (`constant(1)`) given where an event member is declared.
type stateAsEvents struct {
	chord.Watchable[delta.JsonValue]
}

func (stateAsEvents) On(func(contracts.EventDelivery[delta.JsonValue]), context.Context) (contracts.EventSubscription, error) {
	return plainSubscription{}, nil
}

func TestEventMembersFailTheObjectBrokenForAnImplementationThatIsNeitherItsOwnSourceNorAReferencesEventMember(t *testing.T) {
	var mu sync.Mutex
	var others []contracts.EventSource[delta.JsonValue]
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(membersTicks, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			n := int(params.Value("n").(float64))
			own := self.Events()
			mu.Lock()
			others = append(others, own)
			first := others[0]
			mu.Unlock()
			var ticks contracts.EventMember[delta.JsonValue] = own
			switch n {
			// 1: a plain object with `on`; 2: another attempt's source; 3: a state member.
			case 1:
				ticks = plainEvents{}
			case 2:
				ticks = first
			case 3:
				ticks = stateAsEvents{constant(1.0)}
			}
			return contracts.Implementation{Members: contracts.VariantImplementation{
				Events: map[string]contracts.EventMember[delta.JsonValue]{"ticks": ticks},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	base := mustGet(t, host, membersTicks, jsonObject("n", 0.0))
	if base.Events("ticks") == nil {
		t.Fatal("no ticks member")
	}
	for _, n := range []int{1, 2, 3} {
		_, err := host.Get(background, membersTicks, jsonObject("n", float64(n)), GetOptions{})
		want := "app.ticks(" + strconv.Itoa(n) + "): ticks: an event member is this object's own self.events() or a reference's event member"
		if err == nil || err.Error() != want {
			t.Fatalf("n=%d: err = %v, want %q", n, err, want)
		}
		if _, ok := errors.AsType[*data.ChordError](err); !ok {
			t.Fatalf("n=%d: err is %T", n, err)
		}
	}
}

func TestEventMembersBindAnEventOnlyContractLiveNoStateToHydrateAtOnceOverALinkToo(t *testing.T) {
	only := mustContract(contracts.Remote("app.only", contracts.RemoteDeclaration{
		Version:     1,
		Params:      contracts.Params{},
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
		Variants: []contracts.VariantEntry{
			contracts.Var("v", contracts.VariantDeclaration{Events: []contracts.Property{contracts.Prop("ticks", Sample)}}),
		},
	}))
	l := linked(t, func(h FacetHost) error {
		return h.Serve(only, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
				Events: map[string]contracts.EventMember[delta.JsonValue]{"ticks": self.Events()},
			}}, nil
		}, ServeOptions{})
	}, linkedOptions{})
	ref, err := l.Client.Get(background, only, jsonObject(), GetOptions{})
	must(t, err)
	if state := ref.State().Value(); state.Kind != RefStateLive {
		t.Fatalf("state = %+v, want live", state)
	}
}
