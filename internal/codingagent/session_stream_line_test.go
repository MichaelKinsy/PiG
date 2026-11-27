// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package codingagent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsonvalid"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// TestStreamCustomParserMatchesValidAndPrepare checks that streamCustomParser
// agrees with jsonvalid.Valid, recordHash, and prepareIndexLineAfter across
// normal, malformed, escaped, CRLF, duplicate-key, and fallback shapes.
func TestStreamCustomParserMatchesValidAndPrepare(t *testing.T) {
	cases := []struct {
		name         string
		line         string
		wantValid    bool
		wantStreamed bool
	}{
		{
			name:         "plain custom object",
			line:         `{"type":"custom","id":"00000002","parentId":"00000001","timestamp":"2026-01-01T00:00:00.000Z","customType":"example.state","data":{"z":1,"payload":"hello\nworld\"\\\/\b\f\r\t\u00e9","arr":[1,-2,0,3.14,1e-5,true,false,null,{}]}}`,
			wantValid:    true,
			wantStreamed: true,
		},
		{
			name:         "null parentId and null data",
			line:         `{"type":"custom","id":"00000001","parentId":null,"timestamp":"2026-01-01T00:00:00.000Z","customType":"ext","data":null}`,
			wantValid:    true,
			wantStreamed: true,
		},
		{
			name:         "absent parentId and absent timestamp",
			line:         `{"type":"custom","id":"00000001","customType":"ext","data":{"k":"v"}}`,
			wantValid:    true,
			wantStreamed: true,
		},
		{
			name:         "fields after large data",
			line:         `{"type":"custom","customType":"ext","data":{"blob":"` + strings.Repeat("a", 500) + `"},"id":"00000009","parentId":"00000008","timestamp":"2026-01-01T00:00:09.000Z"}`,
			wantValid:    true,
			wantStreamed: true,
		},
		{
			name:         "duplicate type triggers fallback",
			line:         `{"type":"custom","type":"custom","id":"00000002","parentId":null,"timestamp":"2026-01-01T00:00:00.000Z","data":{}}`,
			wantValid:    true,
			wantStreamed: false,
		},
		{
			name:         "escaped key triggers fallback",
			line:         `{"type":"custom","i\u0064":"00000002","parentId":null,"timestamp":"2026-01-01T00:00:00.000Z","data":{}}`,
			wantValid:    true,
			wantStreamed: false,
		},
		{
			name:         "numeric id triggers fallback",
			line:         `{"type":"custom","id":42,"parentId":null,"timestamp":"2026-01-01T00:00:00.000Z","data":{}}`,
			wantValid:    true,
			wantStreamed: false,
		},
		{
			name:         "truncated JSON is invalid",
			line:         `{"type":"custom","id":"00000002","parentId":null,"data":{"payload":"unterminated`,
			wantValid:    false,
			wantStreamed: false,
		},
		{
			name:         "control char in string is invalid",
			line:         "{\"type\":\"custom\",\"id\":\"00000002\",\"data\":\"bad\x01char\"}",
			wantValid:    false,
			wantStreamed: false,
		},
		{
			name:         "trailing garbage is invalid",
			line:         `{"type":"custom","id":"00000002","data":{}} trailing`,
			wantValid:    false,
			wantStreamed: false,
		},
		{
			name:         "bad escape is invalid",
			line:         `{"type":"custom","id":"00000002","data":"bad\x20escape"}`,
			wantValid:    false,
			wantStreamed: false,
		},
		{
			name:         "bad number leading zero is invalid",
			line:         `{"type":"custom","id":"00000002","data":01}`,
			wantValid:    false,
			wantStreamed: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(tc.line)
			if got := jsonvalid.Valid(raw); got != tc.wantValid {
				t.Fatalf("jsonvalid.Valid = %v, want %v", got, tc.wantValid)
			}
			for _, suffix := range []string{"\n", "\r\n", ""} {
				// Feed in awkward tiny chunks to stress boundary transitions (including \r across chunks).
				splitAt := min(len(raw), 17)
				if splitAt == 0 {
					splitAt = len(raw)
				}
				r := bufio.NewReaderSize(strings.NewReader(tc.line[splitAt:]+suffix), 16)
				prep, lineLen, consumed, _, err := streamOversizedCustomLine(raw[:splitAt], r)
				if int(consumed) != len(raw)+len(suffix) {
					t.Fatalf("suffix %q: consumed=%d, want %d", suffix, consumed, len(raw)+len(suffix))
				}
				if !tc.wantValid {
					if err != nil || prep != nil {
						t.Fatalf("suffix %q: invalid line returned prep=%v err=%v, want nil, nil", suffix, prep, err)
					}
					continue
				}
				if lineLen != len(raw) {
					t.Fatalf("suffix %q: lineLen=%d, want %d", suffix, lineLen, len(raw))
				}
				if !tc.wantStreamed {
					if !errors.Is(err, errStreamFallback) {
						t.Fatalf("suffix %q: err=%v, want errStreamFallback", suffix, err)
					}
					continue
				}
				if err != nil || prep == nil {
					t.Fatalf("suffix %q: prep=%v err=%v, want non-nil, nil", suffix, prep, err)
				}
				want := prepareIndexLineAfter(raw, "")
				if prep.state != want.state || !reflect.DeepEqual(prep.base, want.base) || prep.hash != want.hash {
					t.Fatalf("suffix %q: streamed=%+v, want=%+v", suffix, *prep, want)
				}
			}
		})
	}
}

// TestOversizedCustomEntryEndToEnd exercises the >streamLineLimit streaming
// path through Open, GetEntry, GetBranch, GetEntries, GetTree, Clone,
// ExportSessionToJsonl, and fallback on duplicate keys, verifying byte-identical
// results vs LoadEntriesFromFile.
func TestOversizedCustomEntryEndToEnd(t *testing.T) {
	const payloadBytes = streamLineLimit + (512 << 10)
	dir := t.TempDir()
	path := filepath.Join(dir, "oversized.jsonl")
	const ts = "2026-01-01T00:00:00.000Z"

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriter(f)
	writeLine := func(format string, args ...any) {
		t.Helper()
		if _, err := fmt.Fprintf(w, format, args...); err != nil {
			t.Fatal(err)
		}
	}
	writeLine("{\"type\":\"session\",\"version\":3,\"id\":\"019b76da-a800-7000-8000-000000000009\",\"timestamp\":%q,\"cwd\":%q}\n", ts, dir)
	writeLine("{\"type\":\"message\",\"id\":\"00000001\",\"parentId\":null,\"timestamp\":%q,\"message\":{\"role\":\"user\",\"content\":\"hello\",\"timestamp\":1}}\n", ts)
	// Oversized normal custom entry (>4 MiB) -> streamed path.
	writeLine("{\"type\":\"custom\",\"id\":\"00000002\",\"parentId\":\"00000001\",\"timestamp\":%q,\"customType\":\"big.state\",\"data\":{\"z\":2,\"payload\":\"%s\",\"a\":1}}\r\n", ts, strings.Repeat("q", payloadBytes))
	// Oversized malformed custom entry (>4 MiB) -> skipped.
	writeLine("{\"type\":\"custom\",\"id\":\"bad00001\",\"parentId\":\"00000002\",\"data\":\"%s\n", strings.Repeat("m", payloadBytes))
	// Oversized custom entry with duplicate key -> fallback path.
	writeLine("{\"type\":\"custom\",\"type\":\"custom\",\"id\":\"00000003\",\"parentId\":\"00000002\",\"timestamp\":%q,\"customType\":\"fallback.state\",\"data\":{\"blob\":\"%s\"}}\n", ts, strings.Repeat("f", payloadBytes))
	// Trailing normal message.
	writeLine("{\"type\":\"message\",\"id\":\"00000004\",\"parentId\":\"00000003\",\"timestamp\":%q,\"message\":{\"role\":\"user\",\"content\":\"after\",\"timestamp\":2}}\n", ts)
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	session, err := NewSessionManagerWithDir(dir, dir).Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if session.backing == "" {
		t.Fatal("expected file-backed indexed session")
	}
	entries := session.GetEntries()
	if len(entries) != 4 {
		t.Fatalf("got %d entries, want 4", len(entries))
	}
	if got, ok := entries[1].(CustomEntry); !ok || got.ID != "00000002" || got.CustomType != "big.state" {
		t.Fatalf("entry 1 = %T %+v", entries[1], entries[1])
	} else {
		enc, err := json.Marshal(got.Data)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(enc, []byte(`{"z":2,"payload":"`)) || !bytes.HasSuffix(enc, []byte(`","a":1}`)) {
			t.Fatalf("key order not preserved on streamed custom entry")
		}
	}
	if got, ok := entries[2].(CustomEntry); !ok || got.ID != "00000003" || got.CustomType != "fallback.state" {
		t.Fatalf("entry 2 = %T %+v", entries[2], entries[2])
	}

	// Verify byte parity with full resident load.
	whole := residentCopy(t, path)
	sameEntries(t, "oversized GetEntries", entries, whole.GetEntries())
	sameEntries(t, "oversized GetBranch", session.GetBranch(), whole.GetBranch())

	// Verify HandleReads + ReadRaws works with the streamed entry's hash.
	raws, err := sessionentry.ReadRaws(HandleReads{Session: session}.GetEntries())
	if err != nil {
		t.Fatalf("ReadRaws: %v", err)
	}
	for i, raw := range raws {
		if !bytes.Equal(raw, whole.GetEntries()[i].Raw()) {
			t.Fatalf("HandleReads raw %d differs", i)
		}
	}
	if stale := session.staleReads.Load(); stale != 0 {
		t.Fatalf("staleReads = %d, want 0", stale)
	}

	// Verify Clone streams oversized custom entry intact.
	clone, err := NewSessionManagerWithDir(dir, filepath.Join(dir, "clones")).Clone(session, "00000004")
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if len(clone.GetEntries()) != 4 {
		t.Fatalf("clone entries = %d, want 4", len(clone.GetEntries()))
	}
}

// TestResidentEntryTypesHoldNoRedundantRecord verifies that resident entries of
// every type drop their redundant raw JSON copy when re-encoding their decoded
// fields reproduces the line byte-for-byte, and retain the raw copy otherwise.
func TestResidentEntryTypesHoldNoRedundantRecord(t *testing.T) {
	lines := []string{
		`{"type":"session","version":3,"id":"s1","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/w"}`,
		`{"type":"message","id":"m1","parentId":null,"timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"user","content":"hi","timestamp":1}}`,
		`{"type":"thinking_level_change","id":"t1","parentId":"m1","timestamp":"2026-01-01T00:00:02.000Z","thinkingLevel":"high"}`,
		`{"type":"model_change","id":"mc","parentId":"t1","timestamp":"2026-01-01T00:00:03.000Z","provider":"p","modelId":"m"}`,
		`{"type":"usage","id":"us","parentId":"mc","timestamp":"2026-01-01T00:00:04.000Z","kind":"cache_warm","provider":"p","model":"m","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}`,
		`{"type":"label","id":"lb","parentId":"us","timestamp":"2026-01-01T00:00:05.000Z","targetId":"m1","label":"mark"}`,
		`{"type":"session_info","id":"si","parentId":"lb","timestamp":"2026-01-01T00:00:06.000Z","name":"n"}`,
		`{"type":"compaction","id":"co","parentId":"si","timestamp":"2026-01-01T00:00:07.000Z","summary":"s","firstKeptEntryId":"m1","tokensBefore":1,"fromHook":false}`,
		`{"type":"context_edit","id":"ce","parentId":"co","timestamp":"2026-01-01T00:00:08.000Z","targetId":"m1","replacement":null}`,
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := NewSessionManagerWithDir(dir, dir).Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for i, entry := range session.GetEntries() {
		if got := string(entry.Raw()); got != lines[i+1] {
			t.Fatalf("entry %s Raw() = %s, want %s", entry.Base().ID, got, lines[i+1])
		}
		if holdsItsRecord(entry) {
			t.Fatalf("canonical resident entry %s (%T) still retains its raw record alongside its decoded fields", entry.Base().ID, entry)
		}
	}
}

// FuzzStreamCustomLineMatchesFullLine verifies that the streaming parser of an
// oversized custom line agrees with jsonvalid.Valid and, whenever it indexes
// the line itself, produces the exact state, base metadata, and hash of
// prepareIndexLineAfter across arbitrary split points, reader buffer sizes, and
// line endings.
func FuzzStreamCustomLineMatchesFullLine(f *testing.F) {
	seeds := []string{
		`{"type":"custom","id":"00000002","parentId":"00000001","timestamp":"2026-01-01T00:00:00.000Z","customType":"example.state","data":{"z":1,"payload":"hello\nworld\"\\\/\b\f\r\t\u00e9","arr":[1,2.5,-3e4,true,false,null,{}]}}`,
		`{"type":"custom","id":"00000001","parentId":null,"timestamp":"2026-01-01T00:00:00.000Z","customType":"ext","data":null}`,
		`{"type":"custom","id":"00000001","customType":"ext","data":{"k":"v"}}`,
		`{"type":"custom","customType":"ext","data":{"blob":"aaaa"},"id":"00000009","parentId":"00000008","timestamp":"2026-01-01T00:00:09.000Z"}`,
		`{"type":"custom","type":"custom","id":"00000002","parentId":null,"data":{}}`,
		`{"type":"custom","i\u0064":"00000002","parentId":null,"data":{}}`,
		`{"type":"custom","id":42,"parentId":null,"data":{}}`,
		`{"type":"custom","id":"x","data":01}`,
		`{"type":"custom","id":"x","data":{}} trailing`,
		` {"type": "custom" , "id" : "a" , "parentId" : "b", "data" : [ ] }  `,
		`{"type":"custom","id":"","parentId":"","timestamp":"","data":"\ud800"}`,
		`{"type":"custom","id":null,"parentId":{"a":1},"timestamp":5,"data":-0.0e-0}`,
		`{"type":"message","id":"m","message":{"role":"user","content":"\"type\":\"custom\""}}`,
		`{"data":{"type":"custom"},"type":"custom","id":"n"}`,
		"{\"type\":\"custom\",\"id\":\"a\",\"data\":\"x\r\"}",
		`{"type":"custom","id":"a","data":"x"}` + "\r",
	}
	for _, s := range seeds {
		f.Add([]byte(s), uint16(17), uint8(0), uint8(0))
		f.Add([]byte(s), uint16(1), uint8(3), uint8(1))
		f.Add([]byte(s), uint16(len(s)), uint8(40), uint8(2))
	}
	f.Fuzz(func(t *testing.T, raw []byte, split uint16, bufExtra uint8, suffixSel uint8) {
		if len(raw) == 0 || bytes.IndexByte(raw, '\n') >= 0 || raw[len(raw)-1] == '\r' {
			t.Skip()
		}
		i := skipJSONSpace(raw, 0)
		if i >= len(raw) || raw[i] != '{' {
			t.Skip()
		}
		at := int(split)%len(raw) + 1
		suffix := []string{"\n", "\r\n", ""}[int(suffixSel)%3]
		r := bufio.NewReaderSize(strings.NewReader(string(raw[at:])+suffix), 16+int(bufExtra))
		prep, lineLen, consumed, _, err := streamOversizedCustomLine(bytes.Clone(raw[:at]), r)
		if int(consumed) != len(raw)+len(suffix) {
			t.Fatalf("consumed=%d want %d (split %d suffix %q) line %q", consumed, len(raw)+len(suffix), at, suffix, raw)
		}
		valid := jsonvalid.Valid(raw)
		if !valid {
			if prep != nil || err != nil {
				t.Fatalf("invalid line gave prep=%v err=%v (split %d suffix %q) line %q", prep, err, at, suffix, raw)
			}
			return
		}
		if lineLen != len(raw) {
			t.Fatalf("lineLen=%d want %d (split %d suffix %q) line %q", lineLen, len(raw), at, suffix, raw)
		}
		if err != nil {
			if !errors.Is(err, errStreamFallback) || prep != nil {
				t.Fatalf("valid line gave err=%v prep=%v line %q", err, prep, raw)
			}
			return
		}
		if prep == nil {
			t.Fatalf("valid line was dropped: no prep and no fallback (split %d suffix %q) line %q", at, suffix, raw)
		}
		want := prepareIndexLineAfter(raw, "")
		if prep.state != want.state || !reflect.DeepEqual(prep.base, want.base) || prep.hash != want.hash {
			t.Fatalf("streamed differs from the full-line path (split %d suffix %q)\n line %q\n streamed %+v\n full     %+v", at, suffix, raw, prep.base, want.base)
		}
	})
}
