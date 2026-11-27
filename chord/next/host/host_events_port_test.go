package host

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/internal/hostevents"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Not upstream's: the event port (host_events_port.go), a link's provider side for the plugins bridge. Each expectation
// is the link's own, with the link's numbers (remote_events_order_window_test.go, remote_provider_events_test.go) and
// Pi's source line.

// portRig is a host serving the tickers, the ticker held, and a port opened on it whose messages are recorded.
type portRig struct {
	*tickingHost
	Ticker *Reference
	Port   hostevents.Port
	// messages are the port's messages in order: per message its entries as "<hold>/<member> <value>".
	messages [][]string
	// stamps are the entries' stamps, in order.
	stamps []int64
}

func portOf(t *testing.T, options HostOptions) *portRig {
	t.Helper()
	probe := &tickerProbe{}
	errs := &hostErrors{}
	options.OnError = errs.push
	host := newHost(t, options)
	_, err := host.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := serveTickers(h, probe); err != nil {
			return err
		}
		return h.Serve(portPlain, func(*delta.JsonObject, Self) (contracts.MembersImplementation, error) {
			return contracts.MembersImplementation{
				Methods: map[string]contracts.MethodFunction{"ping": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }},
			}, nil
		}, ServeOptions{})
	})}}, background)
	must(t, err)
	rig := &portRig{tickingHost: &tickingHost{Host: host, Errors: errs, Probe: probe}}
	rig.Ticker, err = host.Get(background, Ticker, jsonObject(), GetOptions{})
	must(t, err)
	t.Cleanup(rig.Ticker.Release)
	rig.Port, err = hostevents.Open(rig.Ticker, func(entries []hostevents.Entry) {
		message := make([]string, len(entries))
		for i, entry := range entries {
			message[i] = fmt.Sprintf("%d/%d %s", entry.Hold, entry.Member, entry.Value)
			rig.stamps = append(rig.stamps, entry.At)
		}
		rig.messages = append(rig.messages, message)
	})
	must(t, err)
	t.Cleanup(rig.Port.Close)
	return rig
}

// emit emits ys on the ticker in one turn, and waits for its flush.
func (r *portRig) emit(t *testing.T, ys ...float64) {
	t.Helper()
	hostTurn(r.Host, func() {
		for _, y := range ys {
			must(t, r.Source().Emit(sample(y)))
		}
	})
	flushed(r.Host)
}

// take is the messages since the last take.
func (r *portRig) take() [][]string {
	var taken [][]string
	hostTurn(r.Host, func() { taken, r.messages = r.messages, nil })
	if taken == nil {
		return [][]string{}
	}
	return taken
}

func tick(hold int, y float64) string { return fmt.Sprintf(`%d/0 {"y":%s}`, hold, formatNumber(y)) }

// One message per flush, never one per entry: a turn's emits are one message with every tapped hold's entries in the
// host's entry order (Pi remote/provider.ts:381-390, `sendEvents`: "this origin's admitted entries go as one `event`
// message"), value and stamp as a listener of the same reference gets them, and handed over before the flush calls its
// listeners (Pi host/events.ts:101-104: `for (const sender of senders) sender.sendEvents()` precedes the calls).
func TestAnEventPortGetsOneMessagePerFlushWithEveryTappedHoldsEntriesInEntryOrderBeforeTheListeners(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	rig := portOf(t, HostOptions{Telemetry: recorder})
	members, err := rig.Port.Tap(rig.Ticker, 7)
	must(t, err)
	requireStrings(t, members, []string{"ticks"})
	named, err := rig.Host.Get(background, NamedTicker, jsonObject("name", "a"), GetOptions{})
	must(t, err)
	t.Cleanup(named.Release)
	_, err = rig.Port.Tap(named, 9)
	must(t, err)
	// A second reference to the ticker: each of its events reaches two holds of the port.
	again, err := rig.Host.Get(background, Ticker, jsonObject(), GetOptions{})
	must(t, err)
	t.Cleanup(again.Release)
	_, err = rig.Port.Tap(again, 8)
	must(t, err)
	var order []string
	var listened []int64
	on(t, rig.Ticker, "ticks", func(e contracts.EventDelivery[delta.JsonValue]) {
		// The listener runs inside the flush's section: the port's messages so far are read without entering.
		order = append(order, fmt.Sprintf("listener after %d messages", len(rig.messages)))
		listened = append(listened, e.At)
	}, background)

	hostTurn(rig.Host, func() {
		must(t, rig.Source().Emit(sample(1)))
		must(t, rig.Probe.lastNamed().Emit(sample(2)))
		must(t, rig.Source().Emit(sample(3)))
	})
	flushed(rig.Host)
	requireEqual(t, "messages of one turn", rig.take(), [][]string{{tick(7, 1), tick(8, 1), tick(9, 2), tick(7, 3), tick(8, 3)}})
	requireStrings(t, order, []string{"listener after 1 messages", "listener after 1 messages"})
	// One encode per entry, whatever the number of holds it reaches (Pi remote/provider.ts:374-377, `entry.part`).
	if counts := eventCounts(recorder); counts["chord.events.encodes"] != 3 || counts["chord.events.sent"] != 5 {
		t.Fatalf("encodes = %v, sent = %v, want 3 encodes for 3 events and 5 entries sent", counts["chord.events.encodes"], counts["chord.events.sent"])
	}
	rig.emit(t, 4)
	requireEqual(t, "messages of the next turn", rig.take(), [][]string{{tick(7, 4), tick(8, 4)}})
	var stamps []int64
	hostTurn(rig.Host, func() { stamps = rig.stamps })
	// Hold 7's entries are the 1st, 4th and 6th: the port's stamp is the emit's, the one the listener got.
	requireEqual(t, "stamps of hold 7's entries", []int64{stamps[0], stamps[3], stamps[5]}, listened)
}

// The default event window (Pi host/host.ts:654, `options.eventWindow ?? 32`) at its bound, with the numbers of
// TestTheDefaultEventWindowSendsThe32ndUnacknowledgedMessageAndDropsThe33rd: the 32nd unacknowledged message is sent,
// the 33rd is dropped as visited and counted `chord.events.dropped{reason=window}`, unencoded (Pi
// remote/provider.ts:369-372): never blocked, never refused, never queued. One ack frees one slot.
func TestAnEventPortSendsThe32ndUnacknowledgedMessageAndDropsThe33rd(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	rig := portOf(t, HostOptions{Telemetry: recorder})
	_, err := rig.Port.Tap(rig.Ticker, 0)
	must(t, err)
	for y := 1; y <= 32; y++ {
		rig.emit(t, float64(y))
	}
	if got := len(rig.take()); got != 32 {
		t.Fatalf("messages with 32 outstanding = %d, want 32 (the window is 32)", got)
	}
	if got := eventCounts(recorder)["chord.events.dropped.window"]; got != 0 {
		t.Fatalf("dropped at 32 outstanding = %v, want 0", got)
	}
	rig.emit(t, 33)
	if got := rig.take(); len(got) != 0 {
		t.Fatalf("the 33rd outstanding message was sent: %v, want it dropped", got)
	}
	counts := eventCounts(recorder)
	for name, want := range map[string]float64{
		"chord.events.emitted": 33, "chord.events.sent": 32, "chord.events.dropped.window": 1, "chord.events.encodes": 32,
	} {
		if counts[name] != want {
			t.Fatalf("%s = %v, want %v (all %v)", name, counts[name], want, counts)
		}
	}
	if rig.Errors.Len() != 0 {
		t.Fatalf("errors = %v, want none: a full window only drops", rig.Errors.All())
	}
	// One ack frees one slot: the next emit is sent, the one after it dropped again.
	rig.Port.Ack(1)
	rig.emit(t, 34)
	rig.emit(t, 35)
	requireEqual(t, "messages after one ack", rig.take(), [][]string{{tick(0, 34)}})
}

// An ack is a count: one that counts messages never sent, or a message a second time, frees only what is
// unacknowledged, and the window never grows past the host's `eventWindow` (Pi remote/provider.ts:681-684), with the
// numbers of TestAnEventAckForMessagesNeverSentOrCountedTwiceIsIgnoredAndOpensNoSlot.
func TestAnEventPortsAckForMessagesNeverSentOrCountedTwiceIsIgnoredAndOpensNoSlot(t *testing.T) {
	open := func(t *testing.T) *portRig {
		rig := portOf(t, HostOptions{EventWindow: new(2)})
		_, err := rig.Port.Tap(rig.Ticker, 0)
		must(t, err)
		return rig
	}
	emitTurns := func(t *testing.T, rig *portRig, from, to int) {
		for y := from; y <= to; y++ {
			rig.emit(t, float64(y))
		}
	}
	t.Run("an ack for five messages when none was sent: the window stays 2", func(t *testing.T) {
		rig := open(t)
		rig.Port.Ack(5)
		emitTurns(t, rig, 1, 4)
		requireEqual(t, "messages", rig.take(), [][]string{{tick(0, 1)}, {tick(0, 2)}})
	})
	t.Run("an ack counting more than is outstanding frees only what is outstanding", func(t *testing.T) {
		rig := open(t)
		emitTurns(t, rig, 1, 1)
		rig.Port.Ack(3)
		emitTurns(t, rig, 2, 5)
		requireEqual(t, "messages", rig.take(), [][]string{{tick(0, 1)}, {tick(0, 2)}, {tick(0, 3)}})
	})
	t.Run("the same message acknowledged twice frees one slot", func(t *testing.T) {
		rig := open(t)
		emitTurns(t, rig, 1, 1)
		rig.Port.Ack(1)
		rig.Port.Ack(1)
		emitTurns(t, rig, 2, 5)
		requireEqual(t, "messages", rig.take(), [][]string{{tick(0, 1)}, {tick(0, 2)}, {tick(0, 3)}})
	})
}

// Ack never waits for a section of the host: its caller is the bridge's channel reader, and a section may be waiting
// for what that reader delivers. The slot is free from the host's next turn on.
func TestAnEventPortsAckReturnsWhileASectionOfTheHostRuns(t *testing.T) {
	rig := portOf(t, HostOptions{EventWindow: new(1)})
	_, err := rig.Port.Tap(rig.Ticker, 0)
	must(t, err)
	rig.emit(t, 1)
	entered, release, left := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(left)
		hostTurn(rig.Host, func() {
			close(entered)
			<-release
			// Emitted after the ack was queued: the ack's turn runs before this emit's flush, which finds the slot free.
			must(t, rig.Source().Emit(sample(2)))
		})
	}()
	<-entered
	acked := make(chan struct{})
	go func() {
		defer close(acked)
		rig.Port.Ack(1)
	}()
	select {
	case <-acked:
	case <-time.After(5 * time.Second):
		close(release)
		<-left
		t.Fatal("Ack waited for the section of the host that was running")
	}
	close(release)
	<-left
	flushed(rig.Host)
	rig.emit(t, 3)
	requireEqual(t, "messages", rig.take(), [][]string{{tick(0, 1)}, {tick(0, 2)}})
}

// A tap gets what is emitted after it, as a hold bound now does (remote_hold.go `tapEvents`: `boundAt` is the host's
// counter): an emit of the same turn made before the tap is not its, though it is flushed after it. Untap and Close end
// the taps; a reference released by its holder feeds nothing more.
func TestAnEventPortsTapStartsAtTheHostsCounterAndEndsWithUntapCloseOrItsReferencesRelease(t *testing.T) {
	rig := portOf(t, HostOptions{})
	hostTurn(rig.Host, func() {
		must(t, rig.Source().Emit(sample(1)))
		_, err := rig.Port.Tap(rig.Ticker, 1)
		must(t, err)
		must(t, rig.Source().Emit(sample(2)))
	})
	flushed(rig.Host)
	requireEqual(t, "messages of the turn that tapped", rig.take(), [][]string{{tick(1, 2)}})

	rig.Port.Untap(1)
	rig.Port.Untap(1)
	rig.emit(t, 3)
	requireEqual(t, "messages after Untap", rig.take(), [][]string{})

	other, err := rig.Host.Get(background, Ticker, jsonObject(), GetOptions{})
	must(t, err)
	_, err = rig.Port.Tap(other, 2)
	must(t, err)
	rig.emit(t, 4)
	requireEqual(t, "messages of the second reference", rig.take(), [][]string{{tick(2, 4)}})
	other.Release()
	rig.emit(t, 5)
	requireEqual(t, "messages after the reference's release", rig.take(), [][]string{})
	// Its hold is still the port's until Untap: the bridge untaps what it releases.
	if _, err := rig.Port.Tap(rig.Ticker, 2); err == nil || err.Error() != "hostevents: hold 2 is tapped" {
		t.Fatalf("Tap under a hold in use = %v, want the error %q", err, "hostevents: hold 2 is tapped")
	}
	rig.Port.Untap(2)

	_, err = rig.Port.Tap(rig.Ticker, 3)
	must(t, err)
	rig.Port.Close()
	rig.emit(t, 6)
	requireEqual(t, "messages after Close", rig.take(), [][]string{})
	if _, err := rig.Port.Tap(rig.Ticker, 4); err == nil || err.Error() != "hostevents: the port is closed" {
		t.Fatalf("Tap on a closed port = %v, want the error %q", err, "hostevents: the port is closed")
	}
}

// What cannot be tapped says why: a released reference fails as its `on` does, a type without an event member and a
// reference of another host are refused, and so is a value that is no reference.
func TestAnEventPortRefusesAReleasedReferenceATypeWithoutEventsAndAnotherHostsReference(t *testing.T) {
	rig := portOf(t, HostOptions{})
	released, err := rig.Host.Get(background, Ticker, jsonObject(), GetOptions{})
	must(t, err)
	released.Release()
	_, onErr := released.Events("ticks").On(func(contracts.EventDelivery[delta.JsonValue]) {}, background)
	if _, err := rig.Port.Tap(released, 1); err == nil || onErr == nil || err.Error() != onErr.Error() {
		t.Fatalf("Tap of a released reference = %v, want its `on`'s error %v", err, onErr)
	}
	if _, err := hostevents.Open(released, func([]hostevents.Entry) {}); err == nil || err.Error() != onErr.Error() {
		t.Fatalf("Open on a released reference = %v, want its `on`'s error %v", err, onErr)
	}

	second := portOf(t, HostOptions{})
	if _, err := rig.Port.Tap(second.Ticker, 1); err == nil || !strings.Contains(err.Error(), "app.ticker: a reference of another host than the port's") {
		t.Fatalf("Tap of another host's reference = %v, want it refused", err)
	}
	if _, err := rig.Port.Tap("ticker", 1); err == nil || err.Error() != "hostevents: string is not a host reference" {
		t.Fatalf("Tap of a string = %v, want it refused", err)
	}
	plain, err := rig.Host.Get(background, portPlain, jsonObject(), GetOptions{})
	must(t, err)
	t.Cleanup(plain.Release)
	if _, err := rig.Port.Tap(plain, 1); err == nil || err.Error() != "app.port-plain: the type has no event member" {
		t.Fatalf("Tap of a type without an event member = %v, want it refused", err)
	}
}

// portPlain is a local type without an event member.
var portPlain = mustContract(contracts.Local("app.port-plain", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{},
	Members: []contracts.LocalEntry{contracts.Mem("ping", contracts.NewMethod())},
}))
