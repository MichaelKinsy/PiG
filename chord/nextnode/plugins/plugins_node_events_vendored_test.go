package plugins

import (
	"strings"
	"testing"
)

// Not upstream's: node/events.mjs, the Node side of the events that cross the plugins bridge, run on Node over the
// extracted tree (the files the subprocess runs) with a recording port in place of the control channel and Pi's own
// vendored host. Each expectation is a link's, with the link's numbers (chord/next/host/remote_events_order_window_test.go,
// host_events_port_test.go) and Pi's source line in the vendored tree (node/pi/chord/src).

// evScript is a script over the extracted tree: Pi's chord, the events module, and `hostOf(options)`, a loaded host serving
// `test.ticker`, a local type with the event members `ticks` and `tocks` fed by `sources.ticks` and `sources.tocks`.
// `turn()` lets the event flush and what it queued run; `recording()` is a port that records what is written to it.
func evScript(pi func(string) string, body string) string {
	return strings.Join([]string{
		"const chord = await import(" + pi("index.ts") + ");",
		"const { BACKGROUND_CONTEXT } = await import(" + pi("context/index.ts") + ");",
		"const { createEvents, eventInternals } = await import(" + pi("../../../events.mjs") + ");",
		`const { createHost, defineFacet, event, local, num, obj } = chord;
const Sample = obj({ y: num() });
const Ticker = local("test.ticker", { version: 1, params: {}, members: { ticks: event(Sample), tocks: event(Sample) } });
const sources = {};
const facet = defineFacet((host) => {
	host.serve(Ticker, (_params, self) => {
		sources.ticks = self.events();
		sources.tocks = self.events();
		return { ticks: sources.ticks, tocks: sources.tocks };
	});
});
const reported = [];
async function hostOf(options = {}) {
	const host = createHost({ onError: (error) => reported.push(error), ...options });
	await host.load([{ name: "@test/ticker", facet }], BACKGROUND_CONTEXT);
	return host;
}
const turn = () => new Promise((resolve) => setImmediate(resolve));
function recording() {
	const written = [];
	return { written, control: (message) => void written.push(message) };
}
/** Emits y from..to on ticks, each in a turn of its own (one flush per emit). */
async function emitTurns(from, to) {
	for (let y = from; y <= to; y++) {
		sources.ticks.emit({ y });
		await turn();
	}
}
/** The ys of the messages written since the last call, per message. */
const ys = (port) => port.written.splice(0).map((message) => message.events.map((entry) => JSON.parse(entry.value).y));
const range = (from, to) => Array.from({ length: to - from + 1 }, (_, index) => [from + index]);`,
		body,
		"assert.deepEqual(reported, []);",
	}, "\n")
}

// An `event` message of the Go side reaches the proxy's own source for its member with the Go host's stamp, as an
// event arriving over a link reaches its remote object's node (Pi remote/remote-object.ts:535-547: `events.push(node,
// parsePart(json), at)`): it is delivered at this host's flush, not on arrival. An entry of a hold this side does not
// know, of an index the hold lacks, or of a source nothing taps is dropped unparsed (:519-527, `node.tapped ? node :
// undefined`). The messages of one inbound turn are acknowledged by one `ack` that counts them, sent after the flush
// delivered what they carried (Pi remote/consumer.ts:312-320), whatever became of their entries.
func TestNodeEventsOfTheGoSideReachTheProxysSourceWithTheEmittersStampAndAreAcknowledgedOncePerInboundTurn(t *testing.T) {
	runVendored(t, func(pi func(string) string) string {
		return evScript(pi, `
const host = await hostOf();
const ticker = await host.get(Ticker, {}, BACKGROUND_CONTEXT);
const log = [];
const events = createEvents({ port: { control: (message) => void log.push(message) }, host: () => host });
events.sinks.set(5, [sources.ticks, sources.tocks]);
await ticker.ticks.on((delivery) => void log.push(["ticks", delivery.value, delivery.at]), BACKGROUND_CONTEXT);
events.receive({ op: "event", events: [
	{ hold: 5, member: 0, at: 1111, value: '{"y":1}' },
	{ hold: 9, member: 0, at: 1112, value: "an unknown hold is not parsed" },
	{ hold: 5, member: 1, at: 1113, value: "a source nothing taps is not parsed" },
] });
events.receive({ op: "event", events: [
	{ hold: 5, member: 0, at: 2222, value: '{"y":3}' },
	{ hold: 5, member: 7, at: 2223, value: "an index the hold lacks is not parsed" },
] });
assert.deepEqual(log, [], "delivered or acknowledged on arrival");
await turn();
assert.deepEqual(log, [["ticks", { y: 1 }, 1111], ["ticks", { y: 3 }, 2222], { event: "ack", events: 2 }]);
log.length = 0;
events.sinks.delete(5);
events.receive({ op: "event", events: [{ hold: 5, member: 0, at: 3333, value: '{"y":4}' }] });
await turn();
assert.deepEqual(log, [{ event: "ack", events: 1 }], "a released hold's message is dropped and acknowledged");
await host.close(BACKGROUND_CONTEXT);`)
	})
}

// One message per flush, never one per entry: a turn's emits cross as one `event` message with every tapped hold's
// entries in the host's entry order (Pi remote/provider.ts:382-391, `sendEvents`), value and stamp as a listener gets
// them, written before the flush calls its listeners (Pi host/events.ts:101-104: `for (const sender of senders)
// sender.sendEvents()` precedes the calls), each value encoded once whatever the number of holds it reaches (:374-377).
// A tap gets what is emitted after it (the host's counter at the bind, as a hold's: Pi remote/hold.ts:590-594), and what
// ended it stops it.
func TestNodeEventsOfAFlushCrossAsOneMessageInEntryOrderBeforeTheListenersFromTheBindUntilUntapped(t *testing.T) {
	runVendored(t, func(pi func(string) string) string {
		return evScript(pi, `
const host = await hostOf();
const ticker = await host.get(Ticker, {}, BACKGROUND_CONTEXT);
const again = await host.get(Ticker, {}, BACKGROUND_CONTEXT);
const log = [];
const events = createEvents({ port: { control: (message) => void log.push(message) }, host: () => host });
const at = 1700000000123;
Date.now = () => at;
await ticker.ticks.on((delivery) => void log.push("listener " + delivery.value.y), BACKGROUND_CONTEXT);
// Emitted before the taps and flushed after them: not theirs.
sources.ticks.emit({ y: 0 });
const untap = events.tap(ticker, 7, ["ticks", "tocks"]);
const untapAgain = events.tap(again, 8, ["tocks", "ticks"]);
let encodes = 0;
sources.ticks.emit({ y: 1, toJSON() { encodes++; return { y: 1 }; } });
sources.tocks.emit({ y: 2 });
assert.deepEqual(log, [], "written at the emit, not at the flush");
await turn();
assert.deepEqual(log, [
	{ event: "event", events: [
		{ hold: 7, member: 0, at, value: '{"y":1}' },
		{ hold: 8, member: 1, at, value: '{"y":1}' },
		{ hold: 7, member: 1, at, value: '{"y":2}' },
		{ hold: 8, member: 0, at, value: '{"y":2}' },
	] },
	"listener 0",
	"listener 1",
]);
assert.equal(encodes, 1, "an entry reaching two holds is encoded once");
log.length = 0;
untap();
sources.ticks.emit({ y: 3 });
await turn();
assert.deepEqual(log, [{ event: "event", events: [{ hold: 8, member: 1, at, value: '{"y":3}' }] }, "listener 3"]);
log.length = 0;
untapAgain();
sources.ticks.emit({ y: 4 });
await turn();
assert.deepEqual(log, ["listener 4"]);
await host.close(BACKGROUND_CONTEXT);`)
	})
}

// The default event window (Pi host/host.ts:654, `options.eventWindow ?? 32`) at its bound, with the numbers of
// TestTheDefaultEventWindowSendsThe32ndUnacknowledgedMessageAndDropsThe33rd: the 32nd unacknowledged message is
// written, the 33rd is dropped as visited (Pi remote/provider.ts:369-372): never blocked, never refused, never queued.
// One ack frees one slot.
func TestNodeEventsWriteThe32ndUnacknowledgedMessageAndDropThe33rd(t *testing.T) {
	runVendored(t, func(pi func(string) string) string {
		return evScript(pi, `
const host = await hostOf();
const ticker = await host.get(Ticker, {}, BACKGROUND_CONTEXT);
const port = recording();
const events = createEvents({ port, host: () => host });
events.tap(ticker, 0, ["ticks"]);
await emitTurns(1, 32);
assert.equal(port.written.length, 32, "messages with 32 outstanding (the window is 32)");
await emitTurns(33, 33);
assert.deepEqual(ys(port), range(1, 32), "the 33rd outstanding message was written");
events.acked(1);
await emitTurns(34, 35);
assert.deepEqual(ys(port), [[34]], "after one ack");
await host.close(BACKGROUND_CONTEXT);`)
	})
}

// An ack is a count: one that counts messages never written, or a message a second time, frees only what is
// unacknowledged, and the window never grows past the host's `eventWindow` (Pi remote/provider.ts:681-684), with the
// numbers of TestAnEventAckForMessagesNeverSentOrCountedTwiceIsIgnoredAndOpensNoSlot.
func TestNodeEventsAckForMessagesNeverWrittenOrCountedTwiceIsIgnoredAndOpensNoSlot(t *testing.T) {
	runVendored(t, func(pi func(string) string) string {
		return evScript(pi, `
async function open() {
	const host = await hostOf({ eventWindow: 2 });
	const ticker = await host.get(Ticker, {}, BACKGROUND_CONTEXT);
	const port = recording();
	const events = createEvents({ port, host: () => host });
	events.tap(ticker, 0, ["ticks"]);
	return { host, port, events };
}
{
	const { host, port, events } = await open();
	events.acked(5);
	await emitTurns(1, 4);
	assert.deepEqual(ys(port), [[1], [2]], "an ack for five messages when none was written: the window stays 2");
	await host.close(BACKGROUND_CONTEXT);
}
{
	const { host, port, events } = await open();
	await emitTurns(1, 1);
	events.acked(3);
	await emitTurns(2, 5);
	assert.deepEqual(ys(port), [[1], [2], [3]], "an ack counting more than is outstanding frees only what is outstanding");
	await host.close(BACKGROUND_CONTEXT);
}
{
	const { host, port, events } = await open();
	await emitTurns(1, 1);
	events.acked(1);
	events.acked(1);
	await emitTurns(2, 5);
	assert.deepEqual(ys(port), [[1], [2], [3]], "the same message acknowledged twice frees one slot");
	await host.close(BACKGROUND_CONTEXT);
}`)
	})
}

// A listener that emits while a flush calls it queues the next flush: its event crosses in a message of its own, after
// the first, and takes a slot of its own, as on a link (Pi host/events.ts:104-106: the calls follow the senders, "what
// they emit goes into the next flush"). With 31 messages unacknowledged the flush of 32 is the 32nd message and is
// written; the flush of the 33, which the listener emitted for the 32, finds the window full and is dropped.
func TestNodeEventsAListenersEmitCrossesInTheNextFlushsMessageAndTakesItsOwnSlot(t *testing.T) {
	runVendored(t, func(pi func(string) string) string {
		return evScript(pi, `
const host = await hostOf();
const ticker = await host.get(Ticker, {}, BACKGROUND_CONTEXT);
const port = recording();
const events = createEvents({ port, host: () => host });
// Subscribed before the bind: it is called before anything subscribed after it.
await ticker.ticks.on((delivery) => {
	if (delivery.value.y === 32) sources.ticks.emit({ y: 33 });
	if (delivery.value.y === 1) sources.ticks.emit({ y: 2 });
}, BACKGROUND_CONTEXT);
events.tap(ticker, 0, ["ticks"]);
await emitTurns(1, 1);
assert.deepEqual(ys(port), [[1], [2]], "the emit of a listener crossed in the message of the flush that called it");
await emitTurns(3, 31);
assert.equal(port.written.length, 29);
await emitTurns(32, 32);
assert.deepEqual(ys(port).slice(29), [[32]], "the 32nd message carries the flush of 32 alone; the 33 was the 33rd");
events.acked(1);
await emitTurns(34, 35);
assert.deepEqual(ys(port), [[34]], "the dropped 33 took no slot");
await host.close(BACKGROUND_CONTEXT);`)
	})
}

// The members of the vendored host the bridge's events use are public in Pi's source and not part of the package's API:
// events.mjs checks each where it uses it and throws an error that names it, so a pin that moved one fails here at once
// with that message, and a subprocess fails with it at the first event, never with an arrival stamp or a wrong count.
// The members, at the vendored pin: `ChordHost.eventWindow`, `events`, `metrics` (host/host.ts:558, :560, :580);
// `HostEvents.counter`, `push(node, value, at)` (host/events.ts:71, :76); `EventNode.tapped`, `tap(tap)`, `clear()`
// (:112, :116, :121, :136); `ReferenceEvents.binding` (host/reference-events.ts:23); `Binding.forwardEvents(member, node)`,
// `unforwardEvents(member, node)` (host/reference.ts:609, :614); `jsonPart`, `parsePart` (link/values.ts:12, :48).
func TestNodeEventsSayWhichMemberOfTheVendoredHostIsMissingOrHasAnotherArity(t *testing.T) {
	runVendored(t, func(pi func(string) string) string {
		return evScript(pi, `
const host = await hostOf();
const ticker = await host.get(Ticker, {}, BACKGROUND_CONTEXT);
// The vendored host has every member: a throw here is the pin having moved one, and names it.
const internals = eventInternals(host);
assert.equal(internals.events, host.events);
assert.equal(internals.eventWindow, 32);
const untap = createEvents({ port: recording(), host: () => host }).tap(ticker, 1, ["ticks", "tocks"]);
untap();
const shape = { events: { counter: 0, push(node, value, at) {} }, eventWindow: 32, metrics: { counter(name) {} } };
eventInternals(shape);
const refuses = (run, what) =>
	assert.throws(run, { message: "events cannot cross the plugins bridge: the vendored chord host has no " + what + " any more" });
refuses(() => eventInternals({ ...shape, events: { counter: 0, push(node, value) {} } }), "`+"`events.push(node, value, at)`"+` (host/events.ts)");
refuses(() => eventInternals({ ...shape, events: undefined }), "`+"`events.push(node, value, at)`"+` (host/events.ts)");
refuses(() => eventInternals({ ...shape, events: { push(node, value, at) {} } }), "`+"`events.counter`"+` (host/events.ts)");
refuses(() => eventInternals({ ...shape, eventWindow: undefined }), "`+"`eventWindow`"+` (host/host.ts)");
refuses(() => eventInternals({ ...shape, metrics: {} }), "`+"`metrics.counter(name)`"+` (telemetry.ts)");
const forwarding = "`+"`binding.forwardEvents(member, node)` and `binding.unforwardEvents(member, node)`"+` on a reference's event member (host/reference-events.ts, host/reference.ts)";
const tapOf = (reference) => () => createEvents({ port: recording(), host: () => host }).tap(reference, 1, ["ticks"]);
refuses(tapOf({ ticks: { on() {} } }), forwarding);
refuses(tapOf({ ticks: { binding: { forwardEvents(member) {}, unforwardEvents(member, node) {} } } }), forwarding);
refuses(tapOf({}), forwarding);
// A flush that hands a tap something else than a sender's collector and a stamped entry.
const fed = [];
const fake = { ticks: { binding: { forwardEvents(member, node) { fed.push(node); }, unforwardEvents(member, node) {} } } };
createEvents({ port: recording(), host: () => host }).tap(fake, 1, ["ticks"]);
refuses(
	() => fed[0].deliver({ n: host.events.counter + 1, value: 1, at: 1 }, { call() {} }),
	"`+"`flush.sending(sender)` and an entry's `value` and `at` in a tap's `deliver(entry, flush)`"+` (host/events.ts)",
);
await host.close(BACKGROUND_CONTEXT);`)
	})
}
