package contracttest

import (
	"bufio"
	"bytes"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// Reporter is what the vector functions need of a test: *testing.T or testing.TB, or a recorder when a test proves a check fails
// on a broken implementation.
type Reporter interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
}

//go:embed testdata/oracle/*.jsonl
var oracle embed.FS

func rows[R any](tb Reporter, name string) []R {
	tb.Helper()
	data, err := oracle.ReadFile("testdata/oracle/" + name)
	if err != nil {
		tb.Fatalf("oracle vectors %s: %v", name, err)
	}
	var out []R
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		var row R
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			tb.Fatalf("oracle vectors %s: %v", name, err)
		}
		out = append(out, row)
	}
	if err := sc.Err(); err != nil {
		tb.Fatalf("oracle vectors %s: %v", name, err)
	}
	if len(out) == 0 {
		tb.Fatalf("oracle vectors %s are empty", name)
	}
	return out
}

// EncodeVector is JSON text and the bytes JSON.stringify(JSON.parse(In)) produces: insertion order (integer-like keys
// first, ascending), ECMAScript number formatting, only `"`, `\` and C0 controls escaped, lone surrogates as \udXXX.
type EncodeVector struct {
	In  string `json:"in"`
	Out string `json:"out"`
}

// EncodeVectors returns the encoder vectors.
func EncodeVectors(tb Reporter) []EncodeVector { return rows[EncodeVector](tb, "encode.jsonl") }

// CheckEncode runs every encoder vector: encode reads JSON text and writes what JSON.stringify writes for the parsed value.
func CheckEncode(t Reporter, encode func(in []byte) ([]byte, error)) {
	t.Helper()
	for i, v := range EncodeVectors(t) {
		got, err := encode([]byte(v.In))
		if err != nil {
			t.Errorf("encode vector %d %q: %v", i, v.In, err)
			continue
		}
		if string(got) != v.Out {
			t.Errorf("encode vector %d\n in:   %q\n want: %q\n got:  %q", i, v.In, v.Out, got)
		}
	}
}

// Index is what a JSON.parse-based extraction reads from an entry record; the scanner must produce the same without decoding.
// Head is nil when the record has no `head`; Role and StopReason are empty when the first model message lacks them.
type Index struct {
	Kind           string `json:"kind"`
	ID             int64  `json:"id"`
	ConversationID int64  `json:"conversationId"`
	Head           *int64 `json:"head"`
	Role           string `json:"role"`
	StopReason     string `json:"stopReason"`
	Messages       int    `json:"messages"`
}

// Equal compares every field, Head by value.
func (a Index) Equal(b Index) bool {
	if (a.Head == nil) != (b.Head == nil) || (a.Head != nil && *a.Head != *b.Head) {
		return false
	}
	a.Head, b.Head = nil, nil
	return a == b
}

// ReferenceIndex reads an entry record with a JSON decoder: what the core's scanner must produce without decoding. It rejects what
// the decoder rejects (invalid JSON, a number that is not an integer id).
func ReferenceIndex(record []byte) (Index, error) {
	var o struct {
		Kind           string `json:"kind"`
		ID             int64  `json:"id"`
		ConversationID int64  `json:"conversationId"`
		Head           *int64 `json:"head"`
		Model          []struct {
			Role       string `json:"role"`
			StopReason string `json:"stopReason"`
		} `json:"model"`
	}
	if err := json.Unmarshal(record, &o); err != nil {
		return Index{}, err
	}
	ix := Index{Kind: o.Kind, ID: o.ID, ConversationID: o.ConversationID, Head: o.Head, Messages: len(o.Model)}
	if len(o.Model) > 0 {
		ix.Role, ix.StopReason = o.Model[0].Role, o.Model[0].StopReason
	}
	return ix, nil
}

// ScanVector is an entry record (any key order, unknown fields, escaped keys, nested look-alike keys) and its index.
type ScanVector struct {
	Record string
	Index  Index
}

// ScanVectors returns the scanner vectors.
func ScanVectors(tb Reporter) []ScanVector {
	tb.Helper()
	type wire struct {
		Record string `json:"record"`
		Index  struct {
			Kind           string  `json:"kind"`
			ID             int64   `json:"id"`
			ConversationID int64   `json:"conversationId"`
			Head           *int64  `json:"head"`
			Role           *string `json:"role"`
			StopReason     *string `json:"stopReason"`
			Messages       int     `json:"messages"`
		} `json:"index"`
	}
	var out []ScanVector
	for _, w := range rows[wire](tb, "scan.jsonl") {
		v := ScanVector{Record: w.Record, Index: Index{Kind: w.Index.Kind, ID: w.Index.ID, ConversationID: w.Index.ConversationID, Head: w.Index.Head, Messages: w.Index.Messages}}
		if w.Index.Role != nil {
			v.Index.Role = *w.Index.Role
		}
		if w.Index.StopReason != nil {
			v.Index.StopReason = *w.Index.StopReason
		}
		out = append(out, v)
	}
	return out
}

// CheckScan runs every scanner vector.
func CheckScan(t Reporter, scan func(record []byte) (Index, error)) {
	t.Helper()
	for i, v := range ScanVectors(t) {
		got, err := scan([]byte(v.Record))
		if err != nil {
			t.Errorf("scan vector %d: %v\n%s", i, err, v.Record)
			continue
		}
		if !got.Equal(v.Index) {
			t.Errorf("scan vector %d\n record: %s\n want:   %+v\n got:    %+v", i, v.Record, v.Index, got)
		}
	}
}

// ChordVector is a document before and after an edit and the ops @earendil-works/chord's diffRevisions returns, as the
// compact JSON text JSON.stringify writes (Chord Op tuples: `r`, `s`, `d`, `a`, `t`, ...).
type ChordVector struct {
	Before json.RawMessage `json:"before"`
	After  json.RawMessage `json:"after"`
	Ops    json.RawMessage `json:"ops"`
}

// ChordVectors returns the Chord vectors.
func ChordVectors(tb Reporter) []ChordVector { return rows[ChordVector](tb, "chord.jsonl") }

// CheckChord runs every Chord vector: diff returns the ops as JSON text.
func CheckChord(t Reporter, diff func(before, after []byte) ([]byte, error)) {
	t.Helper()
	for i, v := range ChordVectors(t) {
		got, err := diff(v.Before, v.After)
		if err != nil {
			t.Errorf("chord vector %d: %v", i, err)
			continue
		}
		if !bytes.Equal(got, v.Ops) {
			t.Errorf("chord vector %d\n before: %.200s\n after:  %.200s\n want:   %.300s\n got:    %.300s", i, v.Before, v.After, v.Ops, got)
		}
	}
}

// ArgsVector is pi-ai validateToolArguments(Tool, Call): the converted arguments as JSON text (OK, key order included, which
// coerceWithJsonSchema can change), or the error message text. PlainSchema means Tool.parameters is a plain JSON schema, not
// a TypeBox schema, which selects the coercion path that reassigns keys.
type ArgsVector struct {
	Tool        json.RawMessage `json:"tool"`
	PlainSchema bool            `json:"plainSchema"`
	Call        struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"call"`
	OK    *string `json:"ok"`
	Error *string `json:"error"`
}

// ArgsVectors returns the tool-argument vectors.
func ArgsVectors(tb Reporter) []ArgsVector { return rows[ArgsVector](tb, "args.jsonl") }

// CheckArgs runs every vector: validate returns the arguments JSON, or a non-empty message when validation fails.
func CheckArgs(t Reporter, validate func(tool []byte, plainSchema bool, name string, arguments []byte) (ok []byte, errMessage string)) {
	t.Helper()
	for i, v := range ArgsVectors(t) {
		ok, msg := validate(v.Tool, v.PlainSchema, v.Call.Name, v.Call.Arguments)
		switch {
		case v.Error != nil && msg != *v.Error:
			t.Errorf("args vector %d (%s %s)\n want error: %q\n got:        %q / %q", i, v.Call.Name, v.Call.Arguments, *v.Error, msg, ok)
		case v.OK != nil && (msg != "" || string(ok) != *v.OK):
			t.Errorf("args vector %d (%s %s)\n want: %s\n got:  %s (error %q)", i, v.Call.Name, v.Call.Arguments, *v.OK, ok, msg)
		}
	}
}

// UUIDCall is one uuidv7() call: the clock reads Now, crypto.getRandomValues fills Random (32 hex digits) and Ts, when
// present, is the explicit timestamp argument (a follower id; it bypasses the process-wide monotonic timestamp).
type UUIDCall struct {
	Now    int64  `json:"now"`
	Ts     *int64 `json:"ts"`
	Random string `json:"random"`
}

// UUIDVector is a sequence of calls in one process; the generator's sequence counter and last timestamp carry across calls.
type UUIDVector struct {
	Calls []UUIDCall `json:"calls"`
	Out   []string   `json:"out"`
}

// UUIDVectors returns the uuidv7 vectors.
func UUIDVectors(tb Reporter) []UUIDVector { return rows[UUIDVector](tb, "uuid.jsonl") }

// CheckUUID runs every uuidv7 vector. newGenerator returns a fresh process-level generator over a clock and a random source;
// the generator takes the optional explicit timestamp.
func CheckUUID(t Reporter, newGenerator func(now func() int64, random func(dst []byte)) func(ts *int64) string) {
	t.Helper()
	for i, v := range UUIDVectors(t) {
		var cur UUIDCall
		gen := newGenerator(func() int64 { return cur.Now }, func(dst []byte) {
			b, err := hex.DecodeString(cur.Random)
			if err != nil || len(b) < len(dst) {
				t.Errorf("uuid vector %d: bad random bytes %q", i, cur.Random)
				return
			}
			copy(dst, b)
		})
		for j, c := range v.Calls {
			cur = c
			if got := gen(c.Ts); got != v.Out[j] {
				t.Errorf("uuid vector %d call %d: want %s, got %s", i, j, v.Out[j], got)
				break
			}
		}
	}
}

// Summary counts the vectors of each family, for reports.
func Summary(tb Reporter) string {
	tb.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "encode %d, scan %d, chord %d, args %d, uuid %d", len(EncodeVectors(tb)), len(ScanVectors(tb)), len(ChordVectors(tb)), len(ArgsVectors(tb)), len(UUIDVectors(tb)))
	return b.String()
}
