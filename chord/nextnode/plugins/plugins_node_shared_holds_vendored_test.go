package plugins

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/next/telemetryingest"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Not upstream's: the sharing of one wire hold (Pi 47e9d74f9186 + 90ed3e3b14b5, decision "References share one wire
// hold") as the VENDORED TypeScript does it, seen from a Go provider. Pi's own cases for it (test/remote/
// remote-object.test.ts and others) are ported against the Go host; nothing else in PiG runs Pi's host.ts,
// remote-object.ts and consumer.ts as a consumer and reads what they send. Here a Go host serves one remote counter and
// a Node process runs Pi's host from a vendored tree as its consumer, on a link carried over the process's pipes; the
// test stands on that link and counts the `hold`, `release`, `call` and `abort` messages the Node host sends.
//
// A second test (below) runs the client tree's page sender against the Go ingest facet in the same way.
//
// The two vendored trees are run alike: the Node host's (plugins/node, extracted as the subprocess gets it) and the
// client's (surfaces/client, the page's and the terminal client's runtime; its sources are copied beside its pi-schema
// in the extracted tree's layout, since Node strips no types under node_modules).
//
// The steps are the consumer's; after each it says a word on the link's own pipe (a marker frame written after every
// message of the step), so what the test counts at a word is everything the consumer sent before it:
//   - two references to one object (same type, version, params, placed address, principal, incarnation): ONE `hold`;
//   - each reference calls `slow`; the first reference is released while both calls run: ONE `abort`, of its own call,
//     its call rejects `released`, the provider's handler of that call ends and the other runs on; no `release`;
//   - the other call returns; the second reference is released: the ONE `release`;
//   - a reference made after it holds anew: a second `hold`.

// shServer is the consumer's `hostKind("server")`.
var shServer = mustKind(contracts.NewHostKind("server"))

// shCounter is the counter both sides declare: one count, and a method that returns when the test lets it.
var shCounter = mustContract(contracts.Remote("test.shared-counter", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{},
	HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return shServer.Address(nil), nil },
	Variants: []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{
		State: []contracts.Property{contracts.Prop("n", contracts.Int())},
		Methods: []contracts.MethodEntry{contracts.Meth("slow", contracts.NewMethod(
			contracts.Obj(contracts.Prop("who", contracts.Str())), contracts.Str()))},
	})},
}))

// shConsumer is the consumer's script over a vendored tree: Pi's chord, a link over the process's pipes, a host connected
// by it (the peer is the server), then steps, then the host's close.
// A frame is its length (4 bytes, big endian), a type byte (0: a link message, 1: a word) and the payload.
func shConsumer(pi func(string) string, steps string) string {
	return strings.Join([]string{
		`import assert from "node:assert/strict";`,
		"const chord = await import(" + pi("index.ts") + ");",
		"const { BACKGROUND_CONTEXT } = await import(" + pi("context/index.ts") + ");",
		`const { binaryCodec, createHost, hostKind, int, method, obj, remote, str } = chord;
function write(type, payload) {
	const frame = Buffer.alloc(5 + payload.length);
	frame.writeUInt32BE(payload.length + 1, 0);
	frame[4] = type;
	frame.set(payload, 5);
	process.stdout.write(frame);
}
const pipe = { listener: undefined, queued: [], closed: false, onClose: undefined };
function end() {
	if (pipe.closed) return;
	pipe.closed = true;
	pipe.onClose?.();
}
/** The link: a message is one frame of type 0 on the process's pipes. */
const stdio = {
	send: (message) => void (pipe.closed || write(0, message)),
	onMessage: (listener) => {
		pipe.listener = listener;
		for (const message of pipe.queued.splice(0)) listener(message);
	},
	close: end,
	onClose: (listener) => (pipe.closed ? listener() : void (pipe.onClose = listener)),
};
const heard = new Set();
const waiting = new Map();
let pending = Buffer.alloc(0);
process.stdin.on("data", (chunk) => {
	pending = Buffer.concat([pending, chunk]);
	while (pending.length >= 4 && pending.length >= 4 + pending.readUInt32BE(0)) {
		const size = pending.readUInt32BE(0);
		const type = pending[4];
		const payload = new Uint8Array(pending.subarray(5, 4 + size));
		pending = pending.subarray(4 + size);
		if (type === 0) {
			if (pipe.listener === undefined) pipe.queued.push(payload);
			else pipe.listener(payload);
		} else {
			const word = Buffer.from(payload).toString();
			heard.add(word);
			waiting.get(word)?.();
		}
	}
});
process.stdin.on("end", end);
/** Says word to the test, after every link message written so far. */
const say = (word) => write(1, Buffer.from(word));
/** Resolves once the test said word. */
const told = (word) => (heard.has(word) ? Promise.resolve() : new Promise((resolve) => waiting.set(word, resolve)));
const turn = () => new Promise((resolve) => setImmediate(resolve));

const reported = [];
process.on("unhandledRejection", (error) => {
	console.error("unhandled:", error, "reported by the host:", reported);
	process.exit(1);
});
const host = createHost({ onError: (error) => reported.push(error) });
await host.load([], BACKGROUND_CONTEXT);
host.connect(stdio, { principal: { kind: "server" }, codec: binaryCodec }, BACKGROUND_CONTEXT);

` + steps + `
await host.close(BACKGROUND_CONTEXT);
assert.deepEqual(reported, []);
say("done");`,
	}, "\n")
}

// shSharingSteps are the consumer's steps of the sharing test.
const shSharingSteps = `
const Server = hostKind("server");
const Counter = remote("test.shared-counter", {
	version: 1,
	params: {},
	hostAddress: () => Server(),
	variants: { v: { state: { n: int() }, methods: { slow: method(obj({ who: str() }), str()) } } },
});

const a = await host.get(Counter, {}, BACKGROUND_CONTEXT);
const b = await host.get(Counter, {}, BACKGROUND_CONTEXT);
assert.notEqual(a, b, "two references");
assert.deepEqual([a.variant, a.n.value, b.variant, b.n.value], ["v", 1, "v", 1]);
await turn();
say("got twice");

const first = a.slow({ who: "a" }, BACKGROUND_CONTEXT).then(() => "returned", (error) => error.code);
const second = b.slow({ who: "b" }, BACKGROUND_CONTEXT);
await told("both calls run");
a.release();
assert.equal(await first, "released", "the released reference's call");
await turn();
say("released a");

assert.equal(await second, "b", "the other reference's call");
assert.equal(b.n.value, 1);
b.release();
await turn();
say("released b");

const c = await host.get(Counter, {}, BACKGROUND_CONTEXT);
assert.equal(c.n.value, 1);
say("got anew");
c.release();`

// shWire is what the consumer sent so far, by kind: the holds and releases, and the ids of its calls and aborts in the
// order they were sent.
type shWire struct {
	mu              sync.Mutex
	holds, releases int
	calls, aborts   []int
	// trace is the kinds of the messages of both directions in the order the test saw them (`>`: the consumer's).
	trace []string
}

// provided notes a message of the provider on its way to the consumer.
func (w *shWire) provided(message []byte) {
	kind := "undecoded"
	if decoded, err := link.BinaryCodec.Decode(message); err == nil {
		kind = decoded.Kind()
	}
	w.mu.Lock()
	w.trace = append(w.trace, "<"+kind)
	w.mu.Unlock()
}

func (w *shWire) count(message []byte) error {
	decoded, err := link.BinaryCodec.Decode(message)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.trace = append(w.trace, ">"+decoded.Kind())
	switch sent := decoded.(type) {
	case *link.HoldMessage:
		w.holds++
	case *link.ReleaseMessage:
		w.releases++
	case *link.CallMessage:
		w.calls = append(w.calls, sent.Call)
	case *link.AbortMessage:
		w.aborts = append(w.aborts, sent.Call)
	}
	return nil
}

// shCounts is the consumer's messages up to one point of its pipe.
type shCounts struct {
	holds, releases int
	calls, aborts   []int
}

// snapshot is what the consumer sent so far.
func (w *shWire) snapshot() shCounts {
	w.mu.Lock()
	defer w.mu.Unlock()
	return shCounts{holds: w.holds, releases: w.releases, calls: slices.Clone(w.calls), aborts: slices.Clone(w.aborts)}
}

// shWord is a word of the consumer with what it had sent when the word was read from its pipe: the reader goes on to
// the next frames while the test looks at this one.
type shWord struct {
	text string
	at   shCounts
}

func (w *shWire) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return fmt.Sprintf("holds %d, releases %d, calls %v, aborts %v; messages %v", w.holds, w.releases, w.calls, w.aborts, w.trace)
}

// shFrame writes one frame.
func shFrame(to io.Writer, kind byte, payload []byte) error {
	frame := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(frame, uint32(len(payload)+1))
	frame[4] = kind
	copy(frame[5:], payload)
	_, err := to.Write(frame)
	return err
}

// shClientTree is the client tree's Pi sources laid out as the extracted Node tree is (pi/chord/src, pi/schema,
// pi/node_modules), in a temporary directory.
func shClientTree(t *testing.T) string {
	t.Helper()
	client := filepath.Join("..", "surfaces", "client", "pi")
	tree := t.TempDir()
	for from, to := range map[string]string{
		filepath.Join("chord", "src"):                                    filepath.Join("pi", "chord", "src"),
		filepath.Join("node_modules", "@earendil-works", "pi-schema"):    filepath.Join("pi", "schema"),
		filepath.Join("node_modules", "@earendil-works", "pi-telemetry"): filepath.Join("pi", "node_modules", "@earendil-works", "pi-telemetry"),
	} {
		if err := os.CopyFS(filepath.Join(tree, to), os.DirFS(filepath.Join(client, from))); err != nil {
			t.Fatal(err)
		}
	}
	return tree
}

// shTrees are the two vendored trees a consumer runs from.
var shTrees = []struct {
	name string
	tree func(t *testing.T) string
}{
	{"the Node host's tree (plugins/node)", func(t *testing.T) string {
		tree, err := extractNode(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return tree
	}},
	{"the client tree (surfaces/client)", shClientTree},
}

// shSession is a Go provider with a Node consumer on its link, the test standing between them.
type shSession struct {
	t        *testing.T
	provider host.Host
	wire     *shWire
	// words are the consumer's words; exited is its end; at is the consumer's messages up to the last word heard.
	words  chan shWord
	at     shCounts
	exited chan error
	stdin  io.WriteCloser
	stderr *bytes.Buffer
	// tell says a word to the consumer.
	tell func(word string)

	mu       sync.Mutex
	reported []string
}

// shStart starts a provider host with facet, at the server's address, and the consumer over tree running steps. The
// node is the one of the PATH and a machine without one skips, as the package's other tests over a vendored tree do
// (runVendored). The consumer is killed after two minutes: the bound of every wait of a session.
func shStart(t *testing.T, tree string, facet host.Facet, steps func(pi func(string) string) string) *shSession {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on the PATH")
	}
	session := &shSession{t: t, wire: &shWire{}, words: make(chan shWord, 8), exited: make(chan error, 1), stderr: &bytes.Buffer{}}
	address := shServer.Address(nil)
	session.provider, err = host.CreateHost(host.HostOptions{
		Address: &address, Principal: serverPrincipal,
		OnError: func(err host.HostError) {
			session.mu.Lock()
			session.reported = append(session.reported, fmt.Sprint(err))
			session.mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.provider.Close(background) })
	if _, err := session.provider.Load([]host.HostFacet{{Name: "@test/provider", Facet: facet}}, background); err != nil {
		t.Fatal(err)
	}

	pi := func(path string) string {
		return pcJSON(t, fileURL(filepath.Join(tree, "pi", "chord", "src", filepath.FromSlash(path))))
	}
	script := strings.Join([]string{
		`import { registerHooks } from "node:module";`,
		"registerHooks({ resolve(specifier, context, next) {",
		fmt.Sprintf("  if (specifier === \"@earendil-works/pi-schema\") return { url: %s, shortCircuit: true };",
			pcJSON(t, fileURL(filepath.Join(tree, "pi", "schema", "src", "index.ts")))),
		"  return next(specifier, context);",
		"} });",
		shConsumer(pi, steps(pi)),
	}, "\n")
	ctx, cancel := context.WithTimeout(background, 2*time.Minute)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, node, "--experimental-strip-types", "--no-warnings", "--input-type=module", "--eval", script)
	cmd.Stderr = session.stderr
	if session.stdin, err = cmd.StdinPipe(); err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	// The test stands on the link: what the provider sends goes to the consumer's stdin; what the consumer writes is
	// counted, then given to the provider.
	near, far := link.MemoryLinkPair()
	var writing sync.Mutex
	toConsumer := func(kind byte, payload []byte) {
		writing.Lock()
		defer writing.Unlock()
		_ = shFrame(session.stdin, kind, payload)
	}
	session.tell = func(word string) { toConsumer(1, []byte(word)) }
	far.OnMessage(func(message []byte) {
		session.wire.provided(message)
		toConsumer(0, message)
	})
	// A link the provider closed is the consumer's end of input.
	far.OnClose(func() { _ = session.stdin.Close() })
	go func() {
		head := make([]byte, 4)
		for {
			if _, err := io.ReadFull(stdout, head); err != nil {
				break
			}
			frame := make([]byte, binary.BigEndian.Uint32(head))
			if _, err := io.ReadFull(stdout, frame); err != nil || len(frame) == 0 {
				break
			}
			if frame[0] == 1 {
				session.words <- shWord{text: string(frame[1:]), at: session.wire.snapshot()}
				continue
			}
			if err := session.wire.count(frame[1:]); err != nil {
				session.words <- shWord{text: "not a message of the binary codec: " + err.Error()}
			}
			far.Send(frame[1:])
		}
		session.exited <- cmd.Wait()
	}()
	if err := session.provider.Accept(near, host.LinkOptions{Principal: delta.JsonObjectOf("kind", "owner"), Codec: link.BinaryCodec}, background); err != nil {
		t.Fatal(err)
	}
	return session
}

// state is what a failure shows: the wire, what the provider reported, the consumer's stderr.
func (s *shSession) state() string {
	s.mu.Lock()
	reported := slices.Clone(s.reported)
	s.mu.Unlock()
	return fmt.Sprintf("%v; the provider reported %v\n%s", s.wire, reported, s.stderr.String())
}

// hear waits for the consumer's next word.
func (s *shSession) hear(want string) {
	s.t.Helper()
	select {
	case got := <-s.words:
		if got.text != want {
			s.t.Fatalf("the consumer said %q, want %q (%s)", got.text, want, s.state())
		}
		s.at = got.at
	case err := <-s.exited:
		s.t.Fatalf("the consumer ended before %q: %v (%s)", want, err, s.state())
	}
}

// receive waits for the provider's next handler event.
func (s *shSession) receive(what string, from <-chan string) string {
	s.t.Helper()
	select {
	case got := <-from:
		return got
	case err := <-s.exited:
		s.t.Fatalf("the consumer ended before %s: %v (%s)", what, err, s.state())
		return ""
	}
}

// check compares what the consumer had sent at the last word heard.
func (s *shSession) check(step string, holds, releases int, calls int, aborts []int) {
	s.t.Helper()
	if s.at.holds != holds || s.at.releases != releases || len(s.at.calls) != calls || !slices.Equal(s.at.aborts, aborts) {
		s.t.Fatalf("%s: holds %d, releases %d, calls %v, aborts %v, want holds %d, releases %d, %d calls, aborts %v",
			step, s.at.holds, s.at.releases, s.at.calls, s.at.aborts, holds, releases, calls, aborts)
	}
}

// done hears the consumer's last word and waits for its end (no error of its own); it returns what the provider's host
// reported in the background.
func (s *shSession) done() []string {
	s.t.Helper()
	s.hear("done")
	_ = s.stdin.Close()
	if err := <-s.exited; err != nil {
		s.t.Fatalf("the consumer: %v (%s)", err, s.state())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.reported)
}

func TestTheVendoredHostsReferencesOfOneObjectShareOneWireHoldOfAGoProvider(t *testing.T) {
	for _, each := range shTrees {
		t.Run(each.name, func(t *testing.T) {
			// The Go provider: the counter, its `slow` running until the test lets it return or its call is aborted.
			started, ended := make(chan string, 2), make(chan string, 2)
			proceed := make(chan struct{})
			release := sync.OnceFunc(func() { close(proceed) })
			facet := host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
				return h.Serve(shCounter, func(*delta.JsonObject, *delta.JsonObject, host.Self) (contracts.Implementation, error) {
					return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
						State: map[string]chord.Watchable[delta.JsonValue]{"n": host.Expose(state.NewConstant(1.0))},
						Methods: map[string]contracts.MethodFunction{
							"slow": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
								who, _ := args.(*delta.JsonObject).Value("who").(string)
								started <- who
								// The handler waits for the test, not for the host: the section that admitted its call goes on.
								chordctx.Suspend(ctx)
								select {
								case <-ctx.Done():
									ended <- who + " aborted"
									return nil, context.Cause(ctx)
								case <-proceed:
									ended <- who + " returned"
									return who, nil
								}
							},
						},
					}}, nil
				}, host.ServeOptions{})
			})
			session := shStart(t, each.tree(t), facet, func(func(string) string) string { return shSharingSteps })
			// Whatever the outcome, a handler still waiting returns before the provider closes (cleanups run last first).
			t.Cleanup(release)

			// Two references to one object: one hold.
			session.hear("got twice")
			session.check("two references got", 1, 0, 0, nil)

			// Both calls run at the provider. The consumer called on the first reference first, and a link keeps the order:
			// the first call id on the wire is the first reference's.
			running := []string{session.receive("the first call's handler", started), session.receive("the second call's handler", started)}
			slices.Sort(running)
			if !slices.Equal(running, []string{"a", "b"}) {
				t.Fatalf("handlers running: %v, want a and b", running)
			}
			// No word here: the consumer waits for the test's, so what it sent is what it will have sent until then.
			session.at = session.wire.snapshot()
			session.check("both calls sent", 1, 0, 2, nil)
			own := session.at.calls[0]
			session.tell("both calls run")

			// The first reference released with its call in flight: its call alone is aborted, the hold stays.
			session.hear("released a")
			session.check("the first reference released", 1, 0, 2, []int{own})
			if got := session.receive("the aborted call's handler ending", ended); got != "a aborted" {
				t.Fatalf("the handler that ended: %q, want a's, aborted", got)
			}
			select {
			case got := <-ended:
				t.Fatalf("a second handler ended: %q, want b's still running", got)
			default:
			}

			// The other call returns; the second release is the hold's.
			release()
			session.hear("released b")
			session.check("the second reference released", 1, 1, 2, []int{own})
			if got := session.receive("the other call's handler ending", ended); got != "b returned" {
				t.Fatalf("the handler that ended: %q, want b's, returned", got)
			}

			// A reference made after the last release holds anew.
			session.hear("got anew")
			session.check("a reference made after the release", 2, 1, 2, []int{own})
			if reported := session.done(); len(reported) > 0 {
				t.Fatalf("the provider reported: %v", reported)
			}
		})
	}
}

// shIngested counts what the ingest facet was given to write.
type shIngested struct {
	mu      sync.Mutex
	batches int
}

func (i *shIngested) Ingest(batch telemetryingest.TelemetryBatch) int {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.batches++
	return len(batch.Spans)
}

// Not upstream's: Pi 7ae9ff8c7 gave the ingest facet `admit(principal)`; the Go port has it (IngestOptions.Admit) and the
// client tree's facet.ts has admit too (TestTheClientTreesIngestFacetAsksAdmit); the server is Go, so nothing serves
// that file. What a page of
// a principal the Go server does not admit meets is the vendored SENDER's side of it: the page's sender
// (sendPageTelemetry of the client tree, as web/page.ts starts it) holds `chord.telemetry.page`, the Go facet refuses
// the hold `forbidden` "chord.telemetry.page: not admitted", the sender sends no batch (its object is not live) and its
// span waits in the recorder; nothing reaches the target, and the server's host reports the failed activation.
func TestTheClientTreesPageSenderIsRefusedByAGoIngestFacetThatDoesNotAdmitItsPrincipal(t *testing.T) {
	target := &shIngested{}
	var asked []string
	var askedMu sync.Mutex
	facet := telemetryingest.IngestFacet(telemetryingest.IngestOptions{
		Target: target,
		Admit: func(principal data.Principal) bool {
			askedMu.Lock()
			defer askedMu.Unlock()
			kind, _ := principal.Value("kind").(string)
			asked = append(asked, kind)
			return false
		},
		WorkerHost:          func(data.Principal) (string, bool) { return "", false },
		PrincipalAttributes: func(data.Principal) telemetry.SpanAttributes { return nil },
	})
	session := shStart(t, shClientTree(t), facet, func(pi func(string) string) string {
		return strings.Join([]string{
			"const { PageTelemetry } = await import(" + pi("telemetry-ingest/batch.ts") + ");",
			"const { sendPageTelemetry, TelemetryRecorder } = await import(" + pi("telemetry-ingest/index.ts") + ");",
			`const recorder = new TelemetryRecorder(() => "browser:test", 100);
await recorder.startSpan({ name: "page.click" }, () => undefined);
const sender = sendPageTelemetry(host, recorder, BACKGROUND_CONTEXT);
// A reference to the sender's own object (one hold: they share it) settles when the hold does.
const refused = await host
	.get(PageTelemetry, {}, BACKGROUND_CONTEXT)
	.then(() => ["bound"], (error) => [error.code, error.message]);
await sender.send(true);
await turn();
say("refused " + JSON.stringify({ refused, queued: recorder.queuedSpans }));
sender.stop();`,
		}, "\n")
	})
	session.hear(`refused {"refused":["forbidden","chord.telemetry.page: not admitted"],"queued":1}`)
	session.check("the refused page", 1, 0, 0, nil)
	askedMu.Lock()
	if len(asked) == 0 || slices.ContainsFunc(asked, func(kind string) bool { return kind != "owner" }) {
		t.Fatalf("Admit was asked about %v, want the link's principal (owner)", asked)
	}
	askedMu.Unlock()
	// The Go server's own account of the refusal: one failed activation, reported to the app (HostOptions.OnError).
	reported := session.done()
	if len(reported) != 1 || !strings.Contains(reported[0], "activation_failed") || !strings.Contains(reported[0], "chord.telemetry.page: not admitted") {
		t.Fatalf("the provider reported %v, want the one failed activation of chord.telemetry.page: not admitted", reported)
	}
	target.mu.Lock()
	defer target.mu.Unlock()
	if target.batches != 0 {
		t.Fatalf("the target was given %d batches, want none", target.batches)
	}
}
