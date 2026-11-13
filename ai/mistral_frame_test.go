//go:build !pig_strip_mistral_conversations

// SPDX-License-Identifier: MIT

package ai

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// The member walk that judges a record's "choices" member agrees with decoding the record into a map of raw members wherever it judges, and judges an ordinary record.
func TestMistralChoicesIsArrayMatchesDecode(t *testing.T) {
	judged := []string{
		`{"choices":[]}`, `{"choices":{}}`, `{"choices":null}`, `{}`, `{"a":1,"choices":[1],"choices":2}`, `{"choices":2,"choices":[1]}`,
		` {"x":{"choices":[]},"y":[{"choices":1}]}`, `{"choices" : [ ] }`, `{"choices":"[x"}`, `{"a":"\"choices\":[","choices":[]}`,
		`{"id":"m1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":"a"},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"choices":1}}],"usage":{"choices":3}}`, `{"a":-0.5e+3,"choices":[true]}`,
	}
	for _, text := range judged {
		array, walked := mistralChoicesIsArray(text)
		if !walked {
			t.Errorf("%s: the walk did not judge an ordinary record", text)
			continue
		}
		var record map[string]json.RawMessage
		want := json.Unmarshal([]byte(text), &record) == nil && strings.HasPrefix(string(record["choices"]), "[")
		if array != want {
			t.Errorf("%s: walk says %t, decoding says %t", text, array, want)
		}
	}
	for _, text := range []string{`[]`, `"s"`, `null`, `1`, `{"ch\u006fices":[]}`} {
		if _, walked := mistralChoicesIsArray(text); walked {
			t.Errorf("%s: the walk judged a record it cannot read", text)
		}
	}
}

// A record of one line takes the text of its data line as the joined data lines of the general path give it.
func TestMistralSingleLineDataMatchesJoinedLines(t *testing.T) {
	for _, raw := range []string{`data: {"a":1}`, `data:{"a":1}`, "data:\t \u00a0{\"a\":1} \t", `data:`, `data: [DONE]`, `event: x`, ``, `: comment`, `data: a:b`, `Data: a`} {
		text, single := mistralSingleLineData(raw)
		if !single {
			t.Errorf("%q: a record of one line was not read as one", raw)
			continue
		}
		var data []string
		for _, line := range splitMistralLines(raw) {
			if rest, ok := strings.CutPrefix(line, "data:"); ok {
				data = append(data, strings.TrimLeft(rest, mistralJSWhitespace))
			}
		}
		if want := strings.Trim(strings.Join(data, "\n"), mistralJSWhitespace); text != want {
			t.Errorf("%q: text %q, joined lines give %q", raw, text, want)
		}
	}
	for _, raw := range []string{"data: a\ndata: b", "data: a\r\n", "x\rdata: a"} {
		if _, single := mistralSingleLineData(raw); single {
			t.Errorf("%q: a record of several lines was read as one", raw)
		}
	}
}

// referenceMistralDecode is the rune-by-rune decode of a TextDecoder with the stream option, which mistralTextDecoder's copy of well-formed text must match.
func referenceMistralDecode(decoder *mistralTextDecoder, data []byte) string {
	var text strings.Builder
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			size = openAIInvalidUTF8Prefix(data)
		}
		if !decoder.started {
			decoder.started = true
			if r == '\uFEFF' {
				data = data[size:]
				continue
			}
		}
		text.WriteRune(r)
		data = data[size:]
	}
	return text.String()
}

// Well-formed text, a byte order mark that starts the stream, and sequences split between chunks decode as the rune-by-rune decode does, and the decode does not keep the chunk it was given.
func TestMistralTextDecoderFastPathMatchesRuneDecode(t *testing.T) {
	inputs := [][]byte{
		[]byte("plain"), []byte("\ufeffbom first"), []byte("a\ufeffb"), []byte("héllo wörld ✓ 𝄞"), []byte("\ufeff\ufeffx"),
		[]byte("bad \xff\xfe bytes"), []byte("\xed\xa0\x80 lone surrogate"), []byte("tail \xe2\x9c"), {}, []byte("\ufeff"),
	}
	for _, input := range inputs {
		for cut := 0; cut <= len(input); cut++ {
			var got, want mistralTextDecoder
			var gotText, wantText string
			for _, chunk := range [][]byte{append([]byte(nil), input[:cut]...), append([]byte(nil), input[cut:]...)} {
				gotText += got.decode(chunk)
				// The reference holds back an incomplete sequence at the end of a chunk as decode does.
				data := append(append([]byte(nil), want.pending...), chunk...)
				want.pending = nil
				if hold := mistralIncompleteUTF8Suffix(data); hold > 0 {
					want.pending = append([]byte(nil), data[len(data)-hold:]...)
					data = data[:len(data)-hold]
				}
				wantText += referenceMistralDecode(&want, data)
			}
			gotText += got.flush()
			wantText += referenceMistralDecode(&want, want.pending)
			if gotText != wantText {
				t.Errorf("%q cut at %d: decoded %q, rune-by-rune gives %q", input, cut, gotText, wantText)
			}
		}
	}
	chunk := []byte("shared")
	var decoder mistralTextDecoder
	text := decoder.decode(chunk)
	chunk[0] = 'X'
	if text != "shared" {
		t.Errorf("the decoded text aliases the chunk: %q", text)
	}
}
