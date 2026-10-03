package ai

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"
)

// redactSensitive (types.mjs:66-101) reads resp.text() in full and reports how many characters it cut: "... <N more chars>" counts every
// UTF-16 unit past the first 2000, however long the body. A body that cannot be JSON is counted without being held in memory.
func TestAnthropicFederationErrorBodyCountsEveryCharPastTheLimit(t *testing.T) {
	const prefix = "<htm>"
	for _, tc := range []struct {
		name string
		body string
		// units is the body's length in UTF-16 code units.
		units int
	}{
		{"ASCII text past 1 MiB", strings.Repeat("x", 2000+(1<<20)+37), 2000 + (1 << 20) + 37},
		{"markup with two- and four-byte characters", prefix + strings.Repeat("é😀", 600000), 5 + 3*600000},
		{"text a little over the old limit", strings.Repeat("y", (1<<20)+1), (1 << 20) + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := federationEnvFor(t)
			wire := &federationWire{token: func(_ int, w http.ResponseWriter) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = fmt.Fprint(w, tc.body)
			}}
			baseURL := wire.serve(t)
			message := streamFederation(t, AnthropicConfig{ProviderID: "anthropic", BaseURL: baseURL}, StreamOptions{Env: env})
			want := "Token exchange failed with status 500: " + firstUTF16Units(tc.body, 2000) + fmt.Sprintf("... <%d more chars>", tc.units-2000)
			if message.StopReason != StopReasonError || message.ErrorMessage != want {
				t.Fatalf("stop reason = %q, error length = %d, tail = %q\nwant tail %q", message.StopReason, len(message.ErrorMessage), tail(message.ErrorMessage, 40), tail(want, 40))
			}
		})
	}
}

func firstUTF16Units(s string, n int) string {
	var out strings.Builder
	count := 0
	for _, r := range s {
		width := 1
		if r > 0xffff {
			width = 2
		}
		if count+width > n {
			break
		}
		out.WriteRune(r)
		count += width
	}
	return out.String()
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// readAnthropicTokenBody counts the whole body whatever the read boundaries are, including a character split across reads, and keeps only a bounded prefix.
func TestReadAnthropicTokenBodyCountsAcrossReadBoundaries(t *testing.T) {
	body := strings.Repeat("aé😀\xff\xe2\x82", 120000) // ends inside a 3-byte sequence; \xff is invalid
	for name, reader := range map[string]io.Reader{
		"whole":     strings.NewReader(body),
		"one byte":  iotest.OneByteReader(strings.NewReader(body)),
		"half":      iotest.HalfReader(strings.NewReader(body)),
		"data+EOF":  iotest.DataErrReader(strings.NewReader(body)),
		"odd reads": &chunkReader{data: []byte(body), size: 7},
	} {
		t.Run(name, func(t *testing.T) {
			got := readAnthropicTokenBody(reader)
			if want := utf16Length(body); got.units != want {
				t.Fatalf("units = %d, want %d", got.units, want)
			}
			if len(got.text) != anthropicMaxTokenResponseBytes || !bytes.Equal(got.text, []byte(body)[:anthropicMaxTokenResponseBytes]) || !got.truncated {
				t.Fatalf("kept %d bytes, truncated = %v", len(got.text), got.truncated)
			}
		})
	}
	short := readAnthropicTokenBody(strings.NewReader("small"))
	if string(short.text) != "small" || short.units != 5 || short.truncated {
		t.Fatalf("short body = %+v", short)
	}
}

type chunkReader struct {
	data []byte
	size int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := min(len(p), r.size, len(r.data))
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

// A prefix of a longer body decides "not JSON" only when no continuation can make the whole body valid JSON; otherwise the body keeps its bounded reading.
func TestAnthropicBodyCannotBeJSON(t *testing.T) {
	for _, tc := range []struct {
		prefix string
		want   bool
	}{
		{"<html>", true},
		{"upstream down", true},
		{`{"error":"x"} trailing`, true},
		{`{"error":`, false},
		{`{"error":"unterminated`, false},
		{`[1,2,`, false},
		{`{"error":"x"}   `, false},
		{`12345`, false},
		{`{"error" "x"}`, true},
	} {
		if got := anthropicBodyCannotBeJSON([]byte(tc.prefix)); got != tc.want {
			t.Errorf("anthropicBodyCannotBeJSON(%q) = %v, want %v", tc.prefix, got, tc.want)
		}
	}
}
