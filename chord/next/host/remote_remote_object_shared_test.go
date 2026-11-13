package host

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// pi: packages/chord/src/remote/remote-object.ts

// Ports the describes "sharing" and "state changes" of 'references sharing one wire hold (decision "References share one
// wire hold")' of packages/chord/test/remote/remote-object.test.ts (Pi 47e9d74f9 + 90ed3e3b1). The other describes of
// that block (too_large/part and the retry alone, seen members, the accepted differences, events, cancellation,
// admission, cost, telemetry, GC) are not ported here.
//
// Go forms: `await delivered()` after a raw peer's message is rhSync (the host handled it); a watch delivers its calls
// on its own task, so a case waits for the calls it expects before it compares them.

// rsPanel is Pi's `Panel`: a remote panel whose variants differ in their state members: `wide` has `x`, `y` and `poke`,
// `narrow` only `x`.
var rsPanel = mustContract(contracts.Remote("app.panel", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{}, HostAddress: exServerAddress,
	Variants: []contracts.VariantEntry{
		contracts.Var("wide", contracts.VariantDeclaration{
			State:   []contracts.Property{contracts.Prop("x", contracts.Int()), contracts.Prop("y", contracts.Int())},
			Methods: []contracts.MethodEntry{contracts.Meth("poke", contracts.NewMethod(contracts.Str(), contracts.Int()))},
		}),
		contracts.Var("narrow", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("x", contracts.Int())}}),
	},
}))

// rsBindPanel is Pi's `bindPanel`: it binds hold of rsPanel in variant with values (absent members failed with
// `member threw`).
func rsBindPanel(t testing.TB, peer *RawPeer, hold int, variant string, values map[string]float64) {
	t.Helper()
	names := wireNames(t, rsPanel, variant)
	listed := make([]string, 0, len(names.State))
	for member := range names.State {
		listed = append(listed, member)
	}
	sort.Slice(listed, func(i, j int) bool { return names.State[listed[i]] < names.State[listed[j]] })
	members := []link.BoundMember{}
	failed := []link.FailedMember{}
	for _, member := range listed {
		value, has := values[member]
		if !has {
			failed = append(failed, link.FailedMember{Member: names.State[member], Error: rhWireError(data.NewPluginError("derive", "member threw"))})
			continue
		}
		part, err := link.JsonPart(value)
		must(t, err)
		members = append(members, link.BoundMember{Member: names.State[member], Value: part})
	}
	peer.Send(&link.BoundMessage{Holds: []link.BoundEntry{{Hold: hold, Variant: names.Variant, Members: members, Failed: failed}}})
}

// rsCount is a `state` message replacing hold's `count` of Notes by value.
func rsCount(t testing.TB, hold int, value float64) *link.StateMessage {
	t.Helper()
	ops, err := link.JsonPart([]any{[]any{"r", value}})
	must(t, err)
	return rhStateEntry(hold, []link.StateMember{{Member: editorWire(t).State["count"], Ops: ops}}, nil, nil)
}

// rsFailed is a `failed` message of hold with a cause of kind (retried in a minute when transient).
func rsFailed(hold int, kind string, err error) *link.FailedMessage {
	message := &link.FailedMessage{Hold: hold, Cause: link.WireCause{Kind: kind, Error: rhWireError(err)}}
	if kind == "transient" {
		message.RetryIn = link.Some(60_000)
	}
	return message
}

// rsSame is whether two member values are the very same value (Pi's `toBe`): one array or object, not two equal ones.
func rsSame(a, b any) bool {
	left, right := reflect.ValueOf(a), reflect.ValueOf(b)
	if !left.IsValid() || !right.IsValid() || left.Kind() != right.Kind() {
		return false
	}
	switch left.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Map:
		return left.Pointer() == right.Pointer()
	default:
		return a == b
	}
}

func rsHoldKinds(peer *RawPeer) []string {
	kinds := []string{}
	for _, kind := range peer.Kinds() {
		if kind == "release" || kind == "hold" {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

func TestReferencesSharingOneWireHoldSharing(t *testing.T) {
	down := data.NewPluginError("down", "down")

	// remote-object.test.ts: references sharing one wire hold › sharing › two holds of one remote object: one hold, one snapshot; the second is live at once; a commit reaches both
	t.Run("two holds of one remote object: one hold, one snapshot; the second is live at once; a commit reaches both", func(t *testing.T) {
		rig := rhProvider(t)
		first := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		hold := bindNext(t, rig.Peer, jsonObject("text", "a"))
		waitLive(t, first)
		second := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		if kind := rhRefState(second); kind != RefStateLive {
			t.Fatalf("the second = %v at once, want live", kind)
		}
		if count := memberValue(t, second, "count"); count != float64(1) {
			t.Fatalf("the second's count = %v, want 1", count)
		}
		if holds := takenOf(rig.Peer, "hold"); holds != 0 {
			t.Fatalf("holds of the second = %d, want 0", holds)
		}
		frames := []*nexttest.Collector[nexttest.Frame[delta.JsonValue]]{
			nexttest.FramesOf(rhWatch(t, first.Member("count"))), nexttest.FramesOf(rhWatch(t, second.Member("count"))),
		}
		rig.Peer.Send(rsCount(t, hold, 2))
		rhSync(t, rig.Peer)
		rhWaitFor(t, func() bool { return frames[0].Len() >= 1 && frames[1].Len() >= 1 })
		for index, each := range frames {
			items := each.Items()
			if len(items) != 1 || items[0].Value != float64(2) || rhJSON(t, items[0].Ops) != `[["r",2]]` {
				t.Fatalf("frames of reference %d = %s, want one frame 2 by [[\"r\",2]]", index, rhJSON(t, items))
			}
		}
		// Accepted difference 6: shared values are identical.
		if !rsSame(memberValue(t, first, "notes"), memberValue(t, second, "notes")) {
			t.Fatalf("the two references' notes are two values, want the one shared value")
		}
	})

	// remote-object.test.ts: references sharing one wire hold › sharing › joins show the object's state: loading, failed transient (retryAt), live; failed waiting over the link and broken hold anew
	t.Run("joins show the object's state: loading, failed transient (retryAt), live; failed waiting over the link and broken hold anew", func(t *testing.T) {
		rig := rhProvider(t)
		join := func() *Reference { return rhHoldRef(t, rig.Client, Notes, rhNotes("S1")) }
		first := join()
		hold := rig.Peer.Next("hold").(*link.HoldMessage).Hold
		if kind := rhRefState(join()); kind != RefStateLoading {
			t.Fatalf("a join while loading = %v, want loading", kind)
		}
		rig.Peer.Send(rsFailed(hold, "transient", down))
		rhSync(t, rig.Peer)
		transient := rhState(join())
		if transient.Kind != RefStateFailed || transient.Cause.Kind != data.FailureTransient {
			t.Fatalf("a join while failed = %+v, want failed transient", transient)
		}
		if transient.RetryAt == nil || *transient.RetryAt <= time.Now().UnixMilli() {
			t.Fatalf("retryAt = %v, want a time after now", transient.RetryAt)
		}
		rig.Peer.Send(&link.LoadingMessage{Hold: hold})
		bindHold(t, rig.Peer, hold)
		rhSync(t, rig.Peer)
		if kind := rhRefState(join()); kind != RefStateLive {
			t.Fatalf("a join while live = %v, want live", kind)
		}
		if holds := takenOf(rig.Peer, "hold"); holds != 0 {
			t.Fatalf("holds of the joins = %d, want 0", holds)
		}
		// A refusal over the link: a new reference is a new request (a relay's failed route resolves again for it).
		rig.Peer.Send(rsFailed(hold, "waiting", down))
		rhSync(t, rig.Peer)
		join()
		if id := rig.Peer.Next("hold").(*link.HoldMessage).Hold; id != 1 {
			t.Fatalf("the hold of a reference after a refusal = %d, want 1", id)
		}
		// A broken object leaves the table for good.
		rig.Peer.Send(rsFailed(1, "broken", down))
		rig.Peer.Send(rsFailed(hold, "broken", down))
		rhSync(t, rig.Peer)
		join()
		if id := rig.Peer.Next("hold").(*link.HoldMessage).Hold; id != 2 {
			t.Fatalf("the hold of a reference after a broken failure = %d, want 2", id)
		}
		if state := rhState(first); state.Kind != RefStateFailed || state.Cause.Kind != data.FailureBroken {
			t.Fatalf("the first = %+v, want failed broken", state)
		}
	})

	// remote-object.test.ts: references sharing one wire hold › sharing › releasing one sends nothing and the other keeps receiving; the last sends one release; a reference after it holds anew, also in the same turn
	t.Run("releasing one sends nothing and the other keeps receiving; the last sends one release; a reference after it holds anew, also in the same turn", func(t *testing.T) {
		rig := rhProvider(t)
		first := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		second := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		hold := bindNext(t, rig.Peer)
		waitLive(t, second)
		first.Release()
		rhSync(t, rig.Peer)
		if releases := takenOf(rig.Peer, "release"); releases != 0 {
			t.Fatalf("releases after the first let go = %d, want 0", releases)
		}
		rig.Peer.Send(rsCount(t, hold, 5))
		rhSync(t, rig.Peer)
		if count := memberValue(t, second, "count"); count != float64(5) {
			t.Fatalf("the second's count = %v, want 5", count)
		}
		var third *Reference
		// One turn, as Pi's synchronous `second.release(); client.hold(...)`.
		hostTurn(rig.Client, func() {
			second.Release()
			third = rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		})
		rhSync(t, rig.Peer)
		if kinds := rsHoldKinds(rig.Peer); !slices.Equal(kinds, []string{"release", "hold"}) {
			t.Fatalf("wire = %v, want [release hold]", kinds)
		}
		if kind := rhRefState(third); kind != RefStateLoading {
			t.Fatalf("the third = %v, want loading", kind)
		}
	})

	// remote-object.test.ts: references sharing one wire hold › sharing › a getter timing out or aborted, before and after the object's first binding, releases only its own binding
	t.Run("a getter timing out or aborted, before and after the object's first binding, releases only its own binding", func(t *testing.T) {
		rig := rhProvider(t)
		kept := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		hold := rig.Peer.Next("hold").(*link.HoldMessage).Hold
		timeout := 20 * time.Millisecond
		get := func(ctx context.Context) *rhAsync[*Reference] {
			return rhGo(func() (*Reference, error) {
				return rig.Client.Get(ctx, Notes, rhNotes("S1"), GetOptions{ReadyTimeout: &timeout})
			})
		}
		late := get(background)
		cancelled, cancel := context.WithCancelCause(background)
		aborted := rhGo(func() (*Reference, error) { return rig.Client.Get(cancelled, Notes, rhNotes("S1"), GetOptions{}) })
		cancel(errors.New("gone"))
		if _, err := aborted.wait(); err == nil || err.Error() != "gone" {
			t.Fatalf("the aborted getter = %v, want gone", err)
		}
		_, err := late.wait()
		_ = requireChordError(t, err, data.CodeUnavailable, "")
		bindHold(t, rig.Peer, hold)
		waitLive(t, kept)
		// After the first binding: a getter of a failed object times out.
		rig.Peer.Send(rsFailed(hold, "transient", down))
		rhSync(t, rig.Peer)
		if _, err := get(background).wait(); err == nil {
			t.Fatalf("a getter of a failed object resolved, want its timeout")
		}
		rhSync(t, rig.Peer)
		if releases := takenOf(rig.Peer, "release"); releases != 0 {
			t.Fatalf("releases = %d, want 0 (each getter released only its own binding)", releases)
		}
		if kind := rhRefState(kept); kind != RefStateFailed {
			t.Fatalf("the kept reference = %v, want failed", kind)
		}
	})

	// remote-object.test.ts: references sharing one wire hold › sharing › calls through two references are both answered; calls parked per reference go in order once live
	t.Run("calls through two references are both answered; calls parked per reference go in order once live", func(t *testing.T) {
		rig := rhProvider(t)
		first := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		second := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		parked := []*parkedCallResult{
			startCall(t, first, "add", jsonObject("text", "a"), background),
			startCall(t, second, "add", jsonObject("text", "b"), background),
		}
		hold := bindNext(t, rig.Peer)
		for index, text := range []string{"a", "b"} {
			call := rig.Peer.Next("call").(*link.CallMessage)
			if call.Hold != hold || rhJSON(t, partValue(t, call.Args)) != `{"text":"`+text+`"}` {
				t.Fatalf("call %d = hold %d %s, want hold %d {text: %s}", index, call.Hold, rhJSON(t, partValue(t, call.Args)), hold, text)
			}
			rig.Peer.Send(sharedResult(t, call.Call, float64(index+1)))
		}
		for index, each := range parked {
			value, err := each.wait(background)
			must(t, err)
			if value != float64(index+1) {
				t.Fatalf("call %d = %v, want %d", index, value, index+1)
			}
		}
	})
}

func TestReferencesSharingOneWireHoldStateChanges(t *testing.T) {
	down := data.NewPluginError("down", "down")

	// remote-object.test.ts: references sharing one wire hold › state changes › a rebind to another variant moves every binding in one batch; a member the new variant lacks fails for all
	t.Run("a rebind to another variant moves every binding in one batch; a member the new variant lacks fails for all", func(t *testing.T) {
		rig := rhProvider(t)
		first := rhHoldRef(t, rig.Client, rsPanel, jsonObject())
		second := rhHoldRef(t, rig.Client, rsPanel, jsonObject())
		hold := rig.Peer.Next("hold").(*link.HoldMessage).Hold
		rsBindPanel(t, rig.Peer, hold, "wide", map[string]float64{"x": 1, "y": 2})
		waitLive(t, second)
		calls := []*nexttest.Collector[chord.WatchCall[delta.JsonValue]]{
			nexttest.ChangesOf(rhWatch(t, first.Member("y"))), nexttest.ChangesOf(rhWatch(t, second.Member("y"))),
		}
		rig.Peer.Send(&link.LoadingMessage{Hold: hold})
		rsBindPanel(t, rig.Peer, hold, "narrow", map[string]float64{"x": 3})
		rhSync(t, rig.Peer)
		rhWaitFor(t, func() bool { return calls[0].Len() >= 1 && calls[1].Len() >= 1 })
		for index, ref := range []*Reference{first, second} {
			if variant, _, _ := ref.Variant(); variant != "narrow" {
				t.Fatalf("reference %d's variant = %q, want narrow", index, variant)
			}
			if items := calls[index].Items(); len(items) != 1 || items[0].Kind != chord.WatchCallFailed {
				t.Fatalf("y's calls of reference %d = %+v, want one failed call", index, items)
			}
		}
		if _, err := second.Member("y").Value(); err == nil || !strings.Contains(err.Error(), `has no state member "y"`) {
			t.Fatalf("y.value error = %v, want has no state member \"y\"", err)
		}
	})

	// remote-object.test.ts: references sharing one wire hold › state changes › gone and the host's close end every binding exactly once (watches, subscriptions and calls too); a later reference holds anew
	t.Run("gone and the host's close end every binding exactly once (watches, subscriptions and calls too); a later reference holds anew", func(t *testing.T) {
		rig := rhProvider(t)
		first := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		second := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		hold := bindNext(t, rig.Peer)
		waitLive(t, second)
		watches := []chord.WatchHandle[delta.JsonValue]{rhWatch(t, first.Member("count")), rhWatch(t, second.Member("count"))}
		call := startCall(t, second, "add", jsonObject("text", "a"), background)
		rig.Peer.Next("call")
		ended := data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonEnded, Message: "app.notes(S1): ended"}, nil)
		rig.Peer.Send(&link.GoneMessage{Hold: hold, Error: rhWireError(ended)})
		rhSync(t, rig.Peer)
		if kinds := []RefStateKind{rhRefState(first), rhRefState(second)}; kinds[0] != RefStateEnded || kinds[1] != RefStateEnded {
			t.Fatalf("states = %v, want [ended ended]", kinds)
		}
		for index, watch := range watches {
			<-watch.Closed()
			if kind := watch.End().Kind; kind != chord.WatchEnded {
				t.Fatalf("watch %d ended %v, want ended", index, kind)
			}
		}
		_, err := call.wait(background)
		_ = requireChordError(t, err, data.CodeReleased, data.ReasonEnded)
		rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		if id := rig.Peer.Next("hold").(*link.HoldMessage).Hold; id != 1 {
			t.Fatalf("the hold of a reference after gone = %d, want 1", id)
		}
		third := rhHoldRef(t, rig.Client, Notes, rhNotes("S2"))
		fourth := rhHoldRef(t, rig.Client, Notes, rhNotes("S2"))
		waits := []*rhAsync[chord.WatchHandle[delta.JsonValue]]{}
		for _, ref := range []*Reference{third, fourth} {
			waits = append(waits, rhGo(func() (chord.WatchHandle[delta.JsonValue], error) { return ref.Member("count").Watch(background) }))
		}
		must(t, rig.Client.Close(background))
		if kinds := []RefStateKind{rhRefState(third), rhRefState(fourth)}; kinds[0] != RefStateEnded || kinds[1] != RefStateEnded {
			t.Fatalf("states after the close = %v, want [ended ended]", kinds)
		}
		for _, wait := range waits {
			_, err := wait.wait()
			_ = requireChordError(t, err, data.CodeReleased, "")
		}
	})

	// remote-object.test.ts: references sharing one wire hold › state changes › a link drop: one re-hold, every binding live again
	t.Run("a link drop: one re-hold, every binding live again", func(t *testing.T) {
		rig := rhProvider(t)
		first := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		second := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		bindNext(t, rig.Peer)
		waitLive(t, second)
		rig.Peer.Close()
		rhParentDown(t, rig.Client)
		if kinds := []RefStateKind{rhRefState(first), rhRefState(second)}; kinds[0] != RefStateLoading || kinds[1] != RefStateLoading {
			t.Fatalf("states after the drop = %v, want [loading loading]", kinds)
		}
		next := rawParent(t, rig.Client, link.PartLimits{})
		bindNext(t, next, jsonObject("text", "b"))
		waitLive(t, first)
		waitLive(t, second)
		if holds := takenOf(next, "hold"); holds != 0 {
			t.Fatalf("holds left on the next link = %d, want 0 (one re-hold)", holds)
		}
		if count := memberValue(t, second, "count"); count != float64(1) {
			t.Fatalf("the second's count = %v, want 1", count)
		}
	})

	// remote-object.test.ts: references sharing one wire hold › state changes › failure and loading fan out to every binding: watch callbacks in batch order; a last release while the first binding is pending
	//
	// Go form: a `ref.state` watch delivers on its own task, so the order between two references' callbacks of one batch
	// is not defined here: each batch's callbacks are compared as a set, the batches in order. The order in which the
	// object tells its bindings is TestRemoteObjectMoveTellsItsBindingsInTheOrderTheyJoined.
	t.Run("failure and loading fan out to every binding: watch callbacks in batch order; a last release while the first binding is pending", func(t *testing.T) {
		rig := rhProvider(t)
		first := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		second := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		hold := rig.Peer.Next("hold").(*link.HoldMessage).Hold
		first.Release()
		second.Release()
		rhSync(t, rig.Peer)
		if releases := takenOf(rig.Peer, "release"); releases != 1 {
			t.Fatalf("releases = %d, want 1", releases)
		}
		third := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		fourth := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		next := bindNext(t, rig.Peer)
		if next != hold+1 {
			t.Fatalf("the next hold = %d, want %d", next, hold+1)
		}
		waitLive(t, fourth)
		var mu sync.Mutex
		var order []string
		for name, ref := range map[string]*Reference{"third": third, "fourth": fourth} {
			watch, err := ref.State().Watch(background)
			must(t, err)
			watch.Start(func(_ context.Context, call chord.NativeWatchCall[*RefState]) error {
				if call.Kind == chord.WatchCallValue {
					mu.Lock()
					order = append(order, name+" "+string(call.Value.Kind))
					mu.Unlock()
				}
				return nil
			})
		}
		batch := func(n int) []string {
			t.Helper()
			rhWaitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(order) >= n })
			mu.Lock()
			defer mu.Unlock()
			got := slices.Clone(order[n-2:])
			slices.Sort(got)
			return got
		}
		rig.Peer.Send(rsFailed(next, "transient", down))
		rhSync(t, rig.Peer)
		if got := batch(2); !slices.Equal(got, []string{"fourth failed", "third failed"}) {
			t.Fatalf("the failure's callbacks = %v, want third failed and fourth failed", got)
		}
		rig.Peer.Send(&link.LoadingMessage{Hold: next})
		rhSync(t, rig.Peer)
		if got := batch(4); !slices.Equal(got, []string{"fourth loading", "third loading"}) {
			t.Fatalf("the loading's callbacks = %v, want third loading and fourth loading", got)
		}
	})

	// remote-object.test.ts: references sharing one wire hold › state changes › a broken object that recovers stays out of the table; ending it leaves the newer entry alone
	t.Run("a broken object that recovers stays out of the table; ending it leaves the newer entry alone", func(t *testing.T) {
		rig := rhProvider(t)
		a := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		hold := rig.Peer.Next("hold").(*link.HoldMessage).Hold
		rig.Peer.Send(rsFailed(hold, "broken", down))
		rhSync(t, rig.Peer)
		c := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		newer := rig.Peer.Next("hold").(*link.HoldMessage).Hold
		a.Retry()
		rig.Peer.Next("retry")
		rig.Peer.Send(&link.LoadingMessage{Hold: hold})
		bindHold(t, rig.Peer, hold)
		bindHold(t, rig.Peer, newer)
		rhSync(t, rig.Peer)
		if kinds := []RefStateKind{rhRefState(a), rhRefState(c)}; kinds[0] != RefStateLive || kinds[1] != RefStateLive {
			t.Fatalf("states = %v, want [live live]", kinds)
		}
		a.Release()
		if released := rig.Peer.Next("release").(*link.ReleaseMessage).Hold; released != hold {
			t.Fatalf("released %d, want %d", released, hold)
		}
		// D joins C's object: the recovered one never came back, and its end took nothing of C's.
		d := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		if kind, holds := rhRefState(d), takenOf(rig.Peer, "hold"); kind != RefStateLive || holds != 0 {
			t.Fatalf("d = %v with %d holds, want live with none", kind, holds)
		}
	})

	// remote-object.test.ts: references sharing one wire hold › state changes › a refused hold's retry restarts it for every reference sharing it (accepted difference 8)
	t.Run("a refused hold's retry restarts it for every reference sharing it (accepted difference 8)", func(t *testing.T) {
		rig := rhProvider(t)
		first := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		second := rhHoldRef(t, rig.Client, Notes, rhNotes("S1"))
		hold := rig.Peer.Next("hold").(*link.HoldMessage).Hold
		refused := data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "not served"}, nil)
		rig.Peer.Send(rsFailed(hold, "waiting", refused))
		rhSync(t, rig.Peer)
		var states []*statesCollector
		for _, ref := range []*Reference{first, second} {
			watch, err := ref.State().Watch(background)
			must(t, err)
			states = append(states, statesOf(t, watch))
		}
		first.Retry()
		if retried := rig.Peer.Next("retry").(*link.RetryMessage).Hold; retried != hold {
			t.Fatalf("retry of hold %d, want %d", retried, hold)
		}
		rig.Peer.Send(&link.LoadingMessage{Hold: hold})
		rig.Peer.Send(rsFailed(hold, "waiting", refused))
		rhSync(t, rig.Peer)
		rhWaitFor(t, func() bool { return len(states[0].All()) >= 2 && len(states[1].All()) >= 2 })
		for index, each := range states {
			if got := each.All(); !slices.Equal(got, []string{"loading", "failed: not served"}) {
				t.Fatalf("states of reference %d = %v, want [loading, failed: not served]", index, got)
			}
		}
	})
}

// PiG's own (white box): remoteObject.move tells its bindings their state in the order they joined. A reference's state
// waiters (`binding.until`, what getters and parked calls wait on) run inside `moved`, so their order is the order of
// the object's calls; Pi's case reads it from watch callbacks, whose order across references Go does not define.
func TestRemoteObjectMoveTellsItsBindingsInTheOrderTheyJoined(t *testing.T) {
	rig := rhProvider(t)
	names := []string{"a", "b", "c"}
	refs := []*Reference{}
	for range names {
		refs = append(refs, rhHoldRef(t, rig.Client, Notes, rhNotes("S1")))
	}
	hold := bindNext(t, rig.Peer)
	waitLive(t, refs[2])
	var order []string
	hostTurn(rig.Client, func() {
		for index, ref := range refs {
			ref.binding.until(func(current *RefState) decision {
				order = append(order, names[index]+" "+string(current.Kind))
				return waitDecision
			}, background)
		}
	})
	rig.Peer.Send(rsFailed(hold, "transient", data.NewPluginError("down", "down")))
	rhSync(t, rig.Peer)
	var got []string
	hostTurn(rig.Client, func() { got = slices.Clone(order) })
	if want := []string{"a live", "b live", "c live", "a failed", "b failed", "c failed"}; !slices.Equal(got, want) {
		t.Fatalf("the bindings were told in the order %v, want %v", got, want)
	}
}
