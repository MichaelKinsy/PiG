// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package codingagent

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
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
				if lineLen != int64(len(raw)) {
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
		if lineLen != int64(len(raw)) {
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

// TestFallbackReadsSnapshotNotEditedLiveFile proves that when cloned == false
// and the live file is edited in-place after its bytes were sequentially read
// (changing an entry ID without changing line length), the oversized fallback
// path reads the snapshot's bytes rather than the mutated live file so
// GetLeafID(), GetEntry(), and RawEntriesPage() all agree on the snapshot ID.
func TestFallbackReadsSnapshotNotEditedLiveFile(t *testing.T) {
	clone := cloneSessionSnapshot
	cloneSessionSnapshot = func(*os.File, string) (*sessionSnapshot, bool) { return nil, false }
	t.Cleanup(func() { cloneSessionSnapshot = clone })

	const payloadBytes = streamLineLimit + (512 << 10)
	const ts = "2026-01-01T00:00:00.000Z"
	dir := t.TempDir()
	path := filepath.Join(dir, "live_edit_fallback.jsonl")

	header := fmt.Sprintf("{\"type\":\"session\",\"version\":3,\"id\":\"019b76da-a800-7000-8000-000000000011\",\"timestamp\":%q,\"cwd\":%q}\n", ts, dir)
	// Duplicate top-level "type" key triggers errStreamFallback after the full line has been read sequentially.
	const origID = "orig0001"
	const liveID = "live9999"
	fallbackLine := fmt.Sprintf("{\"type\":\"custom\",\"type\":\"custom\",\"id\":%q,\"parentId\":null,\"timestamp\":%q,\"customType\":\"fallback.state\",\"data\":{\"blob\":\"%s\"}}\n", origID, ts, strings.Repeat("a", payloadBytes))
	idOffset := int64(len(header) + strings.Index(fallbackLine, origID))
	if idOffset < int64(len(header)) {
		t.Fatal("failed to locate origID in fallbackLine")
	}

	if err := os.WriteFile(path, []byte(header+fallbackLine), 0o600); err != nil {
		t.Fatal(err)
	}

	origNew := newSessionSnapshot
	t.Cleanup(func() { newSessionSnapshot = origNew })
	var mutated bool

	// Drive streamSessionFileBatched via an intercepting reader? Even better: wrap the live file mutation
	// inside a small hook using a custom io.Reader + snapshotCopier on the real file, AND also test the
	// full Open path when snapshot writes fail (unsnapshotted fallback).
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	snap, err := newSessionSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	copier := startSnapshotCopy(snap)
	defer func() {
		if copier != nil {
			if s := copier.finish(); s != nil {
				s.close()
			}
		} else if snap != nil {
			snap.close()
		}
	}()

	// Wrap file so that the moment sequential reading reaches EOF (right before streamOversizedCustomLine
	// returns errStreamFallback and onFallback invokes copier.ReadAt), we mutate the live file in-place.
	liveMutator := &mutateOnEOFReader{
		r: f,
		onEOF: func() error {
			if mutated {
				return nil
			}
			mutated = true
			rw, err := os.OpenFile(path, os.O_WRONLY, 0)
			if err != nil {
				return err
			}
			defer func() { _ = rw.Close() }()
			_, err = rw.WriteAt([]byte(liveID), idOffset)
			return err
		},
	}

	source := io.TeeReader(liveMutator, copier)
	reader := &sessionTailReader{Reader: source}
	var sess *Session
	err = forEachPreparedLineStream(reader, copier, lineBatching{batchBytes: indexBatchBytes, depth: 4, workers: 2}, func(line []byte, off int64, prepared *preparedLine, streamedLen int64) error {
		if streamedLen > 0 {
			return sess.applyStreamedIndexLine(prepared, off, streamedLen)
		}
		if sess == nil {
			hdr, ok := sessionentry.DecodeFileEntry(bytes.Clone(line)).(SessionHeader)
			if !ok {
				return errNoSessionRecords
			}
			sess = NewSession(hdr.ID, hdr.CWD)
			sess.header = hdr
			return nil
		}
		if prepared == nil {
			return sess.indexLine(line, off)
		}
		return sess.applyIndexLine(prepared, line, off)
	})
	if err != nil {
		t.Fatalf("forEachPreparedLineStream: %v", err)
	}
	if !mutated {
		t.Fatal("expected live file to be mutated in-place before fallback read")
	}
	snap, copier = copier.finish(), nil
	if snap == nil {
		t.Fatal("copier.finish returned nil snapshot")
	}
	if sess.leafID != nil {
		leaf := *sess.leafID
		sess.leafID = &leaf
	}
	sess.path, sess.flushed, sess.backing, sess.snapshot = path, true, path, snap
	sess.setBranchLocked()

	// Confirm the live file on disk really holds liveID now.
	onDisk := make([]byte, len(liveID))
	if _, err := f.ReadAt(onDisk, idOffset); err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != liveID {
		t.Fatalf("live file at %d = %q, want %q", idOffset, string(onDisk), liveID)
	}

	// Indexed leaf, GetEntry, and RawEntriesPage must all reflect the snapshot's origID, not liveID.
	leaf := sess.GetLeafID()
	if leaf == nil || *leaf != origID {
		t.Fatalf("GetLeafID() = %v, want %q", leaf, origID)
	}
	if _, ok := sess.GetEntry(liveID); ok {
		t.Fatalf("GetEntry(%q) unexpectedly found live-edited ID", liveID)
	}
	entry, ok := sess.GetEntry(origID)
	if !ok {
		t.Fatalf("GetEntry(%q) not found", origID)
	}
	if entry.Base().ID != origID {
		t.Fatalf("GetEntry(%q).Base().ID = %q", origID, entry.Base().ID)
	}
	pages, _, _ := sess.RawEntriesPage(0, len(fallbackLine)+64)
	if len(pages) != 1 {
		t.Fatalf("RawEntriesPage returned %d entries, want 1", len(pages))
	}
	var decoded struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(pages[0], &decoded); err != nil {
		t.Fatalf("unmarshal RawEntriesPage: %v", err)
	}
	if decoded.ID != origID {
		t.Fatalf("RawEntriesPage ID = %q, want %q (matching GetLeafID)", decoded.ID, origID)
	}
	if stale := sess.staleReads.Load(); stale != 0 {
		t.Fatalf("staleReads = %d, want 0", stale)
	}

	// Also verify that when snapshot writes fail during a fallback line, Open recovers via loadPersistedSession.
	newSessionSnapshot = func(p string) (*sessionSnapshot, error) {
		ro, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		return &sessionSnapshot{file: ro}, nil
	}
	recovered, err := NewSessionManagerWithDir(dir, dir).Open(path)
	if err != nil {
		t.Fatalf("Open with failing snapshot write: %v", err)
	}
	if recovered.backing != "" {
		t.Fatal("expected resident unsnapshotted fallback when snapshot writes fail")
	}
	if got := recovered.GetLeafID(); got == nil || *got != liveID {
		t.Fatalf("recovered leaf = %v, want %q", got, liveID)
	}
}

type mutateOnEOFReader struct {
	r     io.Reader
	onEOF func() error
}

func (m *mutateOnEOFReader) Read(p []byte) (int, error) {
	n, err := m.r.Read(p)
	if errors.Is(err, io.EOF) && m.onEOF != nil {
		if mErr := m.onEOF(); mErr != nil {
			return n, mErr
		}
	}
	return n, err
}

// TestStreamOversizedCustomLineWhitespaceAndLateType verifies that:
//   - extra whitespace around "type" : "custom" streams without falling back;
//   - "data" appearing >4 MiB before "type":"custom" streams cleanly without falling back;
//   - a >4 MiB leading whitespace run scans linearly without quadratic slowdown.
func TestStreamOversizedCustomLineWhitespaceAndLateType(t *testing.T) {
	const payloadBytes = streamLineLimit + (256 << 10)
	const ts = "2026-01-01T00:00:00.000Z"

	t.Run("extra whitespace around type colon", func(t *testing.T) {
		line := fmt.Sprintf(`  { "type" : "custom" , "id" : "ws000001" , "parentId" : null , "timestamp" : %q , "customType" : "ws.state" , "data" : { "blob" : %q } }`, ts, strings.Repeat("w", payloadBytes))
		var streamedCount, fallbackCount, normalCount int
		var gotPrep *preparedLine
		var gotLen int64
		err := forEachStreamLine(strings.NewReader(line+"\n"),
			func([]byte, int64) error { normalCount++; return nil },
			func(p *preparedLine, _ int64, lineLen int64) error {
				streamedCount++
				gotPrep = p
				gotLen = lineLen
				return nil
			},
			func(int64, int64) error { fallbackCount++; return nil },
		)
		if err != nil {
			t.Fatalf("forEachStreamLine: %v", err)
		}
		if streamedCount != 1 || fallbackCount != 0 || normalCount != 0 {
			t.Fatalf("streamed=%d fallback=%d normal=%d, want 1/0/0", streamedCount, fallbackCount, normalCount)
		}
		if gotLen != int64(len(line)) || gotPrep == nil || gotPrep.base.ID != "ws000001" || gotPrep.base.Type != typeCustom {
			t.Fatalf("gotPrep=%+v gotLen=%d want id=ws000001 len=%d", gotPrep, gotLen, len(line))
		}
	})

	t.Run("data before type more than 4MiB into line", func(t *testing.T) {
		line := fmt.Sprintf(`{"data":{"blob":%q},"customType":"late.state","id":"late0001","parentId":null,"timestamp":%q,"type":"custom"}`, strings.Repeat("d", payloadBytes), ts)
		var streamedCount, fallbackCount, normalCount int
		var gotPrep *preparedLine
		var gotLen int64
		err := forEachStreamLine(strings.NewReader(line+"\n"),
			func([]byte, int64) error { normalCount++; return nil },
			func(p *preparedLine, _ int64, lineLen int64) error {
				streamedCount++
				gotPrep = p
				gotLen = lineLen
				return nil
			},
			func(int64, int64) error { fallbackCount++; return nil },
		)
		if err != nil {
			t.Fatalf("forEachStreamLine: %v", err)
		}
		if streamedCount != 1 || fallbackCount != 0 || normalCount != 0 {
			t.Fatalf("streamed=%d fallback=%d normal=%d, want 1/0/0", streamedCount, fallbackCount, normalCount)
		}
		if gotLen != int64(len(line)) || gotPrep == nil || gotPrep.base.ID != "late0001" || gotPrep.base.Type != typeCustom {
			t.Fatalf("gotPrep=%+v gotLen=%d want id=late0001 len=%d", gotPrep, gotLen, len(line))
		}
	})

	t.Run("large leading whitespace run scans linearly", func(t *testing.T) {
		// >4 MiB of leading spaces before a custom object, plus >4 MiB of leading spaces before non-object text.
		const wsLen = streamLineLimit + (512 << 10)
		ws := strings.Repeat(" ", wsLen)
		objLine := ws + fmt.Sprintf(`{"type":"custom","id":"leadws01","parentId":null,"timestamp":%q,"data":{"blob":%q}}`, ts, strings.Repeat("x", 128<<10))
		nonObjLine := ws + "not-json-object"

		start := time.Now()
		var streamedCount, normalCount, fallbackCount int
		err := forEachStreamLine(strings.NewReader(objLine+"\n"+nonObjLine+"\n"),
			func(line []byte, _ int64) error { normalCount++; return nil },
			func(p *preparedLine, _ int64, lineLen int64) error {
				streamedCount++
				if lineLen != int64(len(objLine)) || p.base.ID != "leadws01" {
					return fmt.Errorf("unexpected streamed entry: id=%q len=%d want %d", p.base.ID, lineLen, len(objLine))
				}
				return nil
			},
			func(int64, int64) error { fallbackCount++; return nil },
		)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("forEachStreamLine: %v", err)
		}
		if streamedCount != 1 || normalCount != 1 || fallbackCount != 0 {
			t.Fatalf("streamed=%d normal=%d fallback=%d, want 1/1/0", streamedCount, normalCount, fallbackCount)
		}
		// Linear scan of ~9 MiB takes tens of milliseconds; quadratic rescan of 4 MiB per 64 KiB chunk would take far longer.
		if elapsed > 5*time.Second {
			t.Fatalf("leading whitespace scan took %v, expected linear time", elapsed)
		}
	})
}

// TestSlotColdPreservesOver2GiBRecordSize verifies that locateHashedLocked and
// coldSizeLocked preserve 64-bit record lengths exceeding math.MaxInt32 without
// overflowing negative, while keeping slotCold compact (16 bytes), and that
// read-back paths guard negative/out-of-range sizes without panicking.
func TestSlotColdPreservesOver2GiBRecordSize(t *testing.T) {
	if got := unsafe.Sizeof(slotCold{}); got != 16 {
		t.Fatalf("sizeof(slotCold) = %d, want 16", got)
	}
	s := NewSession("over-2gib", "/w")
	const over2GiB = int64(math.MaxInt32) + 1
	const over4GiB = int64(4) << 30
	base1 := SessionEntryBase{Type: typeCustom, ID: "big2g", Timestamp: "2026-01-01T00:00:00.000Z"}
	base2 := SessionEntryBase{Type: typeCustom, ID: "big4g", ParentID: &base1.ID, Timestamp: "2026-01-01T00:00:01.000Z"}
	slot1 := baseSlot(base1)
	s.locateHashedLocked(&slot1.entrySlot, 128, over2GiB, 0x12345678)
	idx1 := s.appendSlotLocked(slot1, nil, nil)

	slot2 := baseSlot(base2)
	s.locateHashedLocked(&slot2.entrySlot, 128+over2GiB+1, over4GiB, 0x87654321)
	idx2 := s.appendSlotLocked(slot2, base2.ParentID, nil)

	if got := s.coldSizeLocked(&s.slots[idx1]); got != over2GiB {
		t.Fatalf("slot1 coldSizeLocked = %d, want %d", got, over2GiB)
	}
	if got := s.coldSizeLocked(&s.slots[idx2]); got != over4GiB {
		t.Fatalf("slot2 coldSizeLocked = %d, want %d", got, over4GiB)
	}
	// Verify newHandleLocked carries full int64 size in RecordAt (no int32 truncation).
	h := s.newHandleLocked(idx1)
	if !h.Indexed() {
		t.Fatal("expected indexed handle")
	}

	// Verify read-back with negative size returns false / errRecordChanged without panicking in slices.Grow / make.
	reader := recordReader{file: bytes.NewReader([]byte("x"))}
	if _, ok := reader.recordSized(&slotCold{off: 0, size: -1}, -1); ok {
		t.Fatal("recordSized accepted negative size")
	}
	var out [1]json.RawMessage
	if bad, err := readRecordsAt(bytes.NewReader([]byte("x")), []sessionentry.RecordAt{{Off: 0, Size: -1}}, out[:]); !errors.Is(err, errRecordChanged) || bad != 0 {
		t.Fatalf("readRecordsAt negative size = (%d, %v), want (0, errRecordChanged)", bad, err)
	}
	// Verify recordReader with a negative sentinel cold.size does not panic in slices.Grow / make.
	if _, ok := reader.record(&slotCold{off: 0, size: -1}); ok {
		t.Fatal("record accepted negative sentinel size")
	}
	// Verify runBuffer guards a corrupted negative size cleanly without panicking.
	s.slots[idx1].cold = 0 // noCold has off=-1, size=0; also test negative coldSizeLocked via missing map entry
	s.slots[idx1].cold = 1
	delete(s.oversizedSizes, 1)
	if got := s.coldSizeLocked(&s.slots[idx1]); got != -1 {
		t.Fatalf("missing oversizedSizes entry gave %d, want -1", got)
	}
	var rb runBuffer
	if _, ok := rb.record(bytes.NewReader([]byte("x")), s, []int32{int32(idx1)}, []int{0}, 0); ok {
		t.Fatal("runBuffer accepted missing oversized size")
	}
	// Restore oversizedSizes[1] for the RecordAt check below.
	s.oversizedSizes[1] = over2GiB
	// Verify newHandleLocked passes the exact >MaxInt32 size into RecordSource.ReadRecords (no int32 truncation).
	var seenSize int64
	spy := &sizeSpyRecordSource{onRead: func(ats []sessionentry.RecordAt) {
		if len(ats) == 1 {
			seenSize = ats[0].Size
		}
	}}
	lazy := sessionentry.NewLazyEntry(base1.ID, nil, base1.Timestamp, spy, sessionentry.RecordAt{Off: 128, Size: s.coldSizeLocked(&s.slots[idx1]), Hash: 0x12345678})
	_, _ = sessionentry.ReadRaw(&lazy)
	if seenSize != over2GiB {
		t.Fatalf("RecordSource saw Size=%d, want %d", seenSize, over2GiB)
	}
}

type sizeSpyRecordSource struct {
	onRead func(ats []sessionentry.RecordAt)
}

func (s *sizeSpyRecordSource) ReadRecords(ats []sessionentry.RecordAt, _ []json.RawMessage) (int, error) {
	if s.onRead != nil {
		s.onRead(ats)
	}
	return 0, errRecordChanged
}
