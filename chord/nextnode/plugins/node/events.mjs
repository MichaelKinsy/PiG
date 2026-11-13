// Events across the bridge between the Go host and this process's host (plugins_node_events.go is the Go side). In Pi the
// app's facets and the plugins' load into one host, so an event reaches a listener inside that host's flush; here the two
// hosts are joined by the control channel, which carries a link's own two messages (Pi remote/provider.ts `queueEvent`,
// `sendEvents`, `#eventAck`; remote/consumer.ts `#events`):
//
//   Go to Node   { op: "event", events: [{ hold, member, at, value }] }     one message per flush of a Go host
//   Node to Go   { event: "ack", events: n }                                n more of them were consumed here
//   Node to Go   { event: "event", events: [{ hold, member, at, value }] }  one message per flush of this host
//   Go to Node   { op: "ack", events: n }                                   n more of them were consumed there
//
// `hold` is the handle of the reference on the side that holds it, `member` an index into the type's event members in the
// order the Go side named them, `at` the emitting host's stamp and `value` the link's JSON part text (`jsonPart`), not the
// control channel's value form (wire.mjs): an event's value crosses as it crosses a link.
//
// Receiving (a proxy of a Go object, proxy.mjs): each entry goes to the proxy's own source for its member, with the Go
// host's stamp, as a remote object hands an arriving event to its node (Pi remote/remote-object.ts:525-547): dropped when
// nothing taps the source, else it takes this host's next counter value and waits for the event flush. A message is
// acknowledged once per inbound turn, after the flush delivered what it consumed (Pi remote/consumer.ts:312-320).
//
// Sending (a reference the Go side holds, bridge.mjs): the reference feeds one node per event member, as it feeds an
// object re-exposing the member (Pi host/reference-events.ts `ReexposedEvents`), tapped from the host's counter value at the
// bind. A flush's entries go as ONE message, written inside the flush before it calls its listeners, while fewer than the
// host's `eventWindow` are unacknowledged; a flush that finds the window full drops its entries as it visits them, counted
// `chord.events.dropped{reason=window}`, before anything is encoded. A subscription with `reference[name].on` could not
// do this: a listener sees entries, not flushes, and one earlier in the order that emits queues the next flush before a
// batch of entries could be closed, so two flushes would cross as one message and the window would count one.
//
// The members of the vendored host this file reaches are public there and not part of the package's API, so each use is
// checked where it is made: a pin that moved one fails with an error that names it, never with a wrong stamp or count.
const tree = new URL("./pi/chord/src/", import.meta.url);
const [{ EventNode }, { jsonPart, parsePart }] = await Promise.all([
	import(new URL("host/events.ts", tree).href),
	import(new URL("link/values.ts", tree).href),
]);

/** Throws that the vendored host lacks `what`, unless `present`. */
function need(present, what) {
	if (!present) throw new Error(`events cannot cross the plugins bridge: the vendored chord host has no ${what} any more`);
}

/** Whether `value` is a function declared with `parameters` parameters. */
const takes = (value, parameters) => typeof value === "function" && value.length === parameters;

/**
 * The members of the vendored `host` the bridge's events use, checked: its events (`push(node, value, at)`, `counter`), its
 * `eventWindow` and its `metrics`, with the event node and the link's JSON part this file imports.
 */
export function eventInternals(host) {
	need(takes(EventNode?.prototype?.tap, 1) && takes(EventNode.prototype.clear, 0), "`EventNode` with `tap(tap)` and `clear()` (host/events.ts)");
	need(Object.getOwnPropertyDescriptor(EventNode.prototype, "tapped")?.get !== undefined, "`EventNode.tapped` (host/events.ts)");
	need(takes(jsonPart, 1) && takes(parsePart, 1), "`jsonPart(value)` and `parsePart(json)` (link/values.ts)");
	const { events, eventWindow, metrics } = host;
	need(takes(events?.push, 3), "`events.push(node, value, at)` (host/events.ts)");
	need(typeof events.counter === "number", "`events.counter` (host/events.ts)");
	need(typeof eventWindow === "number", "`eventWindow` (host/host.ts)");
	need(takes(metrics?.counter, 1), "`metrics.counter(name)` (telemetry.ts)");
	return { events, eventWindow, metrics };
}

/** The binding of a reference's event member `source`, checked: what feeds a node (`forwardEvents`, `unforwardEvents`). */
function bindingOf(source) {
	const binding = source?.binding;
	need(
		takes(binding?.forwardEvents, 2) && takes(binding.unforwardEvents, 2),
		"`binding.forwardEvents(member, node)` and `binding.unforwardEvents(member, node)` on a reference's event member (host/reference-events.ts, host/reference.ts)",
	);
	return binding;
}

/**
 * The events of one driver's bridge: `port` is its control channel (`control(message)`) and `host()` the process's host
 * (undefined before `load`).
 */
export function createEvents({ port, host }) {
	let internals;
	const checked = () => (internals ??= eventInternals(host()));

	/** The sources of the proxies' event members by hold, each list in the order the Go side named the members. */
	const sinks = new Map();
	/** The `event` messages consumed in this inbound turn and not acknowledged yet. */
	let consumed = 0;

	/** One `event` message of the Go side. */
	function receive(message) {
		for (const { hold, member, at, value } of message.events) {
			const node = sinks.get(hold)?.[member];
			// An unknown or released hold, or a source nothing taps: dropped unparsed.
			if (node === undefined || !node.tapped) continue;
			checked().events.push(node, parsePart(value), at);
		}
		if (consumed++ > 0) return;
		queueMicrotask(() => {
			const events = consumed;
			consumed = 0;
			port.control({ event: "ack", events });
		});
	}

	/** This flush's admitted entries, until the flush sends them. */
	let batch = [];
	/** This host's `event` messages the Go side has not acknowledged: at most the host's `eventWindow`. */
	let inFlight = 0;
	/** The entry encoded last and its text: an entry's taps are visited together, so one reaching several holds is encoded once. */
	let encoded;
	let text;

	const sender = {
		/** The flush visited every entry: the admitted ones go as one message, taking a slot. */
		sendEvents() {
			const events = batch;
			batch = [];
			encoded = text = undefined;
			if (events.length === 0) return;
			inFlight++;
			checked().metrics.counter("chord.events.sent").add(events.length);
			port.control({ event: "event", events });
		},
	};

	/** A flush's entry for event member `member` of the reference held as `hold`. */
	function queue(hold, member, entry, flush) {
		const { eventWindow, metrics } = checked();
		if (inFlight >= eventWindow) {
			metrics.counter("chord.events.dropped").add(1, { reason: "window" });
			return;
		}
		if (batch.length === 0) {
			need(takes(flush?.sending, 1) && typeof entry?.at === "number", "`flush.sending(sender)` and an entry's `value` and `at` in a tap's `deliver(entry, flush)` (host/events.ts)");
			flush.sending(sender);
		}
		if (encoded !== entry) {
			encoded = entry;
			text = jsonPart(entry.value);
			metrics.counter("chord.events.encodes").add(1);
		}
		batch.push({ hold, member, at: entry.at, value: text });
	}

	/**
	 * Makes the Go side get what `reference` receives on its event members `names` from now on, under `hold`; `member` of an
	 * entry is the index of its member in `names`. Returns what ends it.
	 */
	function tap(reference, hold, names) {
		const boundAt = checked().events.counter;
		const fed = names.map((name, member) => {
			const binding = bindingOf(reference[name]);
			const node = new EventNode();
			node.tap({ boundAt, deliver: (entry, flush) => queue(hold, member, entry, flush) });
			binding.forwardEvents(name, node);
			return { binding, name, node };
		});
		return () => {
			for (const { binding, name, node } of fed) {
				binding.unforwardEvents(name, node);
				node.clear();
			}
		};
	}

	/** The Go side consumed `count` more of this host's messages: each frees a slot; one counting more frees only what is in flight. */
	function acked(count) {
		for (let freed = 0; freed < count && inFlight > 0; freed++) inFlight--;
	}

	return { sinks, receive, tap, acked };
}
