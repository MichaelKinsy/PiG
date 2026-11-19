//go:build go1.27 && goexperiment.jsonv2

package ai

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"
)

// A splice kept between requests (acquireSplice, releaseSplice) must hold nothing of the request it served: the body of a request is the same bytes through a kept
// splice, after any earlier request, as through a splice made for it.

// keptRequest is one request of the corpus: its payload, and a marker that is in a large string of it and in no other request.
type keptRequest struct {
	name    string
	marker  string
	payload any
}

// markedText is a string over requestSpliceMinBytes that holds marker, so the request that carries it is spliced and a leak of it into another body can be found.
func markedText(marker string, size int) string {
	var builder strings.Builder
	for builder.Len() < size {
		builder.WriteString(marker)
		builder.WriteString(" filler <&> \u2028 ")
	}
	return builder.String()
}

// keptCache is the cache of a request, with the message cache or without.
func keptCache(withMessages bool) *quotedStringCache {
	cache := newQuotedStringCache(1 << 30)
	if !withMessages {
		cache.messages = nil
	}
	return cache
}

// keptCorpus is requests of both payload types that the serialized-shape tests use: anthropic blocks of every kind (tool calls, images, cache markers) and openai-completions
// messages (tool calls, image parts, cache markers), each with a marker of its own in a large string, and one large body.
func keptCorpus(random *rand.Rand, tag string, large bool) []keptRequest {
	var out []keptRequest
	clean := anthSpliceStrings(random)
	for i := range 4 {
		marker := fmt.Sprintf("pig-kept-%s-anthropic-%d", tag, i)
		text := markedText(marker, 2*requestSpliceMinBytes)
		request := anthSpliceRequest(random, clean)
		request.Messages = append(request.Messages,
			anthMessage{Role: "user", Content: []anthContentBlock{
				{Type: "text", Text: text, CacheControl: &anthCacheControl{Type: "ephemeral"}},
				{Type: "tool_use", ID: "toolu_k", Name: "bash", Input: map[string]any{"command": text}},
				{Type: "tool_result", ToolUseID: "toolu_k", Content: text},
				{Type: "image", Source: &anthImageSource{Type: "base64", MediaType: "image/png", Data: text}},
			}},
			anthMessage{Role: "assistant", Content: text})
		wire, _ := request.wire()
		out = append(out, keptRequest{name: "anthropic", marker: marker, payload: wire})
	}
	for i := range 4 {
		marker := fmt.Sprintf("pig-kept-%s-completions-%d", tag, i)
		text := markedText(marker, 2*requestSpliceMinBytes)
		part := text
		request := oaiRequest{Model: "m", Messages: []oaiMessage{
			{Role: "system", Content: "short"},
			{Role: "user", Content: []oaiContentPart{
				{Type: "text", Text: &part, CacheControl: &oaiCacheControl{Type: "ephemeral"}},
				{Type: "image_url", ImageURL: &struct {
					URL string `json:"url"`
				}{URL: "data:image/png;base64," + text}},
			}},
			{Role: "assistant", Content: "", ToolCalls: []oaiRequestToolCall{{ID: "c1", Type: "function", Function: &oaiRequestToolCallFunction{Name: "bash", Arguments: text}}}},
			{Role: "tool", ToolCallID: "c1", Content: text},
			{Role: "user", Content: spliceTestStrings(random)[3] + marker},
		}}
		out = append(out, keptRequest{name: "completions", marker: marker, payload: request})
	}
	if large {
		marker := "pig-kept-" + tag + "-large"
		request := oaiRequest{Model: "m", Messages: []oaiMessage{{Role: "tool", ToolCallID: "c", Content: markedText(marker, 6<<20)}}}
		out = append(out, keptRequest{name: "completions-large", marker: marker, payload: request})
	}
	return out
}

// freshBody is the body of payload through a splice made for it and a cache of its own: what a request got before splices were kept.
func freshBody(t testing.TB, payload any, withMessages bool) []byte {
	t.Helper()
	cache := keptCache(withMessages)
	cache.beginRequest()
	if cache.messages != nil {
		cache.messages.beginRequest()
	}
	splice := &stringSplice{cache: cache, nonce: newSpliceNonce()}
	body, err := splice.marshal(payload, marshalRequestBody)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Clone(body.Bytes())
}

func keptBody(t testing.TB, payload any, cache *quotedStringCache) []byte {
	t.Helper()
	body, err := marshalSpliced(payload, cache, marshalRequestBody)
	if err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func keptSpliceNow() *stringSplice {
	keptSplice.mu.Lock()
	defer keptSplice.mu.Unlock()
	return keptSplice.kept
}

// assertKeptSpliceHoldsNoRequest fails when the kept splice still holds anything of the request it served: its cache, nonce, an encoding, token bytes or message record.
// The body of the next request does not show it (the splice writes every index it reads), the live memory does: a leak here is every large string of the last request retained.
func assertKeptSpliceHoldsNoRequest(t *testing.T, earlier keptRequest) {
	t.Helper()
	splice := keptSpliceNow()
	if splice == nil {
		t.Fatal("no splice kept")
	}
	if splice.cache != nil || splice.nonce != "" || len(splice.quoted) != 0 || len(splice.tokenText) != 0 || len(splice.key.buf) != 0 || len(splice.walking) != 0 {
		t.Fatalf("the kept splice holds the request %s: cache %v nonce %q, %d encodings, %d token bytes, %d key bytes, %d walked", earlier.marker, splice.cache != nil, splice.nonce, len(splice.quoted), len(splice.tokenText), len(splice.key.buf), len(splice.walking))
	}
	for i, quoted := range splice.quoted[:cap(splice.quoted)] {
		if quoted != nil {
			t.Fatalf("the kept splice keeps the encoding %d of request %s (%d bytes, holds the string: %v)", i, earlier.marker, len(quoted), bytes.Contains(quoted, []byte(earlier.marker)))
		}
	}
	for _, b := range splice.tokenText[:cap(splice.tokenText)] {
		if b != 0 {
			t.Fatalf("the kept splice keeps the token bytes of request %s", earlier.marker)
		}
	}
	for _, b := range splice.key.buf[:cap(splice.key.buf)] {
		if b != 0 {
			t.Fatalf("the kept splice keeps the message record of request %s", earlier.marker)
		}
	}
}

func TestAKeptSpliceWritesTheBodyOfAFreshSpliceAfterAnyEarlierRequest(t *testing.T) {
	releaseKeptSplice()
	t.Cleanup(releaseKeptSplice)
	random := rand.New(rand.NewPCG(31, 37))
	reused := 0
	for trial := range 12 {
		corpus := keptCorpus(random, fmt.Sprint(trial), trial == 0)
		random.Shuffle(len(corpus), func(i, j int) { corpus[i], corpus[j] = corpus[j], corpus[i] })
		type sent struct {
			request keptRequest
			body    []byte
			want    []byte
		}
		var history []sent
		shared := keptCache(trial%2 == 0)
		for step, request := range corpus {
			withMessages := trial%2 == 0
			want := freshBody(t, request.payload, withMessages)
			before := keptSpliceNow()
			cache := shared
			if step%3 == 2 {
				cache = keptCache(withMessages)
			}
			got := keptBody(t, request.payload, cache)
			if before != nil && keptSpliceNow() == before {
				reused++
			}
			if keptSpliceNow() == nil {
				t.Fatalf("trial %d step %d: the splice of a request that succeeded was not kept", trial, step)
			}
			assertKeptSpliceHoldsNoRequest(t, request)
			if !bytes.Equal(got, want) {
				leaked := ""
				for _, earlier := range history {
					if bytes.Contains(got, []byte(earlier.request.marker)) {
						leaked += " " + earlier.request.marker
					}
				}
				t.Fatalf("trial %d step %d (%s): body through a kept splice differs from a fresh splice (%d bytes, want %d); strings of earlier requests in it:%s", trial, step, request.name, len(got), len(want), leaked)
			}
			for _, earlier := range history {
				if request.marker != earlier.request.marker && bytes.Contains(got, []byte(earlier.request.marker)) {
					t.Fatalf("trial %d step %d: the body holds %q of an earlier request", trial, step, earlier.request.marker)
				}
			}
			history = append(history, sent{request: request, body: got, want: want})
			// A body a request sent is not written to by the next request.
			for i, earlier := range history {
				if !bytes.Equal(earlier.body, earlier.want) {
					t.Fatalf("trial %d step %d: the body of step %d changed after a later request", trial, step, i)
				}
			}
		}
	}
	if reused < 40 {
		t.Fatalf("only %d requests found a kept splice: the test does not reach the kept path", reused)
	}
}

type panicMarshaler struct{}

func (panicMarshaler) MarshalJSON() ([]byte, error) { panic("pig-kept-panic") }

func failingRequest(kind string) oaiRequest {
	var content any = make(chan int)
	if kind == "panic" {
		content = panicMarshaler{}
	}
	return oaiRequest{Model: "m", Messages: []oaiMessage{{Role: "user", Content: markedText("pig-kept-failing", 2*requestSpliceMinBytes)}, {Role: "user", Content: content}}}
}

func TestAFailedEncodeDropsTheSpliceAndTheNextRequestIsUnchanged(t *testing.T) {
	random := rand.New(rand.NewPCG(41, 43))
	for _, kind := range []string{"error", "panic"} {
		for _, withMessages := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/messages=%v", kind, withMessages), func(t *testing.T) {
				releaseKeptSplice()
				t.Cleanup(releaseKeptSplice)
				corpus := keptCorpus(random, kind, false)
				keptBody(t, corpus[0].payload, keptCache(withMessages))
				first := keptSpliceNow()
				if first == nil {
					t.Fatal("no splice kept after a request that succeeded")
				}
				func() {
					defer func() {
						recovered := recover()
						if (kind == "panic") != (recovered != nil) {
							t.Fatalf("recovered %v from the %s request", recovered, kind)
						}
					}()
					_, err := marshalSpliced(failingRequest(kind), keptCache(withMessages), marshalRequestBody)
					if (kind == "error") != (err != nil) {
						t.Fatalf("%s request returned %v", kind, err)
					}
				}()
				if kept := keptSpliceNow(); kept != nil {
					t.Fatalf("the splice of a request that failed was kept (the one before it: %v)", kept == first)
				}
				for step, request := range corpus {
					want := freshBody(t, request.payload, withMessages)
					if got := keptBody(t, request.payload, keptCache(withMessages)); !bytes.Equal(got, want) {
						t.Fatalf("step %d after the failure (%s): body differs from a fresh splice", step, request.name)
					}
					if step == 0 && keptSpliceNow() == first {
						t.Fatal("the next request was served by the splice of the request that failed")
					}
				}
			})
		}
	}
}

func TestAKeptSpliceServesOneRequestAtATimeAndEveryBodyIsUnchanged(t *testing.T) {
	releaseKeptSplice()
	t.Cleanup(releaseKeptSplice)
	// A request that starts while another has the splice makes its own; of the two that come back, the first is kept.
	cache := keptCache(true)
	first := acquireSplice(cache)
	second := acquireSplice(cache)
	if first == second {
		t.Fatal("two requests at once were given one splice")
	}
	releaseSplice(first)
	releaseSplice(second)
	if keptSpliceNow() != first {
		t.Fatal("the first splice to come back was not the one kept")
	}
	if got := acquireSplice(cache); got != first {
		t.Fatal("a request did not take the kept splice")
	} else if keptSpliceNow() != nil {
		t.Fatal("a splice taken stayed in the slot")
	} else {
		releaseSplice(got)
	}

	random := rand.New(rand.NewPCG(47, 53))
	corpus := keptCorpus(random, "race", false)
	wants := make([][]byte, len(corpus))
	for i, request := range corpus {
		wants[i] = freshBody(t, request.payload, i%2 == 0)
	}
	const goroutines, rounds = 12, 30
	shared := [2]*quotedStringCache{keptCache(false), keptCache(true)}
	var wg sync.WaitGroup
	failures := make(chan string, goroutines*rounds)
	for g := range goroutines {
		wg.Go(func() {
			rng := rand.New(rand.NewPCG(uint64(g), 59))
			for range rounds {
				i := rng.IntN(len(corpus))
				body, err := marshalSpliced(corpus[i].payload, shared[i%2], marshalRequestBody)
				if err != nil {
					failures <- err.Error()
					continue
				}
				// Reference bodies were made with the message cache on for even i; the body does not depend on it.
				if got := body.Bytes(); !bytes.Equal(got, wants[i]) {
					failures <- fmt.Sprintf("goroutine %d: %s request %d: body differs from a fresh splice", g, corpus[i].name, i)
				}
			}
		})
	}
	wg.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
}

func TestAKeptSpliceIsDroppedWhenItIsTooLargeOrTheCachesAreReleased(t *testing.T) {
	t.Cleanup(releaseKeptSplice)
	cache := keptCache(true)
	random := rand.New(rand.NewPCG(61, 67))
	request := keptCorpus(random, "drop", false)[0]

	releaseKeptSplice()
	keptBody(t, request.payload, cache)
	if keptSpliceNow() == nil {
		t.Fatal("nothing kept to drop")
	}
	ReleaseRequestCaches()
	if keptSpliceNow() != nil {
		t.Fatal("ReleaseRequestCaches left the splice kept")
	}

	big := acquireSplice(cache)
	big.scratch.Grow(maxKeptRequestScratch + 1)
	releaseSplice(big)
	if keptSpliceNow() != nil {
		t.Fatalf("a splice with a scratch of %d bytes was kept", big.scratch.Cap())
	}

	many := acquireSplice(cache)
	many.quoted = make([][]byte, 0, maxKeptRequestScratch/quotedEntryBytes+1)
	releaseSplice(many)
	if keptSpliceNow() != nil {
		t.Fatalf("a splice with room for %d encodings was kept", cap(many.quoted))
	}

	// A request whose body is over the bound still writes its body, and what it leaves is not over the bound.
	large := keptCorpus(random, "drop-large", true)
	body := large[len(large)-1]
	if got := keptBody(t, body.payload, cache); !bytes.Equal(got, freshBody(t, body.payload, true)) {
		t.Fatal("the large body differs from a fresh splice")
	}
	if kept := keptSpliceNow(); kept != nil && (kept.scratch.Cap() > maxKeptRequestScratch || cap(kept.quoted)*quotedEntryBytes > maxKeptRequestScratch) {
		t.Fatal("a splice over the bound was kept")
	}
}

func TestAKeptSpliceIsRebuiltForAnotherPayloadTypeOrMessageCache(t *testing.T) {
	t.Cleanup(releaseKeptSplice)
	random := rand.New(rand.NewPCG(71, 73))
	corpus := keptCorpus(random, "rebuild", false)
	anthropic, completions := corpus[0], corpus[len(corpus)-1]
	if anthropic.name != "anthropic" || completions.name != "completions" {
		t.Fatal("corpus order")
	}
	type step struct {
		request      keptRequest
		withMessages bool
	}
	orders := map[string][2]step{
		"anthropic then completions":     {{anthropic, true}, {completions, true}},
		"completions then anthropic":     {{completions, true}, {anthropic, true}},
		"messages then none":             {{anthropic, true}, {anthropic, false}},
		"none then messages":             {{anthropic, false}, {anthropic, true}},
		"completions messages then none": {{completions, true}, {completions, false}},
		"completions none then messages": {{completions, false}, {completions, true}},
	}
	for name, order := range orders {
		t.Run(name, func(t *testing.T) {
			releaseKeptSplice()
			keptBody(t, order[0].request.payload, keptCache(order[0].withMessages))
			splice := keptSpliceNow()
			built := splice.encoding.options
			if built == nil {
				t.Fatal("a kept splice has no encoding")
			}
			got := keptBody(t, order[1].request.payload, keptCache(order[1].withMessages))
			if !bytes.Equal(got, freshBody(t, order[1].request.payload, order[1].withMessages)) {
				t.Fatal("body after the rebuild differs from a fresh splice")
			}
			if keptSpliceNow() != splice {
				t.Fatal("the splice was not the one kept")
			}
			if splice.encoding.options == built {
				t.Fatal("the encoding of the other payload type or message cache was not built again")
			}
			// The same kind of request again keeps what it has.
			again := splice.encoding.options
			keptBody(t, order[1].request.payload, keptCache(order[1].withMessages))
			if splice.encoding.options != again {
				t.Fatal("the encoding was built again for the same kind of request")
			}
		})
	}
}
