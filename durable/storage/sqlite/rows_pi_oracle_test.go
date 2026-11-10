package sqlite

// pi: packages/durable/src/storage/sqlite/rows.ts

// Pi's rows.ts (imported as-is under Node) answers for a corpus of document records and JSON values; the Go row builders
// must give the same cells, the same address key, the same scope columns, the same current-only verdict and the same
// JSON text. The corpus reaches every scope, a missing and an empty key (family 0 and 1), retired and live records, both
// histories, and kinds and keys whose JSON encoding differs from their bytes (a quote, a NUL, a lone surrogate).

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type rowsOracleAnswer struct {
	Records []struct {
		Record  string `json:"record"`
		Row     string `json:"row"`
		Current bool   `json:"current"`
		Address string `json:"address"`
		Scope   string `json:"scope"`
	} `json:"records"`
	Values []string `json:"values"`
}

// rowsOracleRecord is what the oracle builds a record from: the address and the definition semantics documentCreate reads, and the seqs
// the storage stamps. Pi writes the record; nothing the Go record encodes reaches it.
type rowsOracleRecord struct {
	Address    rowsOracleAddress `json:"address"`
	Definition struct {
		History durable.DocumentHistory `json:"history,omitempty"`
		Fork    durable.DocumentFork    `json:"fork,omitempty"`
	} `json:"definition"`
	Id        durable.DocumentId `json:"id"`
	CreatedAt durable.Seq        `json:"createdAt"`
	RetiredAt *durable.Seq       `json:"retiredAt,omitempty"`
}

type rowsOracleAddress struct {
	Kind  string                      `json:"kind"`
	Scope durable.DocumentRecordScope `json:"scope"`
	Key   *string                     `json:"key,omitempty"`
}

func describeRecord(record durable.DocumentRecord) rowsOracleRecord {
	described := rowsOracleRecord{Address: rowsOracleAddress{Kind: record.Kind, Scope: record.Scope, Key: record.Key}, Id: record.Id, CreatedAt: record.CreatedAt, RetiredAt: record.RetiredAt}
	described.Definition.History, described.Definition.Fork = record.History, record.Fork
	return described
}

// rowsCorpus is every combination of the record fields the row builders read.
func rowsCorpus() []durable.DocumentRecord {
	text := func(value string) *string { return &value }
	seq := func(value durable.Seq) *durable.Seq { return &value }
	scopes := []durable.DocumentRecordScope{
		{Kind: durable.ScopeSession},
		{Kind: durable.ScopeConversation, ConversationId: 1},
		{Kind: durable.ScopeConversation, ConversationId: 9007199254740991},
		{Kind: durable.ScopeTask, TaskId: 3},
	}
	kinds := []string{"k", "a\"b\\c", "nul\x00", "\xed\xa0\x80", "é日本", "</&\u2028>"}
	keys := []*string{nil, text(""), text("k"), text("q\"\n"), text("\xed\xb0\x80")}
	retirements := []*durable.Seq{nil, seq(9)}
	var records []durable.DocumentRecord
	id := durable.DocumentId(100)
	for _, scope := range scopes {
		histories := []durable.DocumentHistory{""}
		if scope.Kind == durable.ScopeConversation {
			histories = []durable.DocumentHistory{durable.HistoryLatest, durable.HistoryRewindable}
		}
		for _, history := range histories {
			for _, kind := range kinds {
				for _, key := range keys {
					for _, retired := range retirements {
						id++
						record := durable.DocumentRecord{Id: id, Kind: kind, Key: key, CreatedAt: durable.Seq(id % 7), RetiredAt: retired, Scope: scope, History: history}
						if scope.Kind == durable.ScopeConversation {
							record.Fork = durable.ForkAsOf
							if history == durable.HistoryLatest {
								record.Fork = durable.ForkCurrent
							}
						}
						records = append(records, record)
					}
				}
			}
		}
	}
	return records
}

func TestRowsMatchPi(t *testing.T) {
	records := rowsCorpus()
	values := []any{
		"plain", "", "a<b>&c\u2028\u2029", "\xed\xa0\x80", "\xed\xb0\x80", "x\x00y", "q\"\\\n\t", "\u007f\u0080",
		float64(0), float64(1), 1.5, -2.25, 1e21, 1e-7, 123456789012345680000.0, float64(1 << 53),
		true, false, nil, []any{}, []any{"a", float64(1), nil}, map[string]any{}, map[string]any{"k": "v"},
	}
	described := make([]rowsOracleRecord, len(records))
	for index, record := range records {
		described[index] = describeRecord(record)
	}
	input, err := jsstring.MarshalJSON(map[string]any{"records": described, "values": values})
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), testenv.RequireQualifiedNode(t), "--disable-warning=ExperimentalWarning", "testdata/rows_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var pi rowsOracleAnswer
	if err := json.Unmarshal(output, &pi); err != nil || len(pi.Records) != len(records) || len(pi.Values) != len(values) {
		t.Fatalf("oracle output: %v (%d records for %d, %d values for %d)", err, len(pi.Records), len(records), len(pi.Values), len(values))
	}
	current, live := 0, 0
	for index, record := range records {
		want := pi.Records[index]
		// The storage writes the record through jsstring (encodeJson), which gives a Marshaler's text the form JSON.stringify writes.
		gotRecord, err := jsstring.MarshalJSON(record)
		if err != nil {
			t.Fatal(err)
		}
		if string(gotRecord) != want.Record {
			t.Fatalf("record column of %+v\n got %s\nwant %s (Pi)", record, gotRecord, want.Record)
		}
		row, err := documentRow(record)
		if err != nil {
			t.Fatal(err)
		}
		gotRow, err := jsstring.MarshalJSON(row)
		if err != nil {
			t.Fatal(err)
		}
		if string(gotRow) != want.Row {
			t.Fatalf("documentRow(%+v)\n got %s\nwant %s", record, gotRow, want.Row)
		}
		if got := isCurrentOnly(record); got != want.Current {
			t.Fatalf("isCurrentOnly(%+v) = %v, Pi %v", record, got, want.Current)
		}
		parts := addressPartsOf(record.Kind, record.Scope, record.Key)
		gotAddress, err := jsstring.MarshalJSON([]any{parts.kind, parts.scopeKind, parts.ownerId, parts.family, parts.keyValue})
		if err != nil {
			t.Fatal(err)
		}
		if string(gotAddress) != want.Address {
			t.Fatalf("address of %+v = %s, Pi %s", record, gotAddress, want.Address)
		}
		gotScope, err := jsstring.MarshalJSON(struct {
			ScopeKind string `json:"scopeKind"`
			OwnerID   int64  `json:"ownerId"`
		}{parts.scopeKind, parts.ownerId})
		if err != nil {
			t.Fatal(err)
		}
		if string(gotScope) != want.Scope {
			t.Fatalf("scope columns of %+v = %s, Pi %s", record.Scope, gotScope, want.Scope)
		}
		if want.Current {
			current++
		}
		if record.RetiredAt == nil {
			live++
		}
	}
	// The corpus has to reach both verdicts and both lifetimes, or it would pass on a constant.
	if current == 0 || current == len(records) || live == 0 || live == len(records) {
		t.Fatalf("%d of %d records are current-only, %d live", current, len(records), live)
	}
	for index, value := range values {
		got, err := encodeJSON(value)
		if err != nil {
			t.Fatal(err)
		}
		if got != pi.Values[index] {
			t.Errorf("encodeJSON(%#v) = %s, Pi %s", value, got, pi.Values[index])
		}
	}
}
