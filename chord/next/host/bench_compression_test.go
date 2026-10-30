package host

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/nextnode"
)

// Ports the end-to-end half of packages/chord/test/next/bench/compression.ts: a 500 KB snapshot and a 1 MiB call result over
// one memory link, uncompressed, deflated and inflated synchronously (a Node client) and deflated and inflated
// asynchronously (a client without a compressor, a browser), as the bytes the client received and the time to a live
// hold and to a call's result. The other half (deflate at the origin, synchronous and asynchronous inflate of the two
// payloads) is chord/next/link/bench_test.go BenchmarkCompression. Pi reports the mean of 20; the Go form is `testing.B`
// (ns/op, allocs/op) with the received bytes as metrics. Run by hand (not in CI):
//
//	go test -run '^$' -bench BenchmarkCompressionEndToEnd ./chord/next/host/

var benchCompressionWords = strings.Fields("the a model tool call file read write test error result agent session note code run build")

// benchProse is about length characters of words, from a fixed pseudo-random sequence starting at seed: text that
// compresses like prose and code (about 3x at level 1), not like a repeated string.
func benchProse(seed, length int) string {
	state := seed*7919 + 1
	var words []string
	for total := 0; total < length; {
		state = (state*1103515245 + 12345) % 2147483648
		word := benchCompressionWords[state%len(benchCompressionWords)]
		if state%5 == 0 {
			word += strconv.Itoa(state % 1000)
		}
		words = append(words, word)
		total += len(word) + 1
	}
	text := strings.Join(words, " ")
	return text[:min(length, len(text))]
}

var (
	benchCompressionEntry = contracts.Obj(contracts.Prop("id", contracts.Int()), contracts.Prop("text", contracts.Str()),
		contracts.Prop("author", contracts.Str()), contracts.Prop("at", contracts.Int()))
	benchCompressionShape = contracts.Obj(contracts.Prop("entries", contracts.Arr(benchCompressionEntry)))
	benchCompressionStore = mustContract(contracts.Local("bench.store", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("doc", benchCompressionShape)},
	}))
	// benchCompressionView is a tab's view of the 500 KB document, and a method returning 1 MiB.
	benchCompressionView = remoteOneVariantNamed("bench.view", "owner", contracts.Params{}, contracts.VariantDeclaration{
		State:   []contracts.Property{contracts.Prop("doc", benchCompressionShape)},
		Methods: []contracts.MethodEntry{contracts.Meth("export", contracts.NewMethod(contracts.TSchema{}, contracts.Str()))},
	})
	benchCompressionResult = benchProse(1, 1024*1024)
	benchCompressionDoc    = func() *documents.StateDefinition {
		definition, err := documents.DefineState("doc", documents.StateOptions{
			Schema:  benchCompressionShape,
			Version: 1,
			Initial: func() *delta.JsonObject {
				// The 500 KB document: 500 entries of 960 characters with prose-like text.
				entries := make([]any, 500)
				for id := range entries {
					entries[id] = delta.JsonObjectOf("id", float64(id), "text", benchProse(id, 960), "author", "mario", "at", float64(1_700_000_000+id))
				}
				return delta.JsonObjectOf("entries", entries)
			},
		})
		if err != nil {
			panic(err)
		}
		return definition
	}()
)

// benchCompressionLinked is a server compressing with server and a client with client (nil: it inflates asynchronously),
// connected by one memory link whose client end counts the bytes it receives.
func benchCompressionLinked(b *testing.B, server, client link.Compression) (serverHost, clientHost Host, received func() int) {
	b.Helper()
	onError := func(err HostError) { b.Errorf("onError %s", err.Kind) }
	serverHost = newHost(b, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), Compression: server, OnError: onError})
	must(b, serverHost.Load([]HostFacet{{Name: "@bench/app", Facet: DefineFacet(func(host FacetHost, _ FacetSelf) error {
		if err := host.Serve(benchCompressionStore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"doc": Expose(self.State(benchCompressionDoc))},
			}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return host.Serve(benchCompressionView, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"doc": Expose(self.Get(benchCompressionStore, jsonObject()).Member("doc"))},
				Methods: map[string]contracts.MethodFunction{
					"export": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return benchCompressionResult, nil },
				},
			}}, nil
		}, ServeOptions{})
	})}}, background))
	clientHost = newHost(b, HostOptions{Compression: client, OnError: onError})
	must(b, clientHost.Load(nil, background))
	a, c := link.MemoryLinkPair()
	counted := newCountedLink(c)
	must(b, serverHost.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: link.BinaryCodec}, background))
	must(b, clientHost.Connect(counted, LinkOptions{Principal: serverPrincipal(), Codec: link.BinaryCodec}, background))
	return serverHost, clientHost, counted.received
}

func BenchmarkCompressionEndToEnd(b *testing.B) {
	for _, each := range []struct {
		label          string
		server, client link.Compression
	}{
		{"uncompressed", nil, nil},
		{"deflated, inflated synchronously (Node client)", nextnode.NodeCompression, nextnode.NodeCompression},
		{"deflated, inflated asynchronously (browser client)", nextnode.NodeCompression, nil},
	} {
		b.Run(each.label, func(b *testing.B) {
			server, client, received := benchCompressionLinked(b, each.server, each.client)
			get := func() *Reference {
				ref, err := client.Get(benchCompressionView, jsonObject(), background, GetOptions{})
				must(b, err)
				return ref
			}
			// Warm up: the store and the view activate once.
			get().Release()
			before := received()
			view := get()
			snapshotBytes := received() - before
			view.Release()
			b.Run("hold to live", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					get().Release()
				}
				b.ReportMetric(float64(snapshotBytes), "snapshot-bytes-received")
			})
			held := get()
			beforeCall := received()
			_, err := held.Call("export", nil, background)
			must(b, err)
			resultBytes := received() - beforeCall
			b.Run("call to result", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					_, err := held.Call("export", nil, background)
					must(b, err)
				}
				b.ReportMetric(float64(resultBytes), "result-bytes-received")
			})
			must(b, client.Close(background))
			must(b, server.Close(background))
		})
	}
}
