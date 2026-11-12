package ai

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// quotedShareCorpus is the strings the three escapers of one tool result's text must agree on, padded past the cache's smallest entry: the
// session entry (extensions/sdk/json), the request (marshalRequestBody) and the tool's own structured content (encoding/json with HTML
// escaping off) each wrote them, and one shared encoding must stand for all three wherever they write the same bytes.
func quotedShareCorpus() map[string]string {
	pad := func(s string) string { return s + strings.Repeat("x", requestSpliceMinBytes) }
	var controls strings.Builder
	for c := range 0x20 {
		controls.WriteByte(byte(c))
	}
	return map[string]string{
		"ascii":          strings.Repeat("package main\n\tfunc main() {}\n", 400),
		"empty-tail":     pad(""),
		"controls":       pad(controls.String()),
		"del":            pad("\x7f"),
		"quotes":         pad(`"quoted" and \ backslash and \\"`),
		"html":           pad("<script>alert('x') && 1 > 0</script>"),
		"line-separator": pad("a\u2028b\u2029c"),
		"astral":         pad("😀 café 漢字 \U0010ffff"),
		"noncharacters":  pad("\ufffe\uffff\ufdd0"),
		"replacement":    pad("\ufffd"),
		"latin1":         pad("é\u0080\u009f\u00a0"),
	}
}

// The shared encoding is what the request and the session write: for every string it is marshalRequestBody's bytes, and a TextContent
// appends the same text with it as without it.
func TestTheSharedEncodingOfAStringIsTheBytesTheRequestAndTheSessionWrite(t *testing.T) {
	for name, text := range quotedShareCorpus() {
		t.Run(name, func(t *testing.T) {
			if !utf8.ValidString(text) {
				t.Fatal("the corpus holds a string that is not UTF-8")
			}
			want, err := marshalRequestBody(text)
			if err != nil {
				t.Fatal(err)
			}
			cache := newQuotedStringCache(1 << 30)
			quoted, ok := cache.quoteValid(text)
			if !ok || !bytes.Equal(quoted, want) {
				t.Fatalf("the cache's encoding differs from the request's: ok=%v\n got %q\nwant %q", ok, quoted[:min(len(quoted), 80)], want[:80])
			}
			for _, signature := range []string{"", "sig \"x\" \u2028 \xff"} {
				content := TextContent{Text: text, TextSignature: signature}
				reference, err := appendContent(nil, content.contentType(), content.wire())
				if err != nil {
					t.Fatal(err)
				}
				got, err := content.AppendStringify(nil)
				if err != nil || !bytes.Equal(got, reference) {
					t.Fatalf("AppendStringify with signature %q differs from the encoder's text (err %v)", signature, err)
				}
			}
		})
	}
}

// The encoding of a string that is not UTF-8 is never taken from the cache: the session and the request write their own U+FFFD or escape.
func TestAStringThatIsNotUTF8IsAppendedAsTheEncoderWritesIt(t *testing.T) {
	for name, text := range map[string]string{
		"invalid-byte":   strings.Repeat("a", requestSpliceMinBytes) + "\xff",
		"lone-surrogate": strings.Repeat("a", requestSpliceMinBytes) + "\xed\xa0\x80",
	} {
		t.Run(name, func(t *testing.T) {
			content := TextContent{Text: text}
			reference, err := appendContent(nil, content.contentType(), content.wire())
			if err != nil {
				t.Fatal(err)
			}
			held := len(requestStrings.entries)
			got, err := content.AppendStringify(nil)
			if err != nil || !bytes.Equal(got, reference) || len(requestStrings.entries) != held {
				t.Fatalf("AppendStringify of a string that is not UTF-8 differs from the encoder's text or cached it (err %v)", err)
			}
		})
	}
}

// A session entry that holds a large text encodes it into the request cache, so the request that sends it finds the encoding and the text is
// escaped once between the two.
func TestAppendingATextToTheSessionLetsTheRequestFindItsEncoding(t *testing.T) {
	text := strings.Repeat("a line of a file read by a tool, with \"quotes\"\n", 400)
	requestStrings.release()
	t.Cleanup(requestStrings.release)
	if _, err := (TextContent{Text: text}).AppendStringify(nil); err != nil {
		t.Fatal(err)
	}
	quoted, held := requestStrings.known(text)
	if !held {
		t.Fatal("the encoding the session wrote was not kept for the request")
	}
	misses := requestStrings.misses
	again, ok := requestStrings.quoteValid(text)
	if !ok || &again[0] != &quoted[0] || requestStrings.misses != misses {
		t.Fatal("the request encoded the text the session had encoded")
	}
}

// QuotedPlainText is the encoding encoding/json writes with HTML escaping off wherever JSON.stringify writes the same bytes.
func TestQuotedPlainTextIsWhatBothEncodersWrite(t *testing.T) {
	requestStrings.release()
	t.Cleanup(requestStrings.release)
	for name, text := range quotedShareCorpus() {
		t.Run(name, func(t *testing.T) {
			var want bytes.Buffer
			encoder := json.NewEncoder(&want)
			encoder.SetEscapeHTML(false)
			if err := encoder.Encode(text); err != nil {
				t.Fatal(err)
			}
			stringified, err := sdkjson.MarshalStringify(text)
			if err != nil {
				t.Fatal(err)
			}
			quoted, ok := QuotedPlainText(text)
			shared := bytes.Equal(bytes.TrimSuffix(want.Bytes(), []byte("\n")), stringified)
			if ok != shared {
				t.Fatalf("QuotedPlainText ok=%v, but the two encoders agree: %v", ok, shared)
			}
			if ok && !bytes.Equal(quoted, stringified) {
				t.Fatalf("QuotedPlainText differs from the encoders' bytes")
			}
			if ok && cap(quoted) != len(quoted) {
				t.Fatalf("the shared encoding can be appended to in place: cap %d, len %d", cap(quoted), len(quoted))
			}
		})
	}
}

func TestQuotedPlainTextRefusesWhatItDoesNotKeep(t *testing.T) {
	requestStrings.release()
	t.Cleanup(requestStrings.release)
	for name, text := range map[string]string{
		"short":     "a short text",
		"invalid":   strings.Repeat("a", requestSpliceMinBytes) + "\xff",
		"separator": strings.Repeat("a", requestSpliceMinBytes) + "\u2028",
	} {
		if quoted, ok := QuotedPlainText(text); ok {
			t.Fatalf("%s: QuotedPlainText = %q, want a refusal", name, quoted[:min(len(quoted), 20)])
		}
	}
	long := strings.Repeat("a", requestSpliceMinBytes)
	first, ok := QuotedPlainText(long)
	second, again := QuotedPlainText(long)
	if !ok || !again || &first[0] != &second[0] {
		t.Fatal("a second ask for the same text did not return the encoding the first kept")
	}
}
