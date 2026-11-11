// The telemetry this process's host reports to the app's adapter in Go (RuntimeLoad.Telemetry,
// docs/plan/chord-plugins-node-evaluator.md 10.6): a TelemetryContext whose spans and metrics are ordered data events on the
// control channel, replayed by the Go side into the adapter. The events (`{ event: "telemetry", op, ... }`):
//
//   start   { span, parent?, name, attributes, links }   a span began; `parent` is a context reference (below), none for a root
//   attributes { span, attributes }, event { span, name, attributes }, link { span, context, attributes }, status { span, status }
//   end     { span, error? }   the callback settled; `error` is `{ name, message }` for a thrown Error, `{}` for another value
//   metric  { kind, name, description?, unit?, boundaries?, value, attributes, context? }   one measurement
//
// A context reference is `{ span: id }` (a span of this proxy) or `{ carrier }` (an extracted parent). Propagation needs no
// call to Go: `inject` of a span is the carrier `{ "pig-node-telemetry": "<scope>:<id>" }`, which the Go side resolves to the
// adapter's own carrier of that span; `inject` of an extracted parent is the carrier it was made from; `extract` keeps the raw
// carrier, and the Go side extracts it with the adapter (its own span ids resolved first). The behavior of a span follows
// pi-telemetry's InMemoryTelemetryContext: a span started from a settled span records nothing, a settled span ignores its
// setters, a link to a context this proxy did not make is dropped.

const CARRIER_KEY = "pig-node-telemetry";

/**
 * The proxy TelemetryContext (a TelemetrySpan, as a Context's `telemetry` is). `emit` sends one event; `scope` is the id the Go
 * side gave this process's carriers; `metrics` and `propagation` say which of the adapter's capabilities exist.
 */
export function createTelemetry({ emit, scope, metrics: withMetrics, propagation: withPropagation }) {
	let nextSpan = 0;
	/** The reference of every context this proxy made: a span's `{ span }`, an extracted parent's `{ carrier }`. */
	const references = new WeakMap();

	const referenceOf = (context) => (context === undefined || context === null ? undefined : references.get(context));

	const metrics = withMetrics
		? {
				counter: (name, options) => ({ add: (value, attributes) => measure("counter", name, options, value, attributes) }),
				upDownCounter: (name, options) => ({ add: (value, attributes) => measure("upDownCounter", name, options, value, attributes) }),
				histogram: (name, options) => ({
					record: (value, attributes, context) => measure("histogram", name, options, value, attributes, context),
				}),
			}
		: undefined;

	function measure(kind, name, options, value, attributes, context) {
		emit({
			op: "metric",
			kind,
			name,
			description: options?.description,
			unit: options?.unit,
			boundaries: kind === "histogram" ? options?.boundaries : undefined,
			value,
			attributes: attributes ?? {},
			context: referenceOf(context),
		});
	}

	const propagation = withPropagation
		? {
				inject(context) {
					const reference = referenceOf(context);
					if (reference === undefined) return undefined;
					if (reference.span !== undefined) return { [CARRIER_KEY]: `${scope}:${reference.span}` };
					return { ...reference.carrier };
				},
				extract(carrier) {
					if (typeof carrier !== "object" || carrier === null) return root;
					const parent = {
						metrics,
						propagation,
						startSpan: (options, callback) => start({ reference: { carrier: { ...carrier } } }, options, callback),
					};
					references.set(parent, { carrier: { ...carrier } });
					return parent;
				},
			}
		: undefined;

	/** A span that records nothing: what a span started from a settled span is. */
	function startInert(options, callback) {
		return callback({
			metrics,
			propagation,
			startSpan: startInert,
			addLink: () => {},
			addEvent: () => {},
			setAttributes: () => {},
			setStatus: () => {},
		});
	}

	function linkOf(link) {
		const context = referenceOf(link.context);
		return context === undefined ? undefined : { context, attributes: link.attributes ?? {} };
	}

	async function start(parent, options, callback) {
		if (parent.state?.settled) return startInert(options, callback);
		const span = ++nextSpan;
		const state = { settled: false };
		const reference = { span };
		const links = (options.links ?? []).map(linkOf).filter((link) => link !== undefined);
		emit({ op: "start", span, parent: parent.reference, name: options.name, attributes: options.attributes ?? {}, links });
		const proxy = {
			metrics,
			propagation,
			startSpan: (childOptions, childCallback) => start({ reference, state }, childOptions, childCallback),
			addLink(link) {
				const made = state.settled ? undefined : linkOf(link);
				if (made !== undefined) emit({ op: "link", span, ...made });
			},
			addEvent(name, attributes) {
				if (!state.settled) emit({ op: "event", span, name, attributes: attributes ?? {} });
			},
			setAttributes(attributes) {
				if (!state.settled) emit({ op: "attributes", span, attributes: attributes ?? {} });
			},
			setStatus(status) {
				if (!state.settled) emit({ op: "status", span, status });
			},
		};
		references.set(proxy, reference);
		let failure;
		try {
			return await callback(proxy);
		} catch (error) {
			failure = error instanceof Error ? { name: error.name, message: error.message } : {};
			throw error;
		} finally {
			state.settled = true;
			emit({ op: "end", span, error: failure });
		}
	}

	const root = {
		metrics,
		propagation,
		startSpan: (options, callback) => start({}, options, callback),
		addLink: () => {},
		addEvent: () => {},
		setAttributes: () => {},
		setStatus: () => {},
	};
	return root;
}
