// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

//go:build !race

package codingagent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeCustomPayloadSession(t *testing.T, count, payloadBytes int) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriter(f)
	const ts = "2026-01-01T00:00:00.000Z"
	if _, err := fmt.Fprintf(w, "{\"type\":\"session\",\"version\":3,\"id\":\"019b76da-a800-7000-8000-000000000001\",\"timestamp\":%q,\"cwd\":%q}\n", ts, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(w, "{\"type\":\"message\",\"id\":\"00000001\",\"parentId\":null,\"timestamp\":%q,\"message\":{\"role\":\"user\",\"content\":\"hello\",\"timestamp\":1}}\n", ts); err != nil {
		t.Fatal(err)
	}
	chunk := strings.Repeat("x", 64<<10)
	for i := 1; i <= count; i++ {
		if _, err := fmt.Fprintf(w, "{\"type\":\"custom\",\"id\":\"%08x\",\"parentId\":\"%08x\",\"timestamp\":%q,\"customType\":\"example.state\",\"data\":{\"z\":%d,\"payload\":\"", i+1, i, ts, i); err != nil {
			t.Fatal(err)
		}
		for left := payloadBytes; left > 0; left -= len(chunk) {
			if _, err := w.WriteString(chunk[:min(left, len(chunk))]); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := w.WriteString("\",\"a\":1}}\n"); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeBranchSummaryPayloadSession(t *testing.T, count, summaryBytes int) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "branch-summary.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriter(f)
	const ts = "2026-01-01T00:00:00.000Z"
	if _, err := fmt.Fprintf(w, "{\"type\":\"session\",\"version\":3,\"id\":\"019b76da-a800-7000-8000-000000000002\",\"timestamp\":%q,\"cwd\":%q}\n", ts, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(w, "{\"type\":\"message\",\"id\":\"00000001\",\"parentId\":null,\"timestamp\":%q,\"message\":{\"role\":\"user\",\"content\":\"hello\",\"timestamp\":1}}\n", ts); err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("b", summaryBytes)
	for i := 1; i <= count; i++ {
		if _, err := fmt.Fprintf(w, "{\"type\":\"branch_summary\",\"id\":\"%08x\",\"parentId\":\"%08x\",\"timestamp\":%q,\"fromId\":\"%08x\",\"summary\":%q,\"details\":{\"z\":%d,\"a\":1},\"fromHook\":false}\n",
			i+1, i, ts, i, body, i); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestOpenSessionDoesNotRetainCustomPayloads verifies issue #575: opening a
// file-backed v3 session indexes custom entries lazily instead of decoding and
// retaining their payloads (or cloned lines) on the Go heap, while reading back
// an entry still returns its exact payload and JSON member order.
func TestOpenSessionDoesNotRetainCustomPayloads(t *testing.T) {
	cases := []struct {
		name         string
		count        int
		payloadBytes int
	}{
		{"many_medium", 256, 128 << 10}, // 32 MiB of custom payloads
		{"single_large", 1, 32 << 20},   // 32 MiB on a single line
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeCustomPayloadSession(t, tc.count, tc.payloadBytes)
			dir := filepath.Dir(path)
			before := heapLive()
			session, err := NewSessionManagerWithDir(dir, dir).Open(path)
			if err != nil {
				t.Fatal(err)
			}
			after := heapLive()
			retained := int64(after) - int64(before)
			totalPayload := int64(tc.count) * int64(tc.payloadBytes)
			const ceiling = int64(4 << 20) // 4 MiB ceiling for 32 MiB payload
			t.Logf("%s: totalPayload=%d B, retained=%d B (%.2f MiB)", tc.name, totalPayload, retained, float64(retained)/(1<<20))
			if retained > ceiling {
				t.Fatalf("opening session with %d B of custom payloads retained %d B (%.1f MiB); want at most %d B",
					totalPayload, retained, float64(retained)/(1<<20), ceiling)
			}

			// Verify reading an entry back returns the exact decoded payload and key order.
			lastID := fmt.Sprintf("%08x", tc.count+1)
			entry, ok := session.GetEntry(lastID)
			if !ok {
				t.Fatalf("GetEntry(%s) not found", lastID)
			}
			custom, ok := entry.(CustomEntry)
			if !ok {
				t.Fatalf("GetEntry(%s) returned %T, want CustomEntry", lastID, entry)
			}
			if custom.CustomType != "example.state" {
				t.Fatalf("CustomType = %q, want example.state", custom.CustomType)
			}
			encodedData, err := json.Marshal(custom.Data)
			if err != nil {
				t.Fatal(err)
			}
			wantPrefix := fmt.Sprintf(`{"z":%d,"payload":"`, tc.count)
			wantSuffix := `","a":1}`
			if !strings.HasPrefix(string(encodedData), wantPrefix) || !strings.HasSuffix(string(encodedData), wantSuffix) ||
				len(encodedData) != len(wantPrefix)+tc.payloadBytes+len(wantSuffix) {
				t.Fatalf("decoded data mismatch: len=%d, prefix=%q, suffix=%q",
					len(encodedData), encodedData[:min(32, len(encodedData))], encodedData[max(0, len(encodedData)-16):])
			}
			runtime.KeepAlive(session)
		})
	}
}

// TestOpenSessionDoesNotDoubleRetainBranchSummary verifies branch_summary
// entries never retain both the cloned raw line and decoded summary/details on
// open, and drop to well below payload size when indexed lazily.
func TestOpenSessionDoesNotDoubleRetainBranchSummary(t *testing.T) {
	const count, summaryBytes = 64, 256 << 10 // 16 MiB total
	path := writeBranchSummaryPayloadSession(t, count, summaryBytes)
	dir := filepath.Dir(path)
	before := heapLive()
	session, err := NewSessionManagerWithDir(dir, dir).Open(path)
	if err != nil {
		t.Fatal(err)
	}
	after := heapLive()
	retained := int64(after) - int64(before)
	totalPayload := int64(count) * int64(summaryBytes)
	const ceiling = int64(4 << 20)
	t.Logf("branch_summary: totalPayload=%d B, retained=%d B (%.2f MiB)", totalPayload, retained, float64(retained)/(1<<20))
	if retained > ceiling {
		t.Fatalf("opening session with %d B of branch_summary entries retained %d B (%.1f MiB); want at most %d B",
			totalPayload, retained, float64(retained)/(1<<20), ceiling)
	}
	lastID := fmt.Sprintf("%08x", count+1)
	entry, ok := session.GetEntry(lastID)
	if !ok {
		t.Fatalf("GetEntry(%s) not found", lastID)
	}
	bs, ok := entry.(BranchSummaryEntry)
	if !ok {
		t.Fatalf("GetEntry(%s) returned %T, want BranchSummaryEntry", lastID, entry)
	}
	if len(bs.Summary) != summaryBytes {
		t.Fatalf("Summary len = %d, want %d", len(bs.Summary), summaryBytes)
	}
	encodedDetails, err := json.Marshal(bs.Details)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf(`{"z":%d,"a":1}`, count); string(encodedDetails) != want {
		t.Fatalf("Details = %s, want %s", encodedDetails, want)
	}
	runtime.KeepAlive(session)
}
