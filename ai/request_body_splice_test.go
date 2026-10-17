package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// pi-ai sends JSON.stringify(params) for the whole conversation (openai-completions.ts streamCompletions via the SDK). The cached splice must produce the bytes marshalRequestBody (JSON.stringify) produces for the same request.
func spliceTestStrings(random *rand.Rand) []string {
	atoms := []string{"func main() {\n\tfmt.Println(\"<hi> & bye\")\n}\n", "\u2028\u2029", "\x00\x01\x1f", "\xff\xfe", "😀 café 漢字", "\\\"", "pig-splice-0-", `"pig-splice-deadbeef-0"`}
	var out []string
	for _, size := range []int{0, 1, requestSpliceMinBytes - 1, requestSpliceMinBytes, requestSpliceMinBytes + 1, 3 * requestSpliceMinBytes} {
		var builder strings.Builder
		for builder.Len() < size {
			builder.WriteString(atoms[random.IntN(len(atoms))])
		}
		out = append(out, builder.String())
	}
	return out
}

func TestSplicedCompletionsRequestIsByteIdenticalToMarshal(t *testing.T) {
	random := rand.New(rand.NewPCG(1, 2))
	pool := spliceTestStrings(random)
	for trial := range 200 {
		request := oaiRequest{Model: "m", Messages: []oaiMessage{{Role: "system", Content: "short"}}}
		for range 1 + random.IntN(6) {
			text := pool[random.IntN(len(pool))]
			switch random.IntN(5) {
			case 0:
				request.Messages = append(request.Messages, oaiMessage{Role: "tool", ToolCallID: "c", Content: text})
			case 1:
				request.Messages = append(request.Messages, oaiMessage{Role: "user", Content: []oaiContentPart{{Type: "text", Text: &text}}})
			case 2:
				request.Messages = append(request.Messages, oaiMessage{Role: "assistant", Content: text, Reasoning: text})
			case 3:
				// wire() writes a tool-bearing system message without its content.
				request.Messages = append(request.Messages, oaiMessage{Role: "system", Content: text, Tools: []oaiTool{{Type: "function", Function: &oaiToolFunction{Name: "f"}}}})
			default:
				request.Messages = append(request.Messages, oaiMessage{Role: "user", Content: text})
			}
		}
		want, err := marshalRequestBody(request)
		if err != nil {
			t.Fatal(err)
		}
		cache := newQuotedStringCache(1 << 30)
		for pass := range 2 {
			got, err := marshalSpliced(request, cache, marshalRequestBody)
			if err != nil {
				t.Fatal(err)
			}
			if joined := got.Bytes(); !bytes.Equal(joined, want) || got.size != len(want) {
				got := joined
				t.Fatalf("trial %d pass %d: spliced body differs from marshalRequestBody\n got %.200q\nwant %.200q", trial, pass, got, want)
			}
		}
	}
}

func TestAQuotedStringIsEncodedOnceAndNeverServedForAnotherString(t *testing.T) {
	cache := newQuotedStringCache(1 << 30)
	text := strings.Repeat("a\"b<c\n", 1000)
	first := cache.quote(text)
	if again := cache.quote(strings.Clone(text)); !bytes.Equal(again, first) || cache.misses != 1 {
		t.Fatalf("an equal string was encoded again: misses=%d", cache.misses)
	}
	key := quotedStringKey{len(text), 0}
	for k := range cache.entries {
		key = k
	}
	cache.entries[key].Value.(*quotedString).source = "another string with the same hash"
	if got := cache.quote(text); !bytes.Equal(got, first) || cache.misses != 2 {
		t.Fatalf("a colliding entry served the wrong string: %.40q misses=%d", got, cache.misses)
	}
}

func TestTheQuotedStringCacheForgetsTheLeastRecentlyUsedPastItsLimit(t *testing.T) {
	// An entry holds its source and its encoding: 1000 + 1002 bytes, so two fit and a third evicts.
	cache := newQuotedStringCache(5000)
	a, b, c := strings.Repeat("a", 1000), strings.Repeat("b", 1000), strings.Repeat("c", 1000)
	mk := func(name string) string { return map[string]string{"a": a, "b": b, "c": c}[name] }
	cache.quote(mk("a"))
	cache.quote(mk("b"))
	cache.quote(mk("a")) // a is now the most recent
	cache.quote(mk("c")) // evicts b
	if cache.misses != 3 || cache.held != 2*2002 {
		t.Fatalf("misses=%d held=%d", cache.misses, cache.held)
	}
	cache.quote(mk("a"))
	if cache.misses != 3 {
		t.Fatal("the recently used string was evicted")
	}
	cache.quote(mk("b"))
	if cache.misses != 4 {
		t.Fatal("the least recently used string was kept")
	}
}

// A session whose large strings exceed the limit misses on every request. Each miss must cost an encoding and a constant-time eviction, not a scan of every entry: with a scan, 80 MiB of 4 KiB tool results took 7 s per request against 0.5 s for json.Marshal.
func TestAQuotedStringMissCostsTheSameInALargeCache(t *testing.T) {
	perMiss := func(entries int) time.Duration {
		strs := make([]string, 4*entries)
		for i := range strs {
			strs[i] = fmt.Sprintf("%016d", i)
		}
		cache := newQuotedStringCache(entries * (16 + 18))
		for _, s := range strs[:entries] {
			cache.quote(s)
		}
		best := time.Duration(math.MaxInt64)
		for round := range 5 {
			start := time.Now()
			for _, s := range strs[entries*(1+round%3):][:entries] {
				cache.quote(s)
			}
			best = min(best, time.Since(start)/time.Duration(entries))
		}
		if len(cache.entries) != entries || cache.recency.Len() != entries {
			t.Fatalf("cache holds %d entries (%d in recency order), want %d", len(cache.entries), cache.recency.Len(), entries)
		}
		return best
	}
	small, large := perMiss(64), perMiss(16384)
	// A wall-clock ratio: report-only in pull-request CI, blocking in make test-timing on quiet cores (a Windows runner timed the small cache at 0 s).
	testenv.TimingGateFatal(t, large > 20*small+time.Microsecond, "a miss in a cache of 16384 entries took %v, in a cache of 64 entries %v", large, small)
}

// The provider path: a conversation's large tool results reach the encoder as the same strings on every request, so only the first request encodes them, and the body decodes to the original text.
func TestOpenAICompletionsEncodesEachLargeToolResultOnce(t *testing.T) {
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	provider := NewOpenAIProvider(OpenAIConfig{Model: "probe", ProviderID: "probe-provider", APIKey: "test", BaseURL: server.URL})
	defer func() { _ = provider.Close() }()
	const results = 4
	var messages []Message
	var texts []string
	random := rand.New(rand.NewPCG(3, 4))
	for index := range results {
		text := strings.Repeat("unique <"+strings.Repeat(string(rune('a'+index)), 5)+"> \"line\"\n", 400) + string(rune(random.IntN(1000)+0x100))
		texts = append(texts, text)
		id := string(rune('a' + index))
		messages = append(messages,
			AssistantMessage{StopReason: StopReasonToolUse, Content: []AssistantContentBlock{ToolCall{ID: id, Name: "read", Arguments: JsonObject{}}}},
			ToolResultMessage{ToolCallID: id, ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: text}}})
	}
	send := func() {
		stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: messages, Frozen: true}), StreamOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for range stream.Events(context.WithoutCancel(t.Context())) {
		}
	}
	before := requestStrings.misses
	send()
	send()
	if len(bodies) != 2 {
		t.Fatalf("%d requests", len(bodies))
	}
	if got := requestStrings.misses - before; got != results {
		t.Fatalf("the %d large results were encoded %d times over two requests", results, got)
	}
	// JSON.stringify writes '<', '>' and '&' as they are (openai 7.19.0 internal/request-options.js:29); a cached encoding must too.
	if bytes.Contains(bodies[1], []byte(`\u003c`)) || !bytes.Contains(bodies[1], []byte("unique <aaaaa>")) {
		t.Fatalf("a large tool result was not written as JSON.stringify writes it: %.120q", bodies[1])
	}
	if !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatal("the same conversation produced different bodies")
	}
	var decoded struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(bodies[1], &decoded); err != nil {
		t.Fatal(err)
	}
	var tools []string
	for _, message := range decoded.Messages {
		if message.Role == "tool" {
			tools = append(tools, message.Content.(string))
		}
	}
	if strings.Join(tools, "\x00") != strings.Join(texts, "\x00") {
		t.Fatal("the request does not carry the tool results' text")
	}
}

func TestARequestBodyReadsBackAsOneBufferWhateverTheReadSize(t *testing.T) {
	body := &requestBody{}
	for _, segment := range []string{"", "alpha", "", "be", "gamma-delta-epsilon", "z"} {
		body.add([]byte(segment))
	}
	want := "alphabegamma-delta-epsilonz"
	if string(body.Bytes()) != want || body.size != len(want) {
		t.Fatalf("Bytes = %q size %d", body.Bytes(), body.size)
	}
	for _, chunk := range []int{1, 2, 3, 7, 64} {
		reader := body.reader()
		var got []byte
		buffer := make([]byte, chunk)
		for {
			n, err := reader.Read(buffer)
			got = append(got, buffer[:n]...)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if string(got) != want {
			t.Fatalf("chunk %d: %q", chunk, got)
		}
	}
	// WriteTo continues where Read stopped.
	partial := body.reader()
	head := make([]byte, 3)
	if _, err := io.ReadFull(partial, head); err != nil {
		t.Fatal(err)
	}
	var tail strings.Builder
	if _, err := partial.(io.WriterTo).WriteTo(&tail); err != nil {
		t.Fatal(err)
	}
	if string(head)+tail.String() != want {
		t.Fatalf("Read then WriteTo: %q + %q", head, tail.String())
	}
	// A second reader starts from the beginning even after the first has been consumed (a redirect resends the body).
	var first, second strings.Builder
	if _, err := body.reader().(io.WriterTo).WriteTo(&first); err != nil {
		t.Fatal(err)
	}
	if _, err := body.reader().(io.WriterTo).WriteTo(&second); err != nil {
		t.Fatal(err)
	}
	if first.String() != want || second.String() != want {
		t.Fatalf("WriteTo: %q %q", first.String(), second.String())
	}
}

// The provider sends the segments with the length of the whole body and resends them after a redirect that keeps the method (net/http uses GetBody).
func TestOpenAICompletionsSendsASegmentedBodyWithItsLengthAndResendsItOnRedirect(t *testing.T) {
	var lengths []int64
	var bodies []string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		lengths = append(lengths, r.ContentLength)
		bodies = append(bodies, string(body))
		if r.URL.Path == "/redirect/chat/completions" {
			http.Redirect(w, r, server.URL+"/final/chat/completions", http.StatusTemporaryRedirect)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	provider := NewOpenAIProvider(OpenAIConfig{Model: "probe", ProviderID: "probe-provider", APIKey: "test", BaseURL: server.URL + "/redirect"})
	defer func() { _ = provider.Close() }()
	big := strings.Repeat("redirect me <&> \n", 1000)
	messages := []Message{
		AssistantMessage{StopReason: StopReasonToolUse, Content: []AssistantContentBlock{ToolCall{ID: "a", Name: "read", Arguments: JsonObject{}}}},
		ToolResultMessage{ToolCallID: "a", ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: big}}},
	}
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: messages, Frozen: true}), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for range stream.Events(context.WithoutCancel(t.Context())) {
	}
	if len(bodies) != 2 || bodies[0] != bodies[1] || int64(len(bodies[0])) != lengths[0] || lengths[0] != lengths[1] || !json.Valid([]byte(bodies[0])) {
		t.Fatalf("requests %d lengths %v body sizes %d/%d", len(bodies), lengths, len(bodies[0]), len(bodies[len(bodies)-1]))
	}
}

// net/http sends a *bytes.Reader body in the same write as the request headers and flushes headers first for any other reader, which changes what a server that answers without reading sees (transport_error_test.go). A body of one segment must stay a *bytes.Reader, as before.
func TestAOneSegmentRequestBodyIsABytesReader(t *testing.T) {
	one := &requestBody{}
	one.add([]byte("{}"))
	if _, ok := one.reader().(*bytes.Reader); !ok {
		t.Fatalf("a one-segment body reads through %T", one.reader())
	}
	many := &requestBody{}
	many.add([]byte("{"))
	many.add([]byte("}"))
	if _, ok := many.reader().(*bytes.Reader); ok {
		t.Fatal("a body of several segments was joined into one buffer")
	}
}

// A conversation sends the same string objects with every request, so a string the cache holds is recognised by where its bytes are, without being read or hashed again. The test shows that by changing the bytes under a held string in place, which Go strings never do: the answer for a held string is the cached encoding, and a copy of the string is looked up by its content.
func TestAHeldStringIsRecognisedWithoutReadingIt(t *testing.T) {
	cache := newQuotedStringCache(1 << 20)
	bytesOfS := []byte(strings.Repeat("held text <&>\n", 400))
	s := unsafe.String(&bytesOfS[0], len(bytesOfS)) //nolint:gosec // G103: the test changes the bytes under a string to show the cache does not read a held string.
	first, valid := cache.quoteValid(s)
	if !valid || cache.misses != 1 {
		t.Fatalf("the first lookup did not encode the string: valid=%v misses=%d", valid, cache.misses)
	}
	want := bytes.Clone(first)
	bytesOfS[0] = 0xff
	second, valid := cache.quoteValid(s)
	if !valid || cache.misses != 1 || !bytes.Equal(second, want) {
		t.Fatalf("a held string was read again: valid=%v misses=%d", valid, cache.misses)
	}
	bytesOfS[0] = 'h'
	if copied, valid := cache.quoteValid(strings.Clone(s)); !valid || cache.misses != 1 || !bytes.Equal(copied, want) {
		t.Fatalf("a copy of a held string was not found by content: valid=%v misses=%d", valid, cache.misses)
	}
}

func TestAnEvictedStringIsNoLongerRecognisedByIdentity(t *testing.T) {
	cache := newQuotedStringCache(5000)
	a := strings.Repeat("a", 1000)
	cache.quoteValid(a)
	cache.quoteValid(strings.Repeat("b", 1000))
	cache.quoteValid(strings.Repeat("c", 1000)) // evicts a
	if _, held := cache.known(a); held {
		t.Fatal("an evicted string is still held by identity")
	}
	if len(cache.bySource) != len(cache.entries) || len(cache.bySource) != 2 {
		t.Fatalf("identity index has %d entries for %d", len(cache.bySource), len(cache.entries))
	}
	if _, valid := cache.quoteValid("\xff" + strings.Repeat("x", 1000)); valid || len(cache.bySource) != 2 {
		t.Fatal("a string that is not UTF-8 was cached")
	}
}

// An equal copy that hits by content becomes the entry's source, so a request that re-sends the copy finds it by identity. The entry keeps one source and is counted against the limit once.
func TestEqualCopyOfAHeldStringBecomesTheRecognisedSource(t *testing.T) {
	cache := newQuotedStringCache(1 << 20)
	first := strings.Repeat("copy <&>\n", 500)
	want, _ := cache.quoteValid(first)
	held := cache.held
	copied := strings.Clone(first)
	if _, ok := cache.known(copied); ok {
		t.Fatal("a copy was held before it was used")
	}
	if got, valid := cache.quoteValid(copied); !valid || !bytes.Equal(got, want) || cache.misses != 1 {
		t.Fatalf("the copy was not found by content: misses=%d", cache.misses)
	}
	if got, ok := cache.known(copied); !ok || !bytes.Equal(got, want) {
		t.Fatal("the copy is not held by identity")
	}
	if cache.held != held || len(cache.entries) != 1 || len(cache.bySource) != 1 {
		t.Fatalf("held=%d (was %d) entries=%d identities=%d", cache.held, held, len(cache.entries), len(cache.bySource))
	}
}

// Evicting a copy releases its identity and nothing else, and evicting the first entry leaves the copies recognised.
func TestEvictingACopyKeepsTheEntryAndEvictingTheEntryKeepsTheCopy(t *testing.T) {
	text := strings.Repeat("c", 1000)
	cache := newQuotedStringCache(2*2002 + 1000)
	cache.quoteValid(text)
	copied := strings.Clone(text)
	cache.quoteValid(copied)
	other := strings.Repeat("d", 1000)
	cache.quoteValid(other) // evicts the entry for text, the least recent
	if _, ok := cache.known(copied); !ok {
		t.Fatal("the copy was released with the entry")
	}
	if _, ok := cache.known(text); ok {
		t.Fatal("the evicted entry's string is still held by identity")
	}
	// Evicting a copy leaves its entry: the entry for text is rebuilt, then the copy is the least recent and goes.
	rebuilt, _ := cache.quoteValid(text)
	cache.quoteValid(strings.Repeat("e", 1000))
	if _, ok := cache.known(copied); ok {
		t.Fatal("the copy was kept past the limit")
	}
	if _, ok := cache.known(text); !ok || !cache.holds(rebuilt) || len(cache.entries) != 2 {
		t.Fatalf("evicting a copy took its entry: entries=%d", len(cache.entries))
	}
}

// A string the request cache holds is valid UTF-8, so removing lone surrogates from it is the identity and needs no scan. The test shows it by planting a surrogate under a held string, which Go strings never carry: a held string comes back as it is, and a string the cache does not hold is cleaned.
func TestSanitizingAHeldStringDoesNotScanIt(t *testing.T) {
	saved := requestStrings
	defer func() { requestStrings = saved }()
	requestStrings = newQuotedStringCache(1 << 20)
	buf := []byte(strings.Repeat("held text\n", requestSpliceMinBytes/8))
	s := unsafe.String(&buf[0], len(buf)) //nolint:gosec // G103: the test changes the bytes under a string to show a held string is not scanned.
	if _, valid := requestStrings.quoteValid(s); !valid {
		t.Fatal("the string was not cached")
	}
	copy(buf, "\xed\xa0\x80")
	if got := sanitizeSurrogates(s); got != s {
		t.Fatalf("a held string was scanned: %.20q", got)
	}
	unheld := strings.Clone(s)
	if got := sanitizeSurrogates(unheld); got == unheld || strings.Contains(got, "\xed\xa0\x80") {
		t.Fatalf("a lone surrogate was kept in a string the cache does not hold: %.20q", got)
	}
}

// A string's identity is where its bytes start and how many there are: a prefix of a held string starts at the same byte, and must get its own encoding, not the held string's.
func TestAPrefixOfAHeldStringIsNotTheHeldString(t *testing.T) {
	cache := newQuotedStringCache(1 << 20)
	whole := strings.Repeat("held text <&>\n", 400) + "tail"
	prefix := whole[:len(whole)-len("tail")]
	held, _ := cache.quoteValid(whole)
	got, valid := cache.quoteValid(prefix)
	want, _ := marshalRequestBody(prefix)
	if !valid || !bytes.Equal(got, want) {
		t.Fatalf("a prefix of a held string was encoded as %d bytes ending %q, want %d bytes ending %q (held %d bytes)", len(got), got[max(len(got)-8, 0):], len(want), want[len(want)-8:], len(held))
	}
}
