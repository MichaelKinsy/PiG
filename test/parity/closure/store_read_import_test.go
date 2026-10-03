package closure

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// RebuildStore accepts independently bounded records; accumulated store size is not an external JSONL import.
func TestStoreReadsCanonicalRecordsPastImportLimit(t *testing.T) {
	parallelHeavy(t)
	name := strings.Repeat("x", maxRecordBytes/2)
	var records []Record
	var imports []io.Reader
	total := 0
	for index := 0; total <= maxInputBytes; index++ {
		record := &Rule{Kind: KindRule, ID: fmt.Sprintf("rule:large:%03d", index), Name: name, DefinitionHash: HashBytes([]byte("large-record-rule"))}
		body, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if len(body) > maxRecordBytes {
			t.Fatal("fixture record exceeds the per-record bound")
		}
		records = append(records, record)
		imports = append(imports, bytes.NewReader(body), strings.NewReader("\n"))
		total += len(body) + 1
	}
	graph, err := Build(records)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "large.db")
	if err := RebuildStore(t.Context(), path, graph); err != nil {
		t.Fatal(err)
	}
	if err := VerifyStore(t.Context(), path); err != nil {
		t.Fatalf("valid store of %d independently bounded records (%d bytes) is unreadable: %v", len(records), total, err)
	}
	database, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	decoded, err := readRecords(t.Context(), database)
	if err != nil || len(decoded) != len(records) {
		t.Fatalf("records = %d, want %d; error = %v", len(decoded), len(records), err)
	}
	for index, record := range decoded {
		got, ok := record.(*Rule)
		want := records[index].(*Rule)
		if !ok || *got != *want {
			t.Fatalf("record %d changed during store round trip", index)
		}
	}
	if _, err := DecodeJSONL(io.MultiReader(imports...)); err == nil || !strings.Contains(err.Error(), "closure input exceeds") {
		t.Fatalf("external import limit was lost: %v", err)
	}
}

func TestStoreRejectsOversizedRecordBeforePublication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "atomic.db")
	original, err := Build([]Record{&Facet{Kind: KindFacet, ID: "facet:original", Name: "result"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := RebuildStore(t.Context(), path, original); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	oversized, err := Build([]Record{&Rule{Kind: KindRule, ID: "rule:oversized", Name: strings.Repeat("x", maxRecordBytes), DefinitionHash: HashBytes([]byte("oversized"))}})
	if err != nil {
		t.Fatal(err)
	}
	if err := RebuildStore(t.Context(), path, oversized); err == nil || !strings.Contains(err.Error(), "record rule:oversized exceeds") {
		t.Errorf("oversized rebuild = %v, want bounded-record rejection", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("rejected rebuild replaced the existing store")
	}
	if err := VerifyStore(t.Context(), path); err != nil {
		t.Errorf("existing store became unreadable: %v", err)
	}
}

func TestStoreRejectsNonCanonicalRecordRows(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"empty", "\n", "want one canonical record"},
		{"multiple", `{"kind":"facet","id":"facet:a","name":"result"} {"kind":"facet","id":"facet:b","name":"error"}`, "want one canonical record"},
		{"unknown field", `{"kind":"facet","id":"facet:a","name":"result","extra":true}`, "unknown field"},
		{"oversized", `{"kind":"rule","id":"rule:a","name":"` + strings.Repeat("x", maxRecordBytes) + `","definitionHash":"x"}`, "body exceeds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid.db")
			graph, err := Build(nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := RebuildStore(t.Context(), path, graph); err != nil {
				t.Fatal(err)
			}
			body := []byte(tc.body)
			if err := mutateStore(t.Context(), path, "INSERT INTO records(id, kind, content_hash, body) VALUES (?, ?, ?, ?)", "facet:a", KindFacet, HashBytes(body), body); err != nil {
				t.Fatal(err)
			}
			database, err := openStore(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = database.Close() }()
			if _, err := readRecords(t.Context(), database); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("non-canonical row = %v, want %s", err, tc.want)
			}
		})
	}
}
