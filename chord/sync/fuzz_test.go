package sync

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// logLineSeeds are lines of the shapes the decoder tells apart: valid, and each way a line stops being one.
var logLineSeeds = []string{
	`{"v":2,"seq":1,"nextId":1,"writes":[]}`,
	`{"v":2,"seq":2,"nextId":7,"writes":[{"op":"create","address":{"namespace":"chord","owner":"app.sync","kind":"note"}},[1,"a",null]]}`,
	`{"v":2,"seq":9007199254740991,"nextId":0,"writes":[]}`,
	`{"v":2,"seq":9007199254740992,"nextId":0,"writes":[]}`,
	`{"v":2,"seq":0,"nextId":1,"writes":[]}`,
	`{"v":2,"seq":1.5,"nextId":1,"writes":[]}`,
	`{"v":2,"seq":1,"nextId":-1,"writes":[]}`,
	`{"v":1,"seq":1,"nextId":1,"writes":[]}`,
	`{"v":"2","seq":1,"nextId":1,"writes":[]}`,
	`{"v":2,"seq":1,"nextId":1,"writes":{}}`,
	`{"v":2,"seq":1,"nextId":1}`,
	`{"v":2,"seq":1e0,"nextId":1,"writes":[],"extra":{"a":[{}]}}`,
	`[]`, `null`, `"x"`, `{`, ``, `{"v":2,"seq":1,"nextId":1,"writes":[]} x`, "\ufeff{}", "{\"v\":2\x00}",
}

// FuzzDecodeLogLine: DecodeLogLine never panics, fails only with a *LogFormatError, and a line it accepts names a
// commit with a valid sequence that EncodeLogLine writes as a line decoding to the same commit.
func FuzzDecodeLogLine(f *testing.F) {
	for _, seed := range logLineSeeds {
		f.Add(seed, 1)
	}
	f.Add(logLineSeeds[1], -3)
	f.Fuzz(func(t *testing.T, text string, number int) {
		commit, err := DecodeLogLine(text, number)
		if err != nil {
			if _, ok := errors.AsType[*LogFormatError](err); !ok {
				t.Fatalf("DecodeLogLine(%q) failed with %T %v, want a *LogFormatError", text, err, err)
			}
			if !strings.Contains(err.Error(), fmt.Sprint(number)) {
				t.Fatalf("DecodeLogLine(%q, %d) error %q does not name the line", text, number, err)
			}
			return
		}
		if commit.Seq < 1 || commit.Seq > store.MaxSafeInteger || commit.NextID < -store.MaxSafeInteger || commit.NextID > store.MaxSafeInteger {
			t.Fatalf("DecodeLogLine(%q) accepted seq %d, nextId %d", text, commit.Seq, commit.NextID)
		}
		encoded, err := EncodeLogLine(commit)
		if err != nil {
			t.Fatalf("EncodeLogLine of the commit of %q: %v", text, err)
		}
		if !bytes.HasSuffix(encoded, []byte("\n")) {
			t.Fatalf("EncodeLogLine(%q) = %q, want a newline-terminated line", text, encoded)
		}
		again, err := DecodeLogLine(string(bytes.TrimSuffix(encoded, []byte("\n"))), number)
		if err != nil {
			t.Fatalf("the encoded line %q of %q does not decode: %v", encoded, text, err)
		}
		reencoded, err := EncodeLogLine(again)
		if err != nil || !bytes.Equal(reencoded, encoded) {
			t.Fatalf("encode(decode(encode(%q))) = %q, %v; want %q", text, reencoded, err, encoded)
		}
	})
}

// decoded is what a batch body decodes to: its commits as lines, in order, and how it ended ("" when it ended cleanly).
type decoded struct {
	lines   []string
	sizes   []int
	failure string
}

// decodeAll reads every line of body, checking what each must hold.
func decodeAll(t *testing.T, body SyncBody) decoded {
	t.Helper()
	lines := DecodeBatchLines(body)
	defer func() { _ = lines.Close() }()
	var out decoded
	var previous int64
	for {
		line, ok, err := lines.Next(background)
		if err != nil {
			if _, ok := errors.AsType[*LogFormatError](err); !ok {
				t.Fatalf("DecodeBatchLines failed with %T %v, want a *LogFormatError", err, err)
			}
			out.failure = err.Error()
			if again, ok, afterwards := lines.Next(background); ok || afterwards != nil || again.Bytes != 0 {
				t.Fatalf("Next after a failure returned %v, %v, %v, want the end", again, ok, afterwards)
			}
			return out
		}
		if !ok {
			return out
		}
		if line.Commit.Seq <= previous {
			t.Fatalf("sequence %d follows %d", line.Commit.Seq, previous)
		}
		previous = line.Commit.Seq
		if line.Bytes < 2 || line.Bytes > MaxLineBytes+1 {
			t.Fatalf("line of %d bytes for seq %d", line.Bytes, line.Commit.Seq)
		}
		encoded, err := EncodeLogLine(line.Commit)
		if err != nil {
			t.Fatalf("EncodeLogLine of a decoded commit: %v", err)
		}
		out.lines = append(out.lines, string(encoded))
		out.sizes = append(out.sizes, line.Bytes)
	}
}

// FuzzDecodeBatchLines: a batch body of any bytes decodes without a panic or a hang, fails only with a *LogFormatError
// (or the source's own error, which these sources never raise), yields strictly increasing sequences, and gives the same
// lines and the same ending whatever the size of the chunks it arrives in. plain is the text of a body: gzip-compressed
// when raw is false, so the fuzzer reaches the line decoder; as it is when raw is true, so it reaches the gzip checks.
func FuzzDecodeBatchLines(f *testing.F) {
	join := func(lines ...string) []byte { return []byte(strings.Join(lines, "\n")) }
	f.Add(join(logLineSeeds[0], `{"v":2,"seq":2,"nextId":2,"writes":[]}`, ""), false, uint8(0))
	f.Add(join(logLineSeeds[1], logLineSeeds[0], ""), false, uint8(3))
	f.Add(join(logLineSeeds[0], logLineSeeds[0], ""), false, uint8(1))
	f.Add(join(logLineSeeds[0]), false, uint8(5))
	f.Add([]byte("\ufeff"+logLineSeeds[0]+"\n"), false, uint8(2))
	f.Add([]byte(logLineSeeds[0]+"\n\xff\xfe\n"), false, uint8(7))
	f.Add([]byte("\n"), false, uint8(0))
	f.Add([]byte(""), false, uint8(0))
	f.Add([]byte("not gzip"), true, uint8(4))
	var valid bytes.Buffer
	writer := gzip.NewWriter(&valid)
	_, _ = writer.Write(join(logLineSeeds[0], ""))
	_ = writer.Close()
	f.Add(valid.Bytes(), true, uint8(0))
	f.Add(valid.Bytes()[:valid.Len()-3], true, uint8(1))
	f.Fuzz(func(t *testing.T, plain []byte, raw bool, chunk uint8) {
		body := plain
		if !raw {
			var buffer bytes.Buffer
			writer := gzip.NewWriter(&buffer)
			if _, err := writer.Write(plain); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			body = buffer.Bytes()
		}
		whole := decodeAll(t, chunked(body, max(len(body), 1)))
		size := int(chunk)%16 + 1
		pieces := decodeAll(t, chunked(body, size))
		if fmt.Sprint(whole) != fmt.Sprint(pieces) {
			t.Fatalf("body of %d bytes decodes to %v whole and to %v in chunks of %d", len(body), whole, pieces, size)
		}
	})
}
