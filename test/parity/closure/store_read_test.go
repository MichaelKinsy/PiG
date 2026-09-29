package closure

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadRecordsBeyondImportLimit(t *testing.T) {
	records := provedFixture(t)
	payload, err := json.Marshal(strings.Repeat("x", maxRecordBytes/4))
	if err != nil {
		t.Fatal(err)
	}
	// The store can combine many bounded imports; its aggregate is not one import file.
	for index := range maxInputBytes/len(payload) + 1 {
		records = append(records, &Fact{
			Kind: KindFact, ID: fmt.Sprintf("fact:large:%03d", index), SnapshotID: "snapshot:test",
			FactType: "test", SubjectID: "large", Resolution: "observed", PinIDs: []string{"pin:target"}, Value: payload,
		})
	}
	graph, err := Build(records)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "closure.db")
	if err := RebuildStore(t.Context(), path, graph); err != nil {
		t.Fatal(err)
	}
	database, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	var totalBytes, largestRecord int
	if err := database.QueryRowContext(t.Context(), "SELECT sum(length(body) + 1), max(length(body)) FROM records").Scan(&totalBytes, &largestRecord); err != nil {
		t.Fatal(err)
	}
	if totalBytes <= maxInputBytes || largestRecord > maxRecordBytes {
		t.Fatalf("fixture must exceed the import limit with bounded records: total=%d largest=%d", totalBytes, largestRecord)
	}
	got, err := readRecords(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(records) {
		t.Fatalf("read %d records, want %d", len(got), len(records))
	}
	ids := sortedRecordIDs(graph.Records)
	for index, record := range got {
		body, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if record.RecordID() != ids[index] || HashBytes(body) != graph.RecordHashes[ids[index]] {
			t.Fatalf("record %d changed identity, order, or content", index)
		}
	}
	if err := VerifyStore(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	status, err := ReadStatus(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := RenderStatus(graph.EffectiveVerdicts())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(status, want) {
		t.Fatalf("status changed after store round trip:\ngot: %s\nwant: %s", status, want)
	}
}

func TestReadRecordsValidatesEachRow(t *testing.T) {
	valid := `{"kind":"facet","id":"facet:result","name":"result"}`
	prefix := `{"kind":"facet","id":"facet:large","name":"`
	exactLimit := prefix + strings.Repeat("x", maxRecordBytes-len(prefix)-2) + `"}`
	for _, test := range []struct {
		name, body, hash, wantError string
	}{
		{name: "ordinary", body: valid},
		{name: "exact record limit", body: exactLimit},
		{name: "oversized record", body: exactLimit[:len(exactLimit)-2] + `x"}`, wantError: "exceeds"},
		{name: "empty row", wantError: "want one canonical record"},
		{name: "multiple records in row", body: valid + "\n" + valid, wantError: "want one canonical record"},
		{name: "malformed JSON", body: valid[:len(valid)-1], wantError: "unexpected EOF"},
		{name: "unknown field", body: `{"kind":"facet","id":"facet:result","extra":true}`, wantError: "unknown field"},
		{name: "unknown kind", body: `{"kind":"invented"}`, wantError: "unknown record kind"},
		{name: "derived verdict", body: `{"kind":"verdict"}`, wantError: "not a canonical input"},
		{name: "bad hash", body: valid, hash: HashBytes([]byte("wrong")), wantError: "body hash"},
		{name: "stored attestation", body: `{"kind":"evidence-attestation","id":"attestation:test"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			database := emptyRecordStore(t)
			// A valid prefix must not be returned if a later row is corrupt.
			insertRecordBody(t, database, "a", []byte(valid), "")
			insertRecordBody(t, database, "b", []byte(test.body), test.hash)
			records, err := readRecords(t.Context(), database)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) || records != nil {
					t.Fatalf("readRecords() = %d records, %v; want nil records and %q", len(records), err, test.wantError)
				}
			} else if err != nil || len(records) != 2 {
				t.Fatalf("readRecords() = %d records, %v; want both inserted records", len(records), err)
			}
			if inUse := database.Stats().InUse; inUse != 0 {
				t.Fatalf("readRecords retained %d connections", inUse)
			}
		})
	}
}

func TestReadRecordsEmptyAndCanceled(t *testing.T) {
	database := emptyRecordStore(t)
	records, err := readRecords(t.Context(), database)
	if err != nil || len(records) != 0 {
		t.Fatalf("empty store = %v, %v", records, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if records, err := readRecords(ctx, database); records != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read = %v, %v", records, err)
	}
}

func emptyRecordStore(t *testing.T) *sql.DB {
	t.Helper()
	database, err := openStore(filepath.Join(t.TempDir(), "records.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	// No uniqueness constraint is needed for deliberately corrupt row bodies.
	if _, err := database.ExecContext(t.Context(), "CREATE TABLE records (id TEXT PRIMARY KEY, content_hash TEXT, body BLOB)"); err != nil {
		t.Fatal(err)
	}
	return database
}

func insertRecordBody(t *testing.T, database *sql.DB, id string, body []byte, hash string) {
	t.Helper()
	if hash == "" {
		hash = HashBytes(body)
	}
	if _, err := database.ExecContext(t.Context(), "INSERT INTO records VALUES (?, ?, ?)", id, hash, body); err != nil {
		t.Fatal(err)
	}
}
